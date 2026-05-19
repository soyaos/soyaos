// Package dispatcher routes Agent invocations across the SoyaOS topology.
//
// v0.1.0-alpha.0 ships a single in-process Dispatcher: incoming kernel calls
// are forwarded directly to the local Agent runtime. The interfaces here are
// shaped to admit the distributed Dispatcher (DAG, affinity, preemption)
// described in the architecture doc without touching callers.
package dispatcher

import (
	"context"
	"errors"
)

// ErrNoExecutor is returned by Dispatcher.Dispatch when no executor accepts
// the given request.
var ErrNoExecutor = errors.New("dispatcher: no executor for request")

// Request is the abstract invocation handed to an Executor.
type Request struct {
	AgentID string         // target agent slug (e.g. "echo", "compo")
	Input   map[string]any // structured payload; semantics are agent-specific
	Stream  bool           // caller wants streamed output if supported
}

// Chunk is a single piece of a streamed response.
type Chunk struct {
	Text  string
	Done  bool
	Error error
}

// Executor handles a single Request. The Solo dispatcher delegates straight
// to the local Executor; distributed editions add affinity, queueing and
// retries on top.
type Executor interface {
	Execute(ctx context.Context, req Request, out chan<- Chunk) error
}

// Dispatcher is the public dispatch API. Solo registers exactly one Executor
// (the in-process kernel); Cluster registers a remote-call Executor.
type Dispatcher struct {
	exec Executor
}

// New returns a Dispatcher backed by exec.
func New(exec Executor) *Dispatcher { return &Dispatcher{exec: exec} }

// Dispatch runs req. The chunks channel will be closed when execution finishes.
func (d *Dispatcher) Dispatch(ctx context.Context, req Request, out chan<- Chunk) error {
	if d.exec == nil {
		return ErrNoExecutor
	}
	return d.exec.Execute(ctx, req, out)
}
