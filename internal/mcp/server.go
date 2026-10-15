package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/cleaner"
	"github.com/crisacm/mac-cache-cleaner/internal/config"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
	"github.com/crisacm/mac-cache-cleaner/internal/scan"
	"github.com/crisacm/mac-cache-cleaner/internal/store"
)

// Limits.
const (
	// callTimeout bounds a single tool call (SEC-MCP-08).
	callTimeout = 30 * time.Second
	// rateLimit is the number of calls allowed per window (SEC-MCP-08).
	rateLimit = 120
	// rateWindow is the rate-limit window.
	rateWindow = time.Minute
)

// Deps are the services the MCP server reads from.
type Deps struct {
	Home    string
	Config  config.Config
	Policy  *policy.Policy
	Store   *store.Store
	Scan    *scan.Service
	Cleaner *cleaner.Service
}

// Server is the MCP stdio server.
type Server struct {
	deps     Deps
	now      func() time.Time
	limiter  *rateLimiter
	tools    []tool
	handlers map[string]handlerFunc
}

// handlerFunc runs one tool.
type handlerFunc func(ctx context.Context, s *Server, args json.RawMessage) (any, error)

// New builds a Server.
func New(deps Deps) *Server {
	s := &Server{
		deps:     deps,
		now:      time.Now,
		limiter:  newRateLimiter(rateLimit, rateWindow),
		handlers: map[string]handlerFunc{},
	}
	s.tools = toolCatalog()
	for _, t := range s.tools {
		s.handlers[t.Name] = handlerFor(t.Name)
	}
	return s
}

// Serve reads JSON-RPC messages from in and writes responses to out until EOF.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReaderSize(in, 1<<20)
	writer := bufio.NewWriter(out)
	defer func() { _ = writer.Flush() }()

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		line = []byte(strings.TrimSpace(string(line)))
		if len(line) == 0 {
			if err != nil {
				return nil
			}
			continue
		}

		s.handleLine(ctx, line, writer)
		_ = writer.Flush()

		if err != nil {
			return nil
		}
	}
}

// handleLine decodes, dispatches and writes one response.
func (s *Server) handleLine(ctx context.Context, line []byte, writer *bufio.Writer) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		s.write(writer, response{JSONRPC: "2.0", Error: &rpcError{
			Code: codeParseError, Message: "invalid JSON",
		}})
		return
	}
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		s.write(writer, response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{
			Code: codeInvalidRequest, Message: "unsupported jsonrpc version",
		}})
		return
	}

	resp, ok := s.dispatch(ctx, req)
	if !ok {
		return // notification: no response
	}
	s.write(writer, resp)
}

// dispatch routes a method to its handler.
func (s *Server) dispatch(ctx context.Context, req request) (response, bool) {
	reply := func(result any) (response, bool) {
		return response{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	}
	fail := func(code int, message string) (response, bool) {
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: message}}, true
	}

	switch req.Method {
	case "initialize":
		var p initializeParams
		_ = json.Unmarshal(req.Params, &p)
		version := protocolVersionAlt
		if p.ProtocolVersion == protocolVersion || p.ProtocolVersion == protocolVersionAlt {
			version = p.ProtocolVersion
		}
		return reply(initializeResult{
			ProtocolVersion: version,
			Capabilities:    map[string]any{"tools": map[string]any{}},
			ServerInfo:      serverInfo{Name: serverName, Version: serverVersion},
			Instructions: "Read-only access to MCC's disk-usage index plus plan creation. " +
				"Nothing is ever deleted: applying a plan (outside MCP) only moves items to the Trash. " +
				"Use mcc_explain to check whether an item is actually removable before proposing it.",
		})
	case "notifications/initialized", "notifications/cancelled":
		return response{}, false
	case "ping":
		return reply(map[string]any{})
	case "tools/list":
		return reply(listToolsResult{Tools: s.tools})
	case "tools/call":
		if req.isNotification() {
			return response{}, false
		}
		return s.callTool(ctx, req)
	default:
		if req.isNotification() {
			return response{}, false
		}
		return fail(codeMethodNotFound, "method not found")
	}
}

// callTool runs a tool, enforcing the rate limit, timeout and audit.
func (s *Server) callTool(ctx context.Context, req request) (response, bool) {
	var p callToolParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: codeInvalidParams, Message: "invalid params"}}, true
	}
	started := s.now()
	handler, ok := s.handlers[p.Name]
	if !ok {
		// Even a rejected call is audited (SEC-MCP-08).
		s.audit(p.Name, argsSummary(p.Arguments), "unknown_tool", started, 0)
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: codeInvalidParams, Message: "unknown tool"}}, true
	}

	if !s.limiter.allow(started) {
		return s.toolResponse(req.ID, p.Name, argsSummary(p.Arguments), &toolError{
			Code: "rate_limited", Message: "rate limit exceeded; retry later",
		}, nil, 0, started), true
	}

	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	result, err := handler(callCtx, s, p.Arguments)
	if err != nil {
		var te *toolError
		if errors.As(err, &te) {
			return s.toolResponse(req.ID, p.Name, argsSummary(p.Arguments), te, nil, 0, started), true
		}
		return s.toolResponse(req.ID, p.Name, argsSummary(p.Arguments), &toolError{
			Code: "internal", Message: "internal error",
		}, nil, 0, started), true
	}
	return s.toolResponse(req.ID, p.Name, argsSummary(p.Arguments), nil, result, itemCount(result), started), true
}

// toolResponse builds the response for a tool call and audits it.
func (s *Server) toolResponse(id json.RawMessage, name, args string, te *toolError, result any, count int64, started time.Time) response {
	status := "ok"
	var payload any
	payload = result
	if te != nil {
		status = te.Code
		payload = map[string]any{"error": te}
	}
	text, _ := json.MarshalIndent(payload, "", "  ")
	toolResult := callToolResult{
		Content:           []contentBlock{{Type: "text", Text: string(text)}},
		IsError:           te != nil,
		StructuredContent: payload,
	}

	s.audit(name, args, status, started, count)
	return response{JSONRPC: "2.0", ID: id, Result: toolResult}
}

// audit records a call locally, even if the request was rejected (SEC-MCP-08).
func (s *Server) audit(tool, args, status string, started time.Time, count int64) {
	if tool == "" {
		tool = "(unknown)"
	}
	_ = s.deps.Store.AppendMCPCall(context.WithoutCancel(context.Background()), domain.MCPCall{
		At:          started,
		Tool:        tool,
		ArgsSummary: args,
		Status:      status,
		DurationMS:  s.now().Sub(started).Milliseconds(),
		ItemCount:   count,
	})
}

func (s *Server) write(w *bufio.Writer, resp response) {
	if resp.Result == nil && resp.Error == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintln(w, `{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal error"}}`)
		return
	}
	_, _ = w.Write(data)
	_ = w.WriteByte('\n')
}

// rateLimiter is a simple sliding-window limiter.
type rateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	times  []time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: limit, window: window}
}

func (r *rateLimiter) allow(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := now.Add(-r.window)
	kept := r.times[:0]
	for _, t := range r.times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	r.times = kept
	if len(r.times) >= r.max {
		return false
	}
	r.times = append(r.times, now)
	return true
}
