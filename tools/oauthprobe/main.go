// Command oauthprobe connects to the HydroNode MCP server the way a third-party MCP client does
// (Superhuman Go, Cursor, MCP Inspector, ...): dynamic client registration, authorization code with
// PKCE in the browser, token, then initialize and tools/list on /mcp.
//
// It only uses the standard library and keeps every secret in memory. Run it to see where a client
// gets stuck, for example without the RFC 8707 resource parameter:
//
//	go run ./tools/oauthprobe                 # like Claude: sends resource
//	go run ./tools/oauthprobe -no-resource    # like a client that omits it
//	go run ./tools/oauthprobe -call overview
//	go run ./tools/oauthprobe -resource https://evil.example/mcp        # foreign resource at authorize
//	go run ./tools/oauthprobe -token-resource https://evil.example/mcp  # foreign resource at token
//
// The client shows up as "Unverified app" on the consent page (loopback redirect). Revoke the grant
// afterwards under Account › Sessions & security › Connected apps.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const userAgent = "HydroNodeOAuthProbe/1.0"

func main() {
	server := flag.String("server", "https://hydronode.tech", "HydroNode base URL")
	noResource := flag.Bool("no-resource", false, "omit the resource parameter (RFC 8707)")
	scope := flag.String("scope", "hydronode:read", "requested scope")
	name := flag.String("name", "HydroNode OAuth probe", "client name shown on the consent page")
	call := flag.String("call", "", "optional tool to call with empty arguments after tools/list")
	resource := flag.String("resource", "", "resource to send at authorize instead of <server>/mcp")
	tokenResource := flag.String("token-resource", "", "resource to send at the token endpoint instead of the authorize one")
	flag.Parse()

	if err := run(*server, !*noResource, *resource, *tokenResource, *scope, *name, *call); err != nil {
		fmt.Fprintln(os.Stderr, "\nFAILED:", err)
		os.Exit(1)
	}
}

