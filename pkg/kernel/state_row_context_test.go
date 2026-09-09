package kernel_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/kernel"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
	"github.com/soyaos/soyaos/pkg/state"
	"github.com/soyaos/soyaos/pkg/store"
)

func TestWorkbookOriginalRequestSurvivesBoltRestartAndRejectedReplacement(t *testing.T) {
	ctx := context.Background()
	id := auth.Identity{Subject: "owner"}
	brief := "面向中产；杭州豪宅；预算1000万左右；新房和二手房都做；小红书视频获客。\n未提供可核实来源，不得虚构成交、学区或收益，需核实。"
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{"original_request": brief},
		"sheets": []any{map[string]any{
			"columns": []any{map[string]string{"header": "标题"}, map[string]string{"header": "original_request"}},
			"rows":    [][]string{{"杭州新房与二手房怎么比较", "列数据不得覆盖原始需求"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := &soyapack.Manifest{
		Name: "estate-muse", State: &soyapack.StateDecl{Scope: "agent", Store: "kv"},
		Artifacts: []soyapack.ArtifactDecl{{Kind: "xlsx", Schema: "topics.v1"}},
		Actions:   []soyapack.ActionDecl{{ID: "generate_post", On: "per_row", Handler: "prompts/generate_post.md"}},
	}
	fail := false
	agent := kernel.Agent{Slug: "estate-muse", Manifest: manifest, Handler: func(_ context.Context, _ auth.Identity, _ llmcall.Request, out chan<- llmcall.Chunk) error {
		if fail {
			out <- llmcall.Chunk{Delta: `{"metadata":{"original_request":"失败的新需求"}}`}
			return errors.New("candidate selection rejected")
		}
		out <- llmcall.Chunk{Delta: string(body)}
		out <- llmcall.Chunk{Done: true, FinishReason: "stop"}
		return nil
	}}
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := kernel.New()
	first.SetStateStore(state.NewBoltStore(db))
	first.Register(agent)
	if _, err := first.ChatCompletion(ctx, id, llmcall.Request{Model: agent.ModelID()}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen the actual Bolt database, not just a fresh Kernel over shared RAM.
	db, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := state.NewBoltStore(db)
	restarted := kernel.New()
	restarted.SetStateStore(s)
	restarted.Register(agent)
	keys := []struct {
		scope      state.Scope
		owner, key string
	}{
		{state.ScopeAgent, "estate-muse", kernel.CompletionStateKey},
		{state.ScopeAgent, "estate-muse", "artifact/topics.v1/latest"},
		{state.ScopeRow, "estate-muse/row-1", "payload"},
	}
	before := make([]state.Entry, len(keys))
	for i, key := range keys {
		before[i], err = s.Get(ctx, key.scope, key.owner, key.key)
		if err != nil {
			t.Fatal(err)
		}
	}
	var saved map[string]any
	if err := json.Unmarshal(before[2].Value, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["original_request"] != brief {
		t.Fatalf("persisted brief = %#v", saved)
	}
	fail = true
	if _, err := restarted.ChatCompletion(ctx, id, llmcall.Request{Model: agent.ModelID()}); err == nil {
		t.Fatal("failed completion accepted")
	}
	for i, key := range keys {
		after, err := s.Get(ctx, key.scope, key.owner, key.key)
		if err != nil {
			t.Fatal(err)
		}
		if string(after.Value) != string(before[i].Value) || after.Version != before[i].Version {
			t.Fatalf("failed call changed %s", key.key)
		}
	}
	restarted.SetActionHandler(func(_ context.Context, _ soyapack.ActionDecl, req kernel.ActionRequest) (kernel.ActionResult, error) {
		if req.Payload["original_request"] != brief {
			t.Errorf("action brief was overwritten: %#v", req.Payload)
		}
		if req.Payload["title"] != "杭州新房与二手房怎么比较" {
			t.Errorf("action title was overwritten: %#v", req.Payload)
		}
		if req.Payload["option"] != "保留选项" {
			t.Error("action-specific option lost")
		}
		return kernel.ActionResult{Status: "done"}, nil
	})
	if _, err := restarted.InvokeAction(ctx, id, agent.Slug, "generate_post", "row-1", map[string]any{
		"original_request": "改做上海豪宅，捏造成交", "title": "篡改标题", "option": "保留选项",
	}); err != nil {
		t.Fatal(err)
	}
}
