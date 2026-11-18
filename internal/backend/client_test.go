package backend

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoSendsBearerAndParsesJSON(t *testing.T) {
	var gotAuth, gotPath, gotQuery, gotBody, gotFwd string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotQuery = r.Header.Get("Authorization"), r.URL.Path, r.URL.RawQuery
		gotFwd = r.Header.Get("X-Forwarded-For")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := New(srv.URL, nil)
	raw, err := c.Do(context.Background(), "hoa_abc", http.MethodPost, "/api/agent/v1/messages/read-all", map[string]string{"x": "1"}, map[string]any{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"ok":true}` {
		t.Errorf("raw = %s", raw)
	}
	if gotAuth != "Bearer hoa_abc" || gotPath != "/api/agent/v1/messages/read-all" || gotQuery != "x=1" || gotBody != `{"a":1}` {
		t.Errorf("got %q %q %q %q", gotAuth, gotPath, gotQuery, gotBody)
	}
	if gotFwd != "" {
		t.Errorf("must not send X-Forwarded-For")
	}
}

func TestDoMapsErrorsAndScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="hydronode:write"`)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"INSUFFICIENT_SCOPE","message":"Write access is not granted."}`))
	}))
	defer srv.Close()
	_, err := New(srv.URL, nil).Do(context.Background(), "hoa_x", http.MethodPost, "/api/agent/v1/points/p/value", nil, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if apiErr.Status != 403 || apiErr.Code != "INSUFFICIENT_SCOPE" || apiErr.Scope != "hydronode:write" {
		t.Errorf("got %+v", apiErr)
	}
}

func TestDoRejectsPathTraversalAndRedirects(t *testing.T) {
	redirected := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			redirected = true
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	c := New(srv.URL, nil)
	if _, err := c.Do(context.Background(), "hoa_x", http.MethodGet, "/api/agent/v1/me", nil, nil); err == nil {
		t.Error("expected error on redirect")
	}
	if redirected {
		t.Error("redirect followed")
	}
	if _, err := c.Do(context.Background(), "hoa_x", http.MethodGet, "/api/sensors", nil, nil); err == nil {
		t.Error("non-agent path must be refused")
	}
}

func TestPathSegmentEscapes(t *testing.T) {
	if got := Seg("../../admin"); strings.Contains(got, "/") {
		t.Errorf("Seg = %q", got)
	}
}

func TestDoLimitsResponseSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`"` + strings.Repeat("a", 3<<20) + `"`))
	}))
	defer srv.Close()
	if _, err := New(srv.URL, nil).Do(context.Background(), "hoa_x", http.MethodGet, "/api/agent/v1/me", nil, nil); err == nil {
		t.Error("expected size error")
	}
}
