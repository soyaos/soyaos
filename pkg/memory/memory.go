// Package memory is SoyaMemory — the layered memory subsystem.
//
// The architecture spec defines four layers: working, episodic, semantic, and
// procedural. v0.1.0-alpha.0 ships only the contract types and an in-memory
// KV store usable by Agent-scoped state (DD-010). Vector recall and
// cross-node sync land alongside Cluster.
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

// Entry is a key/value pair in a memory store, tagged with metadata.
type Entry struct {
	Key       string
	Value     []byte
	Layer     Layer
	Scope     Scope
	Owner     string // agent slug, user id, or tenant id depending on scope
	UpdatedAt time.Time
}

// ErrNotFound is returned by KV.Get when the key is unknown.
var ErrNotFound = errors.New("memory: not found")

// KV is the minimal key/value memory contract.
type KV interface {
	Get(ctx context.Context, scope Scope, owner, key string) (Entry, error)
	Put(ctx context.Context, entry Entry) error
	Delete(ctx context.Context, scope Scope, owner, key string) error
}

// InMemory is the Solo / test KV backend.
type InMemory struct {
	mu   sync.RWMutex
	data map[string]Entry // composite key: scope|owner|key
}

// NewInMemory returns an empty in-memory KV store.
func NewInMemory() *InMemory { return &InMemory{data: map[string]Entry{}} }

func key(s Scope, owner, k string) string { return string(s) + "|" + owner + "|" + k }

// Get fetches by composite identity.
func (m *InMemory) Get(_ context.Context, s Scope, owner, k string) (Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.data[key(s, owner, k)]
	if !ok {
		return Entry{}, ErrNotFound
	}
	return e, nil
}

// Put writes an entry, replacing any prior value.
func (m *InMemory) Put(_ context.Context, e Entry) error {
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = time.Now()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key(e.Scope, e.Owner, e.Key)] = e
	return nil
}

// Delete removes an entry by composite identity. Missing keys are no-ops.
func (m *InMemory) Delete(_ context.Context, s Scope, owner, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key(s, owner, k))
	return nil
}
