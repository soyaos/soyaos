package kernel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

// Opt-in model evaluation. Fixtures and credentials remain outside the repo;
// only case names, timings and verdict matches are printed.
type reviewEvaluationProvider struct {
	llmcall.Provider
	responses []string
}

func (p *reviewEvaluationProvider) GenerateStream(ctx context.Context, req llmcall.Request, out chan<- llmcall.Chunk) error {
	chunks := make(chan llmcall.Chunk, 16)
	finished := make(chan error, 1)
	go func() {
		finished <- p.Provider.GenerateStream(ctx, req, chunks)
		close(chunks)
	}()
	var response strings.Builder
	for chunk := range chunks {
		response.WriteString(chunk.Delta)
		select {
		case out <- chunk:
		case <-ctx.Done():
		}
	}
	p.responses = append(p.responses, response.String())
	return <-finished
}

func TestActionReviewRealFixtures(t *testing.T) {
	path := os.Getenv("SOYA_ACTION_REVIEW_FIXTURES")
	if path == "" {
		t.Skip("set SOYA_ACTION_REVIEW_FIXTURES for explicit real-provider evaluation")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Prompt string `json:"prompt"`
		Cases  []struct {
			Name           string                   `json:"name"`
			ActionID       string                   `json:"action_id"`
			Request        json.RawMessage          `json:"request"`
			Draft          string                   `json:"draft"`
			Approved       bool                     `json:"approved"`
			TextValidation *soyapack.TextValidation `json:"text_validation,omitempty"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.Cases) == 0 || fixtures.Prompt == "" {
		t.Fatal("empty fixtures")
	}
	cfg := llmcall.LoadConfigFromEnv()
	if !cfg.Configured() {
		t.Fatal("real provider is not configured")
	}
	provider := &llmcall.OpenAICompat{Cfg: cfg}
	var report []map[string]any
	if path := os.Getenv("SOYA_ACTION_REVIEW_REPORT"); path != "" {
		defer func() {
			data, err := json.MarshalIndent(report, "", "  ")
			if err == nil {
				err = os.WriteFile(path, data, 0600)
			}
			if err != nil {
				t.Errorf("write private review report: %v", err)
			}
		}()
	}
	for _, tc := range fixtures.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			start := time.Now()
			capture := &reviewEvaluationProvider{Provider: provider}
			rejected, failed := reviewActionText(ctx, capture, cfg.Model, fixtures.Prompt, string(tc.Request), tc.Draft, tc.ActionID)
			modelApproved := rejected == nil && failed == nil
			textErr := validateActionText(tc.Draft, tc.TextValidation)
			if textErr != nil && rejected == nil {
				rejected = textErr
			}
			entry := map[string]any{"case": tc.Name, "expected": tc.Approved, "approved": rejected == nil && failed == nil, "elapsed_seconds": time.Since(start).Seconds()}
			entry["model_approved"] = modelApproved
			// Private report only: retain the explicit assessment, not request
			// headers, credentials, reasoning tokens or production logs.
			entry["review_responses"] = capture.responses
			if textErr != nil {
				entry["text_error"] = textErr.Error()
			}
			if rejected != nil {
				entry["rejection"] = rejected.Error()
			}
			if failed != nil {
				entry["failure"] = failed.Error()
			}
			report = append(report, entry)
			if failed != nil {
				t.Fatalf("review protocol/provider failure: %v", failed)
			}
			approved := rejected == nil
			t.Logf("approved=%v expected=%v elapsed=%s", approved, tc.Approved, time.Since(start))
			if approved != tc.Approved {
				t.Fatal("semantic verdict mismatch; inspect private fixture")
			}
		})
	}
}

func TestActionReviewRejectsDuplicateJSONKeys(t *testing.T) {
	checks := make([]map[string]any, 0, 5)
	for _, id := range []string{"business_context", "factual_claims", "method_logic", "format", "safety"} {
		checks = append(checks, map[string]any{"id": id, "passed": true, "reason": "Protocol fixture"})
	}
	encoded, err := json.Marshal(map[string]any{"checks": checks, "approved": true, "findings": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	approved := string(encoded)
	checks[2]["passed"] = false
	encoded, err = json.Marshal(map[string]any{"checks": checks, "approved": false, "findings": []map[string]string{{"quote": "draft", "reason": "Correct this claim"}}})
	if err != nil {
		t.Fatal(err)
	}
	rejected := string(encoded)
	for _, tc := range []struct {
		name, response             string
		wantFailure, wantRejection bool
	}{
		{"valid approval", approved, false, false},
		{"valid rejection", rejected, false, true},
		{"top-level decision", strings.Replace(approved, `"approved":true`, `"approved":false,"approved":true`, 1), true, false},
		{"nested check", strings.Replace(approved, `"passed":true`, `"passed":false,"passed":true`, 1), true, false},
		{"nested finding", strings.Replace(rejected, `"quote":"draft"`, `"quote":"invented","quote":"draft"`, 1), true, false},
		{"escaped duplicate", strings.Replace(approved, `"approved":true`, `"approved":false,"\u0061pproved":true`, 1), true, false},
		{"case-insensitive duplicate", strings.Replace(approved, `"approved":true`, `"approved":false,"APPROVED":true`, 1), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakeProvider{responses: []string{tc.response}}
			rejection, failure := reviewActionText(context.Background(), p, "model", "review", `{}`, "draft")
			if (failure != nil) != tc.wantFailure || (rejection != nil) != tc.wantRejection {
				t.Fatalf("rejection=%v failure=%v", rejection, failure)
			}
		})
	}
}

func TestActionReviewQuoteSchema(t *testing.T) {
	for _, draft := range []string{"首行\n\n  第二行\r\n首行", "一句含\"引号\"和\\反斜线。", "单行全文"} {
		schema, err := actionReviewSchemaFor(draft)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal(schema, &object); err != nil {
			t.Fatal(err)
		}
		properties := object["properties"].(map[string]any)
		items := properties["findings"].(map[string]any)["items"].(map[string]any)
		quotes := items["properties"].(map[string]any)["quote"].(map[string]any)["enum"].([]any)
		seen := map[string]bool{}
		for _, q := range quotes {
			quote := q.(string)
			if quote == "" || !strings.Contains(draft, quote) || seen[quote] {
				t.Fatalf("invalid source quote %q", quote)
			}
			seen[quote] = true
		}
		for _, line := range strings.Split(draft, "\n") {
			if line = strings.TrimSpace(line); line != "" && !seen[line] {
				t.Fatalf("source line missing: %q", line)
			}
		}
	}
	if _, err := actionReviewSchemaFor(" \n\t"); err == nil {
		t.Fatal("empty draft must fail before provider call")
	}
}

func TestActionReviewRequiresConsistentChecks(t *testing.T) {
	for _, mutation := range []string{"missing", "duplicate", "unknown", "missing_passed", "empty_reason", "contradiction"} {
		t.Run(mutation, func(t *testing.T) {
			checks := []map[string]any{}
			for _, id := range []string{"business_context", "factual_claims", "method_logic", "format", "safety"} {
				checks = append(checks, map[string]any{"id": id, "passed": true, "reason": "Protocol fixture"})
			}
			switch mutation {
			case "missing":
				checks = checks[:4]
			case "duplicate":
				checks[4]["id"] = "format"
			case "unknown":
				checks[4]["id"] = "style"
			case "missing_passed":
				delete(checks[4], "passed")
			case "empty_reason":
				checks[4]["reason"] = " "
			case "contradiction":
				checks[4]["passed"] = false
			}
			body, err := json.Marshal(map[string]any{"checks": checks, "approved": true, "findings": []any{}})
			if err != nil {
				t.Fatal(err)
			}
			p := &fakeProvider{responses: []string{string(body)}}
			rejected, failed := reviewActionText(context.Background(), p, "model", "review", `{}`, "draft")
			if rejected != nil || failed == nil {
				t.Fatalf("inconsistent checks must fail closed: rejection=%v failure=%v", rejected, failed)
			}
		})
	}
}

func TestActionReviewPackWiring(t *testing.T) {
	m := minimalAgentManifest("reviewed", "reviewed")
	m.Actions = []soyapack.ActionDecl{{ID: "write", On: "per_row", Handler: "draft.md", ReviewHandler: "review.md"}}
	dir := writePack(t, m, "main")
	for name, body := range map[string]string{"draft.md": "write draft", "review.md": "check supplied evidence"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := &fakeProvider{responses: []string{"待核实稿件", `{"approved":true,"findings":[],"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`}}
	k := New()
	if err := k.registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return p }); err != nil {
		t.Fatal(err)
	}
	res, err := k.InvokeAction(context.Background(), auth.Identity{}, "reviewed", "write", "row-1", map[string]any{"title": "问题"})
	if err != nil || res.Status != "done" || res.Output["semantic_review"] != "passed" || len(p.got) != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", res, err, len(p.got))
	}
	if !strings.Contains(p.got[1].Messages[0].Content, "check supplied evidence") {
		t.Fatal("review prompt not loaded")
	}
	if p.got[0].ResponseJSONSchema != nil || p.got[1].ResponseJSONSchema == nil || !p.got[1].ResponseJSONSchema.Strict {
		t.Fatal("only the review should request its strict response schema")
	}
	if !strings.Contains(p.got[1].Messages[1].Content, `"action_id":"write"`) {
		t.Fatal("review lost selected action identity")
	}
	for _, path := range []string{"missing.md", "../review.md", "/tmp/review.md", "empty.md"} {
		m.Actions[0].ReviewHandler = path
		if err := os.WriteFile(filepath.Join(dir, "empty.md"), []byte(" "), 0600); err != nil {
			t.Fatal(err)
		}
		if err := New().registerFromPack(m, dir, func(_ llmcall.Config) llmcall.Provider { return p }); err == nil {
			t.Fatalf("accepted review handler %q", path)
		}
	}
}

