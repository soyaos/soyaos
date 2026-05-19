// factory_test.go exercises the deterministic heuristic path and the
// LLM-seam fallback. These tests are the contract that locks the
// NewsBeam alpha demo input ("每天早上 9 点把 AI 资讯发到钉钉群")
// against regressions in the keyword-extraction code.
package factory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/soyaos/soyaos/pkg/soyapack"
)

func TestFactory_Heuristic_NewsBeamDemo(t *testing.T) {
	f := &Factory{}
	m, err := f.Translate(context.Background(), "每天早上 9 点把 AI 资讯发到钉钉群", "zh-CN")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if m.Kind != soyapack.KindAgent {
		t.Errorf("Kind = %q, want Agent", m.Kind)
	}
	if m.SpecVersion != soyapack.SpecVersionV0 {
		t.Errorf("SpecVersion = %q, want %q", m.SpecVersion, soyapack.SpecVersionV0)
	}
	// rss_fetch capability hint present.
	foundRSS := false
	for _, u := range m.Uses {
		if u == "tool.rss_fetch" {
			foundRSS = true
			break
		}
	}
	if !foundRSS {
		t.Errorf("Uses missing tool.rss_fetch; got %v", m.Uses)
	}
	if m.Prompt == nil || len(m.Prompt.Tools) == 0 || m.Prompt.Tools[0] != "tool.rss_fetch" {
		t.Errorf("Prompt.Tools = %+v, want [tool.rss_fetch]", m.Prompt)
	}
	// schedule cron 0 9 * * *.
	if len(m.Schedules) != 1 {
		t.Fatalf("Schedules len = %d, want 1", len(m.Schedules))
	}
	if got := m.Schedules[0].Cron; got != "0 9 * * *" {
		t.Errorf("Schedule.Cron = %q, want %q", got, "0 9 * * *")
	}
	// channel = dingtalk.
	if len(m.Channels) != 1 || m.Channels[0].Kind != "dingtalk" {
		t.Errorf("Channels = %+v, want dingtalk binding", m.Channels)
	}
}

func TestFactory_Heuristic_EnglishVariant(t *testing.T) {
	f := &Factory{}
	m, err := f.Translate(context.Background(), "every day at 9am send AI news to Feishu", "en")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(m.Schedules) == 0 || m.Schedules[0].Cron != "0 9 * * *" {
		t.Errorf("english variant cron = %+v", m.Schedules)
	}
	if len(m.Channels) == 0 || m.Channels[0].Kind != "feishu" {
		t.Errorf("english variant channel = %+v", m.Channels)
	}
}

func TestFactory_EmptyInput_Errors(t *testing.T) {
	f := &Factory{}
	if _, err := f.Translate(context.Background(), "   ", "zh-CN"); err == nil {
		t.Fatal("expected error on empty input")
	}
}

// fakeLLM returns a canned YAML manifest. Used to prove the LLM seam wires
// through without exercising the heuristic path.
type fakeLLM struct{ yaml string }

func (f fakeLLM) Generate(_ context.Context, _ string, _ string) (string, error) {
	return f.yaml, nil
}

// erroringLLM proves that a misbehaving LLM never breaks the caller —
// the Factory must fall back to the heuristic manifest.
type erroringLLM struct{}

func (erroringLLM) Generate(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("llm: simulated outage")
}

func TestFactory_LLMSeam_AcceptsLLMOutput(t *testing.T) {
	llm := fakeLLM{yaml: `spec_version: soyapack.v0
kind: Agent
name: llm-overridden
version: 0.1.0
description: produced by fake LLM
authors:
  - name: fake-llm
license: MIT
runtime:
  compat: ">=0.1.0 <0.2.0"
determinism: stateful
entry: agent.main
expose:
  openai_compat: chat
  virtual_model_id: soya:llm-overridden
`}
	f := &Factory{LLM: llm}
	m, err := f.Translate(context.Background(), "anything", "en")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if m.Name != "llm-overridden" {
		t.Errorf("Name = %q, want llm-overridden (LLM should win)", m.Name)
	}
}

func TestFactory_LLMSeam_FallsBackOnError(t *testing.T) {
	f := &Factory{LLM: erroringLLM{}}
	m, err := f.Translate(context.Background(), "每天早上 9 点把 AI 资讯发到钉钉群", "zh-CN")
	if err != nil {
		t.Fatalf("Translate fallback failed: %v", err)
	}
	if m.Name == "llm-overridden" {
		t.Errorf("LLM error should have triggered heuristic fallback, got %q", m.Name)
	}
	if len(m.Schedules) == 0 || m.Schedules[0].Cron != "0 9 * * *" {
		t.Errorf("fallback should keep heuristic cron, got %+v", m.Schedules)
	}
}

func TestStub_StillReturnsErrNotImplemented(t *testing.T) {
	if _, err := (Stub{}).Translate(context.Background(), "hi", "en"); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Stub.Translate err = %v, want ErrNotImplemented", err)
	}
}

func TestSlugify_HandlesCJKAndPunctuation(t *testing.T) {
	got := slugify("每天早上 9 点把 AI 资讯发到钉钉群")
	if got == "" || strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
		t.Errorf("slugify produced invalid slug: %q", got)
	}
}
