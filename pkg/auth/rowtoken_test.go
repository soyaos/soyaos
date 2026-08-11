// rowtoken_test.go pins the row-scoped JWT contract (APP-503 / DD-019):
// mint→verify round-trip, TTL cap rejection, signature-tamper detection,
// and persistent secret on disk.
package auth_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/auth"
)

func TestRowToken_MintVerify_RoundTrip(t *testing.T) {
	s := auth.NewRowTokenSigner([]byte("test-secret-32-bytes-long-padding"))
	tok, err := s.Mint("estate-muse", "star", "row-42", "sk-soya-prefix-abc", 1*time.Hour)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	claims, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.AgentSlug != "estate-muse" || claims.ActionID != "star" || claims.RowID != "row-42" {
		t.Fatalf("claims = %+v", claims)
	}
	if claims.OwnerKey != "sk-soya-prefix-abc" {
		t.Fatalf("OwnerKey = %q", claims.OwnerKey)
	}
}

func TestRowToken_RejectsTTLOverCap(t *testing.T) {
	s := auth.NewRowTokenSigner([]byte("k"))
	if _, err := s.Mint("a", "x", "r", "k", 25*time.Hour); !errors.Is(err, auth.ErrTTLTooLong) {
		t.Fatalf("Mint(25h) = %v, want ErrTTLTooLong", err)
	}
	if _, err := s.Mint("a", "x", "r", "k", 24*time.Hour); err != nil {
		t.Fatalf("Mint(24h exact) = %v, want nil", err)
	}
}

func TestRowToken_RejectsZeroOrNegativeTTL(t *testing.T) {
	s := auth.NewRowTokenSigner([]byte("k"))
	if _, err := s.Mint("a", "x", "r", "k", 0); err == nil {
		t.Fatal("Mint(0) should error")
	}
	if _, err := s.Mint("a", "x", "r", "k", -1*time.Second); err == nil {
		t.Fatal("Mint(-1s) should error")
	}
}

func TestRowToken_RejectsEmptyTriple(t *testing.T) {
	s := auth.NewRowTokenSigner([]byte("k"))
	if _, err := s.Mint("", "x", "r", "k", time.Hour); err == nil {
		t.Fatal("Mint(empty agent) should error")
	}
	if _, err := s.Mint("a", "", "r", "k", time.Hour); err == nil {
		t.Fatal("Mint(empty action) should error")
	}
	if _, err := s.Mint("a", "x", "", "k", time.Hour); err == nil {
		t.Fatal("Mint(empty row) should error")
	}
}

func TestRowToken_TamperedSecretFailsVerify(t *testing.T) {
	s1 := auth.NewRowTokenSigner([]byte("secret-A-padding-padding-padding"))
	s2 := auth.NewRowTokenSigner([]byte("secret-B-padding-padding-padding"))
	tok, err := s1.Mint("a", "x", "r", "k", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Verify(tok); !errors.Is(err, auth.ErrInvalidRowToken) {
		t.Fatalf("Verify(wrong secret) = %v, want ErrInvalidRowToken", err)
	}
}

func TestRowToken_TamperedPayloadFailsVerify(t *testing.T) {
	s := auth.NewRowTokenSigner([]byte("secret-padding-padding-padding-x"))
	tok, _ := s.Mint("a", "x", "r", "k", time.Hour)
	// Flip a character inside the JWT payload section.
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3", len(parts))
	}
	// Mutate the payload (parts[1]). Replace last char with 'A' (or 'B' if it was 'A').
	pl := parts[1]
	last := pl[len(pl)-1]
	if last == 'A' {
		last = 'B'
	} else {
		last = 'A'
	}
	parts[1] = pl[:len(pl)-1] + string(last)
	tampered := strings.Join(parts, ".")
	if _, err := s.Verify(tampered); !errors.Is(err, auth.ErrInvalidRowToken) {
		t.Fatalf("Verify(tampered) = %v, want ErrInvalidRowToken", err)
	}
}

func TestRowToken_ExpiredTokenFailsVerify(t *testing.T) {
	s := auth.NewRowTokenSigner([]byte("secret-padding-padding-padding-x"))
	// 1 ns TTL → token is already expired by the time we Verify.
	tok, err := s.Mint("a", "x", "r", "k", 1)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := s.Verify(tok); !errors.Is(err, auth.ErrInvalidRowToken) {
		t.Fatalf("Verify(expired) = %v, want ErrInvalidRowToken", err)
	}
}

func TestRowToken_LoadOrCreate_PersistsSecret(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "rowtoken-key")

	// First call: file does not exist → should create.
	s1, err := auth.LoadOrCreateRowTokenSigner(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreate#1: %v", err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// On unix the mode bits should be 0600 (the test runs on darwin so
	// check the mode masked to permission bits).
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("key file mode = %v, want 0600", mode)
	}

	// Mint a token with #1; verify it loads cleanly with a fresh
	// signer (#2) reading the same on-disk secret.
	tok, err := s1.Mint("a", "x", "r", "k", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := auth.LoadOrCreateRowTokenSigner(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreate#2: %v", err)
	}
	if _, err := s2.Verify(tok); err != nil {
		t.Fatalf("Verify after reload: %v", err)
	}
}
