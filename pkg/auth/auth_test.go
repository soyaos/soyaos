package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMemoryStore_SeedAndVerify(t *testing.T) {
	s := NewMemoryStore()
	key := s.SeedDevKey()
	if !strings.HasPrefix(key, KeyPrefix) {
		t.Fatalf("seeded key missing %q prefix: %q", KeyPrefix, key)
	}
	id, err := s.Verify(context.Background(), key)
	if err != nil {
		t.Fatalf("Verify(seeded key) = %v", err)
	}
	if id.Subject != "local" {
		t.Fatalf("seeded subject = %q, want %q", id.Subject, "local")
	}
}

func TestMemoryStore_VerifyRejectsUnknownKey(t *testing.T) {
	s := NewMemoryStore()
	if _, err := s.Verify(context.Background(), KeyPrefix+"nope"); err != ErrInvalidKey {
		t.Fatalf("Verify(unknown) = %v, want ErrInvalidKey", err)
	}
	if _, err := s.Verify(context.Background(), "not-a-soya-key"); err != ErrInvalidKey {
		t.Fatalf("Verify(wrong prefix) = %v, want ErrInvalidKey", err)
	}
}

func TestMemoryStore_MintAndRevoke(t *testing.T) {
	s := NewMemoryStore()
	raw, id, err := s.Mint(context.Background(), "alice", []string{"agents:invoke"}, 0)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if !strings.HasPrefix(raw, KeyPrefix) {
		t.Fatalf("minted key missing prefix: %q", raw)
	}
	if !id.HasScope("agents:invoke") {
		t.Fatalf("minted identity missing scope")
	}

	if _, err := s.Verify(context.Background(), raw); err != nil {
		t.Fatalf("Verify(just-minted) = %v", err)
	}
	if err := s.Revoke(context.Background(), id.KeyID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := s.Verify(context.Background(), raw); err != ErrInvalidKey {
		t.Fatalf("Verify(revoked) = %v, want ErrInvalidKey", err)
	}
}

func TestMemoryStore_VerifyHonorsExpiry(t *testing.T) {
	s := NewMemoryStore()
	raw, _, err := s.Mint(context.Background(), "bob", nil, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := s.Verify(context.Background(), raw); err != ErrInvalidKey {
		t.Fatalf("Verify(expired) = %v, want ErrInvalidKey", err)
	}
}

func TestExtractBearer(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Bearer sk-soya-abc", "sk-soya-abc"},
		{"bearer sk-soya-abc", "sk-soya-abc"},
		{"sk-soya-abc", ""},
		{"", ""},
		{"Bearer ", ""},
	}
	for _, c := range cases {
		if got := ExtractBearer(c.in); got != c.want {
			t.Errorf("ExtractBearer(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
