// Package relay implements SoyaMesh's ciphertext-only UDP relay.
//
// The relay authenticates a short-lived routing ticket and forwards opaque
// datagrams between two endpoints. QUIC and mTLS terminate at Moon and Comet,
// never in this package. The relay can therefore account for bytes and see
// network metadata, but it cannot decrypt the stream carried by QUIC.
package relay

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	sessionIDSize = 16
	macSize       = sha256.Size
	tokenSize     = 8 + sessionIDSize + macSize
)

var (
	// ErrInvalidToken is intentionally non-specific: callers must not learn
	// whether an unknown ticket failed its shape, expiry, or signature check.
	ErrInvalidToken = errors.New("relay: invalid or expired routing token")
	// ErrSecretTooShort keeps production deployments from using a guessable
	// shared signing key. Generate one with: openssl rand -base64 32.
	ErrSecretTooShort = errors.New("relay: signing secret must be at least 32 bytes")
)

// Token is a compact, signed routing capability. It contains only an expiry
// time and a random session identifier; it carries no user or artifact data.
type Token struct {
	raw [tokenSize]byte
}

// IssueToken creates a short-lived routing capability shared by exactly one
// Moon/Comet pair. The relay binds each side to the first UDP address that
// presents the token, preventing a later address from silently taking over.
func IssueToken(secret []byte, ttl time.Duration, now time.Time) (Token, error) {
	if len(secret) < 32 {
		return Token{}, ErrSecretTooShort
	}
	if ttl <= 0 {
		return Token{}, fmt.Errorf("relay: token ttl must be positive")
	}

	var token Token
	binary.BigEndian.PutUint64(token.raw[:8], uint64(now.Add(ttl).Unix()))
	if _, err := rand.Read(token.raw[8 : 8+sessionIDSize]); err != nil {
		return Token{}, fmt.Errorf("relay: generate session id: %w", err)
	}
	token.sign(secret)
	return token, nil
}

// ParseToken decodes the URL-safe form produced by Token.String. Signature
// verification happens separately because relay clients intentionally do not
// possess the server's signing secret.
func ParseToken(encoded string) (Token, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) != tokenSize {
		return Token{}, ErrInvalidToken
	}
	var token Token
	copy(token.raw[:], raw)
	return token, nil
}

// String returns a URL-safe bearer representation. Treat it like a password:
// it is deliberately short-lived, but anyone holding it can use its session.
func (t Token) String() string {
	return base64.RawURLEncoding.EncodeToString(t.raw[:])
}

// ExpiresAt returns the embedded absolute expiry time.
func (t Token) ExpiresAt() time.Time {
	return time.Unix(int64(binary.BigEndian.Uint64(t.raw[:8])), 0)
}

// Valid verifies both the signature and expiration without returning details
// that could become a token-validation oracle.
func (t Token) Valid(secret []byte, now time.Time) bool {
	if len(secret) < 32 || !now.Before(t.ExpiresAt()) {
		return false
	}
	want := tokenMAC(secret, t.raw[:8+sessionIDSize])
	return hmac.Equal(t.raw[8+sessionIDSize:], want)
}

func (t Token) sessionID() [sessionIDSize]byte {
	var id [sessionIDSize]byte
	copy(id[:], t.raw[8:8+sessionIDSize])
	return id
}

func (t *Token) sign(secret []byte) {
	copy(t.raw[8+sessionIDSize:], tokenMAC(secret, t.raw[:8+sessionIDSize]))
}

func tokenMAC(secret, message []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}
