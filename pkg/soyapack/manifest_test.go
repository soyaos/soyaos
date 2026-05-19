package soyapack_test

import (
	"errors"
	"os"
	"path/filepath"
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
