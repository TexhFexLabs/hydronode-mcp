// Package auth checks the bearer token of every /mcp request.
//
// Only HydroNode agent tokens (hoa_) are accepted. Anything else (PATs, refresh tokens, JWTs) is
// rejected without a backend call, so a stolen web session or PAT is useless here. Valid tokens
// are cached for 30 s by hash, rejected ones for 10 s, and concurrent checks of the same token
// share one backend call. The plaintext is kept only inside the cache entry and never logged.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/texhfexlabs/hydronode-mcp/internal/backend"
)

const (
	prefix = "hoa_"
	ttl    = 30 * time.Second
	// negativeTTL keeps floods of one bad token off the backend. Short, because a token never
	// turns valid later (new tokens have new values).
	negativeTTL = 10 * time.Second
	maxEntries  = 10000
	maxToken    = 256
)

var (
	ErrNoToken      = errors.New("no bearer token")
	ErrInvalidToken = errors.New("invalid token")
	ErrUnavailable  = errors.New("backend unavailable")
)

// Principal is a verified agent token.
type Principal struct {
	Token  string
	Hash   string // first 12 hex chars of SHA-256, safe for logs and rate-limit keys
	Scopes []string
	Client string
}

func (p *Principal) Has(scope string) bool {
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

type MeFunc func(ctx context.Context, token string) (*backend.Me, error)

type entry struct {
	p       Principal
	invalid bool
	expires time.Time
}

// call is one backend lookup that concurrent requests with the same token wait for.
type call struct {
	done chan struct{}
	p    *Principal
	err  error
}

type Verifier struct {
	me       MeFunc
	now      func() time.Time
	mu       sync.Mutex
	cache    map[[32]byte]entry
	inflight map[[32]byte]*call
}

func NewVerifier(me MeFunc) *Verifier {
	return &Verifier{me: me, now: time.Now, cache: map[[32]byte]entry{}, inflight: map[[32]byte]*call{}}
}

// Cached reports whether the header carries a token that is currently cached as valid. Used to
// let known clients through when their address is over the failed-login limit.
func (v *Verifier) Cached(header string) bool {
	tok, err := Token(header)
	if err != nil {
		return false
	}
	key := sha256.Sum256([]byte(tok))
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.cache[key]
	return ok && !e.invalid && v.now().Before(e.expires)
}

// Token extracts a well-formed agent token from an Authorization header.
func Token(header string) (string, error) {
	fields := strings.Fields(header)
	if len(fields) == 0 {
		return "", ErrNoToken
	}
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		return "", ErrInvalidToken
	}
	t := fields[1]
	if !strings.HasPrefix(t, prefix) || len(t) <= len(prefix) || len(t) > maxToken {
		return "", ErrInvalidToken
	}
	return t, nil
}

func (v *Verifier) Verify(ctx context.Context, header string) (*Principal, error) {
	tok, err := Token(header)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(tok))
	now := v.now()
	v.mu.Lock()
	if e, ok := v.cache[key]; ok && now.Before(e.expires) {
		v.mu.Unlock()
		if e.invalid {
			return nil, ErrInvalidToken
		}
		p := e.p
		return &p, nil
	}
	if c, ok := v.inflight[key]; ok {
		v.mu.Unlock()
		select {
		case <-c.done:
		case <-ctx.Done():
			return nil, ErrUnavailable
		}
		if c.p == nil {
			return nil, c.err
		}
		p := *c.p
		return &p, nil
	}
	c := &call{done: make(chan struct{})}
	v.inflight[key] = c
	v.mu.Unlock()

	c.p, c.err = v.lookup(ctx, tok, key)
	v.mu.Lock()
	delete(v.inflight, key)
	switch {
	case c.err == nil:
		v.store(key, entry{p: *c.p, expires: now.Add(ttl)}, now)
	case errors.Is(c.err, ErrInvalidToken):
		v.store(key, entry{invalid: true, expires: now.Add(negativeTTL)}, now)
	}
	v.mu.Unlock()
	close(c.done)
	if c.p == nil {
		return nil, c.err
	}
	p := *c.p
	return &p, nil
}

func (v *Verifier) lookup(ctx context.Context, tok string, key [32]byte) (*Principal, error) {
	me, err := v.me(ctx, tok)
	if err != nil {
		var be *backend.Error
		if errors.As(err, &be) && (be.Status == 401 || be.Status == 403) {
			return nil, ErrInvalidToken
		}
		return nil, ErrUnavailable
	}
	if me.Auth.Kind != "agent" {
		return nil, ErrInvalidToken
	}
	return &Principal{Token: tok, Hash: hex.EncodeToString(key[:])[:12], Scopes: me.Auth.Scopes, Client: me.Auth.Client}, nil
}

// store adds an entry; v.mu must be held. Keeps the cache bounded.
func (v *Verifier) store(key [32]byte, e entry, now time.Time) {
	if len(v.cache) >= maxEntries {
		for k, old := range v.cache {
			if !now.Before(old.expires) || len(v.cache) >= maxEntries {
				delete(v.cache, k)
			}
			if len(v.cache) < maxEntries*9/10 {
				break
			}
		}
	}
	v.cache[key] = e
}

func (v *Verifier) size() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.cache)
}
