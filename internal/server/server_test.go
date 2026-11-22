package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/texhfexlabs/hydronode-mcp/internal/config"
	"github.com/texhfexlabs/hydronode-mcp/internal/testutil"
)

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

func setup(t *testing.T, mutate func(*config.Config)) (*testutil.Backend, *httptest.Server, *bytes.Buffer) {
	t.Helper()
	be := testutil.NewBackend()
	t.Cleanup(be.Close)
	cfg := config.Config{
		BackendURL:     be.URL,
		PublicURL:      "https://int.example.com",
		AuthServer:     "https://int.example.com",
		AllowedOrigins: []string{"https://claude.ai"},
		RatePerMin:     120,
		StepUpHTTP:     true,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	srv := httptest.NewServer(New(cfg, log, "test"))
	t.Cleanup(srv.Close)
	return be, srv, &logs
}

func post(t *testing.T, url, token, origin, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestNoTokenChallenge(t *testing.T) {
	_, srv, _ := setup(t, nil)
	resp := post(t, srv.URL, "", "", initBody)
	if resp.StatusCode != 401 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	want := `Bearer resource_metadata="https://int.example.com/.well-known/oauth-protected-resource/mcp"`
	if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, want) {
		t.Errorf("WWW-Authenticate = %q", got)
	}
}

func TestOtherTokenKindsRejectedWithoutBackend(t *testing.T) {
	be, srv, _ := setup(t, nil)
	for _, tok := range []string{"hat_x", "hrt_x", "hor_x", "eyJhbGciOiJSUzI1NiJ9.e30.sig"} {
		resp := post(t, srv.URL, tok, "", initBody)
		if resp.StatusCode != 401 {
			t.Errorf("%s: status %d", tok, resp.StatusCode)
		}
		if !strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
			t.Errorf("%s: header %q", tok, resp.Header.Get("WWW-Authenticate"))
		}
	}
	if n := len(be.Calls()); n != 0 {
		t.Errorf("backend called %d times", n)
	}
}

func TestInvalidAgentToken(t *testing.T) {
	_, srv, _ := setup(t, nil)
	resp := post(t, srv.URL, "hoa_unknown", "", initBody)
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("status %d header %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
}

func TestValidTokenInitializesAndCachesMe(t *testing.T) {
	be, srv, _ := setup(t, nil)
	for i := 0; i < 2; i++ {
		resp := post(t, srv.URL, "hoa_read", "", initBody)
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("status %d %s", resp.StatusCode, b)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("content type %q", ct)
		}
		if resp.Header.Get("Mcp-Session-Id") != "" {
			t.Error("stateless server must not issue sessions")
		}
	}
	if n := be.MeCalls.Load(); n != 1 {
		t.Errorf("me calls %d", n)
	}
}

func TestOriginAllowlist(t *testing.T) {
	_, srv, _ := setup(t, nil)
	if resp := post(t, srv.URL, "hoa_read", "https://evil.example.com", initBody); resp.StatusCode != 403 {
		t.Errorf("evil origin: %d", resp.StatusCode)
	}
	if resp := post(t, srv.URL, "hoa_read", "https://claude.ai", initBody); resp.StatusCode != 200 {
		t.Errorf("allowed origin: %d", resp.StatusCode)
	}
}

func TestBodyLimit(t *testing.T) {
	_, srv, _ := setup(t, nil)
	big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("a", 65*1024) + `"}}`
	if resp := post(t, srv.URL, "hoa_read", "", big); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d", resp.StatusCode)
	}
}

