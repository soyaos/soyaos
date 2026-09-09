package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/llmcall"
)

func TestIndexedBatchPartitionsRejectCrossPartitionRows(t *testing.T) {
	cfg := indexedConfig()
	cfg.CandidateRows = 4
	cfg.BatchSize = 2
	cfg.MaxConcurrency = 2
	cfg.BatchColumn = 1
	cfg.BatchValues = []string{"A", "B"}
	rows, err := expandIndexedBatches(context.Background(), cfg, 4, map[string]any{"repair": false}, func(_ context.Context, _ int, p map[string]any) (string, error) {
		value, ok := p["partition_value"].(string)
		if !ok {
			return "", fmt.Errorf("missing partition")
		}
		raw, _ := json.Marshal(map[string]any{"rows": [][]string{{"good-" + value, value}, {"bad-" + value, "wrong"}}})
		return string(raw), nil
	})
	if err != nil || len(rows) != 2 || rows[0][1] != "A" || rows[1][1] != "B" {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

func TestIndexedPartitionSelectionRejectsSkewAndRepairs(t *testing.T) {
	for _, repairSucceeds := range []bool{false, true} {
		t.Run(fmt.Sprint(repairSucceeds), func(t *testing.T) {
			cfg := indexedConfig()
			cfg.CandidateRows, cfg.BatchSize, cfg.MaxConcurrency = 4, 2, 2
			cfg.BatchColumn, cfg.BatchValues, cfg.MinPerPartition = 1, []string{"A", "B"}, 1
			selections := 0
			provider := &indexedFunctionProvider{stream: func(_ context.Context, req llmcall.Request, out chan<- llmcall.Chunk) error {
				var p map[string]any
				if err := json.Unmarshal([]byte(req.Messages[1].Content), &p); err != nil {
					return err
				}
				body := "map"
				if strings.HasPrefix(req.Messages[0].Content, "candidate") {
					v := p["partition_value"].(string)
					b, _ := json.Marshal(map[string]any{"rows": [][]string{{v + "1", v}, {v + "2", v}}})
					body = string(b)
				} else if req.Messages[0].Content == "select" {
					selections++
					body = `{"indices":[1,3]}`
					if selections == 2 {
						if p["repair"] != true || !strings.Contains(fmt.Sprint(p["validation_error"]), "per partition") {
							return fmt.Errorf("missing coverage repair context")
						}
						if repairSucceeds {
							body = `{"indices":[1,2]}`
						}
					}
				}
				out <- llmcall.Chunk{Delta: body}
				return nil
			}}
			out := make(chan llmcall.Chunk, 2)
			err := buildIndexedTableHandler(indexedPrompts(), provider, "model", cfg)(context.Background(), auth.Identity{}, llmcall.Request{}, out)
			if selections != 2 || (err == nil) != repairSucceeds {
				t.Fatalf("selections=%d err=%v", selections, err)
			}
			if !repairSucceeds && len(out) != 0 {
				t.Fatal("invalid partition coverage emitted output")
			}
		})
	}
}

func TestIndexedCandidateSurplusIsBounded(t *testing.T) {
	cfg := indexedConfig()
	rows, err := expandIndexedBatches(context.Background(), cfg, 2, map[string]any{}, func(context.Context, int, map[string]any) (string, error) {
		return `{"rows":[["A","a"],["B","b"],["C","c"]]}`, nil
	})
	if err != nil || len(rows) != 2 || rows[1][0] != "B" {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

func TestIndexedInterleaveRetainsAllRowsWithoutPrefixBias(t *testing.T) {
	cfg := indexedConfig()
	cfg.BatchColumn = 1
	cfg.BatchValues = []string{"A", "B"}
	rows := [][]string{{"a1", "A"}, {"a2", "A"}, {"b1", "B"}, {"b2", "B"}}
	got := interleaveIndexedPartitions(rows, cfg)
	if len(got) != 4 || got[0][0] != "a1" || got[1][0] != "b1" || got[2][0] != "a2" || got[3][0] != "b2" {
		t.Fatalf("bad interleave: %v", got)
	}
}
