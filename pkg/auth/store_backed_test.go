package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/store"
)

func openTempStore(t *testing.T) (store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func TestStoreBacked_SeedAndVerify(t *testing.T) {
	s, _ := openTempStore(t)
	a := auth.NewStoreBacked(s)

	key := a.SeedDevKey()
	if !strings.HasPrefix(key, auth.KeyPrefix) {
		t.Fatalf("seeded key missing %q prefix: %q", auth.KeyPrefix, key)
	}

	id, err := a.Verify(context.Background(), key)
	if err != nil {
		t.Fatalf("Verify(seeded): %v", err)
	}
	if id.Subject != "local" {
		t.Fatalf("Subject = %q, want local", id.Subject)
	}
	if id.HasScope("*") {
		t.Fatal("dev key should NOT carry wildcard scope after S2-A3 (unsafe-dev hardening)")
	}
	if !id.HasScope("agents:invoke") {
		t.Fatal("dev key should carry agents:invoke")
	}
}

func TestStoreBacked_MintAndVerify(t *testing.T) {
	s, _ := openTempStore(t)
	a := auth.NewStoreBacked(s)

	raw, id, err := a.Mint(context.Background(), "alice", []string{"agents:invoke"}, 0)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if id.Subject != "alice" {
		t.Fatalf("Subject = %q", id.Subject)
	}
	got, err := a.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.KeyID != id.KeyID {
		t.Fatalf("KeyID mismatch: %q vs %q", got.KeyID, id.KeyID)
	}
}

func TestStoreBacked_VerifyRejectsUnknownAndMalformed(t *testing.T) {
	s, _ := openTempStore(t)
	a := auth.NewStoreBacked(s)

	if _, err := a.Verify(context.Background(), "not-a-key"); !errors.Is(err, auth.ErrInvalidKey) {
		t.Fatalf("Verify(wrong prefix) = %v", err)
	}
	if _, err := a.Verify(context.Background(), auth.KeyPrefix+"nope"); !errors.Is(err, auth.ErrInvalidKey) {
		t.Fatalf("Verify(unknown) = %v", err)
	}
}

func TestStoreBacked_RevokeRemoves(t *testing.T) {
	s, _ := openTempStore(t)
	a := auth.NewStoreBacked(s)
	raw, id, _ := a.Mint(context.Background(), "bob", nil, 0)

	if err := a.Revoke(context.Background(), id.KeyID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := a.Verify(context.Background(), raw); !errors.Is(err, auth.ErrInvalidKey) {
		t.Fatalf("Verify(revoked) = %v", err)
	}
}

func TestStoreBacked_VerifyHonorsExpiry(t *testing.T) {
	s, _ := openTempStore(t)
	a := auth.NewStoreBacked(s)
	raw, _, _ := a.Mint(context.Background(), "carol", nil, 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if _, err := a.Verify(context.Background(), raw); !errors.Is(err, auth.ErrInvalidKey) {
		t.Fatalf("Verify(expired) = %v", err)
	}
}

func TestStoreBacked_PersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()

	var rawKey string
	{
		s, err := store.Open(dir)
		if err != nil {
			t.Fatalf("Open#1: %v", err)
		}
		a := auth.NewStoreBacked(s)
		rawKey = a.SeedDevKey()
		_, _, err = a.Mint(context.Background(), "persistent", []string{"x"}, 0)
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		_ = s.Close()
	}

	// Simulate a process restart.
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open#2: %v", err)
	}
	defer s.Close()
	a := auth.NewStoreBacked(s)
	if _, err := a.Verify(context.Background(), rawKey); err != nil {
		t.Fatalf("Verify(seeded, post-restart) = %v", err)
	}
	keys, err := a.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("List() returned %d, want 2 (dev + persistent)", len(keys))
	}
}
