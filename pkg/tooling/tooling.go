// Package tooling is the SoyaOS tool registry — the MCP / A2A compatibility
// layer (architecture spec §"Tooling").
//
// Agents declare which tools they may invoke; the registry hands back a Tool
// handle that the kernel can execute, subject to permission checks from
// SoyaAuth.
//
// v0.1.0-alpha.0 ships the local in-memory registry only. MCP / A2A protocol
// adapters and remote tool servers land in later milestones.
package tooling

import (
	"context"
	"errors"
	"sync"
)

// Tool is a callable capability with a stable name and a JSON schema.
type Tool struct {
	Name        string         // dotted name, e.g. "tool.parse_input"
	Description string         // human-readable summary
	InputSchema map[string]any // JSON schema for inputs
	OutputType  string         // canonical mime type of the result
	Handler     Handler        // invocation entry-point
}

// Handler runs a Tool against the supplied input and returns the result.
type Handler func(ctx context.Context, input map[string]any) (any, error)

// ErrUnknownTool is returned when looking up a tool that hasn't been registered.
var ErrUnknownTool = errors.New("tooling: unknown tool")

// Registry is the in-process Tool index.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

// Register adds or replaces a tool by name.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name] = t
}

// Lookup returns a registered tool by name.
func (r *Registry) Lookup(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Names returns every registered tool name.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	return out
}

// Invoke runs the tool by name. Returns ErrUnknownTool if name is not registered.
func (r *Registry) Invoke(ctx context.Context, name string, input map[string]any) (any, error) {
	t, ok := r.Lookup(name)
	if !ok {
		return nil, ErrUnknownTool
	}
	return t.Handler(ctx, input)
}
