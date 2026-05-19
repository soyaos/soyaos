package memory

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/soyaos/soyaos/pkg/store"
)

// StoreNamespace is the bbolt bucket name used by StoreBacked.
const StoreNamespace = "memory.entries"

// StoreBacked is a KV implementation backed by pkg/store.Store. Entries
// survive process restarts. Composite keys use
// store.CompositeKeyString(scope, owner, key) — length-prefixed encoding
// so a hostile owner string can't collide into another tenant's keyspace.
type StoreBacked struct {
	store store.Store
}

// NewStoreBacked returns a KV using s for persistence.
func NewStoreBacked(s store.Store) *StoreBacked { return &StoreBacked{store: s} }

// Get fetches by composite identity. Expired entries are reported as
// ErrNotFound (lazy cleanup).
func (s *StoreBacked) Get(ctx context.Context, scope Scope, owner, k string) (Entry, error) {
	body, err := s.store.Get(ctx, StoreNamespace, store.CompositeKeyString(string(scope), owner, k))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, err
	}
	var e Entry
	if err := json.Unmarshal(body, &e); err != nil {
		return Entry{}, err
	}
	if e.Expired(time.Now()) {
		return Entry{}, ErrNotFound
	}
	return e, nil
}

// Put writes an entry, replacing any prior value. Empty Sensitivity is
// coerced to "low".
func (s *StoreBacked) Put(ctx context.Context, e Entry) error {
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = time.Now()
	}
	if e.Sensitivity == "" {
		e.Sensitivity = SensitivityLow
	}
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return s.store.Put(ctx, StoreNamespace, store.CompositeKeyString(string(e.Scope), e.Owner, e.Key), body)
}

// Delete removes an entry by composite identity. Missing keys are no-ops.
func (s *StoreBacked) Delete(ctx context.Context, scope Scope, owner, k string) error {
	return s.store.Delete(ctx, StoreNamespace, store.CompositeKeyString(string(scope), owner, k))
}

// List returns entries under (scope, owner) whose Key starts with prefix.
// Implementation lists all entries under (scope, owner) — Bolt's key
// ordering means the (scope, owner) keys are co-located but the trailing
// key segment doesn't share a byte prefix with `prefix` because of
// length-prefix framing, so we filter in user space.
func (s *StoreBacked) List(ctx context.Context, scope Scope, owner, prefix string) ([]Entry, error) {
	pairs, err := s.store.List(ctx, StoreNamespace, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0)
	now := time.Now()
	for _, p := range pairs {
		var e Entry
		if err := json.Unmarshal(p.Value, &e); err != nil {
			continue
		}
		if e.Scope != scope || e.Owner != owner {
			continue
		}
		if prefix != "" && !hasPrefix(e.Key, prefix) {
			continue
		}
		if e.Expired(now) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
