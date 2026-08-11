// Package state implements the SoyaOS Stateful Agent runtime store
// (DD-010, EstateMuse Aha #2: "Agent-scoped state with MVCC").
//
// Distinct from pkg/memory (which is read-mostly long-tail recall),
// this package is the strongly-typed, version-checked, row-grained
// state that an Agent mutates inside a single per-row action — think
// "the topic-tier on row 17 changed from B to A, increment the version
// so two parallel writers can't trample each other".
//
// MVCC strategy:
//
//   - Each Entry carries an int64 Version that starts at 1 and increments
//     by 1 on every successful Put / CompareAndSwap.
//   - CompareAndSwap rejects a write whose BaseVersion does not match the
//     stored version, returning ErrConflict. Callers retry by re-reading.
//   - Put bumps the version unconditionally (last-write-wins). Use it for
//     idempotent overwrites where the caller really does mean "force this".
//
// Three scopes are recognised: agent (per-agent global state), user
// (per-user state inside an agent), and row (per-row state for grid /
// action-table agents like EstateMuse).
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/soyaos/soyaos/pkg/store"
)

// Scope discriminates which keyspace an entry belongs to.
type Scope string

const (
	// ScopeAgent is per-Agent global state shared across all users / rows.
	ScopeAgent Scope = "agent"
	// ScopeUser is per-user state inside an Agent.
	ScopeUser Scope = "user"
	// ScopeRow is per-row state for grid / action-table agents (EstateMuse).
	ScopeRow Scope = "row"
)

// Valid reports whether s is one of the three recognised scopes.
func (s Scope) Valid() bool {
	switch s {
	case ScopeAgent, ScopeUser, ScopeRow:
		return true
	}
	return false
}

