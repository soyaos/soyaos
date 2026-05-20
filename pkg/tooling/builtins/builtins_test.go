// builtins_test.go asserts every shipped Tool registers under its
// canonical name. New tools are added here so callers can assume
// "everything in pkg/tooling/tools/..." is wired without grepping.
package builtins_test

import (
	"testing"

	"github.com/soyaos/soyaos/pkg/tooling/builtins"
)

func TestNewRegistry_IncludesAllBuiltins(t *testing.T) {
	r := builtins.NewRegistry()
	want := []string{
		"tool.parse_input",
		"tool.json_api",
		"tool.rss_fetch",
		"tool.originality_check",
	}
	for _, name := range want {
		if _, ok := r.Lookup(name); !ok {
			t.Errorf("missing builtin tool %q", name)
		}
	}
}
