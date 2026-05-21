package llmcall

import (
	"testing"

	"github.com/soyaos/soyaos/pkg/soyapack"
)

// TestResolveConfig_NilDeclEqualsEnv pins the no-override contract: callers
// that don't carry a manifest get exactly what LoadConfigFromEnv would have
// produced. This is the path most production code takes today.
func TestResolveConfig_NilDeclEqualsEnv(t *testing.T) {
	t.Setenv(EnvAPIKey, "sk-env")
	t.Setenv(EnvBaseURL, "https://api.openai.com/v1")
	t.Setenv(EnvModel, "gpt-4o-mini")

	got := ResolveConfig(nil)
	want := LoadConfigFromEnv()
	if got != want {
		t.Fatalf("ResolveConfig(nil) = %+v, want %+v", got, want)
	}
}

// TestResolveConfig_BaseURLAndModelOverride proves manifest decl wins over
// env. We leave api_key_ref empty so the env-supplied APIKey is preserved.
func TestResolveConfig_BaseURLAndModelOverride(t *testing.T) {
	t.Setenv(EnvAPIKey, "sk-env")
	t.Setenv(EnvBaseURL, "https://api.openai.com/v1")
	t.Setenv(EnvModel, "gpt-4o-mini")

	got := ResolveConfig(&soyapack.UpstreamDecl{
		Provider: "openai-compat",
		BaseURL:  "https://api.deepseek.com/v1",
		Model:    "deepseek-chat",
	})
	if got.BaseURL != "https://api.deepseek.com/v1" {
		t.Errorf("BaseURL = %q, want manifest override", got.BaseURL)
	}
	if got.Model != "deepseek-chat" {
		t.Errorf("Model = %q, want manifest override", got.Model)
	}
	if got.APIKey != "sk-env" {
		t.Errorf("APIKey = %q, want env value preserved when api_key_ref is empty", got.APIKey)
	}
}

// TestResolveConfig_APIKeyRefDereferencesEnv exercises the secret-ref path:
// the manifest names an env var, the resolver reads it, the resulting Config
// carries the resolved key (never the literal "${...}").
func TestResolveConfig_APIKeyRefDereferencesEnv(t *testing.T) {
	t.Setenv(EnvAPIKey, "sk-fallback")
	t.Setenv("AGENT_SPECIFIC_KEY", "sk-agent")

	got := ResolveConfig(&soyapack.UpstreamDecl{
		Provider:  "openai-compat",
		APIKeyRef: "${AGENT_SPECIFIC_KEY}",
	})
	if got.APIKey != "sk-agent" {
		t.Fatalf("APIKey = %q, want %q (from AGENT_SPECIFIC_KEY)", got.APIKey, "sk-agent")
	}
}

// TestResolveConfig_APIKeyRefMissingFallsThrough — when the referenced env
// var is unset, the resolver keeps whatever LoadConfigFromEnv supplied so a
// Pack author can declare a preferred key without breaking the operator's
// default. This is the same fallthrough we document in the spec.
func TestResolveConfig_APIKeyRefMissingFallsThrough(t *testing.T) {
	t.Setenv(EnvAPIKey, "sk-fallback")
	// Deliberately do NOT set AGENT_KEY_MISSING.

	got := ResolveConfig(&soyapack.UpstreamDecl{
		Provider:  "openai-compat",
		APIKeyRef: "${AGENT_KEY_MISSING}",
	})
	if got.APIKey != "sk-fallback" {
		t.Fatalf("APIKey = %q, want %q (env fallback)", got.APIKey, "sk-fallback")
	}
}
