package kernel

import (
	"context"
	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
	"strings"
	"testing"
)

func TestIndexedColumnRulesFilterAndRepair(t *testing.T) {
	cfg := indexedConfig()
	cfg.ColumnRules = []soyapack.IndexedColumnRule{{Column: 0, Suffix: "？"}, {Column: 1, Prefix: "核实：", ForbiddenSubstrings: []string{"点燃"}}}
	p := &fakeProvider{responses: []string{"map", `{"rows":[["A？","核实：材料"],["B","核实：材料"],["C？","核实：点燃"]]}`, `{"rows":[["D？","核实：授权"]]}`, `{"indices":[1,2]}`}}
	out := make(chan llmcall.Chunk, 8)
	err := buildIndexedTableHandler(indexedPrompts(), p, "model", cfg)(context.Background(), auth.Identity{}, llmcall.Request{}, out)
	if err != nil {
		t.Fatal(err)
	}
	result := <-out
	if strings.Contains(result.Delta, "点燃") || !strings.Contains(result.Delta, "D？") {
		t.Fatalf("bad snapshot: %s", result.Delta)
	}
}

func TestIndexedColumnRulesExhaustionEmitsNothing(t *testing.T) {
	cfg := indexedConfig()
	cfg.MaxRepairs = 0
	cfg.ColumnRules = []soyapack.IndexedColumnRule{{Column: 1, AllowedValues: []string{"valid"}}}
	p := &fakeProvider{responses: []string{"map", `{"rows":[["A","invalid"],["B","valid"]]}`}}
	out := make(chan llmcall.Chunk, 8)
	if err := buildIndexedTableHandler(indexedPrompts(), p, "model", cfg)(context.Background(), auth.Identity{}, llmcall.Request{}, out); err == nil {
		t.Fatal("accepted shortage")
	}
	if len(out) != 0 {
		t.Fatal("emitted invalid result")
	}
}
