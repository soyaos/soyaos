package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
	"github.com/soyaos/soyaos/pkg/state"
	"github.com/soyaos/soyaos/pkg/store"
)

func TestIndexedTableBatchesBoundConcurrencyAndPreserveOrder(t *testing.T) {
	cfg := indexedConfig()
	cfg.BatchSize = 1
	cfg.MaxConcurrency = 2
	var active, peak atomic.Int32
	started := make(chan int, 3)
	release := make(chan struct{})
	type result struct {
		rows [][]string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		rows, err := expandIndexedBatches(context.Background(), cfg, 3, map[string]any{"original_request": "杭州完整需求", "previous_stage_output": "map", "repair": false}, func(ctx context.Context, _ int, p map[string]any) (string, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			index := p["batch_index"].(int)
			started <- index
			if p["original_request"] != "杭州完整需求" || p["previous_stage_output"] != "map" || p["batch_count"] != 3 || p["target_count"] != 1 {
				return "", fmt.Errorf("lost envelope fields")
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-release:
			}
			if index == 1 {
				time.Sleep(20 * time.Millisecond)
			}
			return fmt.Sprintf(`{"rows":[["title%d","hook%d"]]}`, index, index), nil
		})
		done <- result{rows, err}
	}()
	<-started
	<-started
	select {
	case <-started:
		t.Fatal("exceeded concurrency limit")
	default:
	}
	close(release)
	got := <-done
	if got.err != nil || peak.Load() != 2 || active.Load() != 0 {
		t.Fatalf("err=%v peak=%d active=%d", got.err, peak.Load(), active.Load())
	}
	for i, row := range got.rows {
		if row[0] != fmt.Sprintf("title%d", i+1) {
			t.Fatalf("nondeterministic order: %#v", got.rows)
		}
	}
}

func TestIndexedTableBatchFailureCancelsAndJoinsPeers(t *testing.T) {
	cfg := indexedConfig()
	cfg.BatchSize = 1
	cfg.MaxConcurrency = 2
	peerStarted := make(chan struct{})
	peerExited := make(chan struct{})
	_, err := expandIndexedBatches(context.Background(), cfg, 3, map[string]any{}, func(ctx context.Context, _ int, p map[string]any) (string, error) {
		if p["batch_index"] == 1 {
			<-peerStarted
			return `{"rows":[["invalid"]]}`, nil
		}
		if p["batch_index"] != 2 {
			return "", errors.New("queued batch started after failure")
		}
		close(peerStarted)
		<-ctx.Done()
		close(peerExited)
		return "", ctx.Err()
	})
	if err == nil || !strings.Contains(err.Error(), "columns") {
		t.Fatalf("err=%v", err)
	}
	select {
	case <-peerExited:
	default:
		t.Fatal("returned before peer exited")
	}
}

type indexedFunctionProvider struct {
	fakeProvider
	stream func(context.Context, llmcall.Request, chan<- llmcall.Chunk) error
}

func (p *indexedFunctionProvider) GenerateStream(ctx context.Context, req llmcall.Request, out chan<- llmcall.Chunk) error {
	return p.stream(ctx, req, out)
}

func TestIndexedTableCrossBatchDedupAndRepair(t *testing.T) {
	cfg := indexedConfig()
	cfg.TargetRows = 3
	cfg.CandidateRows = 4
	cfg.BatchSize = 2
	cfg.MaxConcurrency = 2
	var repairs atomic.Int32
	provider := &indexedFunctionProvider{stream: func(_ context.Context, req llmcall.Request, out chan<- llmcall.Chunk) error {
		var payload map[string]any
		if err := json.Unmarshal([]byte(req.Messages[1].Content), &payload); err != nil {
			return err
		}
		if payload["original_request"] != "完整需求" {
			return errors.New("lost original")
		}
		body := "map"
		switch req.Messages[0].Content {
		case "candidate":
			if payload["repair"] == true {
				repairs.Add(1)
				if payload["target_count"] != float64(1) || len(payload["existing_titles"].([]any)) != 2 || payload["batch_count"] != float64(1) {
					return errors.New("invalid repair gap")
				}
				body = `{"rows":[["C","c"]]}`
			} else {
				body = `{"rows":[["A","a"],["B","b"]]}`
			}
		case "select":
			body = `{"indices":[1,2,3]}`
		}
		out <- llmcall.Chunk{Delta: body}
		return nil
	}}
	out := make(chan llmcall.Chunk, 2)
	err := buildIndexedTableHandler(indexedPrompts(), provider, "model", cfg)(context.Background(), auth.Identity{}, llmcall.Request{Messages: []llmcall.Message{{Role: "user", Content: "完整需求"}}}, out)
	if err != nil || repairs.Load() != 1 || len(out) != 2 {
		t.Fatalf("err=%v repairs=%d output=%d", err, repairs.Load(), len(out))
	}
	if !strings.Contains((<-out).Delta, `["C","c"]`) {
		t.Fatal("missing genuine repair row")
	}
}

