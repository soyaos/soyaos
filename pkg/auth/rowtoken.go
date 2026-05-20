// rowtoken.go implements the row-scoped JWT side of the EstateMuse Aha
// "Share a per-row action with a colleague without leaking my sk-soya
// key" (proposed DD-019, APP-503).
//
// When EstateMuse renders an HTML table with per-row buttons, the
// embedded button URL carries a short-lived JWT instead of the user's
// long-lived API key. The token binds the action to exactly one (agent
// slug, action id, row id) triple. TTL is capped at 24h.
//
// Signing uses HS256 with a secret persisted to
// ~/.local/share/soyaos/rowtoken-key (chmod 0600 on first creation).
// The same secret survives process restarts so previously-issued tokens
// keep verifying.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// RowClaims is the claim payload carried in a row-scoped JWT.
//
// OwnerKey holds the *prefix* of the user's sk-soya key (e.g. the
// 16-char KeyID portion) so the gateway can correlate row-action traffic
// with the owner's identity without ever seeing the secret. We never
// store the secret itself in the token.
type RowClaims struct {
	AgentSlug string `json:"agent_slug"`
	ActionID  string `json:"action_id"`
	RowID     string `json:"row_id"`
	OwnerKey  string `json:"owner_key"` // sk-soya key prefix; not the secret
	jwt.RegisteredClaims
}

// MaxRowTokenTTL caps the lifetime of a row token. The spec is "links
// shared in chat never live longer than a day"; 24h is the cap.
const MaxRowTokenTTL = 24 * time.Hour

// ErrTTLTooLong is returned by Mint when the requested ttl exceeds
// MaxRowTokenTTL.
var ErrTTLTooLong = errors.New("auth: row token ttl exceeds 24h cap")

// ErrInvalidRowToken is returned by Verify when the token is malformed,
// expired, or signed with the wrong secret.
var ErrInvalidRowToken = errors.New("auth: invalid row token")

// RowTokenSigner mints and verifies row-scoped JWTs.
type RowTokenSigner struct {
	secret []byte
}

// NewRowTokenSigner returns a signer using the provided secret. Use
// LoadOrCreateRowTokenSigner for the canonical persistent setup.
func NewRowTokenSigner(secret []byte) *RowTokenSigner {
	return &RowTokenSigner{secret: append([]byte(nil), secret...)}
}

// DefaultRowTokenKeyPath returns the canonical secret path:
// ~/.local/share/soyaos/rowtoken-key. Returns ("", err) when the user's
// home directory cannot be resolved.
func DefaultRowTokenKeyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("auth: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "soyaos", "rowtoken-key"), nil
}

// LoadOrCreateRowTokenSigner reads the secret from path, generating a
// new 32-byte secret (chmod 0600) the first time. Subsequent calls
// reuse the on-disk secret so tokens minted before a restart still
// verify.
//
// Pass an empty string to use DefaultRowTokenKeyPath().
func LoadOrCreateRowTokenSigner(path string) (*RowTokenSigner, error) {
	if path == "" {
		p, err := DefaultRowTokenKeyPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	if body, err := os.ReadFile(path); err == nil {
		secret, err := base64.StdEncoding.DecodeString(string(body))
		if err != nil {
			// The on-disk format is "base64(32 random bytes)"; raw
			// bytes would also be safe so fall back rather than
			// nuking the file.
			secret = body
		}
		if len(secret) < 16 {
			return nil, fmt.Errorf("auth: row token key at %s is too short (%d bytes)", path, len(secret))
		}
		return NewRowTokenSigner(secret), nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("auth: read row token key %s: %w", path, err)
	}

	// First-time setup. Make the parent dir, write base64(32 random bytes)
	// at mode 0600.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("auth: mkdir row token key dir: %w", err)
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("auth: generate row token secret: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(raw[:])
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("auth: write row token key %s: %w", path, err)
	}
	return NewRowTokenSigner(raw[:]), nil
}

// Mint issues a row-scoped JWT bound to (agentSlug, actionID, rowID).
// ownerKeyPrefix is the issuer's sk-soya key prefix (NEVER the secret).
// ttl is required and must not exceed MaxRowTokenTTL.
func (s *RowTokenSigner) Mint(agentSlug, actionID, rowID, ownerKeyPrefix string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", fmt.Errorf("auth: row token ttl must be positive")
	}
	if ttl > MaxRowTokenTTL {
		return "", ErrTTLTooLong
	}
	if agentSlug == "" || actionID == "" || rowID == "" {
		return "", fmt.Errorf("auth: row token requires non-empty agent / action / row")
	}
	now := time.Now()
	claims := RowClaims{
		AgentSlug: agentSlug,
		ActionID:  actionID,
		RowID:     rowID,
		OwnerKey:  ownerKeyPrefix,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("auth: sign row token: %w", err)
	}
	return signed, nil
}

// Verify decodes and validates a row token, returning the claims.
// Returns ErrInvalidRowToken for any failure (wrong signature, expired,
// malformed). Callers should also assert the claim values match the
// expected (agentSlug, actionID, rowID) before letting the action run.
func (s *RowTokenSigner) Verify(token string) (*RowClaims, error) {
	claims := &RowClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil || !parsed.Valid {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRowToken, err)
	}
	return claims, nil
}
