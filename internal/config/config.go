// Package config reads hnmcp settings from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
)

// DefaultOrigins are the browser origins of the hosted MCP clients that call /mcp directly.
// Requests without an Origin header (server-side clients) are always allowed.
var DefaultOrigins = []string{"https://claude.ai", "https://claude.com", "https://chatgpt.com"}

type Config struct {
	Listen string
	// BackendURL is the internal backend address (http://hnbackend:8080), never a public host.
	BackendURL string
	// PublicURL is the origin clients use (https://hydronode.tech), without path.
	PublicURL string
	// AuthServer is the OAuth issuer, usually equal to PublicURL.
	AuthServer     string
	AllowedOrigins []string
	RatePerMin     int
	// StepUpHTTP answers a tool call that lacks a scope with HTTP 403 + insufficient_scope
	// (MCP step-up). Off: 200 with only the explaining tool error.
	StepUpHTTP bool
	LogLevel   slog.Level
}

func (c Config) Resource() string { return c.PublicURL + "/mcp" }
func (c Config) ResourceMetadataURL() string {
	return c.PublicURL + "/.well-known/oauth-protected-resource/mcp"
}
func (c Config) ConnectedAppsURL() string { return c.AuthServer + "/account/security#connected-apps" }

func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Listen:         or(getenv("HNMCP_LISTEN"), ":8080"),
		AllowedOrigins: DefaultOrigins,
		RatePerMin:     120,
		StepUpHTTP:     true,
	}
	var err error
	if c.BackendURL, err = origin(getenv("HNMCP_BACKEND_URL"), "HNMCP_BACKEND_URL", false); err != nil {
		return c, err
	}
	if c.PublicURL, err = origin(getenv("HNMCP_PUBLIC_URL"), "HNMCP_PUBLIC_URL", true); err != nil {
		return c, err
	}
	c.AuthServer = c.PublicURL
	if v := getenv("HNMCP_AUTH_SERVER"); v != "" {
		if c.AuthServer, err = origin(v, "HNMCP_AUTH_SERVER", true); err != nil {
			return c, err
		}
	}
	if v := getenv("HNMCP_ALLOWED_ORIGINS"); v != "" {
		c.AllowedOrigins = nil
		for _, o := range strings.Split(v, ",") {
			if o = strings.TrimSpace(o); o != "" {
				c.AllowedOrigins = append(c.AllowedOrigins, strings.TrimSuffix(o, "/"))
			}
		}
	}
	if v := getenv("HNMCP_RATE_PER_MIN"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 10000 {
			return c, errors.New("HNMCP_RATE_PER_MIN must be 1..10000")
		}
		c.RatePerMin = n
	}
	if v := getenv("HNMCP_STEPUP_HTTP"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return c, errors.New("HNMCP_STEPUP_HTTP must be true or false")
		}
		c.StepUpHTTP = b
	}
	if v := getenv("HNMCP_LOG_LEVEL"); v != "" {
		if err := c.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return c, fmt.Errorf("HNMCP_LOG_LEVEL: %w", err)
		}
	}
	return c, nil
}

// origin validates scheme://host[:port] without path, query or userinfo. Public URLs must be
// https except on localhost, so a misconfiguration never advertises plain-HTTP OAuth endpoints.
func origin(raw, name string, public bool) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	u, err := url.Parse(strings.TrimSuffix(raw, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%s: not a URL", name)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("%s must be scheme://host[:port] only", name)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if public && !isLocal(u.Hostname()) {
			return "", fmt.Errorf("%s must use https", name)
		}
	default:
		return "", fmt.Errorf("%s must be http or https", name)
	}
	return u.Scheme + "://" + u.Host, nil
}

func isLocal(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func or(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
