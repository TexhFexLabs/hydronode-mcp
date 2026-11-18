// Package backend talks to the HydroNode Agent API (/api/agent/v1) on the internal network.
//
// The user's token is forwarded unchanged and only to the configured backend: redirects are
// never followed and only Agent API paths can be requested, so a tool can never turn into a
// generic proxy for the backend.
package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	agentPrefix = "/api/agent/v1/"
	maxResponse = 2 << 20
)

// Error is a non-2xx answer of the backend.
type Error struct {
	Status  int
	Code    string
	Message string
	// Scope is the scope named in WWW-Authenticate on 403 insufficient_scope.
	Scope string
	// RetryAfter is the Retry-After header on 429, in seconds as sent.
	RetryAfter string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("backend %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("backend %d %s", e.Status, e.Code)
}

// Me is the subset of GET /api/agent/v1/me that hnmcp needs.
type Me struct {
	Auth struct {
		Kind   string   `json:"kind"`
		Client string   `json:"client"`
		Scopes []string `json:"scopes"`
	} `json:"auth"`
}

type Client struct {
	base string
	hc   *http.Client
}

// New returns a client for base (scheme://host[:port]). hc may be nil.
func New(base string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: strings.TrimSuffix(base, "/"), hc: &c}
}

// Seg escapes one path segment (ids come from the model and are untrusted).
func Seg(s string) string { return url.PathEscape(s) }

var scopeRe = regexp.MustCompile(`scope="([^"]*)"`)

// Do calls the Agent API and returns the raw JSON body of a 2xx answer.
func (c *Client) Do(ctx context.Context, token, method, path string, query map[string]string, body any) (json.RawMessage, error) {
	if !strings.HasPrefix(path, agentPrefix) || strings.Contains(path, "..") {
		return nil, fmt.Errorf("refusing path %q", path)
	}
	u := c.base + path
	if len(query) > 0 {
		q := url.Values{}
		for k, v := range query {
			if v != "" {
				q.Set(k, v)
			}
		}
		if enc := q.Encode(); enc != "" {
			u += "?" + enc
		}
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponse {
		return nil, errors.New("backend answer too large")
	}
	if resp.StatusCode >= 300 {
		e := &Error{Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
		var eb struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &eb) == nil {
			e.Code, e.Message = eb.Error, eb.Message
		}
		if m := scopeRe.FindStringSubmatch(resp.Header.Get("WWW-Authenticate")); m != nil {
			e.Scope = m[1]
		}
		return nil, e
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return data, nil
}

// Me validates the token and returns who it belongs to.
func (c *Client) Me(ctx context.Context, token string) (*Me, error) {
	raw, err := c.Do(ctx, token, http.MethodGet, agentPrefix+"me", nil, nil)
	if err != nil {
		return nil, err
	}
	var m Me
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("me: %w", err)
	}
	return &m, nil
}

// Ping checks the backend's liveness endpoint (no token).
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/q/health/live", nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("backend live: %d", resp.StatusCode)
	}
	return nil
}
