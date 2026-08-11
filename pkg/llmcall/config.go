package llmcall

import (
	"os"
	"strings"

	"github.com/soyaos/soyaos/pkg/soyapack"
)

// Env var names. Keep them grouped under SOYA_MODEL_ so the operator can scan
// one prefix and know everything that points the LLM call layer at a real
// upstream. Manifest-level overrides (SoyaPack v0 prompt.upstream segment)
// land in v0.1.0-alpha.1 and take precedence over these env defaults.
const (
	EnvAPIKey  = "SOYA_MODEL_API_KEY"
	EnvBaseURL = "SOYA_MODEL_BASE_URL"
	EnvModel   = "SOYA_MODEL_DEFAULT"
)

// Defaults applied by LoadConfigFromEnv when the corresponding env var is
// unset. The base URL targets the OpenAI public endpoint because the broader
// OpenAI-Compat ecosystem (DeepSeek, Moonshot, Groq, Together, Ollama, vLLM,
// LM Studio …) all expose the same shape behind a different base URL — the
// operator just overrides EnvBaseURL.
const (
	DefaultBaseURL = "https://api.openai.com/v1"
	DefaultModel   = "gpt-4o-mini"
)

// Config is the resolved upstream LLM configuration: the three values you
// need to make one OpenAI-compatible HTTP call (api key, base URL, model).
//
// A Config is considered "configured" when APIKey is non-empty. With APIKey
// unset the soyaos binary still boots — only the `soya:echo` reference Agent
// answers — so the operator's path to "talk to a real model" is exactly
// "export SOYA_MODEL_API_KEY".
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
}

// LoadConfigFromEnv reads the three SOYA_MODEL_* env vars and applies the
// defaults documented on DefaultBaseURL / DefaultModel. APIKey is left empty
// when the env var is unset — the caller decides whether that is fatal.
func LoadConfigFromEnv() Config {
	cfg := Config{
		APIKey:  os.Getenv(EnvAPIKey),
		BaseURL: os.Getenv(EnvBaseURL),
		Model:   os.Getenv(EnvModel),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	return cfg
}

// Configured reports whether the operator has supplied enough to make a real
// upstream call. APIKey is the single signal — base URL and model both have
// safe defaults.
func (c Config) Configured() bool { return c.APIKey != "" }

// ResolveConfig merges a manifest-supplied UpstreamDecl on top of the
// env-derived Config. Precedence is:
//
//	decl (prompt.upstream)  >  env (SOYA_MODEL_*)  >  built-in defaults
//
// Passing a nil decl is the same as calling LoadConfigFromEnv directly:
// callers that don't care about per-Agent BYOK overrides need no
// branching at the call site.
//
// APIKeyRef is dereferenced through os.Getenv against the env name
// embedded in the ${ENV_NAME} reference. When that env var is unset, the
// resolver falls through to the operator's SOYA_MODEL_API_KEY (already
// baked into the base Config) — this lets a Pack author declare the
// reference without forcing every operator to populate it. (APP-543)
func ResolveConfig(decl *soyapack.UpstreamDecl) Config {
	cfg := LoadConfigFromEnv()
	if decl == nil {
		return cfg
	}
	if decl.BaseURL != "" {
		cfg.BaseURL = decl.BaseURL
	}
	if decl.Model != "" {
		cfg.Model = decl.Model
	}
	if decl.APIKeyRef != "" {
		envName := strings.TrimSuffix(strings.TrimPrefix(decl.APIKeyRef, "${"), "}")
		if v := os.Getenv(envName); v != "" {
			cfg.APIKey = v
		}
	}
	return cfg
}
