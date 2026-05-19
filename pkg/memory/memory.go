// Package memory is SoyaMemory — the layered memory subsystem.
//
// The architecture spec defines four layers: working, episodic, semantic, and
// procedural. v0.1.0-alpha ships only the contract types, an in-memory KV
// store, and a Store-backed KV (added by APP-461) usable by Agent-scoped
// state (DD-010). Vector recall and cross-node sync land alongside Cluster.
package memory

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Layer enumerates the four canonical layers from the architecture spec.
type Layer string

const (
	LayerWorking    Layer = "working"
	LayerEpisodic   Layer = "episodic"
	LayerSemantic   Layer = "semantic"
	LayerProcedural Layer = "procedural"
)

// Scope distinguishes user-scoped memory from Agent-scoped state (DD-010).
type Scope string

const (
	ScopeAgent  Scope = "agent"
	ScopeUser   Scope = "user"
	ScopeTenant Scope = "tenant"
)

// Sensitivity labels each Entry for downstream redaction / audit policies.
// Locked vocabulary so callers across modules speak the same words.
type Sensitivity string

const (
	SensitivityLow    Sensitivity = "low"
	SensitivityMedium Sensitivity = "medium"
	SensitivityHigh   Sensitivity = "high"
)

// Entry is a key/value pair in a memory store, tagged with metadata.
//
// TTL + Sensitivity were added in APP-461 (R0 Agent-memory-systems review
// P1) so EstateMuse-era Agent-scoped state can declare retention and
// downstream auditors can apply redaction policies.
type Entry struct {
	Key         string
	Value       []byte
	Layer       Layer
	Scope       Scope
	Owner       string // agent slug, user id, or tenant id depending on scope
	UpdatedAt   time.Time
	TTL         time.Duration // zero means no expiry
	Sensitivity Sensitivity   // empty defaults to SensitivityLow for legacy callers
}

// ExpiresAt computes the deadline from UpdatedAt + TTL. Zero TTL returns
// the zero time (no expiry).
func (e Entry) ExpiresAt() time.Time {
	if e.TTL == 0 {
		return time.Time{}
	}
	return e.UpdatedAt.Add(e.TTL)
}

// Expired reports whether the entry's TTL elapsed before now.
func (e Entry) Expired(now time.Time) bool {
	exp := e.ExpiresAt()
	return !exp.IsZero() && now.After(exp)
}

// ErrNotFound is returned by KV.Get when the key is unknown.
var ErrNotFound = errors.New("memory: not found")

// KV is the minimal key/value memory contract.
type KV interface {
	Get(ctx context.Context, scope Scope, owner, key string) (Entry, error)
	Put(ctx context.Context, entry Entry) error
	Delete(ctx context.Context, scope Scope, owner, key string) error
	List(ctx context.Context, scope Scope, owner, prefix string) ([]Entry, error)
}

// InMemory is the Solo / test KV backend, intentionally separate from the
// Store-backed one in store_backed.go so unit tests that don't need disk
// can skip opening bolt.
type InMemory struct {
	mu   sync.RWMutex
	data map[string]Entry // composite key: see compositeKey()
}

// NewInMemory returns an empty in-memory KV store.
func NewInMemory() *InMemory { return &InMemory{data: map[string]Entry{}} }

// compositeKey replaces the previous `|`-separator scheme with a NUL-safe
// length-style separator. Not bullet-proof against adversarial owners
// (callers should still sanitize), but matches what StoreBacked does and
// closes the obvious injection footgun R0 flagged.
func compositeKey(s Scope, owner, k string) string {
	return string(s) + "\x00" + owner + "\x00" + k
}

// Get fetches by composite identity. Expired entries are reported as
// ErrNotFound (lazy cleanup).
func (m *InMemory) Get(_ context.Context, s Scope, owner, k string) (Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.data[compositeKey(s, owner, k)]
	if !ok {
		return Entry{}, ErrNotFound
	}
	if e.Expired(time.Now()) {
		return Entry{}, ErrNotFound
	}
	return e, nil
}

// Put writes an entry, replacing any prior value. Empty Sensitivity is
// coerced to "low" so downstream consumers always see a value.
func (m *InMemory) Put(_ context.Context, e Entry) error {
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = time.Now()
	}
	if e.Sensitivity == "" {
		e.Sensitivity = SensitivityLow
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[compositeKey(e.Scope, e.Owner, e.Key)] = e
	return nil
}

// Delete removes an entry by composite identity. Missing keys are no-ops.
func (m *InMemory) Delete(_ context.Context, s Scope, owner, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, compositeKey(s, owner, k))
	return nil
}

// List returns entries under (scope, owner) whose Key starts with prefix.
// Expired entries are skipped.
func (m *InMemory) List(_ context.Context, s Scope, owner, prefix string) ([]Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Entry, 0)
	now := time.Now()
	for _, e := range m.data {
		if e.Scope != s || e.Owner != owner {
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

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
