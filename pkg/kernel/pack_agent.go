// pack_agent.go wires the SoyaPack manifest layer to the kernel registry —
// the second leg of EPIC 1 Stream B (APP-541). RegisterFromPack turns a
// validated *soyapack.Manifest plus the on-disk Pack directory into a
// kernel.Agent.
//
// Two prompt-body shapes are supported (mutually exclusive in the
// manifest, enforced upstream by soyapack.Validate):
//
//   - m.Entry              — single system prompt (the v0 default).
//   - m.Prompt.Steps[]     — N-stage prompt chain (APP-550 Compo Phase B).
//     The kernel runs each stage with the previous stage's *full*
//     response fed in as the user message of the next stage; only the
//     final stage's stream is forwarded to the caller. This is how Compo
//     reaches its grade-school-ready output quality without sacrificing
//     the OpenAI-Compat streaming surface.
//
// The handler resolves the BYOK upstream via llmcall.ResolveConfig
// (manifest.prompt.upstream  >  env SOYA_MODEL_*  >  defaults). The
// upstream model id is rewritten to the resolved real model so the
// caller's virtual id (`soya:<slug>`) never leaks to the upstream.
//
// This is the path EstateMuse, NewsBeam, Compo, SilentCut all take in
// later stages: write a soyapack.yaml + one or more prompt files,
// point the kernel at the directory, and the Agent shows up at GET
// /v1/agents.
package kernel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

// providerFactory builds an llmcall.Provider from a resolved Config. The
// indirection exists so unit tests can substitute a fake provider — see
// pack_agent_test.go. Production callers never touch this; RegisterFromPack
// falls back to llmcall.OpenAICompat when the factory is nil.
type providerFactory func(cfg llmcall.Config) llmcall.Provider

// RegisterFromPack registers an Agent built from a SoyaPack manifest plus
// its on-disk source directory. The manifest is assumed to have already
// passed soyapack.Validate — this function does not re-run validation,
// it only checks the structural pre-conditions it needs to wire up the
// Handler (entry or prompt.steps present, expose.virtual_model_id present).
//
// packDir must be an absolute path to the directory containing
// soyapack.yaml; m.Entry / m.Prompt.Steps[*].Prompt are resolved relative
// to it.
func (k *Kernel) RegisterFromPack(m *soyapack.Manifest, packDir string) error {
	return k.registerFromPack(m, packDir, nil)
}

// registerFromPack is the implementation seam: tests pass a custom
// providerFactory to avoid spinning up a real HTTP upstream. Production
// callers go through RegisterFromPack which threads nil here.
func (k *Kernel) registerFromPack(m *soyapack.Manifest, packDir string, factory providerFactory) error {
	if m == nil {
		return fmt.Errorf("kernel: RegisterFromPack: nil manifest")
	}
	hasSteps := m.Prompt != nil && len(m.Prompt.Steps) > 0
	if m.Entry == "" && !hasSteps {
		return fmt.Errorf("kernel: pack %q missing entry / prompt.steps", m.Name)
	}
	if m.Entry != "" && hasSteps {
		// soyapack.Validate already rejects this combination, but the
		// kernel is the last guard before runtime — refuse to register
		// rather than silently picking one shape.
		return fmt.Errorf("kernel: pack %q has both entry and prompt.steps (mutually exclusive)", m.Name)
	}
	if m.Expose == nil || m.Expose.VirtualModelID == "" {
		return fmt.Errorf("kernel: pack %q missing expose.virtual_model_id", m.Name)
	}
	slug := strings.TrimPrefix(m.Expose.VirtualModelID, VirtualModelPrefix)
	if slug == "" || slug == m.Expose.VirtualModelID {
		// Either the prefix was absent or the id was just "soya:". Either
		// way the surface contract is broken; refuse to register rather
		// than silently dropping the request later.
		return fmt.Errorf("kernel: pack %q expose.virtual_model_id %q must be of the form soya:<slug>",
			m.Name, m.Expose.VirtualModelID)
	}

	// Load every system prompt body up front. Disk reads on every chat
	// call would be wasteful and would also race with a Pack uninstall.
	// We cache the bodies at register time and never re-read them.
	prompts, err := loadPromptBodies(m, packDir)
	if err != nil {
		return err
	}

	// Manifest-level prompt.upstream wins over env SOYA_MODEL_*; see
	// llmcall.ResolveConfig and APP-543. Nil decl is the same as
	// LoadConfigFromEnv so packs that don't pin an upstream still work.
	var decl *soyapack.UpstreamDecl
	if m.Prompt != nil {
		decl = m.Prompt.Upstream
	}
	cfg := llmcall.ResolveConfig(decl)

	var provider llmcall.Provider
	if factory != nil {
		provider = factory(cfg)
	} else {
		provider = llmcall.OpenAICompat{Cfg: cfg}
	}

	handler := buildPackHandler(prompts, provider, cfg.Model)

	k.Register(Agent{
		Slug:        slug,
		Description: m.Description,
		Handler:     handler,
		Manifest:    m,
	})
	return nil
}

