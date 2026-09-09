package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
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
