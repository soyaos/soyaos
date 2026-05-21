package kernel

import (
	"context"
	"errors"
	"fmt"
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
//
// For the multi-step chain tests we also expose `responses`: the n-th
// non-streaming Generate call returns responses[n] (defaulting to "ok"
// when the slice runs out). This lets a 3-step chain test thread a
// distinct value through each stage and assert it surfaces in the
// next stage's user message.
type fakeProvider struct {
	mu        sync.Mutex
	got       []llmcall.Request
	responses []string
	calls     int
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Generate(_ context.Context, req llmcall.Request) (llmcall.Response, error) {
	f.mu.Lock()
	f.got = append(f.got, req)
	idx := f.calls
	f.calls++
	body := "ok"
	if idx < len(f.responses) {
		body = f.responses[idx]
	}
	f.mu.Unlock()
	return llmcall.Response{Model: req.Model, Content: body}, nil
}

func (f *fakeProvider) GenerateStream(_ context.Context, req llmcall.Request, out chan<- llmcall.Chunk) error {
	f.mu.Lock()
	f.got = append(f.got, req)
	idx := f.calls
	f.calls++
	body := "ok"
	if idx < len(f.responses) {
		body = f.responses[idx]
	}
	f.mu.Unlock()
	out <- llmcall.Chunk{Delta: body}
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

// writePackWithSteps lays out a pack whose prompt bodies are declared
// via m.Prompt.Steps. The bodies slice maps 1:1 onto m.Prompt.Steps.
func writePackWithSteps(t *testing.T, m *soyapack.Manifest, bodies []string) string {
	t.Helper()
	dir := t.TempDir()
	if m.Prompt == nil || len(m.Prompt.Steps) == 0 {
		t.Fatal("test fixture: manifest missing prompt.steps")
	}
	if len(bodies) != len(m.Prompt.Steps) {
		t.Fatalf("test fixture: %d bodies for %d steps", len(bodies), len(m.Prompt.Steps))
	}
	for i, step := range m.Prompt.Steps {
		p := filepath.Join(dir, step.Prompt)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(bodies[i]), 0o644); err != nil {
			t.Fatalf("write step %q: %v", step.ID, err)
		}
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

// --- prompt.steps[] chain (APP-550 Compo Phase B) ---------------------------

func TestRegisterFromPack_PromptSteps_SingleStepFallsBackToStream(t *testing.T) {
	// A 1-element steps[] should behave like a single-prompt path:
	// one streaming call that prepends the system body and preserves
	// the caller's user message.
	m := minimalAgentManifest("solo-step", "solo-step")
	m.Entry = ""
	m.Prompt = &soyapack.Prompt{
		Steps: []soyapack.PromptStep{{ID: "only", Prompt: "prompts/only.md"}},
	}
	dir := writePackWithSteps(t, m, []string{"BE BRIEF"})

	k := New()
	fake := &fakeProvider{}
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return fake }); err != nil {
		t.Fatalf("registerFromPack: %v", err)
	}
	resp, err := k.ChatCompletion(context.Background(), auth.Identity{Subject: "u1"}, llmcall.Request{
		Model:    "soya:solo-step",
		Messages: []llmcall.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Content == "" {
		t.Fatal("empty response content")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.got) != 1 {
		t.Fatalf("expected 1 provider call, got %d", len(fake.got))
	}
	if !strings.Contains(fake.got[0].Messages[0].Content, "BE BRIEF") {
		t.Errorf("system prompt missing: %+v", fake.got[0].Messages)
	}
}

func TestRegisterFromPack_PromptSteps_TwoStepChain(t *testing.T) {
	m := minimalAgentManifest("pair", "pair")
	m.Entry = ""
	m.Prompt = &soyapack.Prompt{
		Steps: []soyapack.PromptStep{
			{ID: "fetch", Prompt: "prompts/fetch.md"},
			{ID: "summarize", Prompt: "prompts/summarize.md"},
		},
	}
	dir := writePackWithSteps(t, m, []string{"STEP1 SYSTEM", "STEP2 SYSTEM"})

	k := New()
	// fake.Generate (stage 1) returns "fetched-output"; stage 2 (stream)
	// returns the final body.
	fake := &fakeProvider{responses: []string{"fetched-output", "final-summary"}}
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return fake }); err != nil {
		t.Fatalf("registerFromPack: %v", err)
	}
	resp, err := k.ChatCompletion(context.Background(), auth.Identity{Subject: "u1"}, llmcall.Request{
		Model:    "soya:pair",
		Messages: []llmcall.Message{{Role: "user", Content: "morning news"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Content != "final-summary" {
		t.Errorf("response content = %q, want final-summary", resp.Content)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.got) != 2 {
		t.Fatalf("expected 2 provider calls (stage1 + stage2 stream), got %d", len(fake.got))
	}
	// Stage 1: system=STEP1 SYSTEM, user=original.
	if !strings.Contains(fake.got[0].Messages[0].Content, "STEP1") {
		t.Errorf("stage1 system: %q", fake.got[0].Messages[0].Content)
	}
	if fake.got[0].Messages[1].Content != "morning news" {
		t.Errorf("stage1 user: %q", fake.got[0].Messages[1].Content)
	}
	// Stage 2: system=STEP2 SYSTEM, user=stage1 full response.
	if !strings.Contains(fake.got[1].Messages[0].Content, "STEP2") {
		t.Errorf("stage2 system: %q", fake.got[1].Messages[0].Content)
	}
	if fake.got[1].Messages[1].Content != "fetched-output" {
		t.Errorf("stage2 user = %q, want stage1 output (fetched-output)", fake.got[1].Messages[1].Content)
	}
}

func TestRegisterFromPack_PromptSteps_ThreeStepChainThreadsResponses(t *testing.T) {
	m := minimalAgentManifest("trio", "trio")
	m.Entry = ""
	m.Prompt = &soyapack.Prompt{
		Steps: []soyapack.PromptStep{
			{ID: "analyze", Prompt: "prompts/analyze.md"},
			{ID: "generate", Prompt: "prompts/generate.md"},
			{ID: "refine", Prompt: "prompts/refine.md"},
		},
	}
	dir := writePackWithSteps(t, m, []string{"ANALYZE", "GENERATE", "REFINE"})

	k := New()
	// Stage 1 Generate → "analysis"; stage 2 Generate → "draft"; stage 3
	// GenerateStream → final.
	fake := &fakeProvider{responses: []string{"analysis", "draft", "polished"}}
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return fake }); err != nil {
		t.Fatalf("registerFromPack: %v", err)
	}
	resp, err := k.ChatCompletion(context.Background(), auth.Identity{Subject: "u1"}, llmcall.Request{
		Model:    "soya:trio",
		Messages: []llmcall.Message{{Role: "user", Content: "title=春天的雨"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if resp.Content != "polished" {
		t.Errorf("final response = %q, want polished", resp.Content)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.got) != 3 {
		t.Fatalf("expected 3 provider calls, got %d", len(fake.got))
	}
	// Trace what each stage saw.
	stages := []struct {
		wantSystem string
		wantUser   string
	}{
		{"ANALYZE", "title=春天的雨"},
		{"GENERATE", "analysis"},
		{"REFINE", "draft"},
	}
	for i, want := range stages {
		got := fake.got[i]
		if !strings.Contains(got.Messages[0].Content, want.wantSystem) {
			t.Errorf("stage[%d] system = %q, want substring %q", i, got.Messages[0].Content, want.wantSystem)
		}
		if got.Messages[1].Content != want.wantUser {
			t.Errorf("stage[%d] user = %q, want %q", i, got.Messages[1].Content, want.wantUser)
		}
	}
}

func TestRegisterFromPack_PromptSteps_MissingStepFile(t *testing.T) {
	m := minimalAgentManifest("missing-step", "missing-step")
	m.Entry = ""
	m.Prompt = &soyapack.Prompt{
		Steps: []soyapack.PromptStep{
			{ID: "a", Prompt: "prompts/a.md"},
			{ID: "b", Prompt: "prompts/b.md"},
		},
	}
	// Only write the first step's file.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompts", "a.md"), []byte("only"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := New().RegisterFromPack(m, dir)
	if err == nil {
		t.Fatal("RegisterFromPack(missing step file) returned nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}

func TestRegisterFromPack_PromptSteps_RejectsEntryAndStepsTogether(t *testing.T) {
	m := minimalAgentManifest("conflict", "conflict")
	// keep m.Entry set, also declare steps — the kernel must refuse.
	m.Prompt = &soyapack.Prompt{
		Steps: []soyapack.PromptStep{{ID: "x", Prompt: "prompts/x.md"}},
	}
	if err := New().RegisterFromPack(m, t.TempDir()); err == nil {
		t.Fatal("RegisterFromPack(entry + steps) returned nil")
	}
}

// --- schedules + channels hooks (APP-552 NewsBeam) --------------------------

func TestRegisterFromPack_Schedules_InvokesHook(t *testing.T) {
	m := minimalAgentManifest("ticker", "ticker")
	m.Schedules = []soyapack.ScheduleDecl{{
		Cron:           "0 9 * * *",
		MissedFire:     "once",
		IdempotencyKey: "ticker:{date}",
	}}
	dir := writePack(t, m, "system")

	type captured struct {
		jobID string
		spec  ScheduleSpec
		fire  func(ctx context.Context)
	}
	var got []captured
	k := New()
	k.SetScheduleHook(func(jobID string, spec ScheduleSpec, fire func(ctx context.Context)) error {
		got = append(got, captured{jobID, spec, fire})
		return nil
	})

	fake := &fakeProvider{responses: []string{"digest body"}}
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return fake }); err != nil {
		t.Fatalf("registerFromPack: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ScheduleHook called %d times, want 1", len(got))
	}
	if got[0].jobID != "pack:ticker:0" {
		t.Errorf("jobID = %q", got[0].jobID)
	}
	if got[0].spec.Cron != "0 9 * * *" {
		t.Errorf("spec.Cron = %q", got[0].spec.Cron)
	}
	if got[0].spec.MissedFire != "once" {
		t.Errorf("spec.MissedFire = %q", got[0].spec.MissedFire)
	}
	if got[0].spec.IdempotencyKey != "ticker:{date}" {
		t.Errorf("spec.IdempotencyKey = %q", got[0].spec.IdempotencyKey)
	}

	// Invoking the Fire callback drives the Agent's Handler with no
	// user messages — the system prompt seeds everything.
	got[0].fire(context.Background())
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.got) == 0 {
		t.Fatal("Fire() did not invoke provider")
	}
	for _, msg := range fake.got[0].Messages {
		if msg.Role == "user" {
			t.Errorf("scheduled fire produced user message: %+v", msg)
		}
	}
}

func TestRegisterFromPack_Schedules_WithoutHook_LogsAndContinues(t *testing.T) {
	m := minimalAgentManifest("orphan-sched", "orphan-sched")
	m.Schedules = []soyapack.ScheduleDecl{{Cron: "0 * * * *"}}
	dir := writePack(t, m, "system")

	var warnings []string
	k := New()
	k.SetLogger(func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	})

	fake := &fakeProvider{}
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return fake }); err != nil {
		t.Fatalf("registerFromPack: %v", err)
	}
	if _, ok := k.Lookup("soya:orphan-sched"); !ok {
		t.Fatal("agent must still register even without a ScheduleHook")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "no ScheduleHook wired") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a 'no ScheduleHook wired' warning, got %v", warnings)
	}
}

// fakePublisher records every Send call.
type fakePublisher struct {
	mu   sync.Mutex
	sent []string
	err  error
}

func (p *fakePublisher) Send(_ context.Context, _ string, body string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.sent = append(p.sent, body)
	return nil
}

func TestRegisterFromPack_Channels_ResolvedByHook(t *testing.T) {
	m := minimalAgentManifest("pushy", "pushy")
	m.Channels = []soyapack.ChannelDecl{{
		Kind:      "dingtalk",
		BindingID: "robot-1",
		Secrets:   map[string]string{"access_token_ref": "${SOYA_DINGTALK_ACCESS_TOKEN}"},
	}}
	m.Schedules = []soyapack.ScheduleDecl{{Cron: "0 9 * * *"}}
	dir := writePack(t, m, "produce a digest")

	pub := &fakePublisher{}
	var resolved []ChannelBindingSpec

	type captured struct {
		fire func(ctx context.Context)
	}
	var jobs []captured

	k := New()
	k.SetChannelHook(func(decl ChannelBindingSpec) (ChannelPublisher, error) {
		resolved = append(resolved, decl)
		return pub, nil
	})
	k.SetScheduleHook(func(_ string, _ ScheduleSpec, fire func(ctx context.Context)) error {
		jobs = append(jobs, captured{fire})
		return nil
	})

	fake := &fakeProvider{responses: []string{"news body"}}
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return fake }); err != nil {
		t.Fatalf("registerFromPack: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("ChannelHook called %d times, want 1", len(resolved))
	}
	if resolved[0].Kind != "dingtalk" || resolved[0].BindingID != "robot-1" {
		t.Errorf("ChannelBindingSpec = %+v", resolved[0])
	}
	if resolved[0].Secrets["access_token_ref"] != "${SOYA_DINGTALK_ACCESS_TOKEN}" {
		t.Errorf("secrets[access_token_ref] = %q", resolved[0].Secrets["access_token_ref"])
	}

	// Firing the schedule must drive the handler + push through the
	// resolved publisher.
	if len(jobs) != 1 {
		t.Fatalf("ScheduleHook calls = %d, want 1", len(jobs))
	}
	jobs[0].fire(context.Background())
	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.sent) != 1 {
		t.Fatalf("publisher Send count = %d, want 1", len(pub.sent))
	}
	if pub.sent[0] != "news body" {
		t.Errorf("publisher body = %q, want %q", pub.sent[0], "news body")
	}
}

func TestRegisterFromPack_Channels_HookError_DoesNotBlockRegistration(t *testing.T) {
	m := minimalAgentManifest("broken-ch", "broken-ch")
	m.Channels = []soyapack.ChannelDecl{{Kind: "dingtalk"}}
	dir := writePack(t, m, "system")

	var warnings []string
	k := New()
	k.SetLogger(func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	})
	k.SetChannelHook(func(_ ChannelBindingSpec) (ChannelPublisher, error) {
		return nil, errors.New("env var SOYA_DINGTALK_ACCESS_TOKEN not set")
	})

	fake := &fakeProvider{}
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return fake }); err != nil {
		t.Fatalf("registerFromPack: %v", err)
	}
	if _, ok := k.Lookup("soya:broken-ch"); !ok {
		t.Fatal("agent must still register when channel hook errors")
	}
	if len(warnings) == 0 {
		t.Errorf("expected a warning when channel hook fails")
	}
}

