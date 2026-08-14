package soyapack_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/soyaos/soyaos/pkg/soyapack"
)

// fixturePath resolves a path relative to the repo's examples/manifests
// directory regardless of where `go test` is invoked from.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", "examples", "manifests", name),
		filepath.Join("examples", "manifests", name),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatalf("fixture %q not found in any candidate path", name)
	return ""
}

func TestLoadAndValidate_AllThreeKinds(t *testing.T) {
	cases := []struct {
		fixture string
		kind    soyapack.Kind
	}{
		{"agent.yaml", soyapack.KindAgent},
		{"skill.yaml", soyapack.KindSkill},
		{"memory.yaml", soyapack.KindMemory},
	}
	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			m, err := soyapack.LoadFromFile(fixturePath(t, c.fixture))
			if err != nil {
				t.Fatalf("LoadFromFile: %v", err)
			}
			if m.Kind != c.kind {
				t.Fatalf("kind = %q, want %q", m.Kind, c.kind)
			}
			if m.SpecVersion != soyapack.SpecVersionV0 {
				t.Fatalf("spec_version = %q", m.SpecVersion)
			}
			if err := soyapack.Validate(m); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestLoad_RejectsUnknownTopLevelField(t *testing.T) {
	body := `spec_version: soyapack.v0
kind: Agent
name: test
version: 0.1.0
description: x
authors: [{name: a}]
license: MIT
runtime: { compat: ">=0.1.0 <0.2.0" }
determinism: read-only
entry: prompts/main.md
expose: { openai_compat: chat, virtual_model_id: soya:test }
weird_field_not_in_spec: yes
`
	if _, err := soyapack.LoadFromBytes([]byte(body)); err == nil {
		t.Fatal("expected error for unknown top-level field, got nil")
	} else if !strings.Contains(err.Error(), "weird_field_not_in_spec") {
		t.Fatalf("error should name the bad field, got: %v", err)
	}
}

func TestLoad_PassesThroughXExtension(t *testing.T) {
	body := `spec_version: soyapack.v0
kind: Agent
name: test
version: 0.1.0
description: x
authors: [{name: a}]
license: MIT
runtime: { compat: ">=0.1.0 <0.2.0" }
determinism: read-only
entry: prompts/main.md
expose: { openai_compat: chat, virtual_model_id: soya:test }
x-custom-vendor: { foo: bar, n: 42 }
`
	m, err := soyapack.LoadFromBytes([]byte(body))
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	if v, ok := m.Extensions["x-custom-vendor"]; !ok {
		t.Fatalf("x-custom-vendor missing; Extensions = %v", m.Extensions)
	} else {
		mp, ok := v.(map[string]any)
		if !ok || mp["foo"] != "bar" {
			t.Fatalf("x-custom-vendor decoded unexpectedly: %v (%T)", v, v)
		}
	}
}

func TestLoadAndValidate_DataPlaneDirect(t *testing.T) {
	body := `spec_version: soyapack.v0
kind: Agent
name: silent-cut
version: 0.1.0-alpha.1
description: Render a short video without sending MP4 bytes through Planet.
authors: [{name: SoyaOS Contributors}]
license: MIT
runtime: { compat: ">=0.1.0 <0.2.0" }
determinism: stateful
affinity: comet
entry: prompts/main.md
expose: { openai_compat: both, virtual_model_id: soya:silent-cut }
data_plane:
  direct: true
`
	m, err := soyapack.LoadFromBytes([]byte(body))
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	if m.DataPlane == nil || !m.DataPlane.Direct {
		t.Fatalf("data_plane.direct was not decoded: %+v", m.DataPlane)
	}
	if err := soyapack.Validate(m); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_RejectsBadSpecVersion(t *testing.T) {
	m := minimalAgentManifest()
	m.SpecVersion = "soyapack.v1"
	err := soyapack.Validate(m)
	if err == nil || !errors.Is(err, soyapack.ErrInvalidManifest) {
		t.Fatalf("Validate(bad spec_version) = %v, want ErrInvalidManifest", err)
	}
}

func TestValidate_RejectsBadKind(t *testing.T) {
	m := minimalAgentManifest()
	m.Kind = "BadKind"
	if err := soyapack.Validate(m); err == nil {
		t.Fatal("Validate(bad kind) returned nil")
	}
}

func TestValidate_RejectsBadName(t *testing.T) {
	bad := []string{"Bad", "-leading", "trailing-", "with_underscore", ""}
	for _, n := range bad {
		m := minimalAgentManifest()
		m.Name = n
		if err := soyapack.Validate(m); err == nil {
			t.Fatalf("Validate(name=%q) returned nil, want error", n)
		}
	}
}

func TestValidate_RejectsBadVersion(t *testing.T) {
	bad := []string{"", "1", "1.0", "v1.0.0", "1.0.0.0", "1.0.0+"}
	for _, v := range bad {
		m := minimalAgentManifest()
		m.Version = v
		if err := soyapack.Validate(m); err == nil {
			t.Fatalf("Validate(version=%q) returned nil, want error", v)
		}
	}
}

func TestValidate_AcceptsGoodVersionForms(t *testing.T) {
	good := []string{"0.1.0", "0.1.0-alpha.0", "1.0.0", "1.2.3-rc.1+build.1"}
	for _, v := range good {
		m := minimalAgentManifest()
		m.Version = v
		if err := soyapack.Validate(m); err != nil {
			t.Fatalf("Validate(version=%q) = %v, want nil", v, err)
		}
	}
}

func TestValidate_AgentRequiresEntryAndExpose(t *testing.T) {
	m := minimalAgentManifest()
	m.Entry = ""
	if err := soyapack.Validate(m); err == nil {
		t.Fatal("Validate(no entry) returned nil")
	}
	m = minimalAgentManifest()
	m.Expose = nil
	if err := soyapack.Validate(m); err == nil {
		t.Fatal("Validate(no expose) returned nil")
	}
}

// --- prompt.steps[] (APP-550 Compo Phase B) ---------------------------------

func TestValidate_PromptSteps_AcceptedAsAlternativeToEntry(t *testing.T) {
	m := minimalAgentManifest()
	m.Entry = ""
	m.Prompt = &soyapack.Prompt{
		Steps: []soyapack.PromptStep{
			{ID: "analyze", Prompt: "prompts/analyze.md"},
			{ID: "generate", Prompt: "prompts/generate.md"},
		},
	}
	if err := soyapack.Validate(m); err != nil {
		t.Fatalf("Validate(prompt.steps only) = %v, want nil", err)
	}
}

func TestValidate_PromptSteps_MutuallyExclusiveWithEntry(t *testing.T) {
	m := minimalAgentManifest() // already has Entry set
	m.Prompt = &soyapack.Prompt{
		Steps: []soyapack.PromptStep{{ID: "a", Prompt: "prompts/a.md"}},
	}
	err := soyapack.Validate(m)
	if err == nil {
		t.Fatal("Validate(entry + prompt.steps) returned nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should mention mutual exclusion, got %v", err)
	}
}

func TestValidate_PromptSteps_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name  string
		steps []soyapack.PromptStep
	}{
		{"missing id", []soyapack.PromptStep{{Prompt: "prompts/a.md"}}},
		{"missing prompt", []soyapack.PromptStep{{ID: "x"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := minimalAgentManifest()
			m.Entry = ""
			m.Prompt = &soyapack.Prompt{Steps: c.steps}
			if err := soyapack.Validate(m); err == nil {
				t.Fatal("Validate returned nil")
			}
		})
	}
}

func TestValidate_PromptSteps_RejectsDuplicateID(t *testing.T) {
	m := minimalAgentManifest()
	m.Entry = ""
	m.Prompt = &soyapack.Prompt{
		Steps: []soyapack.PromptStep{
			{ID: "x", Prompt: "a.md"},
			{ID: "x", Prompt: "b.md"},
		},
	}
	if err := soyapack.Validate(m); err == nil {
		t.Fatal("Validate(duplicate step id) returned nil")
	}
}

// --- channels[].secrets (APP-552 NewsBeam) ----------------------------------

func TestValidate_ChannelSecrets_AcceptsEnvRef(t *testing.T) {
	m := minimalAgentManifest()
	m.Channels = []soyapack.ChannelDecl{{
		Kind:      "dingtalk",
		BindingID: "news-beam-test",
		Secrets: map[string]string{
			"access_token_ref": "${SOYA_DINGTALK_ACCESS_TOKEN}",
			"secret_ref":       "${SOYA_DINGTALK_SECRET}",
		},
	}}
	if err := soyapack.Validate(m); err != nil {
		t.Fatalf("Validate(channel with env-ref secrets) = %v, want nil", err)
	}
}

func TestValidate_ChannelSecrets_RejectsInline(t *testing.T) {
	m := minimalAgentManifest()
	m.Channels = []soyapack.ChannelDecl{{
		Kind:    "dingtalk",
		Secrets: map[string]string{"access_token_ref": "tk-deadbeef"},
	}}
	if err := soyapack.Validate(m); err == nil {
		t.Fatal("Validate(inline channel secret) returned nil")
	}
}

func TestLoad_PromptStepsRoundTrip(t *testing.T) {
	body := `spec_version: soyapack.v0
kind: Agent
name: chained
version: 0.1.0
description: x
authors: [{name: a}]
license: MIT
runtime: { compat: ">=0.1.0 <0.2.0" }
determinism: read-only
expose: { openai_compat: chat, virtual_model_id: soya:chained }
prompt:
  steps:
    - { id: analyze, prompt: prompts/analyze.md }
    - { id: generate, prompt: prompts/generate.md }
    - { id: refine, prompt: prompts/refine.md }
`
	m, err := soyapack.LoadFromBytes([]byte(body))
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	if m.Prompt == nil || len(m.Prompt.Steps) != 3 {
		t.Fatalf("steps not parsed: prompt=%+v", m.Prompt)
	}
	if m.Prompt.Steps[1].ID != "generate" || m.Prompt.Steps[1].Prompt != "prompts/generate.md" {
		t.Errorf("steps[1] = %+v", m.Prompt.Steps[1])
	}
	if err := soyapack.Validate(m); err != nil {
		t.Fatalf("Validate(chained) = %v", err)
	}
}

func TestLoad_ChannelsWithSecretsRoundTrip(t *testing.T) {
	body := `spec_version: soyapack.v0
kind: Agent
name: pushy
version: 0.1.0
description: x
authors: [{name: a}]
license: MIT
runtime: { compat: ">=0.1.0 <0.2.0" }
determinism: read-only
entry: prompts/main.md
expose: { openai_compat: chat, virtual_model_id: soya:pushy }
channels:
  - kind: dingtalk
    binding_id: my-robot
    secrets:
      access_token_ref: ${SOYA_DINGTALK_ACCESS_TOKEN}
      secret_ref: ${SOYA_DINGTALK_SECRET}
`
	m, err := soyapack.LoadFromBytes([]byte(body))
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	if len(m.Channels) != 1 {
		t.Fatalf("channels not parsed: %+v", m.Channels)
	}
	if m.Channels[0].BindingID != "my-robot" {
		t.Errorf("binding_id = %q", m.Channels[0].BindingID)
	}
	if m.Channels[0].Secrets["access_token_ref"] != "${SOYA_DINGTALK_ACCESS_TOKEN}" {
		t.Errorf("secrets[access_token_ref] = %q", m.Channels[0].Secrets["access_token_ref"])
	}
	if err := soyapack.Validate(m); err != nil {
		t.Fatalf("Validate(channels) = %v", err)
	}
}

func TestValidate_RejectsBadVirtualModelID(t *testing.T) {
	bad := []string{"openai/gpt-4", "Soya:x", "soya:BAD_CASE", ""}
	for _, vid := range bad {
		m := minimalAgentManifest()
		m.Expose.VirtualModelID = vid
		if vid == "" {
			continue // empty is allowed (default)
		}
		if err := soyapack.Validate(m); err == nil {
			t.Fatalf("Validate(virtual_model_id=%q) returned nil", vid)
		}
	}
}

func TestValidate_RejectsBadArtifactKind(t *testing.T) {
	m := minimalAgentManifest()
	m.Artifacts = []soyapack.ArtifactDecl{{Kind: "svg", Schema: "x.v1"}}
	if err := soyapack.Validate(m); err == nil {
		t.Fatal("Validate(artifact kind=svg) returned nil")
	}
}

func TestValidate_ScheduleRequiresCronOrOnce(t *testing.T) {
	m := minimalAgentManifest()
	m.Schedules = []soyapack.ScheduleDecl{{TZ: "UTC"}}
	if err := soyapack.Validate(m); err == nil {
		t.Fatal("Validate(schedule without cron/once) returned nil")
	}
}

func TestValidate_PromptUpstream_Valid(t *testing.T) {
	m := minimalAgentManifest()
	m.Prompt = &soyapack.Prompt{
		Upstream: &soyapack.UpstreamDecl{
			Provider:  "openai-compat",
			BaseURL:   "https://api.deepseek.com/v1",
			Model:     "deepseek-chat",
			APIKeyRef: "${SOYA_MODEL_API_KEY}",
		},
	}
	if err := soyapack.Validate(m); err != nil {
		t.Fatalf("Validate(valid upstream) = %v, want nil", err)
	}
}

func TestValidate_PromptUpstream_RejectsUnsupportedProvider(t *testing.T) {
	m := minimalAgentManifest()
	m.Prompt = &soyapack.Prompt{
		Upstream: &soyapack.UpstreamDecl{Provider: "anthropic"},
	}
	err := soyapack.Validate(m)
	if err == nil {
		t.Fatal("Validate(unsupported provider) returned nil")
	}
	if !errors.Is(err, soyapack.ErrUnsupportedProvider) {
		t.Fatalf("error must wrap ErrUnsupportedProvider, got %v", err)
	}
}

func TestValidate_PromptUpstream_RejectsBadAPIKeyRef(t *testing.T) {
	bad := []string{
		"sk-deadbeef",       // inline secret form
		"$ENV_NAME",         // missing braces
		"${lowercase}",      // lower-case not allowed
		"${1LEADING_DIGIT}", // must start with letter or underscore
		"${MY-DASH}",        // dash not allowed
		"${}",               // empty name
	}
	for _, ref := range bad {
		m := minimalAgentManifest()
		m.Prompt = &soyapack.Prompt{
			Upstream: &soyapack.UpstreamDecl{Provider: "openai-compat", APIKeyRef: ref},
		}
		err := soyapack.Validate(m)
		if err == nil {
			t.Fatalf("Validate(api_key_ref=%q) returned nil", ref)
		}
		if !errors.Is(err, soyapack.ErrBadAPIKeyRef) {
			t.Fatalf("api_key_ref=%q: error must wrap ErrBadAPIKeyRef, got %v", ref, err)
		}
	}
}

func TestValidate_PromptUpstream_NilOK(t *testing.T) {
	m := minimalAgentManifest()
	m.Prompt = &soyapack.Prompt{Scaffold: "minimal-input-high-quality"}
	if err := soyapack.Validate(m); err != nil {
		t.Fatalf("Validate(no upstream) = %v, want nil", err)
	}
	m = minimalAgentManifest()
	m.Prompt = nil
	if err := soyapack.Validate(m); err != nil {
		t.Fatalf("Validate(no prompt) = %v, want nil", err)
	}
}

func TestLoad_PromptUpstream_NoInlineAPIKeyField(t *testing.T) {
	// Defense-in-depth: the typed UpstreamDecl struct has no `APIKey` field,
	// so even if yaml.v3's lenient nested decode silently drops an
	// `api_key:` line, the manifest can never surface an inline secret to
	// runtime code. This test pins that surface — a future contributor
	// adding `APIKey` to UpstreamDecl would have to rip out this assertion.
	body := `spec_version: soyapack.v0
kind: Agent
name: leaky
version: 0.1.0
description: x
authors: [{name: a}]
license: MIT
runtime: { compat: ">=0.1.0 <0.2.0" }
determinism: read-only
entry: prompts/main.md
expose: { openai_compat: chat, virtual_model_id: soya:leaky }
prompt:
  upstream:
    provider: openai-compat
    api_key_ref: ${SOYA_MODEL_API_KEY}
`
	m, err := soyapack.LoadFromBytes([]byte(body))
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}
	if m.Prompt == nil || m.Prompt.Upstream == nil {
		t.Fatal("expected prompt.upstream to be populated")
	}
	if m.Prompt.Upstream.APIKeyRef != "${SOYA_MODEL_API_KEY}" {
		t.Fatalf("api_key_ref = %q", m.Prompt.Upstream.APIKeyRef)
	}
	// Sanity-check the struct surface stays ref-only by reflecting on
	// declared fields — the only way to leak inline secrets would be to
	// add a new typed field here.
	if got := reflectFieldNames(*m.Prompt.Upstream); !containsAll(got, []string{"Provider", "BaseURL", "Model", "APIKeyRef"}) || len(got) != 4 {
		t.Fatalf("UpstreamDecl fields drifted: %v (must be exactly 4 ref-only fields)", got)
	}
}

func reflectFieldNames(v any) []string {
	t := reflect.TypeOf(v)
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		out = append(out, t.Field(i).Name)
	}
	return out
}

func containsAll(haystack, needles []string) bool {
	set := map[string]bool{}
	for _, h := range haystack {
		set[h] = true
	}
	for _, n := range needles {
		if !set[n] {
			return false
		}
	}
	return true
}

// minimalAgentManifest returns the smallest manifest that should pass
// Validate. Helper for negative-case unit tests.
func minimalAgentManifest() *soyapack.Manifest {
	return &soyapack.Manifest{
		SpecVersion: soyapack.SpecVersionV0,
		Kind:        soyapack.KindAgent,
		Name:        "minimal",
		Version:     "0.1.0",
		Description: "x",
		Authors:     []soyapack.Author{{Name: "a"}},
		License:     "MIT",
		Runtime:     soyapack.RuntimeCompat{Compat: ">=0.1.0"},
		Determinism: soyapack.DeterminismReadOnly,
		Entry:       "prompts/main.md",
		Expose:      &soyapack.Expose{OpenAICompat: "chat", VirtualModelID: "soya:minimal"},
	}
}