func TestRateLimitPerToken(t *testing.T) {
	_, srv, _ := setup(t, func(c *config.Config) { c.RatePerMin = 2 })
	for i := 0; i < 2; i++ {
		if resp := post(t, srv.URL, "hoa_read", "", initBody); resp.StatusCode != 200 {
			t.Fatalf("req %d: %d", i, resp.StatusCode)
		}
	}
	resp := post(t, srv.URL, "hoa_read", "", initBody)
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Errorf("status %d retry %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	// Another token has its own budget.
	if resp := post(t, srv.URL, "hoa_write", "", initBody); resp.StatusCode != 200 {
		t.Errorf("other token: %d", resp.StatusCode)
	}
}

func TestFailedTokensLimitedPerClientIP(t *testing.T) {
	be, srv, _ := setup(t, nil)
	send := func(tok, ip string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(initBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("CF-Connecting-IP", ip)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	// A client that is already verified (cached) keeps working from the same shared address.
	if r := send("hoa_read", "203.0.113.7"); r.StatusCode != 200 {
		t.Fatalf("valid token: %d", r.StatusCode)
	}
	for i := 0; i < failedAuthPerMin; i++ {
		if r := send(fmt.Sprintf("hoa_random%d", i), "203.0.113.7"); r.StatusCode != 401 {
			t.Fatalf("try %d: %d", i, r.StatusCode)
		}
	}
	before := len(be.Calls())
	r := send("hoa_randomX", "203.0.113.7")
	if r.StatusCode != http.StatusTooManyRequests || r.Header.Get("Retry-After") == "" {
		t.Errorf("status %d retry %q", r.StatusCode, r.Header.Get("Retry-After"))
	}
	if len(be.Calls()) != before {
		t.Error("blocked request still reached the backend")
	}
	if r := send("hoa_read", "203.0.113.7"); r.StatusCode != 200 {
		t.Errorf("cached valid token blocked: %d", r.StatusCode)
	}
	// Other addresses are not affected.
	if r := send("hoa_other", "198.51.100.1"); r.StatusCode != 401 {
		t.Errorf("other ip: %d", r.StatusCode)
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	_, srv, _ := setup(t, nil)
	resp, err := http.Get(srv.URL + "/.well-known/oauth-protected-resource/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m["resource"] != "https://int.example.com/mcp" {
		t.Errorf("resource %v", m["resource"])
	}
	as, _ := m["authorization_servers"].([]any)
	if len(as) != 1 || as[0] != "https://int.example.com" {
		t.Errorf("authorization_servers %v", m["authorization_servers"])
	}
	if sc, _ := m["scopes_supported"].([]any); len(sc) != 3 {
		t.Errorf("scopes %v", m["scopes_supported"])
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("metadata must be readable cross-origin")
	}
}

func TestHealth(t *testing.T) {
	_, srv, _ := setup(t, nil)
	for _, p := range []string{"/q/health/live", "/q/health/ready"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
}

func TestUnknownPath404(t *testing.T) {
	_, srv, _ := setup(t, nil)
	// The root metadata document would name the origin as resource (RFC 9728 §3.1); hnmcp only
	// serves the path-suffixed one for /mcp.
	for _, p := range []string{"/api/agent/v1/me", "/.well-known/oauth-protected-resource"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("%s: status %d", p, resp.StatusCode)
		}
	}
}

func TestLogsNeverContainTokens(t *testing.T) {
	_, srv, logs := setup(t, nil)
	post(t, srv.URL, "hoa_read", "", initBody)
	post(t, srv.URL, "hoa_unknown", "", initBody)
	post(t, srv.URL, "hoa_read", "", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"overview","arguments":{}}}`)
	time.Sleep(10 * time.Millisecond)
	if strings.Contains(logs.String(), "hoa_") {
		t.Errorf("token in logs: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"tool":"overview"`) {
		t.Errorf("tool call not logged: %s", logs.String())
	}
}

func TestAccessLogNamesMethodToolAndClientButNoArguments(t *testing.T) {
	_, srv, logs := setup(t, nil)
	post(t, srv.URL, "hoa_read", "https://claude.ai", initBody)
	resp := post(t, srv.URL, "hoa_read", "https://claude.ai",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"find","arguments":{"query":"secret-greenhouse-name"}}}`)
	io.Copy(io.Discard, resp.Body)

	type line struct {
		Msg    string `json:"msg"`
		RPC    string `json:"rpc"`
		Tool   string `json:"tool"`
		Client string `json:"client"`
	}
	var rpcs, tools, clients []string
	for _, l := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var e line
		if json.Unmarshal([]byte(l), &e) != nil {
			continue
		}
		if e.Msg == "request" {
			rpcs = append(rpcs, e.RPC)
			if e.Client != "" {
				clients = append(clients, e.Client)
			}
		}
		if e.Msg == "tool start" {
			tools = append(tools, e.Tool)
		}
	}
	if fmt.Sprint(rpcs) != "[initialize tools/call]" {
		t.Errorf("rpc fields %v", rpcs)
	}
	if fmt.Sprint(clients) != "[t 1]" {
		t.Errorf("client fields %v", clients)
	}
	if fmt.Sprint(tools) != "[find]" {
		t.Errorf("tool start lines %v", tools)
	}
	if strings.Contains(logs.String(), "secret-greenhouse-name") {
		t.Error("tool arguments must never reach the log")
	}
}

func TestDescribeUntrustedBodies(t *testing.T) {
	cases := map[string]rpcInfo{
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`: {Method: "batch"},
		`{"jsonrpc":"2.0","id":3,"result":{}}`:       {Method: "response"},
		`not json`:                                   {Method: "?"},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"a\nb"}}`: {Method: "tools/call", Tool: "ab"},
	}
	for body, want := range cases {
		if got := describe([]byte(body)); got != want {
			t.Errorf("%s: got %+v want %+v", body, got, want)
		}
	}
}
