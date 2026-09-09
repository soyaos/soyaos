package soyapack_test

import (
	"errors"
	"github.com/soyaos/soyaos/pkg/soyapack"
	"testing"
)

func TestValidateActionTimeout(t *testing.T) {
	for _, tc := range []struct {
		timeout string
		valid   bool
	}{{"", true}, {"60s", true}, {"1m30s", true}, {"10ms", true}, {"0", false}, {"0s", false}, {"-1s", false}, {"60", false}, {"invalid", false}} {
		m, err := soyapack.LoadFromFile(fixturePath(t, "agent.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		m.Actions = []soyapack.ActionDecl{{ID: "run", On: "per_row", Handler: "prompt.md", Timeout: tc.timeout}}
		err = soyapack.Validate(m)
		if (err == nil) != tc.valid {
			t.Fatalf("timeout=%q err=%v", tc.timeout, err)
		}
		if !tc.valid && !errors.Is(err, soyapack.ErrInvalidManifest) {
			t.Fatalf("missing manifest sentinel: %v", err)
		}
	}
}
