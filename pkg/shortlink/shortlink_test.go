// shortlink_test.go covers Shorten/Resolve round-trip, deterministic
// codes (same URL → same short URL) and the Base-prefix stripping in
// Resolve.
package shortlink_test

import (
	"context"
	"errors"
	"testing"

	"github.com/soyaos/soyaos/pkg/shortlink"
	"github.com/soyaos/soyaos/pkg/store"
)

func openStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestShortlink_RoundTrip(t *testing.T) {
	s := &shortlink.Store{KV: openStore(t), Base: "https://s.soya.os/"}
	ctx := context.Background()
	long := "https://example.com/very/long/path?query=value"

	short, err := s.Shorten(ctx, long)
	if err != nil {
		t.Fatalf("Shorten: %v", err)
	}
	if got := short[:len(s.Base)]; got != s.Base {
		t.Errorf("short %q missing base prefix %q", short, s.Base)
	}
	if got := len(short) - len(s.Base); got != shortlink.CodeLen {
		t.Errorf("code length = %d, want %d", got, shortlink.CodeLen)
	}

	resolved, err := s.Resolve(ctx, short)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != long {
		t.Errorf("Resolve = %q, want %q", resolved, long)
	}
}

func TestShortlink_SameURL_IdempotentCode(t *testing.T) {
	s := &shortlink.Store{KV: openStore(t)}
	ctx := context.Background()
	a, _ := s.Shorten(ctx, "https://example.com/x")
	b, _ := s.Shorten(ctx, "https://example.com/x")
	if a != b {
		t.Errorf("same URL produced different codes: %q vs %q", a, b)
	}
}

func TestShortlink_Resolve_StripBase(t *testing.T) {
	s := &shortlink.Store{KV: openStore(t), Base: "https://s/"}
	ctx := context.Background()
	short, _ := s.Shorten(ctx, "https://example.com/x")
	// Strip Base manually and re-resolve to make sure either form works.
	bare := short[len(s.Base):]
	if got, err := s.Resolve(ctx, bare); err != nil || got != "https://example.com/x" {
		t.Errorf("Resolve(bare) = (%q, %v)", got, err)
	}
}

func TestShortlink_UnknownCode_NotFound(t *testing.T) {
	s := &shortlink.Store{KV: openStore(t)}
	_, err := s.Resolve(context.Background(), "AAAAAAAA")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestShortlink_NilStore(t *testing.T) {
	var s *shortlink.Store
	if _, err := s.Shorten(context.Background(), "x"); err == nil {
		t.Error("Shorten on nil receiver should error")
	}
}

func TestShortlink_EmptyURL_Errors(t *testing.T) {
	s := &shortlink.Store{KV: openStore(t)}
	if _, err := s.Shorten(context.Background(), ""); err == nil {
		t.Error("Shorten('') should error")
	}
}
