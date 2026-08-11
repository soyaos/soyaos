package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goodManifest is a minimal-but-valid soyapack.v0 Agent manifest used by the
// pack-validate happy path test. Mirrors examples/manifests/agent.yaml in the
// fields Validate enforces.
const goodManifest = `spec_version: soyapack.v0
kind: Agent
name: hello
version: 0.1.0
description: minimal agent for cmd/soyaos pack validate tests
authors:
  - name: tester
    email: t@example.com
license: MIT
runtime:
  compat: ">=0.1.0 <0.2.0"
determinism: read-only
entry: prompts/main.md
expose:
  openai_compat: chat
  virtual_model_id: soya:hello
`

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "soyapack.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir
}

// TestCmdPackValidate_Happy covers a valid manifest both via dir arg and via
// direct .yaml path.
func TestCmdPackValidate_Happy(t *testing.T) {
	dir := writeManifest(t, goodManifest)

	if err := cmdPackValidate([]string{dir}); err != nil {
		t.Fatalf("cmdPackValidate(dir) = %v, want nil", err)
	}
	if err := cmdPackValidate([]string{filepath.Join(dir, "soyapack.yaml")}); err != nil {
		t.Fatalf("cmdPackValidate(file) = %v, want nil", err)
	}
}

// TestCmdPackValidate_MissingPath verifies the loader's error surfaces when
// the path does not exist.
func TestCmdPackValidate_MissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", "soyapack.yaml")
	err := cmdPackValidate([]string{missing})
	if err == nil {
		t.Fatal("cmdPackValidate(missing) = nil, want error")
	}
}

// TestCmdPackValidate_BrokenYAML feeds in a manifest with a structurally bad
// field; either the loader or Validate must surface a non-nil error.
func TestCmdPackValidate_BrokenYAML(t *testing.T) {
	// Unbalanced braces force a YAML parse error inside LoadFromFile.
	broken := strings.Replace(goodManifest, "expose:\n  openai_compat: chat\n", "expose: {oh no\n", 1)
	dir := writeManifest(t, broken)
	if err := cmdPackValidate([]string{dir}); err == nil {
		t.Fatal("cmdPackValidate(broken yaml) = nil, want error")
	}
}

// TestCmdPackValidate_MissingArg ensures the helper rejects an empty args
// slice rather than panicking.
func TestCmdPackValidate_MissingArg(t *testing.T) {
	if err := cmdPackValidate(nil); err == nil {
		t.Fatal("cmdPackValidate(nil) = nil, want error")
	}
}
