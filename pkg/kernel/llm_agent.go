package kernel

import (
	"context"
	"fmt"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/llmcall"
)

// NewLLMAgent builds the BYOK reference Agent registered by `soyaos start`
// when the SOYA_MODEL_* env vars resolve to a configured llmcall.Config. The
// returned Agent forwards every chat-completion request to the configured
// OpenAI-Compatible upstream and streams chunks back to the caller.
//
// `slug` is the virtual model suffix (e.g. "llm" → "soya:llm"). Multiple
// LLM agents can coexist if a future operator wants to surface several
// upstreams as separate virtual models.
func NewLLMAgent(slug string, cfg llmcall.Config) Agent {
	provider := llmcall.OpenAICompat{Cfg: cfg}
	return Agent{
		Slug:        slug,
		Description: fmt.Sprintf("BYOK upstream LLM via %s (model %s)", provider.Name(), cfg.Model),
		Handler: func(ctx context.Context, _ auth.Identity, req llmcall.Request, out chan<- llmcall.Chunk) error {
			return provider.GenerateStream(ctx, req, out)
		},
	}
}
