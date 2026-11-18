// Package testutil holds a fake HydroNode backend for tests.
package testutil

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
)

// Call is one request the fake backend received.
type Call struct {
	Method, Path, Query, Body, Auth string
}

// Backend answers /api/agent/v1/* with canned JSON. Tokens: hoa_read (read only),
// hoa_write (read + write), hoa_all (read + write + automations), anything else 401.
type Backend struct {
	*httptest.Server
	MeCalls atomic.Int32
	mu      sync.Mutex
	calls   []Call
	// Routes maps "METHOD /path" to a JSON body; missing → {"ok":true}.
	Routes map[string]string
}

func NewBackend() *Backend {
	b := &Backend{Routes: map[string]string{}}
	b.Server = httptest.NewServer(http.HandlerFunc(b.serve))
	return b
}

func (b *Backend) Calls() []Call {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Call(nil), b.calls...)
}

func scopesFor(tok string) []string {
	switch tok {
	case "hoa_read":
		return []string{"hydronode:read"}
	case "hoa_write":
		return []string{"hydronode:read", "hydronode:write"}
	case "hoa_all":
		return []string{"hydronode:read", "hydronode:write", "hydronode:automations"}
	}
	return nil
}

func (b *Backend) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	b.mu.Lock()
	b.calls = append(b.calls, Call{r.Method, r.URL.Path, r.URL.RawQuery, string(body), r.Header.Get("Authorization")})
	b.mu.Unlock()
	if r.URL.Path == "/q/health/live" {
		_, _ = w.Write([]byte(`{"status":"UP"}`))
		return
	}
	scopes := scopesFor(tok)
	if scopes == nil {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"UNAUTHORIZED"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/agent/v1/me" {
		b.MeCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"account": map[string]any{"name": "Test User", "timeZone": "Europe/Berlin"},
			"auth":    map[string]any{"kind": "agent", "client": "Test Client", "scopes": scopes, "projects": "all"},
		})
		return
	}
	has := func(s string) bool {
		for _, x := range scopes {
			if x == s {
				return true
			}
		}
		return false
	}
	if r.Method != http.MethodGet {
		need := "hydronode:write"
		if r.Method == http.MethodPost && r.URL.Path == "/api/agent/v1/automations" {
			need = "hydronode:automations"
		}
		if !has(need) {
			w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="`+need+`"`)
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"INSUFFICIENT_SCOPE","message":"Missing scope."}`))
			return
		}
	}
	key := r.Method + " " + r.URL.Path
	if v, ok := b.Routes[key]; ok {
		if strings.HasPrefix(v, "!") { // "!404 {json}" → status + body
			code, rest, _ := strings.Cut(v[1:], " ")
			switch code {
			case "404":
				w.WriteHeader(http.StatusNotFound)
			case "429":
				w.Header().Set("Retry-After", "17")
				w.WriteHeader(http.StatusTooManyRequests)
			case "400":
				w.WriteHeader(http.StatusBadRequest)
			default:
				w.WriteHeader(http.StatusInternalServerError)
			}
			_, _ = w.Write([]byte(rest))
			return
		}
		_, _ = w.Write([]byte(v))
		return
	}
	_, _ = w.Write([]byte(`{"ok":true}`))
}
