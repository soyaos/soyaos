// Package kernel is SoyaKernel — the routing brain that maps a virtual model
// id (e.g. "soya:echo") to a registered Agent, drives the chosen model
// Provider, and emits structured chunks to the caller.
//
// In v0.1.0-alpha.0 the kernel implements only the Solo path:
//
//  1. The OpenAI-Compat Gateway resolves Authorization → Identity via pkg/auth.
//  2. The Gateway calls kernel.ChatCompletion with the resolved Identity.
//  3. The kernel finds the registered Agent for the model id.
//  4. The Agent's Handler is invoked; its returned chunks are forwarded.
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
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

// VirtualModelPrefix is the locked-in prefix for SoyaOS-hosted virtual
// model ids (terminology patch in 设计文档对齐清单). Every registered Agent
// must expose at least one model id of the form "soya:<slug>".
const VirtualModelPrefix = "soya:"

// Handler is the per-Agent execution callback. The kernel hands it the
// authenticated Identity, the upstream chat Request, and a channel into
// which the Agent streams Chunks.
type Handler func(ctx context.Context, id auth.Identity, req llmcall.Request, out chan<- llmcall.Chunk) error

// Agent is a registered Agent descriptor.
type Agent struct {
	Slug        string             // matches the suffix after VirtualModelPrefix
	Description string             // one-line summary
	Handler     Handler            // invocation entry-point
	Manifest    *soyapack.Manifest // optional: declarative manifest for actions/state/etc. (APP-502)
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

	// Per-row Action dispatch (APP-502). Lock is separate from `mu` so an
	// in-flight action does not block agent registration / lookup.
	actionMu      sync.RWMutex
	actionHandler ActionHandler

	// Optional pluggable hooks consumed by RegisterFromPack when a Pack
	// declares `schedules:` (DD-007) or `channels:` (DD-006) blocks.
	// Each hook is independent: a host may wire only the scheduler, only
	// the channel publisher, both, or neither. When unset the kernel
	// logs a warning via Warnf and continues — schedules and channels
	// are best-effort; an unwired hook must never block agent registration.
	hooksMu      sync.RWMutex
	scheduleHook ScheduleHook
	channelHook  ChannelHook
	logger       func(format string, args ...any)
}

// ScheduleHook is the per-Pack scheduler-registration callback the
// host wires when it owns a running scheduler. RegisterFromPack
// invokes this once per manifest.schedules[] entry. The Fire callback
// runs the Agent's Handler with no caller-supplied user message —
// schedules are autonomous triggers, not chat turns.
type ScheduleHook func(jobID string, decl ScheduleSpec, fire func(ctx context.Context)) error

// ScheduleSpec mirrors the wire-level fields the kernel needs to hand
// over without forcing pkg/kernel to import pkg/scheduler (which would
// pull in bbolt + the time wheel). The host adapter translates this
// into scheduler.Job at registration time.
type ScheduleSpec struct {
	Cron           string
	Once           string
	TZ             string
	IdempotencyKey string
	MissedFire     string
	Payload        map[string]any
}

// ChannelHook is the per-Pack outbound-publisher resolver. When a Pack
// declares `channels:`, the kernel asks the host to produce a
// ChannelPublisher for each entry — typically by looking up the
// channel kind in a connector registry, resolving the env-var-ref
// secrets, and binding the result. The kernel never reads env vars
// directly; resolution stays in the host so test harnesses can
// inject fake credentials.
type ChannelHook func(decl ChannelBindingSpec) (ChannelPublisher, error)

// ChannelBindingSpec is the wire shape of one manifest.channels[]
// entry handed to a ChannelHook.
type ChannelBindingSpec struct {
	Kind      string
	BindingID string
	Secrets   map[string]string // values are still ${ENV_NAME} refs; host resolves
}

// ChannelPublisher is the outbound side of a wired channel. The
// kernel calls Send() with the Agent's final artifact body after a
// schedule fire (or, eventually, after any chat completion).
type ChannelPublisher interface {
	Send(ctx context.Context, title, body string) error
}

// SetScheduleHook wires the per-Pack scheduler-registration callback.
func (k *Kernel) SetScheduleHook(h ScheduleHook) {
	k.hooksMu.Lock()
	defer k.hooksMu.Unlock()
	k.scheduleHook = h
}

// SetChannelHook wires the per-Pack outbound-publisher resolver.
func (k *Kernel) SetChannelHook(h ChannelHook) {
	k.hooksMu.Lock()
	defer k.hooksMu.Unlock()
	k.channelHook = h
}

// SetLogger installs a host-side logger for kernel-level warnings
// (best-effort schedule/channel wiring failures). Defaults to a
// silent logger.
func (k *Kernel) SetLogger(logger func(format string, args ...any)) {
	k.hooksMu.Lock()
	defer k.hooksMu.Unlock()
	k.logger = logger
}

func (k *Kernel) getHooks() (ScheduleHook, ChannelHook, func(format string, args ...any)) {
	k.hooksMu.RLock()
	defer k.hooksMu.RUnlock()
	logger := k.logger
	if logger == nil {
		logger = func(string, ...any) {}
	}
	return k.scheduleHook, k.channelHook, logger
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
// llmcall.Response.
func (k *Kernel) ChatCompletion(ctx context.Context, id auth.Identity, req llmcall.Request) (llmcall.Response, error) {
	agent, ok := k.Lookup(req.Model)
	if !ok {
		return llmcall.Response{}, fmt.Errorf("%w: %s", ErrUnknownAgent, req.Model)
	}

	out := make(chan llmcall.Chunk, 8)
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
		return llmcall.Response{}, err
	}
	return llmcall.Response{Model: agent.ModelID(), Content: sb.String()}, nil
}

// ChatCompletionStream drives a streaming completion. Chunks are written to
// out as they arrive. The channel is closed by the kernel once the Agent
// signals Done (or an error occurs).
func (k *Kernel) ChatCompletionStream(ctx context.Context, id auth.Identity, req llmcall.Request, out chan<- llmcall.Chunk) error {
	agent, ok := k.Lookup(req.Model)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAgent, req.Model)
	}
	return agent.Handler(ctx, id, req, out)
}
