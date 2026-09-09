package kernel

import (
	"context"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
	"testing"
)

func TestActionTextCountsActualSection(t *testing.T) {
	cfg := &soyapack.TextValidation{Section: "口播全文", MinChars: 2, MaxChars: 3}
	for _, tc := range []struct {
		text  string
		valid bool
	}{{"## 3. 口播全文\n你好！\n## 待核实\n其他内容不计", true}, {"## 口播全文\n一二三四五\n（约2字）", false}, {"没有对应章节", false}} {
		if err := validateActionText(tc.text, cfg); (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.text, err)
		}
	}
}

func TestActionTextRepairAndReject(t *testing.T) {
	cfg := &soyapack.TextValidation{MaxChars: 3, MinChars: 2, MaxRepairs: 1}
	p := &fakeProvider{responses: []string{"一二三四五", "你好"}}
	req := llmcall.Request{Messages: []llmcall.Message{{Role: "system", Content: "system"}, {Role: "user", Content: `{"payload":{"original_request":"完整需求"}}`}}}
	got, err := collectValidatedAction(context.Background(), p, soyapack.ActionDecl{TextValidation: cfg}, req.Messages[1].Content, req)
	if err != nil || got != "你好" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	p = &fakeProvider{responses: []string{"一二三四五", "仍然超过字数"}}
	if _, err := collectValidatedAction(context.Background(), p, soyapack.ActionDecl{TextValidation: cfg}, req.Messages[1].Content, req); err == nil {
		t.Fatal("accepted invalid repaired text")
	}
}

func TestActionTextForbiddenPhrase(t *testing.T) {
	cfg := &soyapack.TextValidation{MaxChars: 100, ForbiddenPhrases: []string{"根据相关规定"}}
	if validateActionText("根据相关规定必须如此", cfg) == nil {
		t.Fatal("accepted unsupported assertion")
	}
}
