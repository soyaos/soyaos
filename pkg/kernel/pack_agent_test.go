package kernel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

// fakeProvider records every Request handed to it so tests can assert what
// the kernel wired up — in particular that the system prompt is prepended
// and that the upstream model id is the resolved real model, not the
// caller's virtual id.
type fakeProvider struct {
	mu  sync.Mutex
	got []llmcall.Request
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Generate(_ context.Context, req llmcall.Request) (llmcall.Response, error) {
	f.mu.Lock()
	f.got = append(f.got, req)
	f.mu.Unlock()
	return llmcall.Response{Model: req.Model, Content: "ok"}, nil
}

func (f *fakeProvider) GenerateStream(_ context.Context, req llmcall.Request, out chan<- llmcall.Chunk) error {
	f.mu.Lock()
	f.got = append(f.got, req)
	f.mu.Unlock()
	out <- llmcall.Chunk{Delta: "ok"}
	out <- llmcall.Chunk{Done: true, FinishReason: "stop"}
	return nil
}

func (f *fakeProvider) lastRequest(t *testing.T) llmcall.Request {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) == 0 {
		t.Fatal("fakeProvider: no request observed")
	}
	return f.got[len(f.got)-1]
}

// writePack lays out a minimal Pack directory with soyapack.yaml + the
// prompt file referenced by m.Entry. The test cases construct the
// manifest in Go and supply the prompt body — we don't exercise the YAML
// loader here, that's pkg/soyapack's job.
func writePack(t *testing.T, m *soyapack.Manifest, promptBody string) string {
	t.Helper()
	dir := t.TempDir()
	if m.Entry == "" {
		t.Fatal("test fixture: manifest missing Entry")
	}
	promptPath := filepath.Join(dir, m.Entry)
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(promptPath, []byte(promptBody), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	return dir
}

func minimalAgentManifest(name, slug string) *soyapack.Manifest {
	return &soyapack.Manifest{
		SpecVersion: soyapack.SpecVersionV0,
		Kind:        soyapack.KindAgent,
		Name:        name,
		Version:     "0.1.0",
		Description: "test agent",
		Authors:     []soyapack.Author{{Name: "tester"}},
		License:     "MIT",
		Runtime:     soyapack.RuntimeCompat{Compat: ">=0.1.0"},
		Determinism: soyapack.DeterminismReadOnly,
		Entry:       "prompts/main.md",
		Expose:      &soyapack.Expose{OpenAICompat: "chat", VirtualModelID: "soya:" + slug},
	}
}

func TestRegisterFromPack_RegistersAgentUnderSlug(t *testing.T) {
	m := minimalAgentManifest("hello-agent", "hello")
	dir := writePack(t, m, "You are a helpful assistant.")

	k := New()
	fake := &fakeProvider{}
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return fake }); err != nil {
		t.Fatalf("registerFromPack: %v", err)
	}
	agent, ok := k.Lookup("soya:hello")
	if !ok {
		t.Fatal("agent not registered under soya:hello")
	}
	if agent.Slug != "hello" {
		t.Errorf("slug = %q, want hello", agent.Slug)
	}
	if agent.Description != "test agent" {
		t.Errorf("description = %q", agent.Description)
	}
	if agent.Manifest != m {
		t.Error("Manifest pointer not stored on Agent")
	}
}

func TestRegisterFromPack_HandlerPrependsSystemPrompt(t *testing.T) {
	m := minimalAgentManifest("greeter", "greeter")
	m.Prompt = &soyapack.Prompt{
		Upstream: &soyapack.UpstreamDecl{
			Provider: "openai-compat",
			Model:    "gpt-4o-mini",
			BaseURL:  "https://api.example.com/v1",
		},
	}
	dir := writePack(t, m, "SYSTEM: greet the user warmly.")

	k := New()
	fake := &fakeProvider{}
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return fake }); err != nil {
		t.Fatalf("registerFromPack: %v", err)
	}

	resp, err := k.ChatCompletion(context.Background(), auth.Identity{Subject: "u1"}, llmcall.Request{
		Model:    "soya:greeter",
		Messages: []llmcall.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Content == "" {
		t.Fatal("empty response content")
	}

	req := fake.lastRequest(t)
	if len(req.Messages) != 2 {
		t.Fatalf("provider got %d messages, want 2 (system + user)", len(req.Messages))
	}
	if req.Messages[0].Role != "system" {
		t.Errorf("messages[0].role = %q, want system", req.Messages[0].Role)
	}
	if !strings.Contains(req.Messages[0].Content, "greet the user warmly") {
		t.Errorf("system prompt body not threaded through: %q", req.Messages[0].Content)
	}
	if req.Messages[1].Role != "user" || req.Messages[1].Content != "hi" {
		t.Errorf("user message corrupted: %+v", req.Messages[1])
	}
	// The handler rewrites the model id to the resolved real model so the
	// upstream doesn't see "soya:greeter" and 400 on an unknown model.
	if req.Model != "gpt-4o-mini" {
		t.Errorf("upstream model = %q, want gpt-4o-mini (resolved from manifest)", req.Model)
	}
}

func TestRegisterFromPack_MissingEntry(t *testing.T) {
	m := minimalAgentManifest("noentry", "noentry")
	m.Entry = ""
	k := New()
	err := k.RegisterFromPack(m, t.TempDir())
	if err == nil {
		t.Fatal("RegisterFromPack(no entry) returned nil")
	}
	if !strings.Contains(err.Error(), "entry") {
		t.Errorf("error should mention entry, got %v", err)
	}
}

func TestRegisterFromPack_MissingPromptFile(t *testing.T) {
	m := minimalAgentManifest("missing", "missing")
	k := New()
	err := k.RegisterFromPack(m, t.TempDir()) // no prompt file at prompts/main.md
	if err == nil {
		t.Fatal("RegisterFromPack(no prompt file) returned nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error should wrap os.ErrNotExist, got %v", err)
	}
}

func TestRegisterFromPack_NilManifest(t *testing.T) {
	k := New()
	if err := k.RegisterFromPack(nil, ""); err == nil {
		t.Fatal("RegisterFromPack(nil) returned nil")
	}
}

func TestRegisterFromPack_MissingExpose(t *testing.T) {
	m := minimalAgentManifest("noexpose", "noexpose")
	m.Expose = nil
	dir := writePack(t, m, "x")
	if err := New().RegisterFromPack(m, dir); err == nil {
		t.Fatal("RegisterFromPack(no expose) returned nil")
	}
}

func TestRegisterFromPack_BadVirtualModelID(t *testing.T) {
	m := minimalAgentManifest("badvm", "badvm")
	m.Expose.VirtualModelID = "not-a-soya-prefix"
	dir := writePack(t, m, "x")
	err := New().RegisterFromPack(m, dir)
	if err == nil {
		t.Fatal("RegisterFromPack(bad virtual_model_id) returned nil")
	}
	if !strings.Contains(err.Error(), "soya:<slug>") {
		t.Errorf("error should mention required form, got %v", err)
	}
}

func TestRegisterFromPack_ListIncludesAgent(t *testing.T) {
	m := minimalAgentManifest("listed", "listed")
	dir := writePack(t, m, "system")
	k := New()
	fake := &fakeProvider{}
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return fake }); err != nil {
		t.Fatalf("registerFromPack: %v", err)
	}
	found := false
	for _, a := range k.List() {
		if a.ModelID() == "soya:listed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("kernel.List() does not contain soya:listed")
	}
}
