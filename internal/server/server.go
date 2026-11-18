// Package server is the HTTP side of hnmcp: /mcp (Streamable HTTP, stateless, JSON answers),
// RFC 9728 metadata and health. Order of checks for /mcp: origin, body size, failed-token limit
// per address, bearer token, rate limit per token, timeout, then the MCP handler.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/texhfexlabs/hydronode-mcp/internal/auth"
	"github.com/texhfexlabs/hydronode-mcp/internal/backend"
	"github.com/texhfexlabs/hydronode-mcp/internal/config"
	"github.com/texhfexlabs/hydronode-mcp/internal/tools"
)

const (
	maxBody        = 64 << 10
	requestTimeout = 20 * time.Second
	// failedAuthPerMin caps rejected tokens per client address. Above it, only tokens that are
	// already verified (cached) get through, so random tokens cannot drive backend lookups.
	// High on purpose: big clients share egress addresses and every expired token is one failure.
	failedAuthPerMin = 300
)

var Scopes = []string{"hydronode:read", "hydronode:write", "hydronode:automations"}

type ctxKey int

const (
	keyPrincipal ctxKey = iota
	keyStepUp
)

// New builds the handler. version shows up in the MCP server info.
func New(cfg config.Config, log *slog.Logger, version string) http.Handler {
	bc := backend.New(cfg.BackendURL, nil)
	verifier := auth.NewVerifier(bc.Me)

	ms := mcp.NewServer(&mcp.Implementation{Name: "hydronode", Title: "HydroNode", Version: version},
		&mcp.ServerOptions{Instructions: tools.Instructions})
	tools.Register(ms, tools.Deps{Backend: bc, ConnectedAppsURL: cfg.ConnectedAppsURL(), Log: log})

	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: maxBody,
			// Traefik in front sets Host to the public name; the localhost rebinding check would
			// reject legitimate traffic there. Origin is checked below instead.
			DisableLocalhostProtection: true})

	// The SDK hands TokenInfo to every tool; our middleware already verified the token.
	passthrough := sdkauth.RequireBearerToken(func(ctx context.Context, _ string, r *http.Request) (*sdkauth.TokenInfo, error) {
		p, _ := r.Context().Value(keyPrincipal).(*auth.Principal)
		st, _ := r.Context().Value(keyStepUp).(*tools.StepUp)
		if p == nil {
			return nil, sdkauth.ErrInvalidToken
		}
		return tools.TokenInfo(p, st, time.Now().Add(time.Minute)), nil
	}, nil)

	limiter := newLimiter(cfg.RatePerMin)
	h := &handler{cfg: cfg, log: log, verifier: verifier, limiter: limiter, failed: newLimiter(failedAuthPerMin)}
	inner := http.TimeoutHandler(passthrough(stream), requestTimeout, `{"jsonrpc":"2.0","error":{"code":-32000,"message":"timeout"}}`)

	mux := http.NewServeMux()
	mux.Handle("/mcp", h.mcp(inner))
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", h.metadata)
	mux.HandleFunc("GET /q/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"UP"}`))
	})
	mux.HandleFunc("GET /q/health/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := bc.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"DOWN","reason":"backend unreachable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"UP"}`))
	})
	return securityHeaders(mux)
}

type handler struct {
	cfg      config.Config
	log      *slog.Logger
	verifier *auth.Verifier
	limiter  *limiter
	failed   *limiter
}

// clientIP is the caller's address as Cloudflare saw it. Requests only reach hnmcp through the
// Cloudflare tunnel and Traefik, which set these headers; RemoteAddr is the fallback for tests.
func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (h *handler) metadata(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource":                 h.cfg.Resource(),
		"authorization_servers":    []string{h.cfg.AuthServer},
		"scopes_supported":         Scopes,
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "HydroNode",
		"resource_documentation":   h.cfg.AuthServer + "/docs/guide/mcp",
	})
}

func (h *handler) challenge(w http.ResponseWriter, status int, errCode, scope string) {
	v := fmt.Sprintf("Bearer resource_metadata=%q", h.cfg.ResourceMetadataURL())
	if errCode != "" {
		v += fmt.Sprintf(", error=%q", errCode)
	}
	if scope != "" {
		v += fmt.Sprintf(", scope=%q", scope)
	}
	w.Header().Set("WWW-Authenticate", v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, or(errCode, "unauthorized"))
}

func (h *handler) cors(w http.ResponseWriter, origin string) {
	if origin == "" {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Session-Id, Mcp-Protocol-Version")
}

func (h *handler) mcp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		origin := r.Header.Get("Origin")
		if origin != "" && !slices.Contains(h.cfg.AllowedOrigins, strings.TrimSuffix(origin, "/")) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		h.cors(w, origin)
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Mcp-Protocol-Version, Mcp-Session-Id")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.ContentLength > maxBody {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)

		authz := r.Header.Get("Authorization")
		ip := clientIP(r)
		if authz != "" && h.failed.over(ip, time.Now()) && !h.verifier.Cached(authz) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many failed sign-ins", http.StatusTooManyRequests)
			h.access(r, http.StatusTooManyRequests, start, "")
			return
		}
		p, err := h.verifier.Verify(r.Context(), authz)
		switch {
		case errors.Is(err, auth.ErrNoToken):
			h.challenge(w, http.StatusUnauthorized, "", "")
			h.access(r, http.StatusUnauthorized, start, "")
			return
		case errors.Is(err, auth.ErrUnavailable):
			w.Header().Set("Retry-After", "5")
			http.Error(w, "backend unavailable", http.StatusServiceUnavailable)
			h.access(r, http.StatusServiceUnavailable, start, "")
			return
		case err != nil:
			h.failed.allow(ip, time.Now())
			h.challenge(w, http.StatusUnauthorized, "invalid_token", "")
			h.access(r, http.StatusUnauthorized, start, "")
			return
		}
		if ok, retry := h.limiter.allow(p.Hash, time.Now()); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			h.access(r, http.StatusTooManyRequests, start, p.Hash)
			return
		}
		st := &tools.StepUp{}
		ctx := context.WithValue(context.WithValue(r.Context(), keyPrincipal, p), keyStepUp, st)
		sw := &stepUpWriter{ResponseWriter: w, st: st, h: h}
		next.ServeHTTP(sw, r.WithContext(ctx))
		h.access(r, sw.status, start, p.Hash)
	})
}

func (h *handler) access(r *http.Request, status int, start time.Time, grant string) {
	h.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", status, "ms", time.Since(start).Milliseconds(), "grant", grant)
}

// stepUpWriter turns a 200 into 403 + insufficient_scope when a tool reported a missing scope.
// The JSON-RPC body (with the explaining tool error) is still sent.
type stepUpWriter struct {
	http.ResponseWriter
	st     *tools.StepUp
	h      *handler
	status int
	wrote  bool
}

func (w *stepUpWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	if scope := w.st.Scope(); scope != "" && code == http.StatusOK && w.h.cfg.StepUpHTTP {
		need := []string{"hydronode:read", "hydronode:write"}
		if scope == "hydronode:automations" {
			need = append(need, scope)
		}
		v := fmt.Sprintf("Bearer resource_metadata=%q, error=\"insufficient_scope\", scope=%q", w.h.cfg.ResourceMetadataURL(), strings.Join(need, " "))
		w.Header().Set("WWW-Authenticate", v)
		code = http.StatusForbidden
	}
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *stepUpWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *stepUpWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func or(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// limiter is a fixed one-minute window per token hash, in memory. The backend has its own
// limits per grant; this one protects hnmcp and the backend from a flood through one token.
type limiter struct {
	perMin int
	mu     sync.Mutex
	window time.Time
	counts map[string]int
}

func newLimiter(perMin int) *limiter {
	return &limiter{perMin: perMin, counts: map[string]int{}}
}

// over reports whether key has used up this minute's budget, without counting.
func (l *limiter) over(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Truncate(time.Minute).Equal(l.window) {
		return false
	}
	return l.counts[key] >= l.perMin
}

func (l *limiter) allow(key string, now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := now.Truncate(time.Minute)
	if !w.Equal(l.window) {
		l.window = w
		clear(l.counts)
	}
	if l.counts[key] >= l.perMin {
		return false, int(w.Add(time.Minute).Sub(now).Seconds()) + 1
	}
	l.counts[key]++
	return true, 0
}
