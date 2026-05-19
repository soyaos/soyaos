// Package mesh is SoyaMesh — the overlay network connecting Planet, Moon and
// Comet nodes.
//
// In Solo (All-in-One) mode SoyaMesh degenerates to direct function calls:
// the three "remote" peers are pointers in the same process. The Transport
// interface defined here is what the Cluster/Cloud editions will satisfy
// with QUIC + libp2p + STUN/TURN (see SoyaOS Architecture §"Moon ↔ Comet
// direct" + §"STUN/TURN sourcing").
package mesh

import (
	"context"
	"errors"
	"sync"
)

// ErrNoRoute is returned when no transport can reach the requested peer.
var ErrNoRoute = errors.New("mesh: no route to peer")

// Transport delivers a message from this node to a named peer. The contents
// and framing are opaque — callers above this layer (Dispatcher, Kernel)
// supply structured payloads.
type Transport interface {
	// Send delivers payload to peer. In-process implementations may return
	// the response synchronously; out-of-process transports wrap a stream.
	Send(ctx context.Context, peer string, payload []byte) ([]byte, error)
}

// Handler processes an inbound payload addressed to this node.
type Handler func(ctx context.Context, payload []byte) ([]byte, error)

// InProcess is the Solo-edition transport — all peers share the same Go
// process, so sends are direct function dispatches.
type InProcess struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewInProcess returns an empty in-process transport.
func NewInProcess() *InProcess { return &InProcess{handlers: map[string]Handler{}} }

// Mount registers a Handler for a logical peer name.
func (t *InProcess) Mount(peer string, h Handler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handlers[peer] = h
}

// Send dispatches synchronously to the Handler for peer.
func (t *InProcess) Send(ctx context.Context, peer string, payload []byte) ([]byte, error) {
	t.mu.RLock()
	h, ok := t.handlers[peer]
	t.mu.RUnlock()
	if !ok {
		return nil, ErrNoRoute
	}
	return h(ctx, payload)
}