func TestIndexedTableFailurePreservesCompletionArtifactAndRows(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	persisted := state.NewBoltStore(db)
	cfg := indexedConfig()
	cfg.MaxRepairs = 0
	cfg.BatchSize = 1
	cfg.MaxConcurrency = 2
	var generations atomic.Int32
	provider := &indexedFunctionProvider{stream: func(_ context.Context, req llmcall.Request, out chan<- llmcall.Chunk) error {
		var payload map[string]any
		if err := json.Unmarshal([]byte(req.Messages[1].Content), &payload); err != nil {
			return err
		}
		body := "brief"
		switch req.Messages[0].Content {
		case "candidate":
			body = fmt.Sprintf(`{"rows":[["generation%d-title%v","hook"]]}`, generations.Load(), payload["batch_index"])
		case "select":
			if generations.Add(1) == 1 {
				body = `{"indices":[1,2]}`
			} else {
				body = `{"indices":[1,1]}`
			}
		}
		out <- llmcall.Chunk{Delta: body}
		return nil
	}}
	agent := Agent{Slug: "indexed", Manifest: &soyapack.Manifest{Name: "indexed", State: &soyapack.StateDecl{Scope: "agent", Store: "kv"}, Artifacts: []soyapack.ArtifactDecl{{Kind: "xlsx", Schema: "topics.v1"}}}, Handler: buildIndexedTableHandler(indexedPrompts(), provider, "model", cfg)}
	k := New()
	k.SetStateStore(persisted)
	k.Register(agent)
	ctx := context.Background()
	id := auth.Identity{Subject: "owner"}
	req := llmcall.Request{Model: agent.ModelID()}
	if _, err := k.ChatCompletion(ctx, id, req); err != nil {
		t.Fatal(err)
	}
	keys := []struct {
		scope      state.Scope
		owner, key string
	}{{state.ScopeAgent, "indexed", CompletionStateKey}, {state.ScopeAgent, "indexed", "artifact/topics.v1/latest"}, {state.ScopeRow, rowStateOwner(agent, id, "row-1"), rowPayloadStateKey}, {state.ScopeRow, rowStateOwner(agent, id, "row-2"), rowPayloadStateKey}}
	before := make([]state.Entry, len(keys))
	for i, key := range keys {
		entry, err := persisted.Get(ctx, key.scope, key.owner, key.key)
		if err != nil {
			t.Fatal(err)
		}
		before[i] = entry
	}
	if _, err := k.ChatCompletion(ctx, id, req); err == nil {
		t.Fatal("invalid selection accepted")
	}
	for i, key := range keys {
		after, err := persisted.Get(ctx, key.scope, key.owner, key.key)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before[i], after) {
			t.Fatalf("failed generation mutated %s/%s: before=%#v after=%#v", key.owner, key.key, before[i], after)
		}
	}
}

func indexedConfig() soyapack.IndexedTable {
	return soyapack.IndexedTable{TargetRows: 2, CandidateRows: 3, Columns: []string{"标题", "钩子"}, SheetName: "Topics", MaxRepairs: 1, TimeoutSeconds: 1}
}
func indexedPrompts() []promptBody {
	return []promptBody{{id: "collect", body: "collect"}, {id: "candidate", body: "candidate"}, {id: "select", body: "select"}}
}

func TestIndexedTableRegistrationOptIn(t *testing.T) {
	m := minimalAgentManifest("indexed", "indexed")
	m.Entry = ""
	cfg := indexedConfig()
	m.Prompt = &soyapack.Prompt{IndexedTable: &cfg, Steps: []soyapack.PromptStep{{ID: "collect", Prompt: "prompts/collect.md"}, {ID: "candidate", Prompt: "prompts/candidate.md"}, {ID: "select", Prompt: "prompts/select.md"}}}
	dir := writePackWithSteps(t, m, []string{"collect", "candidate", "select"})
	p := &fakeProvider{responses: []string{"brief", `{"rows":[["A","a"],["B","b"]]}`, `{"indices":[2,1]}`}}
	k := New()
	if err := k.registerFromPack(m, dir, func(llmcall.Config) llmcall.Provider { return p }); err != nil {
		t.Fatal(err)
	}
	response, err := k.ChatCompletion(context.Background(), auth.Identity{}, llmcall.Request{Model: "soya:indexed"})
	if err != nil || !strings.Contains(response.Content, `"sheets"`) {
		t.Fatalf("response=%s err=%v", response.Content, err)
	}
	cfg.TimeoutSeconds = 0
	if err := New().registerFromPack(m, dir, func(llmcall.Config) llmcall.Provider { return p }); err == nil {
		t.Fatal("registration accepted invalid indexed config")
	}
}

