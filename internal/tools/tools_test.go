package tools_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/texhfexlabs/hydronode-mcp/internal/config"
	"github.com/texhfexlabs/hydronode-mcp/internal/server"
	"github.com/texhfexlabs/hydronode-mcp/internal/testutil"
	"github.com/texhfexlabs/hydronode-mcp/internal/tools"
)

type bearer struct{ tok string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}

type env struct {
	be  *testutil.Backend
	url string
}

func newEnv(t *testing.T) env {
	t.Helper()
	be := testutil.NewBackend()
	t.Cleanup(be.Close)
	cfg := config.Config{BackendURL: be.URL, PublicURL: "https://int.example.com", AuthServer: "https://int.example.com", RatePerMin: 1000, StepUpHTTP: true}
	srv := httptest.NewServer(server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), "test"))
	t.Cleanup(srv.Close)
	return env{be, srv.URL}
}

func (e env) session(t *testing.T, tok string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	s, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   e.url + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{tok}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func text(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func lastCall(t *testing.T, be *testutil.Backend) testutil.Call {
	t.Helper()
	calls := be.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Path != "/api/agent/v1/me" {
			return calls[i]
		}
	}
	t.Fatal("no backend call")
	return testutil.Call{}
}

func TestListToolsAndAnnotations(t *testing.T) {
	e := newEnv(t)
	s := e.session(t, "hoa_read")
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		got[tl.Name] = tl
	}
	if len(got) != len(tools.Names) || len(got) != 19 {
		t.Fatalf("tools = %d", len(got))
	}
	for _, name := range tools.ReadNames {
		tl := got[name]
		if tl == nil || tl.Annotations == nil || !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s: missing readOnlyHint", name)
		}
		if tl != nil && tl.OutputSchema == nil {
			t.Errorf("%s: missing output schema", name)
		}
	}
	for _, name := range []string{"set_value", "send_command", "toggle_automation", "create_automation"} {
		a := got[name].Annotations
		if a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint {
			t.Errorf("%s: want destructiveHint", name)
		}
	}
	for _, name := range []string{"toggle_automation", "mark_messages"} {
		if a := got[name].Annotations; a == nil || !a.IdempotentHint {
			t.Errorf("%s: want idempotentHint", name)
		}
	}
	for name, tl := range got {
		if len(tl.Description) < 20 {
			t.Errorf("%s: description too short", name)
		}
	}
}

func TestInstructionsAndPrompts(t *testing.T) {
	if n := len(tools.Instructions); n > 600 || n < 100 {
		t.Errorf("instructions length %d", n)
	}
	e := newEnv(t)
	s := e.session(t, "hoa_read")
	if s.InitializeResult().Instructions != tools.Instructions {
		t.Error("instructions not sent")
	}
	res, err := s.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Prompts) != 4 {
		t.Fatalf("prompts = %d", len(res.Prompts))
	}
	p, err := s.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "why_offline", Arguments: map[string]string{"device": "Greenhouse"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Messages) == 0 || !strings.Contains(p.Messages[0].Content.(*mcp.TextContent).Text, "Greenhouse") {
		t.Error("prompt does not use argument")
	}
}

