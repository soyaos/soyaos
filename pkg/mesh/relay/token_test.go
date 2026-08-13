package relay

import (
	"errors"
	"testing"
	"time"
)

func TestTokenRoundTripAndExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	secret := []byte("0123456789abcdef0123456789abcdef")
	token, err := IssueToken(secret, 5*time.Minute, now)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	parsed, err := ParseToken(token.String())
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if !parsed.Valid(secret, now.Add(time.Minute)) {
		t.Fatal("token should be valid before expiry")
	}
	if parsed.Valid(secret, now.Add(5*time.Minute)) {
		t.Fatal("token should be invalid at expiry")
	}
	if parsed.Valid([]byte("abcdef0123456789abcdef0123456789"), now) {
		t.Fatal("token should reject a different signing secret")
	}
}

func TestIssueTokenRequiresStrongSecret(t *testing.T) {
	_, err := IssueToken([]byte("short"), time.Minute, time.Now())
	if !errors.Is(err, ErrSecretTooShort) {
		t.Fatalf("err=%v, want ErrSecretTooShort", err)
	}
}
