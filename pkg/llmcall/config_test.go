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
	t.Setenv(EnvEnableThinking, "")
	t.Setenv(EnvThinkingBudget, "")

	got := ResolveConfig(nil)
	want := LoadConfigFromEnv()
	if got != want {
		t.Fatalf("ResolveConfig(nil) = %+v, want %+v", got, want)
	}
}

func TestLoadConfigFromEnvThinkingBudget(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{{"", 0}, {"512", 512}, {"1", 1}, {"32768", 32768}, {"0", 0}, {"-1", 0}, {"32769", 0}, {"oops", 0}} {
		t.Setenv(EnvThinkingBudget, tc.raw)
		if got := LoadConfigFromEnv().ThinkingBudget; got != tc.want {
			t.Fatalf("budget %q -> %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestLoadConfigFromEnvOptionalThinking(t *testing.T) {
	t.Setenv(EnvEnableThinking, "")
	if got := LoadConfigFromEnv().EnableThinking; got != nil {
		t.Fatalf("unset %s = %v, want nil", EnvEnableThinking, *got)
	}

	t.Setenv(EnvEnableThinking, "false")
	got := LoadConfigFromEnv().EnableThinking
	if got == nil || *got {
		t.Fatalf("%s=false = %v, want pointer to false", EnvEnableThinking, got)
	}

	// Invalid values are ignored instead of leaking a malformed vendor
	// extension into every upstream request.
	t.Setenv(EnvEnableThinking, "sometimes")
	if got := LoadConfigFromEnv().EnableThinking; got != nil {
		t.Fatalf("invalid %s should be ignored, got %v", EnvEnableThinking, *got)
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