// --- manifest.actions[] wiring (DD-010 EstateMuse — APP-553) ----------------

func TestRegisterFromPack_Actions_RegisterAndInvoke(t *testing.T) {
	// EstateMuse-style Pack: one per-row action whose handler prompt
	// lives at prompts/generate_post.md. After registerFromPack returns,
	// the kernel must be able to InvokeAction for that (slug, action_id)
	// and the resolved handler must:
	//
	//   - prepend the prompt body as the system message,
	//   - thread { row_id, payload } through as the user message,
	//   - rewrite the upstream model id to the resolved real model,
	//   - return ActionResult.Status == "done" with the LLM body in
	//     Output["content"].
	m := minimalAgentManifest("estate-muse", "estate-muse")
	m.Prompt = &soyapack.Prompt{
		Upstream: &soyapack.UpstreamDecl{
			Provider: "openai-compat",
			Model:    "gpt-4o-mini",
			BaseURL:  "https://api.example.com/v1",
		},
	}
	m.Actions = []soyapack.ActionDecl{{
		ID:        "generate_post",
		On:        "per_row",
		Handler:   "prompts/generate_post.md",
		Timeout:   "60s",
		Artifacts: []string{"wechat_post"},
	}}
	dir := writePack(t, m, "SYSTEM main prompt")
	// Also write the action handler prompt at the path the manifest names.
	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompts", "generate_post.md"),
		[]byte("ACTION: generate a wechat post for the row."), 0o644); err != nil {
		t.Fatalf("write action handler: %v", err)
	}

	k := New()
	fake := &fakeProvider{responses: []string{"<post body for row-17>"}}
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return fake }); err != nil {
		t.Fatalf("registerFromPack: %v", err)
	}

	res, err := k.InvokeAction(context.Background(),
		auth.Identity{Subject: "u1"},
		"estate-muse", "generate_post", "row-17",
		map[string]any{"title": "杭州亚运村"},
	)
	if err != nil {
		t.Fatalf("InvokeAction: %v", err)
	}
	if res.Status != "done" {
		t.Errorf("Status = %q, want done", res.Status)
	}
	if res.RowID != "row-17" {
		t.Errorf("RowID = %q, want row-17", res.RowID)
	}
	content, _ := res.Output["content"].(string)
	if content != "<post body for row-17>" {
		t.Errorf("Output[content] = %q, want LLM body", content)
	}

	// Provider must have seen exactly one call with system=action prompt,
	// user=JSON envelope, model=resolved real model.
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.got) != 1 {
		t.Fatalf("provider got %d calls, want 1", len(fake.got))
	}
	req := fake.got[0]
	if req.Model != "gpt-4o-mini" {
		t.Errorf("upstream model = %q, want gpt-4o-mini", req.Model)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("provider got %d messages, want 2", len(req.Messages))
	}
	if !strings.Contains(req.Messages[0].Content, "ACTION:") {
		t.Errorf("system message missing action prompt body: %q", req.Messages[0].Content)
	}
	if !strings.Contains(req.Messages[1].Content, "row-17") {
		t.Errorf("user message missing row_id: %q", req.Messages[1].Content)
	}
	if !strings.Contains(req.Messages[1].Content, "杭州亚运村") {
		t.Errorf("user message missing payload: %q", req.Messages[1].Content)
	}
}

