package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func base() map[string]string {
	return map[string]string{
		"HNMCP_BACKEND_URL": "http://hnbackend:8080",
		"HNMCP_PUBLIC_URL":  "https://int.example.com",
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" {
		t.Errorf("listen = %q", c.Listen)
	}
	if got := c.Resource(); got != "https://int.example.com/mcp" {
		t.Errorf("resource = %q", got)
	}
	if got := c.ResourceMetadataURL(); got != "https://int.example.com/.well-known/oauth-protected-resource/mcp" {
		t.Errorf("metadata = %q", got)
	}
	if c.AuthServer != "https://int.example.com" {
		t.Errorf("auth server = %q", c.AuthServer)
	}
	if !c.StepUpHTTP {
		t.Error("step-up over HTTP must default to on")
	}
	if c.RatePerMin != 120 {
		t.Errorf("rate = %d", c.RatePerMin)
	}
	if len(c.AllowedOrigins) == 0 || c.AllowedOrigins[0] != "https://claude.ai" {
		t.Errorf("origins = %v", c.AllowedOrigins)
	}
	if got := c.ConnectedAppsURL(); got != "https://int.example.com/account/security#connected-apps" {
		t.Errorf("connected apps = %q", got)
	}
}

func TestLoadTrimsTrailingSlash(t *testing.T) {
	m := base()
	m["HNMCP_PUBLIC_URL"] = "https://int.example.com/"
	m["HNMCP_BACKEND_URL"] = "http://hnbackend:8080/"
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.Resource() != "https://int.example.com/mcp" || c.BackendURL != "http://hnbackend:8080" {
		t.Errorf("got %q %q", c.Resource(), c.BackendURL)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]map[string]string{
		"missing backend":       {"HNMCP_PUBLIC_URL": "https://int.example.com"},
		"missing public":        {"HNMCP_BACKEND_URL": "http://hnbackend:8080"},
		"public not https":      {"HNMCP_BACKEND_URL": "http://hnbackend:8080", "HNMCP_PUBLIC_URL": "http://int.example.com"},
		"public with path":      {"HNMCP_BACKEND_URL": "http://hnbackend:8080", "HNMCP_PUBLIC_URL": "https://int.example.com/x"},
		"backend bad scheme":    {"HNMCP_BACKEND_URL": "ftp://hnbackend", "HNMCP_PUBLIC_URL": "https://int.example.com"},
		"backend with userinfo": {"HNMCP_BACKEND_URL": "http://u:p@hnbackend:8080", "HNMCP_PUBLIC_URL": "https://int.example.com"},
		"bad rate":              {"HNMCP_BACKEND_URL": "http://hnbackend:8080", "HNMCP_PUBLIC_URL": "https://int.example.com", "HNMCP_RATE_PER_MIN": "0"},
	}
	for name, m := range cases {
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoadAllowsLocalhostHTTP(t *testing.T) {
	m := map[string]string{
		"HNMCP_BACKEND_URL": "http://localhost:8080",
		"HNMCP_PUBLIC_URL":  "http://localhost:8091",
		"HNMCP_AUTH_SERVER": "http://localhost:8080",
	}
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.Resource() != "http://localhost:8091/mcp" || c.AuthServer != "http://localhost:8080" {
		t.Errorf("got %q %q", c.Resource(), c.AuthServer)
	}
}

func TestLoadOrigins(t *testing.T) {
	m := base()
	m["HNMCP_ALLOWED_ORIGINS"] = " https://a.example.com , https://b.example.com,"
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.AllowedOrigins, "|") != "https://a.example.com|https://b.example.com" {
		t.Errorf("origins = %v", c.AllowedOrigins)
	}
}

func TestStepUpHTTPSwitch(t *testing.T) {
	m := base()
	m["HNMCP_STEPUP_HTTP"] = "false"
	c, err := Load(env(m))
	if err != nil || c.StepUpHTTP {
		t.Errorf("err %v stepup %v", err, c.StepUpHTTP)
	}
	m["HNMCP_STEPUP_HTTP"] = "maybe"
	if _, err := Load(env(m)); err == nil {
		t.Error("expected error")
	}
}
