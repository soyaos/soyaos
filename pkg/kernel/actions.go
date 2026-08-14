// actions.go wires per-row Action invocation into the kernel — the
// EstateMuse Aha "Agents have three entry points: conversation,
// scheduled, and per-row action" (DD-010, APP-502).
//
// Conversation and scheduled triggers already exist (ChatCompletion +
// pkg/scheduler). This file adds the third leg: a caller (the
// OpenAI-Compat gateway, in v0.1.0) looks up an Agent's manifest, finds
// an ActionDecl by id, and asks the kernel to dispatch it for a given
// row.
//
// In alpha the action dispatcher just hands the payload to a registered
// ActionHandler (defaulting to a stub that echoes the request). Real
// Stage 5 wiring will route through the sandboxed runtime.
package kernel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

// ErrUnknownAction is returned by InvokeAction when the agent has no
// ActionDecl with the requested id.
var ErrUnknownAction = errors.New("kernel: unknown action id")

// ErrNoManifest is returned by InvokeAction when the agent was registered
// without a Manifest (so we don't know which actions are declared).
var ErrNoManifest = errors.New("kernel: agent has no manifest")

// ActionRequest is the input handed to an ActionHandler.
type ActionRequest struct {
	AgentSlug string
	ActionID  string
	RowID     string
	Payload   map[string]any
	Identity  auth.Identity
}

// ActionResult is what InvokeAction returns to the gateway. TaskID is the
// queueable id callers stash to poll for completion.
type ActionResult struct {
	TaskID     string         `json:"task_id"`
	Status     string         `json:"status"` // queued / running / done / failed
	AgentSlug  string         `json:"agent_slug"`
	ActionID   string         `json:"action_id"`
	RowID      string         `json:"row_id"`
	Output     map[string]any `json:"output,omitempty"`
	EnqueuedAt time.Time      `json:"enqueued_at"`
}

// ActionHandler is the per-Agent action dispatcher. Alpha kernel keeps a
// single default handler that just echoes inputs; a future revision will
// hand off to the sandbox runtime.
type ActionHandler func(ctx context.Context, decl soyapack.ActionDecl, req ActionRequest) (ActionResult, error)

// SetActionHandler replaces the kernel's action handler. Tests use this
// to assert that the dispatcher is invoked with the expected arguments.
//
// Per-Pack handlers registered via RegisterPackAction win over this
// global handler — see InvokeAction. The global handler is the fallback
// for Packs whose manifest declares actions but for which no per-Pack
// handler was registered (e.g. legacy EchoAgent flows).
func (k *Kernel) SetActionHandler(h ActionHandler) {
	k.actionMu.Lock()
	defer k.actionMu.Unlock()
	k.actionHandler = h
}

// RegisterPackAction installs a per-Pack ActionHandler keyed by
// (agentSlug, actionID). RegisterFromPack uses this to wire each
// manifest.actions[] entry to a handler that loads the action's
// prompt file and runs it through the upstream LLM. Tests use it to
// inject deterministic handlers without touching the global dispatcher.
//
// Returning ok=false from Lookup is harmless — InvokeAction falls back
// to the global ActionHandler (set via SetActionHandler) or the default
// echo handler.
func (k *Kernel) RegisterPackAction(agentSlug, actionID string, h ActionHandler) {
	k.actionMu.Lock()
	defer k.actionMu.Unlock()
	if k.packActions == nil {
		k.packActions = map[string]map[string]ActionHandler{}
	}
	if k.packActions[agentSlug] == nil {
		k.packActions[agentSlug] = map[string]ActionHandler{}
	}
	k.packActions[agentSlug][actionID] = h
}

// lookupPackAction returns the registered per-Pack ActionHandler for
// (agentSlug, actionID), if any. The boolean second return reports
// presence so callers can distinguish "registered nil" from "absent".
func (k *Kernel) lookupPackAction(agentSlug, actionID string) (ActionHandler, bool) {
	k.actionMu.RLock()
	defer k.actionMu.RUnlock()
	if k.packActions == nil {
		return nil, false
	}
	bySlug, ok := k.packActions[agentSlug]
	if !ok {
		return nil, false
	}
	h, ok := bySlug[actionID]
	return h, ok
}