func TestActionReviewRepairsAgainstOriginalEvidence(t *testing.T) {
	bad := "色差说明低成本应急"
	good := "记录报修受理开工修复时间"
	reject := `{"approved":false,"findings":[{"quote":"色差说明低成本应急","reason":"没有成本证据，只能记录可核实时间"}],"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":false,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`
	p := &fakeProvider{responses: []string{bad, reject, good, `{"approved":true,"findings":[],"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`}}
	req := llmcall.Request{Model: "test-model", Messages: []llmcall.Message{{Role: "system", Content: "draft"}, {Role: "user", Content: `{"row_id":"row-2","payload":{"original_request":"完整需求","title":"有效选题","hook":"待审建议"}}`}}}
	decl := soyapack.ActionDecl{TextValidation: &soyapack.TextValidation{MinChars: 1, MaxChars: 100, MaxRepairs: 1}}
	got, err := collectValidatedAction(context.Background(), p, decl, req.Messages[1].Content, req, "review evidence")
	if err != nil || got != good {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if len(p.got) != 4 {
		t.Fatalf("calls=%d", len(p.got))
	}
	for _, i := range []int{1, 2, 3} {
		if !strings.Contains(p.got[i].Messages[1].Content, "完整需求") || p.got[i].Model != "test-model" {
			t.Fatalf("lost context/model at %d", i)
		}
	}
	if !strings.Contains(p.got[2].Messages[1].Content, "没有成本证据") {
		t.Fatal("repair omitted semantic feedback")
	}
}

func TestActionReviewFailsClosed(t *testing.T) {
	for _, response := range []string{
		`{}`, `{"approved":true,"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`, `{"approved":true,"findings":null,"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`,
		`{"approved":false,"findings":[],"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":false,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`,
		`{"approved":true,"findings":[{"quote":"稿件","reason":"bad"}],"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`,
		`{"approved":false,"findings":[{"quote":"不存在","reason":"bad"}],"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":false,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`,
		`{"approved":false,"findings":[{"quote":"稿件","reason":" "}],"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":false,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`,
		`{"approved":true,"findings":[],"extra":true,"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`,
		`{"approved":true,"findings":[]} {"approved":false}`,
		"not json",
	} {
		t.Run(response, func(t *testing.T) {
			p := &fakeProvider{responses: []string{response}}
			rejected, failed := reviewActionText(context.Background(), p, "test", "review", `{"payload":{}}`, "稿件")
			if failed == nil || rejected != nil {
				t.Fatalf("accepted malformed verdict: rejected=%v failed=%v", rejected, failed)
			}
		})
	}
}

func TestActionReviewRejectsAfterRepairBudget(t *testing.T) {
	rejected := `{"approved":false,"findings":[{"quote":"无依据","reason":"缺少证据"}],"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":false,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`
	p := &fakeProvider{responses: []string{"无依据", rejected, "仍无依据", rejected}}
	req := llmcall.Request{Messages: []llmcall.Message{{Role: "system", Content: "draft"}, {Role: "user", Content: `{"payload":{}}`}}}
	decl := soyapack.ActionDecl{TextValidation: &soyapack.TextValidation{MinChars: 1, MaxChars: 100, MaxRepairs: 1}}
	got, err := collectValidatedAction(context.Background(), p, decl, req.Messages[1].Content, req, "review")
	if got != "" || err == nil || !strings.Contains(err.Error(), "semantic review rejected") || len(p.got) != 4 {
		t.Fatalf("got=%q err=%v calls=%d", got, err, len(p.got))
	}
}

func TestActionReviewCombinesLengthAndSemanticFeedback(t *testing.T) {
	bad := "无依据" + strings.Repeat("长", 30)
	p := &fakeProvider{responses: []string{bad, `{"approved":false,"findings":[{"quote":"无依据","reason":"删除没有证据的因果结论"}],"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":false,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`, "核实记录", `{"approved":true,"findings":[],"checks":[{"id":"business_context","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"factual_claims","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"method_logic","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"format","passed":true,"reason":"Protocol fixture; not factual evaluation"},{"id":"safety","passed":true,"reason":"Protocol fixture; not factual evaluation"}]}`}}
	req := llmcall.Request{Messages: []llmcall.Message{{Role: "system", Content: "draft"}, {Role: "user", Content: `{"payload":{"title":"有效题目"}}`}}}
	decl := soyapack.ActionDecl{TextValidation: &soyapack.TextValidation{MinChars: 1, MaxChars: 10, MaxRepairs: 1}}
	got, err := collectValidatedAction(context.Background(), p, decl, req.Messages[1].Content, req, "review")
	if err != nil || got != "核实记录" || len(p.got) != 4 {
		t.Fatalf("got=%q err=%v calls=%d", got, err, len(p.got))
	}
	feedback := p.got[2].Messages[1].Content
	for _, required := range []string{"33 letters/numbers", "删除没有证据的因果结论", "有效题目"} {
		if !strings.Contains(feedback, required) {
			t.Fatalf("repair missing %q", required)
		}
	}
}
