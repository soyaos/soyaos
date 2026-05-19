// Package auth implements SoyaAuth — zero-trust identity, API keys and
// capability tokens.
//
// v0.1.0-alpha.0 ships only the minimum needed by the OpenAI-Compat gateway:
//
//   - API keys in the canonical `sk-soya-...` format (DD-005).
//   - A pluggable Verifier interface so the Solo in-memory store can be
//     replaced by a database-backed store in Cluster/Cloud editions without
//     touching the gateway.
//
// Capability tokens (short-lived, scope-bound) and row-scoped action tokens
// (DD-010 / DD-019) will land alongside the modules that need them.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

// KeyPrefix is the mandatory prefix for every SoyaOS API key.
//
// Locked by the cross-document terminology patch in
// "SoyaOS 设计文档对齐清单" — do not change without a DD.
const KeyPrefix = "sk-soya-"

// ErrInvalidKey is returned when an API key fails verification.
var ErrInvalidKey = errors.New("auth: invalid api key")

// Identity is the resolved owner of an authenticated request.
type Identity struct {
	KeyID     string    // opaque key identifier (not the secret)
	Subject   string    // tenant / user id; "local" in Solo
	IssuedAt  time.Time // when this key was minted
	ExpiresAt time.Time // zero value means no expiry
	Scopes    []string  // capability scopes; empty == default scope
}

// HasScope reports whether the identity carries the given scope.
func (i Identity) HasScope(s string) bool {
	for _, x := range i.Scopes {
		if x == s {
			return true
		}
	}
	return false
}

// Verifier resolves a raw API key to an Identity, or returns ErrInvalidKey.
type Verifier interface {
	Verify(ctx context.Context, rawKey string) (Identity, error)
}

// Issuer mints new keys. Solo uses an in-memory store; production editions
// substitute a database-backed store.
type Issuer interface {
	Mint(ctx context.Context, subject string, scopes []string, ttl time.Duration) (rawKey string, ident Identity, err error)
	Revoke(ctx context.Context, keyID string) error
}

// MemoryStore is the Solo / dev / test backend: a process-local key registry.
// It implements both Verifier and Issuer.
type MemoryStore struct {
	mu   sync.RWMutex
	keys map[string]Identity // map[rawKey]identity — constant-time compare on lookup
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{keys: make(map[string]Identity)}
}

// SeedDevKey registers the dev-time bootstrap key `sk-soya-dev-local` so the
// Solo quickstart works without setup. Returns the key it registered.
//
// This key must never be used outside local development; production deployments
// should call Mint() and rotate.
func (s *MemoryStore) SeedDevKey() string {
	const devKey = KeyPrefix + "dev-local"
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[devKey] = Identity{
		KeyID:    "dev-local",
		Subject:  "local",
		IssuedAt: time.Now(),
		Scopes:   []string{"*"},
	}
	return devKey
}

// Mint generates a fresh key with the given subject, scopes, and TTL.
// ttl == 0 means no expiry.
func (s *MemoryStore) Mint(_ context.Context, subject string, scopes []string, ttl time.Duration) (string, Identity, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", Identity{}, err
	}
	keyID := hex.EncodeToString(raw[:8])
	secret := hex.EncodeToString(raw[:])
	rawKey := KeyPrefix + secret

	id := Identity{
		KeyID:    keyID,
		Subject:  subject,
		IssuedAt: time.Now(),
		Scopes:   append([]string(nil), scopes...),
	}
	if ttl > 0 {
		id.ExpiresAt = id.IssuedAt.Add(ttl)
	}

	s.mu.Lock()
	s.keys[rawKey] = id
	s.mu.Unlock()
	return rawKey, id, nil
}

// Verify checks a raw key against the store and returns its Identity.
func (s *MemoryStore) Verify(_ context.Context, rawKey string) (Identity, error) {
	if !strings.HasPrefix(rawKey, KeyPrefix) {
		return Identity{}, ErrInvalidKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for stored, id := range s.keys {
		// constant-time compare guards against timing side-channels
		if len(stored) == len(rawKey) && subtle.ConstantTimeCompare([]byte(stored), []byte(rawKey)) == 1 {
			if !id.ExpiresAt.IsZero() && time.Now().After(id.ExpiresAt) {
				return Identity{}, ErrInvalidKey
			}
			return id, nil
		}
	}
	return Identity{}, ErrInvalidKey
}

// Revoke deletes a key by its KeyID.
func (s *MemoryStore) Revoke(_ context.Context, keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for raw, id := range s.keys {
		if id.KeyID == keyID {
			delete(s.keys, raw)
			return nil
		}
	}
	return ErrInvalidKey
}

// ExtractBearer parses an HTTP Authorization header value and returns the raw
// key, or "" if not a Bearer credential.
func ExtractBearer(authHeader string) string {
	const prefix = "Bearer "
	if len(authHeader) <= len(prefix) {
		return ""
	}
	if !strings.EqualFold(authHeader[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(authHeader[len(prefix):])
}