func TestReadToolsCallBackend(t *testing.T) {
	e := newEnv(t)
	e.be.Routes["GET /api/agent/v1/overview"] = `{"devices":{"total":3,"healthy":2,"offline":1},"openAnomalies":1,"unreadMessages":2,"needsAttention":[]}`
	s := e.session(t, "hoa_read")
	cases := []struct {
		tool  string
		args  map[string]any
		path  string
		query string
	}{
		{"overview", nil, "/api/agent/v1/overview", ""},
		{"find", map[string]any{"query": "green house"}, "/api/agent/v1/find", "q=green+house"},
		{"get_sensor", map[string]any{"sensor_id": "6f1c2a3b-0000-4000-8000-000000000001"}, "/api/agent/v1/sensors/6f1c2a3b-0000-4000-8000-000000000001", ""},
		{"summarize_history", map[string]any{"point_id": "p1", "from": "7d"}, "/api/agent/v1/points/p1/summary", "from=7d"},
		{"find_periods", map[string]any{"point_id": "p1", "below": 30.5}, "/api/agent/v1/points/p1/periods", "below=30.5"},
		{"compare", map[string]any{"point_ids": []string{"a", "b"}}, "/api/agent/v1/compare", "points=a%2Cb"},
		{"compare", map[string]any{"point_ids": []string{"a"}, "shift": "7d"}, "/api/agent/v1/compare", "points=a&shift=7d"},
		{"list_anomalies", map[string]any{"state": "open"}, "/api/agent/v1/anomalies", "state=open"},
		{"get_anomaly", map[string]any{"anomaly_id": "x1"}, "/api/agent/v1/anomalies/x1", ""},
		{"list_messages", map[string]any{"unread_only": true}, "/api/agent/v1/messages", "unread=true"},
		{"device_health", nil, "/api/agent/v1/devices/health", ""},
		{"console", map[string]any{"sensor_id": "s1", "rejected_only": true, "limit": 10}, "/api/agent/v1/console", "limit=10&rejected=true&sensor=s1"},
		{"public_stations", map[string]any{"near": "52.5,13.4", "radius_km": 5}, "/api/agent/v1/stations/public", "near=52.5%2C13.4&radiusKm=5"},
		{"list_automations", nil, "/api/agent/v1/automations", ""},
		{"list_automations", map[string]any{"automation_id": "a9"}, "/api/agent/v1/automations/a9", ""},
	}
	for _, c := range cases {
		res := call(t, s, c.tool, c.args)
		if res.IsError {
			t.Errorf("%s: error %s", c.tool, text(res))
			continue
		}
		got := lastCall(t, e.be)
		if got.Method != "GET" || got.Path != c.path || got.Query != c.query || got.Auth != "Bearer hoa_read" {
			t.Errorf("%s: got %s %s?%s", c.tool, got.Method, got.Path, got.Query)
		}
		if text(res) == "" || res.StructuredContent == nil {
			t.Errorf("%s: missing text or structured content", c.tool)
		}
	}
	res := call(t, s, "overview", nil)
	if !strings.Contains(text(res), "3 devices") {
		t.Errorf("overview summary: %q", text(res))
	}
}

func TestRejectsBadIDsWithoutBackendCall(t *testing.T) {
	e := newEnv(t)
	s := e.session(t, "hoa_read")
	before := len(e.be.Calls())
	for _, id := range []string{"../admin", "a/b", "", strings.Repeat("x", 80), "a b"} {
		res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_sensor", Arguments: map[string]any{"sensor_id": id}})
		if err == nil && !res.IsError {
			t.Errorf("%q accepted", id)
		}
	}
	if after := len(e.be.Calls()); after != before {
		t.Errorf("backend called %d times", after-before)
	}
}

func TestWriteTools(t *testing.T) {
	e := newEnv(t)
	s := e.session(t, "hoa_all")
	cases := []struct {
		tool   string
		args   map[string]any
		method string
		path   string
		query  string
		body   string
	}{
		{"set_value", map[string]any{"point_id": "p1", "value": true, "wait_seconds": 10}, "POST", "/api/agent/v1/points/p1/value", "", `{"value":true,"waitSeconds":10}`},
		{"send_command", map[string]any{"sensor_id": "s1", "name": "relay1", "type": "BOOL", "value": "true"}, "POST", "/api/agent/v1/sensors/s1/commands", "", `{"name":"relay1","type":"BOOL","value":"true"}`},
		{"log_value", map[string]any{"point_id": "p2", "value": 6.8}, "POST", "/api/agent/v1/points/p2/manual", "", `{"value":6.8}`},
		{"mark_messages", map[string]any{"all": true, "action": "read"}, "POST", "/api/agent/v1/messages/read-all", "", ""},
		{"mark_messages", map[string]any{"message_ids": []string{"m1"}, "action": "archive"}, "POST", "/api/agent/v1/messages/m1/archive", "", ""},
		{"toggle_automation", map[string]any{"automation_id": "a1", "enabled": false}, "PUT", "/api/agent/v1/automations/a1/enabled", "", `{"enabled":false}`},
		{"create_automation", map[string]any{"definition": map[string]any{"name": "Hot"}}, "POST", "/api/agent/v1/automations", "dryRun=true", `{"name":"Hot"}`},
		{"create_automation", map[string]any{"definition": map[string]any{"name": "Hot"}, "dry_run": false}, "POST", "/api/agent/v1/automations", "dryRun=false", `{"name":"Hot"}`},
	}
	for _, c := range cases {
		res := call(t, s, c.tool, c.args)
		if res.IsError {
			t.Errorf("%s: error %s", c.tool, text(res))
			continue
		}
		got := lastCall(t, e.be)
		if got.Method != c.method || got.Path != c.path || got.Query != c.query || got.Body != c.body {
			t.Errorf("%s: got %s %s?%s %s", c.tool, got.Method, got.Path, got.Query, got.Body)
		}
	}
}