// Entry is one versioned key/value pair.
type Entry struct {
	Scope     Scope     `json:"scope"`
	OwnerID   string    `json:"owner_id"` // agent slug, user id, or row id
	Key       string    `json:"key"`
	Value     []byte    `json:"value"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store is the persistence contract for Stateful Agent runtime data.
type Store interface {
	Get(ctx context.Context, scope Scope, owner, key string) (Entry, error)
	Put(ctx context.Context, scope Scope, owner, key string, value []byte) (Entry, error)
	CompareAndSwap(ctx context.Context, scope Scope, owner, key string, baseVersion int64, value []byte) (Entry, error)
	Delete(ctx context.Context, scope Scope, owner, key string) error
	List(ctx context.Context, scope Scope, owner, prefix string) ([]Entry, error)
}

// ErrConflict is returned by CompareAndSwap when the supplied base version
// no longer matches the stored entry — the canonical signal that another
// writer beat the caller to the punch.
var ErrConflict = errors.New("state: version conflict (concurrent modification)")

// ErrNotFound is returned by Get when the (scope, owner, key) tuple is
// unknown.
var ErrNotFound = errors.New("state: key not found")

// ErrInvalidScope is returned when an unrecognised Scope value is supplied.
var ErrInvalidScope = errors.New("state: invalid scope")

// StoreNamespace is the bbolt bucket name used by BoltStore.
const StoreNamespace = "state.entries"

// BoltStore is a Store backed by pkg/store.Store. Entries survive process
// restarts. Composite keys use store.CompositeKeyString(scope, owner, key)
// — length-prefixed so adversarial owner / key strings can't tunnel into
// another scope's keyspace.
type BoltStore struct {
	store store.Store
	// mu serializes every write made through this BoltStore. Put and
	// CompareAndSwap both derive the next value from a preceding read, so
	// locking only the underlying Put transaction would still allow two
	// callers to commit the same version. Delete participates as well so it
	// cannot slip between a CompareAndSwap read and write.
	mu sync.Mutex
}

// NewBoltStore returns a Store backed by s.
func NewBoltStore(s store.Store) *BoltStore { return &BoltStore{store: s} }

// Get fetches by composite identity.
func (b *BoltStore) Get(ctx context.Context, scope Scope, owner, key string) (Entry, error) {
	if !scope.Valid() {
		return Entry{}, fmt.Errorf("%w: %q", ErrInvalidScope, scope)
	}
	body, err := b.store.Get(ctx, StoreNamespace, store.CompositeKeyString(string(scope), owner, key))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, err
	}
	var e Entry
	if err := json.Unmarshal(body, &e); err != nil {
		return Entry{}, fmt.Errorf("state: decode entry: %w", err)
	}
	return e, nil
}

// Put writes value at (scope, owner, key), bumping the version by 1
// (or starting at 1 if the entry didn't exist). Last write wins.
func (b *BoltStore) Put(ctx context.Context, scope Scope, owner, key string, value []byte) (Entry, error) {
	if !scope.Valid() {
		return Entry{}, fmt.Errorf("%w: %q", ErrInvalidScope, scope)
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	existing, err := b.Get(ctx, scope, owner, key)
	var nextVersion int64 = 1
	if err == nil {
		nextVersion = existing.Version + 1
	} else if !errors.Is(err, ErrNotFound) {
		return Entry{}, err
	}
	e := Entry{
		Scope:     scope,
		OwnerID:   owner,
		Key:       key,
		Value:     append([]byte(nil), value...),
		Version:   nextVersion,
		UpdatedAt: time.Now(),
	}
	body, err := json.Marshal(e)
	if err != nil {
		return Entry{}, fmt.Errorf("state: encode entry: %w", err)
	}
	if err := b.store.Put(ctx, StoreNamespace, store.CompositeKeyString(string(scope), owner, key), body); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// CompareAndSwap atomically replaces the value iff the current version
// equals baseVersion. Returns ErrConflict when the version moved on, or
// ErrNotFound when the key never existed (callers asking to swap a
// not-yet-existing entry should pass baseVersion=0).
//
// baseVersion=0 means "this should be the first write to this key";
// CompareAndSwap returns ErrConflict if the key already exists.
func (b *BoltStore) CompareAndSwap(ctx context.Context, scope Scope, owner, key string, baseVersion int64, value []byte) (Entry, error) {
	if !scope.Valid() {
		return Entry{}, fmt.Errorf("%w: %q", ErrInvalidScope, scope)
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	existing, err := b.Get(ctx, scope, owner, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			if baseVersion != 0 {
				return Entry{}, ErrConflict
			}
			// Initial insert.
			e := Entry{
				Scope:     scope,
				OwnerID:   owner,
				Key:       key,
				Value:     append([]byte(nil), value...),
				Version:   1,
				UpdatedAt: time.Now(),
			}
			body, err := json.Marshal(e)
			if err != nil {
				return Entry{}, fmt.Errorf("state: encode entry: %w", err)
			}
			if err := b.store.Put(ctx, StoreNamespace, store.CompositeKeyString(string(scope), owner, key), body); err != nil {
				return Entry{}, err
			}
			return e, nil
		}
		return Entry{}, err
	}
	if existing.Version != baseVersion {
		return Entry{}, ErrConflict
	}
	next := Entry{
		Scope:     scope,
		OwnerID:   owner,
		Key:       key,
		Value:     append([]byte(nil), value...),
		Version:   existing.Version + 1,
		UpdatedAt: time.Now(),
	}
	body, err := json.Marshal(next)
	if err != nil {
		return Entry{}, fmt.Errorf("state: encode entry: %w", err)
	}
	if err := b.store.Put(ctx, StoreNamespace, store.CompositeKeyString(string(scope), owner, key), body); err != nil {
		return Entry{}, err
	}
	return next, nil
}

// Delete removes the entry, no-op on missing key.
func (b *BoltStore) Delete(ctx context.Context, scope Scope, owner, key string) error {
	if !scope.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidScope, scope)
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.store.Delete(ctx, StoreNamespace, store.CompositeKeyString(string(scope), owner, key))
}

// List returns every entry under (scope, owner) whose Key starts with
// prefix. Implementation enumerates the namespace and filters in user
// space — the composite key is length-prefixed so byte-level prefix
// scans against `prefix` would not work directly.
func (b *BoltStore) List(ctx context.Context, scope Scope, owner, prefix string) ([]Entry, error) {
	if !scope.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidScope, scope)
	}
	pairs, err := b.store.List(ctx, StoreNamespace, nil)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0)
	for _, p := range pairs {
		var e Entry
		if err := json.Unmarshal(p.Value, &e); err != nil {
			continue
		}
		if e.Scope != scope || e.OwnerID != owner {
			continue
		}
		if prefix != "" && !hasPrefix(e.Key, prefix) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
