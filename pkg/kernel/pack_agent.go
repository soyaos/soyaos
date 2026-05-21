// pack_agent.go wires the SoyaPack manifest layer to the kernel registry —
// the second leg of EPIC 1 Stream B (APP-541). RegisterFromPack turns a
// validated *soyapack.Manifest plus the on-disk Pack directory into a
// kernel.Agent whose Handler:
//
//  1. reads the system prompt from `m.Entry` (relative to packDir),
//  2. resolves the BYOK upstream via llmcall.ResolveConfig
//     (manifest.prompt.upstream  >  env SOYA_MODEL_*  >  defaults),
//  3. prepends the system prompt to every chat request, and
//  4. streams the upstream OpenAI-Compat response back to the caller.
//
// This is the path EstateMuse, NewsBeam, Compo, SilentCut all take in
// later stages: write a soyapack.yaml + a prompt file, point the kernel
// at the directory, and the Agent shows up at GET /v1/agents.
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
// Handler (entry path present, expose.virtual_model_id present).
//
// packDir must be an absolute path to the directory containing
// soyapack.yaml; m.Entry is resolved relative to it.
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
	if m.Entry == "" {
		return fmt.Errorf("kernel: pack %q missing entry (prompt path)", m.Name)
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

	promptPath := filepath.Join(packDir, m.Entry)
	body, err := os.ReadFile(promptPath)
	if err != nil {
		return fmt.Errorf("kernel: read pack %q entry %s: %w", m.Name, promptPath, err)
	}
	systemPrompt := string(body)

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

	handler := func(ctx context.Context, _ auth.Identity, req llmcall.Request, out chan<- llmcall.Chunk) error {
		// Prepend the system prompt; preserve everything else the caller
		// sent. The upstream model id is rewritten to the resolved real
		// model (cfg.Model); a caller-supplied "soya:<slug>" virtual id
		// would otherwise hit the upstream verbatim and 400.
		injected := make([]llmcall.Message, 0, len(req.Messages)+1)
		injected = append(injected, llmcall.Message{Role: "system", Content: systemPrompt})
		injected = append(injected, req.Messages...)
		return provider.GenerateStream(ctx, llmcall.Request{
			Model:       cfg.Model,
			Messages:    injected,
			Temperature: req.Temperature,
			MaxTokens:   req.MaxTokens,
			Stream:      true,
		}, out)
	}

	k.Register(Agent{
		Slug:        slug,
		Description: m.Description,
		Handler:     handler,
		Manifest:    m,
	})
	return nil
}