func TestWaitSecondsClamped(t *testing.T) {
	e := newEnv(t)
	s := e.session(t, "hoa_write")
	call(t, s, "set_value", map[string]any{"point_id": "p1", "value": 1, "wait_seconds": 60})
	if got := lastCall(t, e.be).Body; got != `{"value":1,"waitSeconds":20}` {
		t.Errorf("body %s", got)
	}
}

func TestBackendErrorsBecomeToolErrors(t *testing.T) {
	e := newEnv(t)
	e.be.Routes["GET /api/agent/v1/sensors/gone"] = `!404 {"error":"NOT_FOUND"}`
	e.be.Routes["GET /api/agent/v1/devices/health"] = `!429 {"error":"RATE_LIMITED"}`
	e.be.Routes["GET /api/agent/v1/points/p1/summary"] = `!400 {"error":"INVALID_RANGE","message":"from is after to."}`
	s := e.session(t, "hoa_read")
	if r := call(t, s, "get_sensor", map[string]any{"sensor_id": "gone"}); !r.IsError || !strings.Contains(strings.ToLower(text(r)), "not found") {
		t.Errorf("404: %q", text(r))
	}
	if r := call(t, s, "device_health", nil); !r.IsError || !strings.Contains(text(r), "17 seconds") {
		t.Errorf("429: %q", text(r))
	}
	if r := call(t, s, "summarize_history", map[string]any{"point_id": "p1"}); !r.IsError || !strings.Contains(text(r), "from is after to.") {
		t.Errorf("400: %q", text(r))
	}
}

type rpcResult struct {
	status int
	header string
	isErr  bool
	text   string
}

func rawCall(t *testing.T, url, tok, tool string, args map[string]any) rpcResult {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	req, _ := http.NewRequest(http.MethodPost, url+"/mcp", strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rpc struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		t.Fatal(err)
	}
	r := rpcResult{status: resp.StatusCode, header: resp.Header.Get("WWW-Authenticate"), isErr: rpc.Result.IsError}
	if len(rpc.Result.Content) > 0 {
		r.text = rpc.Result.Content[0].Text
	}
	return r
}

// Missing scope: the tool explains it and the HTTP answer carries the step-up challenge.
func TestWriteWithoutScopeStepUp(t *testing.T) {
	e := newEnv(t)
	r := rawCall(t, e.url, "hoa_read", "set_value", map[string]any{"point_id": "p1", "value": true})
	if r.status != 403 {
		t.Errorf("status %d", r.status)
	}
	if !strings.Contains(r.header, `error="insufficient_scope"`) || !strings.Contains(r.header, `scope="hydronode:read hydronode:write"`) || !strings.Contains(r.header, "resource_metadata=") {
		t.Errorf("WWW-Authenticate %q", r.header)
	}
	if !r.isErr || !strings.Contains(r.text, "Write access is not granted. Allow it under Connected apps: https://int.example.com/account/security#connected-apps") {
		t.Errorf("result %+v", r)
	}

	r = rawCall(t, e.url, "hoa_write", "create_automation", map[string]any{"definition": map[string]any{}})
	if r.status != 403 || !strings.Contains(r.header, `scope="hydronode:read hydronode:write hydronode:automations"`) {
		t.Errorf("automations status %d header %q", r.status, r.header)
	}
	if !r.isErr || !strings.Contains(r.text, "Creating automations is not granted") {
		t.Errorf("automations: %+v", r)
	}
}

// With HTTP step-up off the answer stays 200 and only the tool text explains it.
func TestStepUpHTTPOff(t *testing.T) {
	be := testutil.NewBackend()
	t.Cleanup(be.Close)
	cfg := config.Config{BackendURL: be.URL, PublicURL: "https://int.example.com", AuthServer: "https://int.example.com", RatePerMin: 1000, StepUpHTTP: false}
	srv := httptest.NewServer(server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), "test"))
	t.Cleanup(srv.Close)
	r := rawCall(t, srv.URL, "hoa_read", "set_value", map[string]any{"point_id": "p1", "value": true})
	if r.status != 200 || r.header != "" || !r.isErr || !strings.Contains(r.text, "Write access is not granted") {
		t.Errorf("got %+v", r)
	}
}
