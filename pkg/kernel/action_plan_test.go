package kernel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

func TestActionPlanIsNotReviewEvidenceAndSurvivesRepair(t *testing.T) {
	m := minimalAgentManifest("planned", "planned")
	m.Actions = []soyapack.ActionDecl{{ID: "write", On: "per_row", Handler: "draft.md", PlanHandler: "plan.md", ReviewHandler: "review.md", TextValidation: &soyapack.TextValidation{MinChars: 1, MaxChars: 30, MaxRepairs: 1}}}
	dir := writePack(t, m, "main")
	for name, body := range map[string]string{"draft.md": "draft", "plan.md": "plan", "review.md": "review"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := &fakeProvider{responses: []string{"AI_PLAN_NOT_EVIDENCE", "无依据", `{"approved":false,"findings":[{"quote":"无依据","reason":"核实原始记录"}],"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":false,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`, "核实记录", `{"approved":true,"findings":[],"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`}}
	k := New()
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return p }); err != nil {
		t.Fatal(err)
	}
	result, err := k.InvokeAction(context.Background(), auth.Identity{}, "planned", "write", "row-2", map[string]any{"original_request": "原始需求", "title": "选题"})
	if err != nil || result.Output["content"] != "核实记录" || len(p.got) != 5 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, len(p.got))
	}
	for i, req := range p.got {
		user := req.Messages[1].Content
		if !strings.Contains(user, "原始需求") {
			t.Fatalf("lost original request at %d", i)
		}
		wantPlan := i == 1 || i == 3
		if strings.Contains(user, "AI_PLAN_NOT_EVIDENCE") != wantPlan {
			t.Fatalf("plan exposure mismatch at %d", i)
		}
	}
}

func TestActionPlanEmptyResultFailsClosed(t *testing.T) {
	p := &fakeProvider{responses: []string{" "}}
	h := buildPackActionHandler("draft", "review", "plan", p, "model")
	res, err := h(context.Background(), soyapack.ActionDecl{}, ActionRequest{Payload: map[string]any{"title": "题目"}})
	if err == nil || res.Status == "done" || len(p.got) != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", res, err, len(p.got))
	}
}
