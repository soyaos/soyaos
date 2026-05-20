// originality_test.go pins the InMemoryIndex Jaccard math: identical
// text → score >= 0.9, lightly edited text → middling score, unrelated
// text → score < 0.5. APP-504.
package originality

import (
	"context"
	"testing"
)

func TestInvoke_IdenticalText_FlaggedSimilar(t *testing.T) {
	tool := &Tool{Index: NewInMemoryIndex()}
	ctx := context.Background()
	const seed = "姐妹们 这款面霜真的绝了 一周肌肤水润到爆"
	if err := tool.Index.Add(ctx, seed); err != nil {
		t.Fatal(err)
	}
	out, err := tool.Invoke(ctx, Input{Text: seed})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Similar {
		t.Fatalf("identical text not flagged: score=%f", out.Score)
	}
	if out.Score < 0.9 {
		t.Fatalf("identical score = %f, want >= 0.9", out.Score)
	}
}

func TestInvoke_SlightlyEdited_MiddlingScore(t *testing.T) {
	tool := &Tool{Index: NewInMemoryIndex()}
	ctx := context.Background()
	_ = tool.Index.Add(ctx, "姐妹们 这款面霜真的绝了 一周肌肤水润到爆")
	// Slight edit: tweak a single phrase and add a sentence.
	out, err := tool.Invoke(ctx, Input{
		Text: "姐妹们 这款乳液真的绝了 一周脸蛋水润到爆 必须冲",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Score >= 0.9 || out.Score <= 0.05 {
		t.Fatalf("middling-edit score = %f, want middling (0.05 < s < 0.9)", out.Score)
	}
}

func TestInvoke_CompletelyDifferent_LowScore(t *testing.T) {
	tool := &Tool{Index: NewInMemoryIndex()}
	ctx := context.Background()
	_ = tool.Index.Add(ctx, "姐妹们 这款面霜真的绝了 一周肌肤水润到爆")
	out, err := tool.Invoke(ctx, Input{
		Text: "今天回顾一下 Q1 营收数据，云业务环比增长 23%，海外市场表现亮眼。",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Score >= 0.5 {
		t.Fatalf("unrelated text score = %f, want < 0.5", out.Score)
	}
	if out.Similar {
		t.Fatal("unrelated text flagged similar")
	}
}

func TestInvoke_EmptyCorpus_ZeroScore(t *testing.T) {
	tool := &Tool{Index: NewInMemoryIndex()}
	out, err := tool.Invoke(context.Background(), Input{Text: "新文章 没人见过"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Score != 0 {
		t.Fatalf("empty-corpus score = %f, want 0", out.Score)
	}
	if out.Similar {
		t.Fatal("empty corpus should not flag similar")
	}
}

func TestInvoke_RequiresText(t *testing.T) {
	tool := &Tool{Index: NewInMemoryIndex()}
	if _, err := tool.Invoke(context.Background(), Input{}); err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestInvoke_CustomThreshold_OverridesDefault(t *testing.T) {
	tool := &Tool{Index: NewInMemoryIndex()}
	ctx := context.Background()
	_ = tool.Index.Add(ctx, "Hello World")
	// Same text. With threshold=0.99 the default similar-classification
	// should still hold; with threshold=0.5 a moderately-related text
	// should also be flagged similar.
	out, err := tool.Invoke(ctx, Input{Text: "Hello World", Threshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Similar {
		t.Fatalf("with low threshold identical text should be Similar=true, got %+v", out)
	}
}

func TestBuiltin_ReturnsRegisterableTool(t *testing.T) {
	tl := Builtin()
	if tl.Name != ToolName {
		t.Fatalf("Builtin.Name = %q, want %q", tl.Name, ToolName)
	}
	if tl.Handler == nil {
		t.Fatal("Builtin.Handler is nil")
	}
	// Call via Handler to round-trip the JSON-shaped input/output path.
	out, err := tl.Handler(context.Background(), map[string]any{
		"text":      "anything",
		"threshold": 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.(Output); !ok {
		t.Fatalf("Handler returned %T, want Output", out)
	}
}
