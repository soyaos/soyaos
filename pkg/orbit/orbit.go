// Package orbit implements node-registry / lifecycle / bootstrap concerns
// for SoyaOS — the Planet ↔ Moon ↔ Comet topology described in the
// architecture doc.
//
// v0.1.0-alpha.0 ships only the in-process Solo flavor: a registry that
// holds the three role descriptors of the running process, so other modules
// can introspect "am I Planet? am I Moon? am I Comet?".
//
// Reverse-dial-first (all Moon/Comet connections originate inside the
// intranet) and bootstrap-token lifecycle land alongside the distributed
// Cluster edition.
package orbit

import (
	"sync"
	"time"
)

// Role labels each of the three SoyaOS node roles.
type Role string

const (
	RolePlanet Role = "planet"
	RoleMoon   Role = "moon"
	RoleComet  Role = "comet"
)

// Node describes one node-role instance within a running SoyaOS process.
type Node struct {
	ID         string    // stable id for this node
	Role       Role      // planet / moon / comet
	StartedAt  time.Time // process boot time for this role
	HostsComet bool      // true when this node is willing to host Comet tasks
}

// Registry is the in-process map of active node roles. In Solo mode it holds
// exactly three Nodes — one Planet, one Moon, one Comet — all backed by the
// same process.
type Registry struct {
	mu    sync.RWMutex
	nodes map[string]Node
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{nodes: map[string]Node{}} }

// Register adds (or replaces) a Node.
func (r *Registry) Register(n Node) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[n.ID] = n
}

// Deregister removes a Node by id.
func (r *Registry) Deregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.nodes, id)
}

// List returns a snapshot of all registered Nodes.
func (r *Registry) List() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, n)
	}
	return out
}

// SeedSolo registers the three in-process roles for Solo mode.
//
// HostsComet is set on the Comet node — task scheduling will route Comet
// jobs to it. Planet and Moon do not host Comet tasks by default in Solo.
func (r *Registry) SeedSolo(now time.Time) {
	r.Register(Node{ID: "planet-local", Role: RolePlanet, StartedAt: now})
	r.Register(Node{ID: "moon-local", Role: RoleMoon, StartedAt: now})
	r.Register(Node{ID: "comet-local", Role: RoleComet, StartedAt: now, HostsComet: true})
}
