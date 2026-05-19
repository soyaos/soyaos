// Package kernel is SoyaKernel — the routing brain that maps a virtual model
// id (e.g. "soya:echo") to a registered Agent, drives the chosen model
// Provider, and emits structured chunks to the caller.
//
// In v0.1.0-alpha.0 the kernel implements only the Solo path:
//
//   1. The OpenAI-Compat Gateway resolves Authorization → Identity via pkg/auth.
//   2. The Gateway calls kernel.ChatCompletion with the resolved Identity.
//   3. The kernel finds the registered Agent for the model id.
//   4. The Agent's Handler is invoked; its returned chunks are forwarded.
//
// This is enough to ship a working OpenAI-Compat smoke test through an Echo
// Agent — and it leaves room for real LLM-backed Agents (DD-008 / 009 / 010
// / 011) to plug in by registering an Agent with a non-echo Handler.
package kernel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/modelgw"
)

// VirtualModelPrefix is the locked-in prefix for SoyaOS-hosted virtual
// model ids (terminology patch in 设计文档对齐清单). Every registered Agent
// must expose at least one model id of the form "soya:<slug>".
const VirtualModelPrefix = "soya:"

// Handler is the per-Agent execution callback. The kernel hands it the
// authenticated Identity, the upstream chat Request, and a channel into
// which the Agent streams Chunks.
type Handler func(ctx context.Context, id auth.Identity, req modelgw.Request, out chan<- modelgw.Chunk) error

// Agent is a registered Agent descriptor.
type Agent struct {
	Slug        string  // matches the suffix after VirtualModelPrefix
	Description string  // one-line summary
	Handler     Handler // invocation entry-point
}

// ModelID returns the canonical virtual model id (e.g. "soya:echo").
func (a Agent) ModelID() string { return VirtualModelPrefix + a.Slug }

// ErrUnknownAgent is returned when no Agent is registered under the given
// model id.
var ErrUnknownAgent = errors.New("kernel: unknown agent / model id")

// Kernel is the runtime registry plus dispatch logic.
type Kernel struct {
	mu     sync.RWMutex
	agents map[string]Agent // keyed by full model id ("soya:echo")
}

// New returns an empty kernel.
func New() *Kernel { return &Kernel{agents: map[string]Agent{}} }

// Register adds (or replaces) an Agent in the kernel.
func (k *Kernel) Register(a Agent) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.agents[a.ModelID()] = a
}

// List returns every registered Agent.
func (k *Kernel) List() []Agent {
	k.mu.RLock()
	defer k.mu.RUnlock()
	out := make([]Agent, 0, len(k.agents))
	for _, a := range k.agents {
		out = append(out, a)
	}
	return out
}

// Lookup finds an Agent by model id. The model id can be either the full
// `soya:<slug>` form or a bare `<slug>` for compatibility with clients that
// can't carry the prefix.
func (k *Kernel) Lookup(modelID string) (Agent, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if a, ok := k.agents[modelID]; ok {
		return a, true
	}
	if !strings.HasPrefix(modelID, VirtualModelPrefix) {
		if a, ok := k.agents[VirtualModelPrefix+modelID]; ok {
			return a, true
		}
	}
	return Agent{}, false
}

// ChatCompletion drives a non-streaming completion. The kernel collects all
// chunks from the Agent's Handler and concatenates them into a single
// modelgw.Response.
func (k *Kernel) ChatCompletion(ctx context.Context, id auth.Identity, req modelgw.Request) (modelgw.Response, error) {
	agent, ok := k.Lookup(req.Model)
	if !ok {
		return modelgw.Response{}, fmt.Errorf("%w: %s", ErrUnknownAgent, req.Model)
	}

	out := make(chan modelgw.Chunk, 8)
	errCh := make(chan error, 1)
	go func() { errCh <- agent.Handler(ctx, id, req, out); close(out) }()

	var sb strings.Builder
	for c := range out {
		if c.Done {
			break
		}
		sb.WriteString(c.Delta)
	}
	if err := <-errCh; err != nil {
		return modelgw.Response{}, err
	}
	return modelgw.Response{Model: agent.ModelID(), Content: sb.String()}, nil
}

// ChatCompletionStream drives a streaming completion. Chunks are written to
// out as they arrive. The channel is closed by the kernel once the Agent
// signals Done (or an error occurs).
func (k *Kernel) ChatCompletionStream(ctx context.Context, id auth.Identity, req modelgw.Request, out chan<- modelgw.Chunk) error {
	agent, ok := k.Lookup(req.Model)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAgent, req.Model)
	}
	return agent.Handler(ctx, id, req, out)
}