func run(server string, withResource bool, resource, tokenResource, scope, name, call string) error {
	base := strings.TrimRight(server, "/")
	mcpURL := base + "/mcp"
	if resource == "" {
		resource = mcpURL
	}
	if tokenResource == "" {
		tokenResource = resource
	}
	client := &http.Client{Timeout: 30 * time.Second}

	step("1. Authorization server metadata")
	var meta struct {
		Authorization string `json:"authorization_endpoint"`
		Token         string `json:"token_endpoint"`
		Registration  string `json:"registration_endpoint"`
	}
	if err := getJSON(client, base+"/.well-known/oauth-authorization-server", &meta); err != nil {
		return err
	}
	fmt.Println("   authorize:", meta.Authorization)
	fmt.Println("   token:    ", meta.Token)
	fmt.Println("   register: ", meta.Registration)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", listener.Addr().(*net.TCPAddr).Port)

	step("2. Dynamic client registration")
	var reg struct {
		ClientID string `json:"client_id"`
	}
	body := map[string]any{
		"client_name":                name,
		"redirect_uris":              []string{redirect},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	if err := postJSON(client, meta.Registration, body, &reg); err != nil {
		return err
	}
	fmt.Println("   client_id:", reg.ClientID)

	step("3. Authorize in the browser")
	verifier := random(32)
	sum := sha256.Sum256([]byte(verifier))
	state := random(16)
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {reg.ClientID},
		"redirect_uri":          {redirect},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"scope":                 {scope},
	}
	if withResource {
		q.Set("resource", resource)
		fmt.Println("   resource:", resource)
	} else {
		fmt.Println("   resource: (omitted)")
	}
	authURL := meta.Authorization + "?" + q.Encode()
	fmt.Println("   open:", authURL)
	openBrowser(authURL)

	code, err := waitForCallback(listener, state)
	if err != nil {
		return err
	}
	fmt.Println("   got a code")

	step("4. Token")
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirect},
		"client_id":     {reg.ClientID},
		"code_verifier": {verifier},
	}
	if withResource {
		form.Set("resource", tokenResource)
		fmt.Println("   resource:", tokenResource)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := postForm(client, meta.Token, form, &tok); err != nil {
		return err
	}
	fmt.Printf("   access token %s… scope=%q expires_in=%d\n", prefix(tok.AccessToken), tok.Scope, tok.ExpiresIn)

	step("5. MCP initialize")
	session := ""
	init := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "oauthprobe", "version": "1.0"},
		},
	}
	res, session, err := rpc(client, mcpURL, tok.AccessToken, session, init)
	if err != nil {
		return err
	}
	var initRes struct {
		Result struct {
			ServerInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	_ = json.Unmarshal(res, &initRes)
	fmt.Printf("   server %s %s, protocol %s\n", initRes.Result.ServerInfo.Name,
		initRes.Result.ServerInfo.Version, initRes.Result.ProtocolVersion)
	_, _, _ = rpc(client, mcpURL, tok.AccessToken, session,
		map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})

	step("6. MCP tools/list")
	res, _, err = rpc(client, mcpURL, tok.AccessToken, session,
		map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	if err != nil {
		return err
	}
	var list struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	_ = json.Unmarshal(res, &list)
	for _, t := range list.Result.Tools {
		fmt.Println("   -", t.Name)
	}
	fmt.Printf("   %d tools\n", len(list.Result.Tools))

	if call != "" {
		step("7. MCP tools/call " + call)
		res, _, err = rpc(client, mcpURL, tok.AccessToken, session, map[string]any{
			"jsonrpc": "2.0", "id": 3, "method": "tools/call",
			"params": map[string]any{"name": call, "arguments": map[string]any{}},
		})
		if err != nil {
			return err
		}
		fmt.Println("  ", truncate(string(res), 1500))
	}

	fmt.Println("\nOK. Revoke the grant under Account › Sessions & security › Connected apps.")
	return nil
}

func step(s string) { fmt.Println("\n" + s) }

// waitForCallback serves one request on the loopback redirect and returns the code.
func waitForCallback(l net.Listener, state string) (string, error) {
	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		var res result
		switch {
		case q.Get("error") != "":
			res.err = fmt.Errorf("authorize refused: error=%s description=%q", q.Get("error"), q.Get("error_description"))
		case q.Get("state") != state:
			res.err = errors.New("state does not match")
		case q.Get("code") == "":
			res.err = errors.New("no code in the callback")
		default:
			res.code = q.Get("code")
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if res.err != nil {
			fmt.Fprintln(w, "HydroNode OAuth probe: "+res.err.Error())
		} else {
			fmt.Fprintln(w, "HydroNode OAuth probe: code received, you can close this tab.")
		}
		select {
		case done <- res:
		default:
		}
	})
	go func() { _ = srv.Serve(l) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	fmt.Println("   waiting for the browser (5 min) …")
	select {
	case r := <-done:
		return r.code, r.err
	case <-time.After(5 * time.Minute):
		return "", errors.New("no callback within 5 minutes")
	}
}

// rpc posts one JSON-RPC message to the Streamable HTTP endpoint and returns the response message
// (plain JSON or the first SSE data event) and the session id, if the server set one.
func rpc(c *http.Client, endpoint, token, session string, msg any) ([]byte, string, error) {
	b, _ := json.Marshal(msg)
	req, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	req.Header.Set("User-Agent", userAgent)
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, session, err
	}
	defer resp.Body.Close()
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		session = s
	}
	if resp.StatusCode == http.StatusAccepted {
		return nil, session, nil
	}
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, session, fmt.Errorf("POST %s: %s %s (WWW-Authenticate: %s)", endpoint, resp.Status,
			truncate(string(raw), 500), resp.Header.Get("WWW-Authenticate"))
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data:"); ok {
				return []byte(strings.TrimSpace(data)), session, nil
			}
		}
		return nil, session, errors.New("event stream ended without data")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	return raw, session, err
}

func getJSON(c *http.Client, u string, out any) error {
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	return do(c, req, out)
}

func postJSON(c *http.Client, u string, body, out any) error {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, u, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	return do(c, req, out)
}

func postForm(c *http.Client, u string, form url.Values, out any) error {
	req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	return do(c, req, out)
}

func do(c *http.Client, req *http.Request, out any) error {
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s %s", req.Method, req.URL.Redacted(), resp.Status, truncate(string(raw), 500))
	}
	return json.Unmarshal(raw, out)
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start()
}

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func prefix(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
