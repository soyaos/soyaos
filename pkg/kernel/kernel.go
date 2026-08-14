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
	"io"
	"strings"
	"sync"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
	"github.com/soyaos/soyaos/pkg/state"
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

	// Stateful Packs persist their latest completion and row payloads through
	// this host-provided store. The Solo host wires a bbolt-backed instance;
	// tests and embedded hosts may supply any implementation of state.Store.
	stateMu    sync.RWMutex
	stateStore state.Store

	// Per-row Action dispatch (APP-502). Lock is separate from `mu` so an
	// in-flight action does not block agent registration / lookup.
	//
	// `actionHandler` is the global fallback; `packActions` is the per-
	// (agentSlug, actionID) registry RegisterFromPack populates so each
	// Pack can ship its own prompt-backed handler (DD-010 EstateMuse —
	// APP-553).
	actionMu      sync.RWMutex
	actionHandler ActionHandler
	packActions   map[string]map[string]ActionHandler

	// Optional pluggable hooks consumed by RegisterFromPack when a Pack
	// declares `schedules:` (DD-007), `channels:` (DD-006) or
	// `storage_nas:` (DD-011 SilentCut — APP-554) blocks.
	//
	// Each hook is independent: a host may wire only the scheduler, only
	// the channel publisher, both, or none of the above. When a hook is
	// unset the kernel logs a warning via Warnf and continues —
	// schedules / channels / storage_nas are best-effort; an unwired
	// hook must never block agent registration.
	//
	// `nasTargets` caches the resolved NAS targets per agent slug so the
	// agent's Handler (or its actions) can write artifacts home without
	// re-resolving on every invocation.
	hooksMu      sync.RWMutex
	scheduleHook ScheduleHook
	channelHook  ChannelHook
	nasHook      NASHook
	nasTargets   map[string]map[string]NASTarget
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

// NASHook is the per-Pack NAS-target resolver. When a Pack declares
// `storage_nas:` (DD-011 SilentCut — APP-554), the kernel asks the
// host to produce one NASTarget per entry — typically by resolving
// the env-var-ref host + secrets, opening a pkg/connectors/nas.NAS
// handle, and returning both. The kernel never reads env vars
// directly; resolution stays in the host so test harnesses can
// inject fake handles.
type NASHook func(decl NASBindingSpec) (NASTarget, error)

// NASBindingSpec is the wire shape of one manifest.storage_nas[]
// entry handed to a NASHook.
type NASBindingSpec struct {
	// ID is the per-Pack target identifier (defaults to "primary").
	ID string
	// Protocol is one of smb/nfs/webdav/s3.
	Protocol string
	// HostRef is the env-var ref ("${SOYA_NAS_HOST}") or literal
	// host; the host adapter is responsible for resolving env refs.
	HostRef string
	// Share is protocol-specific (SMB share, NFS export, WebDAV
	// root, S3 bucket).
	Share string
	// Access is "ro" / "rw".
	Access string
	// Secrets carries the env-var refs the host adapter dereferences.
	Secrets map[string]string
}

// NASTarget is a resolved NAS handle plus the path layout the Agent
// should write under. Comet calls Handle.Write(ctx, BasePath+"/...")
// when emitting artifacts.
//
// Handle is io.Closer-shaped via its embedded pkg/connectors/nas.NAS
// — see that package for the full surface. We hold the interface
// directly here so kernel callers don't need to import the connector
// types just to inspect their target.
type NASTarget struct {
	ID       string
	Protocol string
	BasePath string // protocol-specific, typically the resolved Share path
	Handle   NASWriter
}

// NASWriter is the minimal write contract kernel callers care about.
// pkg/connectors/nas.NAS satisfies this directly — defining the
// interface locally avoids a kernel → connectors import cycle.
type NASWriter interface {
	// Write copies r to path on the remote share and returns the
	// number of bytes delivered.
	Write(ctx context.Context, path string, r io.Reader) (int64, error)
	// Close releases the underlying connection.
	Close() error
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

// SetNASHook wires the per-Pack NAS target resolver (DD-011 SilentCut).
// When unset, manifest.storage_nas[] entries are best-effort skipped
// with a warning — the Agent still registers and remains chat-reachable.
func (k *Kernel) SetNASHook(h NASHook) {
	k.hooksMu.Lock()
	defer k.hooksMu.Unlock()
	k.nasHook = h
}

// LookupNAS returns the resolved NASTarget for the named agent slug
// and target id ("primary" when the manifest's storage_nas[*].id was
// omitted). Returns (zero, false) when no target was resolved — either
// because the Pack declared no storage_nas[], or because the NASHook
// was unset / errored at registration time.
//
// Agent action handlers call this when they want to write a final
// artifact (SilentCut's MP4) home. Calling LookupNAS from inside the
// handler keeps the lifecycle simple: the kernel owns the handle, the
// handler borrows it.
func (k *Kernel) LookupNAS(agentSlug, targetID string) (NASTarget, bool) {
	k.hooksMu.RLock()
	defer k.hooksMu.RUnlock()
	if k.nasTargets == nil {
		return NASTarget{}, false
	}
	bySlug, ok := k.nasTargets[agentSlug]
	if !ok {
		return NASTarget{}, false
	}
	if targetID == "" {
		targetID = "primary"
	}
	t, ok := bySlug[targetID]
	return t, ok
}

// storeNASTarget records the resolved NASTarget under the agent slug.
// Called from pack_agent.go after RegisterFromPack resolves
// manifest.storage_nas[] via the configured hook.
func (k *Kernel) storeNASTarget(agentSlug string, t NASTarget) {
	k.hooksMu.Lock()
	defer k.hooksMu.Unlock()
	if k.nasTargets == nil {
		k.nasTargets = map[string]map[string]NASTarget{}
	}
	if k.nasTargets[agentSlug] == nil {
		k.nasTargets[agentSlug] = map[string]NASTarget{}
	}
	if t.ID == "" {
		t.ID = "primary"
	}
	k.nasTargets[agentSlug][t.ID] = t
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

// getNASHook is split out from getHooks because nasHook arrived later
// (APP-554) and only one caller — registerFromPack — needs it.
func (k *Kernel) getNASHook() NASHook {
	k.hooksMu.RLock()
	defer k.hooksMu.RUnlock()
	return k.nasHook
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
	content := sb.String()
	if err := k.persistAgentCompletion(ctx, agent, id, content); err != nil {
		return llmcall.Response{}, err
	}
	return llmcall.Response{Model: agent.ModelID(), Content: content}, nil
}

// ChatCompletionStream drives a streaming completion. Chunks are written to
// out as they arrive. The channel is closed by the kernel once the Agent
// signals Done (or an error occurs).
func (k *Kernel) ChatCompletionStream(ctx context.Context, id auth.Identity, req llmcall.Request, out chan<- llmcall.Chunk) error {
	agent, ok := k.Lookup(req.Model)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAgent, req.Model)
	}

	inner := make(chan llmcall.Chunk, 8)
	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Handler(ctx, id, req, inner)
		close(inner)
	}()

	var content strings.Builder
	var done *llmcall.Chunk
	for chunk := range inner {
		if chunk.Done {
			copy := chunk
			done = &copy
			continue
		}
		content.WriteString(chunk.Delta)
		out <- chunk
	}
	if err := <-errCh; err != nil {
		return err
	}
	if err := k.persistAgentCompletion(ctx, agent, id, content.String()); err != nil {
		return err
	}
	if done != nil {
		out <- *done
	}
	return nil
}
