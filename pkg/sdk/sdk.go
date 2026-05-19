// Package sdk is the Go SDK for SoyaOS Agent authors.
//
// Today it is a thin façade: the SDK exposes a single ergonomic Agent
// builder that wires a Handler into the kernel without forcing authors to
// import internal packages. As the M0 prerequisites land (Artifact
// abstraction, Channel Connectors, Scheduler, Stateful state), each will
// gain a builder on this surface.
//
// External Agent authors should depend on this package, not on pkg/kernel
// directly.
package sdk

import (
	"context"
	"errors"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/kernel"
	"github.com/soyaos/soyaos/pkg/modelgw"
)

// Reply is the value returned by an Agent's chat handler. Use New() or
// stream by writing to the channel that Build() opens.
type Reply struct {
	Content string
}

// ChatFunc is the friendlier form of kernel.Handler — authors implement this
// and the SDK takes care of streaming plumbing.
type ChatFunc func(ctx context.Context, id auth.Identity, msgs []modelgw.Message) (Reply, error)

// Agent is an SDK-level Agent descriptor.
type Agent struct {
	Slug        string
	Description string
	Chat        ChatFunc
}

// ErrEmptyChat is returned when an Agent is built without a Chat func.
var ErrEmptyChat = errors.New("sdk: Agent.Chat is nil")

// Build converts an SDK Agent into a kernel.Agent that the runtime can
// register. The resulting Handler streams Reply.Content as a single chunk.
func (a Agent) Build() (kernel.Agent, error) {
	if a.Chat == nil {
		return kernel.Agent{}, ErrEmptyChat
	}
	chat := a.Chat
	handler := func(ctx context.Context, id auth.Identity, req modelgw.Request, out chan<- modelgw.Chunk) error {
		reply, err := chat(ctx, id, req.Messages)
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- modelgw.Chunk{Delta: reply.Content}:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- modelgw.Chunk{Done: true}:
		}
		return nil
	}
	return kernel.Agent{Slug: a.Slug, Description: a.Description, Handler: handler}, nil
}