// promptBody pairs a step id with its pre-loaded system prompt. For the
// single-prompt (m.Entry) case the list has exactly one entry whose id
// is empty.
type promptBody struct {
	id   string
	body string
}

// loadPromptBodies resolves and reads every prompt file the Pack
// declares (entry or prompt.steps[]). Returns the ordered list of
// (stepID, body) pairs the handler will run.
func loadPromptBodies(m *soyapack.Manifest, packDir string) ([]promptBody, error) {
	if m.Entry != "" {
		body, err := os.ReadFile(filepath.Join(packDir, m.Entry))
		if err != nil {
			return nil, fmt.Errorf("kernel: read pack %q entry %s: %w", m.Name, m.Entry, err)
		}
		return []promptBody{{id: "", body: string(body)}}, nil
	}
	out := make([]promptBody, 0, len(m.Prompt.Steps))
	for _, step := range m.Prompt.Steps {
		body, err := os.ReadFile(filepath.Join(packDir, step.Prompt))
		if err != nil {
			return nil, fmt.Errorf("kernel: read pack %q step %q prompt %s: %w",
				m.Name, step.ID, step.Prompt, err)
		}
		out = append(out, promptBody{id: step.ID, body: string(body)})
	}
	return out, nil
}

// buildPackHandler turns the resolved prompt bodies + provider into a
// kernel.Handler. The single-prompt case uses the original streaming
// shape; the multi-step case runs N-1 non-streaming generations
// followed by a streaming final stage.
func buildPackHandler(prompts []promptBody, provider llmcall.Provider, resolvedModel string) Handler {
	return func(ctx context.Context, _ auth.Identity, req llmcall.Request, out chan<- llmcall.Chunk) error {
		if len(prompts) <= 1 {
			// Single-prompt path: prepend the system prompt; preserve
			// everything else the caller sent. The upstream model id
			// is rewritten to the resolved real model; a caller-
			// supplied "soya:<slug>" virtual id would otherwise hit
			// the upstream verbatim and 400.
			systemPrompt := ""
			if len(prompts) == 1 {
				systemPrompt = prompts[0].body
			}
			injected := make([]llmcall.Message, 0, len(req.Messages)+1)
			if systemPrompt != "" {
				injected = append(injected, llmcall.Message{Role: "system", Content: systemPrompt})
			}
			injected = append(injected, req.Messages...)
			return provider.GenerateStream(ctx, llmcall.Request{
				Model:       resolvedModel,
				Messages:    injected,
				Temperature: req.Temperature,
				MaxTokens:   req.MaxTokens,
				Stream:      true,
			}, out)
		}

		// Multi-step chain. Stages 1..N-1 run non-streaming; their full
		// response becomes the user message of the next stage. Stage N
		// streams. We preserve the caller's original conversation as
		// the user message of stage 1, then collapse to a single
		// "the previous stage said: ..." user message thereafter so
		// each stage sees only what the chain explicitly threads.
		userPayload := combineUserMessages(req.Messages)

		for i := 0; i < len(prompts)-1; i++ {
			stage := prompts[i]
			resp, err := provider.Generate(ctx, llmcall.Request{
				Model: resolvedModel,
				Messages: []llmcall.Message{
					{Role: "system", Content: stage.body},
					{Role: "user", Content: userPayload},
				},
				Temperature: req.Temperature,
				MaxTokens:   req.MaxTokens,
			})
			if err != nil {
				return fmt.Errorf("kernel: prompt step %q (#%d): %w", stage.id, i, err)
			}
			userPayload = resp.Content
		}

		// Final stage — stream back to the caller.
		final := prompts[len(prompts)-1]
		return provider.GenerateStream(ctx, llmcall.Request{
			Model: resolvedModel,
			Messages: []llmcall.Message{
				{Role: "system", Content: final.body},
				{Role: "user", Content: userPayload},
			},
			Temperature: req.Temperature,
			MaxTokens:   req.MaxTokens,
			Stream:      true,
		}, out)
	}
}

// combineUserMessages collapses the caller's message history into the
// single user payload that seeds stage 1 of a prompt chain. We keep
// only `user` and `assistant` turns; system messages from the caller
// are dropped (the chain owns the system role).
func combineUserMessages(messages []llmcall.Message) string {
	var parts []string
	for _, msg := range messages {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		if msg.Content == "" {
			continue
		}
		if msg.Role == "assistant" {
			parts = append(parts, "Previous assistant turn: "+msg.Content)
			continue
		}
		parts = append(parts, msg.Content)
	}
	return strings.Join(parts, "\n\n")
}
