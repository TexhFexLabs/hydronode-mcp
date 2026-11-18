package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/texhfexlabs/hydronode-mcp/internal/backend"
)

type fakeMe struct {
	calls atomic.Int32
	err   error
}

func (f *fakeMe) Me(_ context.Context, token string) (*backend.Me, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	m := &backend.Me{}
	m.Auth.Kind = "agent"
	m.Auth.Client = "Claude"
	m.Auth.Scopes = []string{"hydronode:read"}
	return m, nil
}

func TestVerifyRejectsOtherKindsWithoutBackendCall(t *testing.T) {
	f := &fakeMe{}
	v := NewVerifier(f.Me)
	for _, h := range []string{"", "Basic abc", "Bearer hat_x", "Bearer hrt_x", "Bearer hor_x", "Bearer eyJhbGciOi.x.y", "Bearer hoa_", "bearer hoa_x y"} {
		if _, err := v.Verify(context.Background(), h); err == nil {
			t.Errorf("%q accepted", h)
		}
	}
	if f.calls.Load() != 0 {
		t.Errorf("backend called %d times", f.calls.Load())
	}
}

func TestVerifyCachesThirtySeconds(t *testing.T) {
	f := &fakeMe{}
	v := NewVerifier(f.Me)
	now := time.Unix(1_800_000_000, 0)
	v.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		p, err := v.Verify(context.Background(), "Bearer hoa_token1")
		if err != nil {
			t.Fatal(err)
		}
		if p.Client != "Claude" || len(p.Hash) != 12 || p.Token != "hoa_token1" {
			t.Errorf("principal = %+v", p)
		}
	}
	if f.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", f.calls.Load())
	}
	now = now.Add(31 * time.Second)
	if _, err := v.Verify(context.Background(), "Bearer hoa_token1"); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", f.calls.Load())
	}
}

func TestVerifyInvalidToken(t *testing.T) {
	f := &fakeMe{err: &backend.Error{Status: 401, Code: "UNAUTHORIZED"}}
	v := NewVerifier(f.Me)
	now := time.Unix(1_800_000_000, 0)
	v.now = func() time.Time { return now }
	_, err := v.Verify(context.Background(), "Bearer hoa_bad")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v", err)
	}
	// A rejected token is remembered briefly, so floods of the same bad token stay off the backend.
	f.err = nil
	if _, err := v.Verify(context.Background(), "Bearer hoa_bad"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("within negative ttl: %v", err)
	}
	if f.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", f.calls.Load())
	}
	// Never cached as valid; asked again after the short window.
	now = now.Add(negativeTTL + time.Second)
	if _, err := v.Verify(context.Background(), "Bearer hoa_bad"); err != nil {
		t.Fatalf("after negative ttl: %v", err)
	}
}

func TestVerifyCollapsesConcurrentLookups(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	me := func(_ context.Context, _ string) (*backend.Me, error) {
		calls.Add(1)
		<-release
		m := &backend.Me{}
		m.Auth.Kind = "agent"
		return m, nil
	}
	v := NewVerifier(me)
	const n = 20
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := v.Verify(context.Background(), "Bearer hoa_same")
			errs <- err
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if c := calls.Load(); c != 1 {
		t.Errorf("backend calls = %d, want 1", c)
	}
}

func TestVerifyBackendDown(t *testing.T) {
	f := &fakeMe{err: errors.New("dial tcp: refused")}
	_, err := NewVerifier(f.Me).Verify(context.Background(), "Bearer hoa_x")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestCacheBounded(t *testing.T) {
	f := &fakeMe{}
	v := NewVerifier(f.Me)
	for i := 0; i < maxEntries+50; i++ {
		_, _ = v.Verify(context.Background(), "Bearer hoa_"+string(rune('a'+i%26))+time.Duration(i).String())
	}
	if n := v.size(); n > maxEntries {
		t.Errorf("cache size %d", n)
	}
}