func TestRegisterFromPack_Actions_MissingHandlerFile(t *testing.T) {
	m := minimalAgentManifest("act-nofile", "act-nofile")
	m.Actions = []soyapack.ActionDecl{{
		ID: "missing", On: "per_row", Handler: "prompts/missing.md",
	}}
	dir := writePack(t, m, "x")
	err := New().RegisterFromPack(m, dir)
	if err == nil {
		t.Fatal("RegisterFromPack(missing action prompt) returned nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}

func TestRegisterFromPack_Actions_UnknownActionStill404(t *testing.T) {
	// Registering one action must not register others; InvokeAction for
	// an undeclared id still returns ErrUnknownAction.
	m := minimalAgentManifest("act-one", "act-one")
	m.Actions = []soyapack.ActionDecl{{
		ID: "first", On: "per_row", Handler: "prompts/first.md",
	}}
	dir := writePack(t, m, "x")
	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompts", "first.md"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	k := New()
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return &fakeProvider{} }); err != nil {
		t.Fatalf("registerFromPack: %v", err)
	}
	_, err := k.InvokeAction(context.Background(), auth.Identity{}, "act-one", "second", "row-1", nil)
	if !errors.Is(err, ErrUnknownAction) {
		t.Errorf("expected ErrUnknownAction, got %v", err)
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