func TestIndexedTableAssemblyAndRepair(t *testing.T) {
	fake := &fakeProvider{responses: []string{"brief", `{"rows":[[" A ","original hook"],["a","duplicate"]]}`, `{"rows":[["B","second hook"]]}`, `{"indices":[1,1]}`, `{"indices":[2,1]}`}}
	out := make(chan llmcall.Chunk, 10)
	brief := "杭州 1000万 新房二手房 小红书视频 获客"
	err := buildIndexedTableHandler(indexedPrompts(), fake, "model", indexedConfig())(context.Background(), auth.Identity{}, llmcall.Request{Messages: []llmcall.Message{{Role: "user", Content: brief}}}, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("chunks=%d", len(out))
	}
	chunk := <-out
	if !(<-out).Done {
		t.Fatal("missing final marker")
	}
	var snapshot struct {
		Metadata struct {
			OriginalRequest string `json:"original_request"`
		} `json:"metadata"`
		Sheets []struct {
			Rows [][]string `json:"rows"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal([]byte(chunk.Delta), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Metadata.OriginalRequest != brief {
		t.Fatal("final snapshot lost authoritative original request")
	}
	if got := snapshot.Sheets[0].Rows; len(got) != 2 || got[0][0] != "B" || got[1][0] != " A " || got[1][1] != "original hook" {
		t.Fatalf("candidate text rewritten: %#v", got)
	}
	if len(fake.got) != 5 {
		t.Fatalf("calls=%d", len(fake.got))
	}
	for i, req := range fake.got {
		var payload map[string]any
		if err := json.Unmarshal([]byte(req.Messages[1].Content), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["original_request"] != brief {
			t.Fatalf("stage %d lost original", i)
		}
		if i == 2 && (payload["target_count"] != float64(1) || payload["repair"] != true || payload["existing_titles"] == nil) {
			t.Fatalf("bad gap repair: %#v", payload)
		}
		if i == 4 && payload["validation_error"] == nil {
			t.Fatal("selection repair needs error")
		}
	}
}

func TestIndexedTableRejectsInvalidOutputWithoutEmission(t *testing.T) {
	for _, tc := range []struct {
		name      string
		responses []string
		calls     int
	}{
		{"insufficient", []string{"brief", `{"rows":[["A","a"]]}`, `{"rows":[["A","a"]]}`}, 3},
		{"wrong columns", []string{"brief", `{"rows":[["A"]]}`}, 2},
		{"empty cell", []string{"brief", `{"rows":[["A",""]]}`}, 2},
		{"excess candidates", []string{"brief", `{"rows":[["A","a"],["B","b"],["C","c"],["D","d"]]}`}, 2},
		{"bad selection repair exhausted", []string{"brief", `{"rows":[["A","a"],["B","b"]]}`, `{"indices":[0,1]}`, `{"indices":[1,3]}`}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeProvider{responses: tc.responses}
			out := make(chan llmcall.Chunk, 10)
			err := buildIndexedTableHandler(indexedPrompts(), fake, "model", indexedConfig())(context.Background(), auth.Identity{}, llmcall.Request{}, out)
			if err == nil || len(out) != 0 || len(fake.got) != tc.calls {
				t.Fatalf("err=%v chunks=%d calls=%d", err, len(out), len(fake.got))
			}
		})
	}
}

func TestIndexedTableIndexValidation(t *testing.T) {
	for _, input := range []string{`{"indices":[0,1]}`, `{"indices":[1,3]}`, `{"indices":[1,1]}`, `{"indices":[1]}`, `{"indices":[1,2,3]}`, `{"indices":[1.5,2]}`, `{"indices":["1",2]}`, `{"indices":[1,2],"extra":true}`, `{"indices":[1,2]} {}`, `null`} {
		if _, err := selectIndexedRows(input, [][]string{{"A"}, {"B"}}, 2); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
}

type indexedCancelProvider struct {
	fakeProvider
	canceled chan struct{}
}

func (p *indexedCancelProvider) GenerateStream(ctx context.Context, _ llmcall.Request, _ chan<- llmcall.Chunk) error {
	<-ctx.Done()
	close(p.canceled)
	return ctx.Err()
}
func TestIndexedTableDeadlineCancelsProvider(t *testing.T) {
	p := &indexedCancelProvider{canceled: make(chan struct{})}
	out := make(chan llmcall.Chunk, 2)
	start := time.Now()
	err := buildIndexedTableHandler(indexedPrompts(), p, "model", indexedConfig())(context.Background(), auth.Identity{}, llmcall.Request{}, out)
	if !errors.Is(err, context.DeadlineExceeded) || len(out) != 0 {
		t.Fatalf("err=%v output=%d", err, len(out))
	}
	select {
	case <-p.canceled:
	default:
		t.Fatal("provider not canceled")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("deadline not bounded")
	}
}

func TestIndexedTableDisabledRepairBudget(t *testing.T) {
	cfg := indexedConfig()
	cfg.MaxRepairs = 0
	p := &fakeProvider{responses: []string{"brief", `{"rows":[["A","a"]]}`}}
	out := make(chan llmcall.Chunk, 2)
	err := buildIndexedTableHandler(indexedPrompts(), p, "model", cfg)(context.Background(), auth.Identity{}, llmcall.Request{}, out)
	if err == nil || !strings.Contains(err.Error(), "repair budget") || len(p.got) != 2 || len(out) != 0 {
		t.Fatalf("err=%v calls=%d chunks=%d", err, len(p.got), len(out))
	}
}