// kernelActionFields is the mixin embedded into Kernel via the
// `actionMu` / `actionHandler` fields declared on Kernel below. (We
// declare them in this file so all action-specific state stays here.)
//
// Keeping the lock separate from the agents-map lock means an in-flight
// action invocation does not block agent registration / lookup.

// GetAgentManifest returns the manifest of a registered Agent, looked up
// by slug. Returns (nil, false) when the agent is unknown or has no
// manifest attached.
func (k *Kernel) GetAgentManifest(slug string) (*soyapack.Manifest, bool) {
	a, ok := k.Lookup(slug)
	if !ok || a.Manifest == nil {
		return nil, false
	}
	return a.Manifest, true
}

// InvokeAction dispatches a per-row Action for the named agent. Returns
// ErrUnknownAgent / ErrNoManifest / ErrUnknownAction for the three
// canonical failure modes, otherwise the result of the action handler.
//
// The alpha implementation runs the handler synchronously and returns a
// completed ActionResult. Even so it stamps a TaskID so callers can
// store the result against a stable correlation id.
func (k *Kernel) InvokeAction(ctx context.Context, id auth.Identity, slug, actionID, rowID string, payload map[string]any) (ActionResult, error) {
	agent, ok := k.Lookup(slug)
	if !ok {
		return ActionResult{}, fmt.Errorf("%w: %s", ErrUnknownAgent, slug)
	}
	if agent.Manifest == nil {
		return ActionResult{}, fmt.Errorf("%w: %s", ErrNoManifest, slug)
	}
	var decl soyapack.ActionDecl
	found := false
	for _, a := range agent.Manifest.Actions {
		if a.ID == actionID {
			decl = a
			found = true
			break
		}
	}
	if !found {
		return ActionResult{}, fmt.Errorf("%w: %s/%s", ErrUnknownAction, slug, actionID)
	}

	// Resolution order: per-Pack handler (registered by RegisterFromPack)
	// wins, then the global ActionHandler (set via SetActionHandler), then
	// the default echo handler. This ordering lets a Pack ship its own
	// per-row handler without having to take over the global dispatch
	// surface, while leaving older tests that wire SetActionHandler
	// untouched.
	h, ok := k.lookupPackAction(agent.Slug, actionID)
	if !ok {
		k.actionMu.RLock()
		h = k.actionHandler
		k.actionMu.RUnlock()
	}
	if h == nil {
		h = defaultActionHandler
	}
	req := ActionRequest{
		AgentSlug: agent.Slug,
		ActionID:  decl.ID,
		RowID:     rowID,
		Payload:   payload,
		Identity:  id,
	}
	stored, err := k.loadRowPayload(ctx, agent, id, rowID)
	if err != nil {
		return ActionResult{}, err
	}
	if stored != nil {
		// Caller fields may carry action-specific options, but the persisted
		// workbook row is authoritative for the original topic context.
		merged := make(map[string]any, len(payload)+len(stored))
		for key, value := range payload {
			merged[key] = value
		}
		for key, value := range stored {
			merged[key] = value
		}
		req.Payload = merged
	}
	return h(ctx, decl, req)
}

// defaultActionHandler is the alpha placeholder: it stamps a TaskID,
// marks the action as queued, and echoes the request payload. Future
// revisions will route to the sandboxed runtime.
func defaultActionHandler(_ context.Context, decl soyapack.ActionDecl, req ActionRequest) (ActionResult, error) {
	return ActionResult{
		TaskID:     newTaskID(),
		Status:     "queued",
		AgentSlug:  req.AgentSlug,
		ActionID:   decl.ID,
		RowID:      req.RowID,
		Output:     map[string]any{"echo": req.Payload, "handler": decl.Handler},
		EnqueuedAt: time.Now(),
	}, nil
}

// newTaskID returns a 16-byte hex correlation id. Not crypto-grade — it's
// a request correlator, not a security token.
func newTaskID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// Fall back to a time-only id so we never hand the caller an
		// empty string. The collision risk is irrelevant for alpha
		// debugging traffic.
		return fmt.Sprintf("task-%x", time.Now().UnixNano())
	}
	return "task-" + hex.EncodeToString(raw[:])
}
