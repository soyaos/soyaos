package soyapack

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadFromFile reads a `soyapack.yaml` from disk and returns a parsed Manifest.
// It does NOT call Validate; callers that need strict validation should call
// Validate explicitly. The split lets test fixtures express invalid cases for
// negative tests.
func LoadFromFile(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("soyapack: open %s: %w", path, err)
	}
	defer f.Close()
	return LoadFromReader(f)
}

// LoadFromReader parses a Manifest from an io.Reader. Useful for tests and for
// callers that already have the bytes in memory.
func LoadFromReader(r io.Reader) (*Manifest, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("soyapack: read: %w", err)
	}
	return LoadFromBytes(b)
}

// LoadFromBytes parses YAML bytes into a Manifest. Strictness:
//
//   - Top-level unknown fields (not prefixed with `x-`) → error.
//   - YAML syntax errors → error.
//
// Validate semantic constraints with a follow-up call to Validate.
func LoadFromBytes(b []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("soyapack: parse: %w", err)
	}
	return &m, nil
}
