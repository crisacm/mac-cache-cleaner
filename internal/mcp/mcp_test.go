package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crisacm/mac-cache-cleaner/internal/cleaner"
	"github.com/crisacm/mac-cache-cleaner/internal/config"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
	"github.com/crisacm/mac-cache-cleaner/internal/scan"
	"github.com/crisacm/mac-cache-cleaner/internal/store"
)

const hostileName = "evil\x1b[31mname"

const infoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.example.foo</string>
<key>CFBundleName</key><string>Foo</string>
</dict></plist>`

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, make([]byte, size), 0o600))
}

type nopTrasher struct{}

func (nopTrasher) Trash(context.Context, string) error { return nil }

type noRunning struct{}

func (noRunning) BundleIDs(context.Context) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}

// testServer builds a hermetic fixture, a store and a server, and runs one scan.
func testServer(t *testing.T) (*Server, Deps, string) {
	t.Helper()
	home := t.TempDir()
	caches := filepath.Join(home, "Library", "Caches")
	writeFile(t, filepath.Join(caches, "com.example", "data.bin"), 4096)
	writeFile(t, filepath.Join(caches, "Homebrew", "bottle.tbz"), 2048)
	writeFile(t, filepath.Join(caches, hostileName, "x.bin"), 128)
	writeFile(t, filepath.Join(caches, "com.orphan.leftover", "y.bin"), 64)
	writeFile(t, filepath.Join(home, "Library", "Application Support", "com.example.app", "db"), 256)
	require.NoError(t, os.MkdirAll(filepath.Join(home, "Library", "Keychains"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "Documents"), 0o750))
	// An installed application so discovery is complete and confidence intact.
	writeFile(t, filepath.Join(home, "Applications", "Foo.app", "Contents", "Info.plist"), 0)
	require.NoError(t, os.WriteFile(filepath.Join(home, "Applications", "Foo.app", "Contents", "Info.plist"),
		[]byte(infoPlist), 0o600))

	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "mcc"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	pol := policy.New(home, st)
	deps := Deps{
		Home:    home,
		Config:  config.Default(home),
		Policy:  pol,
		Store:   st,
		Scan:    scan.New(st, pol),
		Cleaner: cleaner.New(st, pol, home, cleaner.WithTrasher(nopTrasher{}), cleaner.WithRunningApps(noRunning{})),
	}
	srv := New(deps)

	out, err := deps.Scan.Run(ctx, scan.Options{Roots: []string{caches}})
	require.NoError(t, err)
	return srv, deps, out.Scan.ID
}

// rpc sends one JSON-RPC message through Serve and returns the decoded response.
func rpc(t *testing.T, s *Server, msg map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(msg)
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, s.Serve(context.Background(), bytes.NewReader(append(raw, '\n')), &out))
	var resp map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &resp), "response must be valid JSON: %s", out.String())
	return resp
}

func callTool(t *testing.T, s *Server, name string, args map[string]any) map[string]any {
	t.Helper()
	resp := rpc(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	require.NotNil(t, resp["result"], "tools/call must return a result: %v", resp)
	result := resp["result"].(map[string]any)
	return result
}

// structured returns the structuredContent of a successful tool call.
func structured(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	require.NotEqual(t, true, result["isError"], "unexpected tool error: %v", result)
	sc, ok := result["structuredContent"].(map[string]any)
	require.True(t, ok, "result must carry structuredContent")
	return sc
}

func items(t *testing.T, sc map[string]any) []map[string]any {
	t.Helper()
	raw, ok := sc["items"].([]any)
	require.True(t, ok, "expected an items array")
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(map[string]any))
	}
	return out
}

// --- protocol ----------------------------------------------------------

func TestInitializeAndToolsList(t *testing.T) {
	s, _, _ := testServer(t)

	init := rpc(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-06-18"},
	})
	result := init["result"].(map[string]any)
	require.Equal(t, "2025-06-18", result["protocolVersion"])
	require.Equal(t, "mcc", result["serverInfo"].(map[string]any)["name"])

	list := rpc(t, s, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	tools := list["result"].(map[string]any)["tools"].([]any)
	require.NotEmpty(t, tools)

	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	require.True(t, names["mcc_get_status"])
	require.True(t, names["mcc_create_plan"])
	// No mutating tool may be exposed (SEC-MCP-02, SEC-MCP-04).
	for _, forbidden := range []string{"apply", "delete", "remove", "scan", "grant", "consent", "purge", "config"} {
		for name := range names {
			require.NotContains(t, strings.ToLower(name), forbidden, "mutating tool exposed")
		}
	}
}

func TestUnknownMethodAndBadJSON(t *testing.T) {
	s, _, _ := testServer(t)

	resp := rpc(t, s, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "does/not/exist"})
	require.Equal(t, float64(codeMethodNotFound), resp["error"].(map[string]any)["code"])

	var out bytes.Buffer
	require.NoError(t, s.Serve(context.Background(), bytes.NewReader([]byte("{not json}\n")), &out))
	var bad map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &bad))
	require.Equal(t, float64(codeParseError), bad["error"].(map[string]any)["code"])
}

func TestToolDescriptionsAreStatic(t *testing.T) {
	s, deps, _ := testServer(t)
	raw, err := json.Marshal(s.tools)
	require.NoError(t, err)
	text := string(raw)
	require.NotContains(t, text, deps.Home, "tool descriptions must not contain filesystem text (SEC-MCP-06)")
	require.NotContains(t, text, "~\x00")
	require.NotContains(t, text, "\x1b")
}

func TestInvalidArguments(t *testing.T) {
	s, _, _ := testServer(t)
	res := callTool(t, s, "mcc_get_entry", map[string]any{"entry_id": "nope"})
	require.Equal(t, true, res["isError"])
	sc := res["structuredContent"].(map[string]any)
	require.Equal(t, "not_found", sc["error"].(map[string]any)["code"])

	res = callTool(t, s, "mcc_list_large", map[string]any{"unknown_field": 1})
	require.Equal(t, true, res["isError"])
	require.Equal(t, "invalid_argument", res["structuredContent"].(map[string]any)["error"].(map[string]any)["code"])

	res = callTool(t, s, "mcc_create_plan", map[string]any{"entry_ids": []string{}})
	require.Equal(t, true, res["isError"])
}

// --- read-only tools ---------------------------------------------------

func TestGetStatus(t *testing.T) {
	s, deps, scanID := testServer(t)
	sc := structured(t, callTool(t, s, "mcc_get_status", nil))
	require.Equal(t, true, sc["has_scan"])
	require.Equal(t, true, sc["untrusted_data"])
	require.Equal(t, scanID, sc["scan"].(map[string]any)["id"])
	require.NotEmpty(t, sc["policy"])
	require.NotNil(t, sc["roots"])
	require.NotContains(t, sc["data_dir"].(string), deps.Home, "data dir must be redacted")
}

func TestGetStatusWithoutScan(t *testing.T) {
	home := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "mcc"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	pol := policy.New(home, st)
	deps := Deps{
		Home: home, Config: config.Default(home), Policy: pol, Store: st,
		Scan: scan.New(st, pol), Cleaner: cleaner.New(st, pol, home, cleaner.WithTrasher(nopTrasher{}), cleaner.WithRunningApps(noRunning{})),
	}
	s := New(deps)

	sc := structured(t, callTool(t, s, "mcc_get_status", nil))
	require.Equal(t, false, sc["has_scan"])

	// Tools that need the index report no_scan with a static message.
	res := callTool(t, s, "mcc_list_caches", nil)
	require.Equal(t, true, res["isError"])
	require.Equal(t, "no_scan", res["structuredContent"].(map[string]any)["error"].(map[string]any)["code"])
}

func TestListAndPaginationAndSanitization(t *testing.T) {
	s, _, _ := testServer(t)

	// Caches: paginated, untrusted flag set.
	sc := structured(t, callTool(t, s, "mcc_list_caches", map[string]any{"page_size": 1}))
	require.Equal(t, true, sc["untrusted_data"])
	require.Len(t, items(t, sc), 1)
	require.Equal(t, true, sc["truncated"])
	require.NotEmpty(t, sc["next_page_token"])

	// Orphans include the hostile name, sanitized.
	sc = structured(t, callTool(t, s, "mcc_list_orphans", nil))
	require.NotContains(t, mustJSON(t, sc), "\x1b", "ANSI must be stripped")
	require.Contains(t, mustJSON(t, sc), "evilname")

	// No returned path is absolute: the home prefix is redacted.
	for _, it := range items(t, sc) {
		require.True(t, strings.HasPrefix(it["path"].(string), "~/"), "path must be redacted: %v", it["path"])
	}
}

func TestItemCarriesEligibility(t *testing.T) {
	s, _, _ := testServer(t)
	sc := structured(t, callTool(t, s, "mcc_list_caches", nil))
	found := false
	for _, it := range items(t, sc) {
		require.NotEmpty(t, it["cleanup_eligibility"])
		switch it["data_class"] {
		case string(domain.ClassRegenerableCache):
			require.Equal(t, string(domain.EligibilityAuto), it["cleanup_eligibility"])
			found = true
		case string(domain.ClassAppData):
			require.Equal(t, string(domain.EligibilityExplicitSelection), it["cleanup_eligibility"])
		default:
			require.Equal(t, string(domain.EligibilityReportOnly), it["cleanup_eligibility"])
		}
	}
	require.True(t, found, "at least one auto-plannable cache is expected")
}

func TestListChildrenHidesFilesByDefault(t *testing.T) {
	s, deps, scanID := testServer(t)
	rootID := domain.ComputeEntryID(scanID, filepath.Join(deps.Home, "Library", "Caches", "com.example"))

	sc := structured(t, callTool(t, s, "mcc_list_children", map[string]any{"entry_id": rootID}))
	require.Empty(t, items(t, sc), "files are hidden by default")

	sc = structured(t, callTool(t, s, "mcc_list_children", map[string]any{"entry_id": rootID, "show_files": true}))
	require.Len(t, items(t, sc), 1)
	require.Equal(t, false, items(t, sc)[0]["is_dir"])
	require.Equal(t, string(domain.KindFile), items(t, sc)[0]["kind"])
}

func TestTier2AndTier1PathsAreGated(t *testing.T) {
	s, deps, _ := testServer(t)

	// Tier 2 is never readable from a path-accepting tool (SEC-MCP-09).
	res := callTool(t, s, "mcc_list_large", map[string]any{"path_prefix": filepath.Join(deps.Home, "Library", "Keychains")})
	require.Equal(t, true, res["isError"])
	require.Equal(t, "blocked", res["structuredContent"].(map[string]any)["error"].(map[string]any)["code"])

	// Tier 1 without consent is blocked...
	docs := filepath.Join(deps.Home, "Documents")
	res = callTool(t, s, "mcc_list_large", map[string]any{"path_prefix": docs})
	require.Equal(t, true, res["isError"])

	// ...and allowed once consent exists.
	require.NoError(t, deps.Store.GrantConsent(context.Background(), domain.Consent{
		Root: docs, Tier: domain.TierUserData, GrantedAt: time.Now(),
	}))
	structured(t, callTool(t, s, "mcc_list_large", map[string]any{"path_prefix": docs}))
}

func TestExplainReasons(t *testing.T) {
	s, deps, scanID := testServer(t)
	ctx := context.Background()

	// An allowlisted root is never planned.
	rootID := domain.ComputeEntryID(scanID, filepath.Join(deps.Home, "Library", "Caches"))
	sc := structured(t, callTool(t, s, "mcc_explain", map[string]any{"entry_id": rootID}))
	require.Equal(t, false, sc["removable_now"])
	require.Equal(t, true, sc["allowlisted_root"])

	// A regenerable cache child is removable now.
	childID := domain.ComputeEntryID(scanID, filepath.Join(deps.Home, "Library", "Caches", "Homebrew"))
	sc = structured(t, callTool(t, s, "mcc_explain", map[string]any{"entry_id": childID}))
	require.Equal(t, true, sc["removable_now"])
	require.Equal(t, string(domain.EligibilityAuto), sc["cleanup_eligibility"])

	// A user_file entry is report-only. Insert one synthetically.
	uf := domain.Entry{
		EntryID: "user-file-1", ScanID: scanID, Path: filepath.Join(deps.Home, "Documents", "x"),
		Kind: domain.KindFile, DataClass: domain.ClassUserFile, AllocatedBytes: 10,
	}
	require.NoError(t, deps.Store.InsertEntries(ctx, []domain.Entry{uf}))
	sc = structured(t, callTool(t, s, "mcc_explain", map[string]any{"entry_id": uf.EntryID}))
	require.Equal(t, string(domain.EligibilityReportOnly), sc["cleanup_eligibility"])
	require.Equal(t, false, sc["removable_now"])
}

func TestVerifyOrphan(t *testing.T) {
	s, deps, scanID := testServer(t)
	leftover := filepath.Join(deps.Home, "Library", "Caches", "com.orphan.leftover")
	id := domain.ComputeEntryID(scanID, leftover)

	sc := structured(t, callTool(t, s, "mcc_verify_orphan", map[string]any{"entry_id": id}))
	require.Equal(t, false, sc["app_found"])
	require.Equal(t, "high", sc["confidence"])
	require.Equal(t, true, sc["discovery_complete"])
}

func TestGetReport(t *testing.T) {
	s, _, _ := testServer(t)
	sc := structured(t, callTool(t, s, "mcc_get_report", map[string]any{"top_n": 2}))
	require.Equal(t, true, sc["untrusted_data"])
	require.NotNil(t, sc["totals"])
	require.NotNil(t, sc["by_class"])
	require.LessOrEqual(t, len(sc["top_caches"].([]any)), 2)
}

// --- plan --------------------------------------------------------------

func TestCreatePlanAndGetPlan(t *testing.T) {
	s, deps, scanID := testServer(t)
	homebrew := filepath.Join(deps.Home, "Library", "Caches", "Homebrew")
	id := domain.ComputeEntryID(scanID, homebrew)

	sc := structured(t, callTool(t, s, "mcc_create_plan", map[string]any{"entry_ids": []string{id}}))
	planID, _ := sc["plan_id"].(string)
	require.NotEmpty(t, planID)
	require.Equal(t, float64(1), sc["item_count"])
	require.Greater(t, sc["total_size"].(float64), float64(0))
	require.Equal(t, true, sc["untrusted_data"])

	got := structured(t, callTool(t, s, "mcc_get_plan", map[string]any{"plan_id": planID}))
	require.Equal(t, planID, got["plan_id"])
	require.Equal(t, float64(1), got["item_count"])
	require.Equal(t, "pending", got["status"])
}

func TestCreatePlanSkipsReportOnlyAndUnknown(t *testing.T) {
	s, deps, scanID := testServer(t)
	ctx := context.Background()
	uf := domain.Entry{
		EntryID: "user-file-2", ScanID: scanID, Path: filepath.Join(deps.Home, "Documents", "y"),
		Kind: domain.KindFile, DataClass: domain.ClassUserFile, AllocatedBytes: 10,
	}
	require.NoError(t, deps.Store.InsertEntries(ctx, []domain.Entry{uf}))

	sc := structured(t, callTool(t, s, "mcc_create_plan", map[string]any{
		"entry_ids": []string{uf.EntryID, "does-not-exist"},
	}))
	require.Equal(t, float64(0), sc["item_count"])
	skipped := sc["skipped"].([]any)
	require.Len(t, skipped, 2)
	reasons := mustJSON(t, skipped)
	require.Contains(t, reasons, "report-only")
	require.Contains(t, reasons, "not found")
}

// --- limits and audit --------------------------------------------------

func TestRateLimit(t *testing.T) {
	s, _, _ := testServer(t)
	s.limiter = newRateLimiter(1, time.Minute)
	structured(t, callTool(t, s, "mcc_get_status", nil))
	res := callTool(t, s, "mcc_get_status", nil)
	require.Equal(t, true, res["isError"])
	require.Equal(t, "rate_limited", res["structuredContent"].(map[string]any)["error"].(map[string]any)["code"])
}

func TestCallsAreAudited(t *testing.T) {
	s, deps, _ := testServer(t)
	structured(t, callTool(t, s, "mcc_list_caches", nil))

	calls, err := deps.Store.MCPCalls(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, calls, 1)
	require.Equal(t, "mcc_list_caches", calls[0].Tool)
	require.Equal(t, "ok", calls[0].Status)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestPlannableReflectsPolicy(t *testing.T) {
	s, deps, scanID := testServer(t)
	ctx := context.Background()

	// An allowlisted root appears with auto_plan (class-based) but is not
	// plannable, with a clear reason.
	rootID := domain.ComputeEntryID(scanID, filepath.Join(deps.Home, "Library", "Caches"))
	sc := structured(t, callTool(t, s, "mcc_get_entry", map[string]any{"entry_id": rootID}))
	it := sc["item"].(map[string]any)
	require.Equal(t, string(domain.EligibilityAuto), it["cleanup_eligibility"])
	require.Equal(t, false, it["plannable"])
	require.Equal(t, "allowlisted_root", it["plannable_reason"])

	// A cache child is plannable.
	childID := domain.ComputeEntryID(scanID, filepath.Join(deps.Home, "Library", "Caches", "Homebrew"))
	sc = structured(t, callTool(t, s, "mcc_get_entry", map[string]any{"entry_id": childID}))
	it = sc["item"].(map[string]any)
	require.Equal(t, true, it["plannable"])
	require.Nil(t, it["plannable_reason"], "no reason when plannable")

	// A report-only entry is not plannable.
	uf := domain.Entry{
		EntryID: "user-file-3", ScanID: scanID, Path: filepath.Join(deps.Home, "Documents", "z"),
		Kind: domain.KindFile, DataClass: domain.ClassUserFile, AllocatedBytes: 1,
	}
	require.NoError(t, deps.Store.InsertEntries(ctx, []domain.Entry{uf}))
	sc = structured(t, callTool(t, s, "mcc_get_entry", map[string]any{"entry_id": uf.EntryID}))
	it = sc["item"].(map[string]any)
	require.Equal(t, false, it["plannable"])
	require.Equal(t, "report_only", it["plannable_reason"])
}

func TestUnknownToolIsAudited(t *testing.T) {
	s, deps, _ := testServer(t)
	resp := rpc(t, s, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "mcc_apply_plan", "arguments": map[string]any{}},
	})
	require.NotNil(t, resp["error"], "an unknown tool must fail")

	calls, err := deps.Store.MCPCalls(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, calls, 1)
	require.Equal(t, "mcc_apply_plan", calls[0].Tool)
	require.Equal(t, "unknown_tool", calls[0].Status)
}

// TestServeStopsOnContextCancel verifies that Serve returns promptly when the
// context is cancelled even though the reader is blocked with no input (the
// Ctrl+C case: a terminal that never sends more JSON-RPC).
func TestServeStopsOnContextCancel(t *testing.T) {
	s, _, _ := testServer(t)

	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, pr, &out) }()

	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled, "cancellation must surface as context.Canceled")
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop after context cancellation")
	}
}
