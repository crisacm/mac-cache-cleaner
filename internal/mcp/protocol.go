// Package mcp implements the Model Context Protocol server for MCC.
//
// It speaks newline-delimited JSON-RPC 2.0 over stdio (SEC-MCP-01) and exposes
// read-only tools plus a plan tool. It contains no business logic: it calls the
// existing services, paginates, sanitizes and renders structured JSON. It never
// applies a plan, grants consent, scans, deletes or returns file contents.
package mcp

import "encoding/json"

// JSON-RPC 2.0 error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

// MCP protocol versions this server can answer.
const (
	protocolVersion    = "2025-06-18"
	protocolVersionAlt = "2024-11-05"
)

// Server identity.
const (
	serverName    = "mcc"
	serverVersion = "1.0.0"
)

// request is a JSON-RPC request or notification.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the message has no id (no response expected).
func (r request) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// response is a JSON-RPC response.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// initializeParams is the client's initialize request.
type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

// serverInfo describes the server.
type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// initializeResult is the server's initialize response.
type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      serverInfo     `json:"serverInfo"`
	Instructions    string         `json:"instructions,omitempty"`
}

// tool describes one MCP tool.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// listToolsResult is the tools/list response.
type listToolsResult struct {
	Tools []tool `json:"tools"`
}

// callToolParams is the tools/call request.
type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// contentBlock is one MCP content block.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// callToolResult is the tools/call response.
type callToolResult struct {
	Content           []contentBlock `json:"content"`
	IsError           bool           `json:"isError,omitempty"`
	StructuredContent any            `json:"structuredContent,omitempty"`
}

// toolError is a tool-level failure with a static code and message. It never
// contains filesystem-derived text (SEC-MCP-06).
type toolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *toolError) Error() string { return e.Message }

func errTool(code, message string) *toolError { return &toolError{Code: code, Message: message} }
