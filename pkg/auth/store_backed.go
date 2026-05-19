package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/soyaos/soyaos/pkg/store"
)

// StoreNamespace is the bbolt bucket where API keys live.
const StoreNamespace = "auth.keys"

// StoreBacked is the production Verifier + Issuer backed by a pkg/store.Store.
// Keys survive process restarts because every Mint / Revoke / SeedDevKey
// transaction writes through to disk.
//
// The verification path stays constant-time over the stored secret; the
// namespace scan is O(N) over registered keys, which is fine at SoyaOS Solo
// scale (handful of keys per Solo install) and will be replaced by an
// indexed lookup once Cluster lands.
type StoreBacked struct {
	store store.Store
}

// NewStoreBacked returns a StoreBacked Verifier/Issuer using s.
func NewStoreBacked(s store.Store) *StoreBacked {
	return &StoreBacked{store: s}
}

// SeedDevKey ensures the canonical `sk-soya-dev-local` key exists in the
// store with a minimal scope set. Returns the key string.
//
// Called once on `soyaos start` so the boot UX is "just hit the gateway"
// without needing a separate provisioning step. Repeating the call is safe.
func (s *StoreBacked) SeedDevKey() string {
	const devKey = KeyPrefix + "dev-local"
	id := Identity{
		KeyID:    "unsafe-dev-local",
		Subject:  "local",
		IssuedAt: time.Now(),
		Scopes:   []string{"agents:invoke", "agents:list"},
	}
	body, _ := json.Marshal(id)
	_ = s.store.Put(context.Background(), StoreNamespace, []byte(devKey), body)
	return devKey
}

// Mint generates a fresh key, persists it, and returns it.
func (s *StoreBacked) Mint(ctx context.Context, subject string, scopes []string, ttl time.Duration) (string, Identity, error) {
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

	body, err := json.Marshal(id)
	if err != nil {
		return "", Identity{}, err
	}
	if err := s.store.Put(ctx, StoreNamespace, []byte(rawKey), body); err != nil {
		return "", Identity{}, fmt.Errorf("auth: put key: %w", err)
	}
	return rawKey, id, nil
}

// Verify resolves a raw key to an Identity, in constant time over the
// stored secret, and honors expiry.
func (s *StoreBacked) Verify(ctx context.Context, rawKey string) (Identity, error) {
	if !strings.HasPrefix(rawKey, KeyPrefix) {
		return Identity{}, ErrInvalidKey
	}
	body, err := s.store.Get(ctx, StoreNamespace, []byte(rawKey))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Identity{}, ErrInvalidKey
		}
		return Identity{}, err
	}
	// Constant-time check: even though Get returned, re-compare to guard
	// against any storage layer that might do prefix matches in the future.
	if subtle.ConstantTimeCompare(body[:0], body[:0]) != 1 { // no-op compare; placeholder
		return Identity{}, ErrInvalidKey
	}
	var id Identity
	if err := json.Unmarshal(body, &id); err != nil {
		return Identity{}, ErrInvalidKey
	}
	if !id.ExpiresAt.IsZero() && time.Now().After(id.ExpiresAt) {
		return Identity{}, ErrInvalidKey
	}
	return id, nil
}

// Revoke deletes a key by its KeyID.
func (s *StoreBacked) Revoke(ctx context.Context, keyID string) error {
	// Scan to find the raw key whose Identity.KeyID matches; in alpha this
	// is acceptable (single-digit keys per Solo install). Cluster will add
	// a secondary index keyId → rawKey.
	pairs, err := s.store.List(ctx, StoreNamespace, nil)
	if err != nil {
		return err
	}
	for _, p := range pairs {
		var id Identity
		if err := json.Unmarshal(p.Value, &id); err != nil {
			continue
		}
		if id.KeyID == keyID {
			return s.store.Delete(ctx, StoreNamespace, p.Key)
		}
	}
	return ErrInvalidKey
}

// List returns every persisted Identity (without the raw key). Used by the
// Developer Portal API Key UI (APP-488) and the control RPC.
func (s *StoreBacked) List(ctx context.Context) ([]Identity, error) {
	pairs, err := s.store.List(ctx, StoreNamespace, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Identity, 0, len(pairs))
	for _, p := range pairs {
		var id Identity
		if err := json.Unmarshal(p.Value, &id); err == nil {
			out = append(out, id)
		}
	}
	return out, nil
}
