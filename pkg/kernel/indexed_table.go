package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

// The handler publishes no chunks until every row and index is validated.
// Consequently a failed generation cannot replace the kernel's last completion
// or supply invalid rows to artifact rendering / row-action persistence.
func buildIndexedTableHandler(prompts []promptBody, provider llmcall.Provider, model string, cfg soyapack.IndexedTable) Handler {
	return func(parent context.Context, _ auth.Identity, req llmcall.Request, out chan<- llmcall.Chunk) error {
		if err := cfg.Validate(len(prompts)); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(parent, time.Duration(cfg.TimeoutSeconds)*time.Second)
		defer cancel()
		original := combineUserMessages(req.Messages)
		maxTokens := req.MaxTokens
		if maxTokens < 4096 {
			maxTokens = 4096
		}
		call := func(callCtx context.Context, step int, payload map[string]any) (string, error) {
			started := time.Now()
			data, err := json.Marshal(payload)
			if err != nil {
				return "", err
			}
			systemPrompt := prompts[step].body
			if partition, ok := payload["partition_value"].(string); ok {
				systemPrompt += fmt.Sprintf("\n本次仅生成分区 %q。每一行的第%d列（%s）必须精确等于 %q，不得输出其他分区；只展开主题地图中该分区的问题。", partition, cfg.BatchColumn+1, cfg.Columns[cfg.BatchColumn], partition)
			}
			content, err := streamCollect(callCtx, provider, llmcall.Request{Model: model, Messages: []llmcall.Message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: string(data)}}, Temperature: req.Temperature, MaxTokens: maxTokens, Stream: true, ResponseFormat: req.ResponseFormat})
			slog.Info("indexed_table stage", "stage", []string{"collect", "expand", "selection"}[step], "batch_index", payload["batch_index"], "batch_count", payload["batch_count"], "target_count", payload["target_count"], "repair", payload["repair"], "elapsed_ms", time.Since(started).Milliseconds(), "success", err == nil && callCtx.Err() == nil)
			if err != nil {
				return "", fmt.Errorf("indexed_table step %q: %w", prompts[step].id, err)
			}
			if err := callCtx.Err(); err != nil {
				return "", err
			}
			return content, nil
		}
		collected, err := call(ctx, 0, map[string]any{"original_request": original, "target_count": cfg.CandidateRows, "repair": false})
		if err != nil {
			return err
		}
		rows := [][]string{}
		seen := map[string]bool{}
		for attempt := 0; ; attempt++ {
			count := cfg.CandidateRows
			if attempt > 0 {
				count = cfg.TargetRows - len(rows)
			}
			payload := map[string]any{"original_request": original, "previous_stage_output": collected, "target_count": count, "repair": attempt > 0}
			if attempt > 0 {
				titles := make([]string, len(rows))
				for i, row := range rows {
					titles[i] = row[0]
				}
				payload["existing_titles"] = titles
				payload["previous_stage_output"] = "补缺阶段：不要重新展开原始主题地图，生成已有标题列表未覆盖的新问题。"
			}
			batchRows, err := expandIndexedBatches(ctx, cfg, count, payload, call)
			if err != nil {
				return err
			}
			for _, row := range batchRows {
				// A title is the identity of a topic: changing metadata cannot pad the table.
				key := strings.ToLower(strings.Join(strings.Fields(row[0]), " "))
				if !seen[key] {
					rows = append(rows, row)
					seen[key] = true
				}
			}
			slog.Info("indexed_table candidates", "repair", attempt > 0, "received_count", len(batchRows), "unique_count", len(rows), "target_count", cfg.TargetRows)
			if len(rows) >= cfg.TargetRows {
				break
			}
			if attempt >= cfg.MaxRepairs {
				return fmt.Errorf("indexed_table requires %d unique rows, got %d after repair budget", cfg.TargetRows, len(rows))
			}
		}
		candidateJSON, _ := json.Marshal(map[string]any{"rows": rows})
		validationError := ""
		var selected [][]string
		for attempt := 0; ; attempt++ {
			payload := map[string]any{"original_request": original, "previous_stage_output": string(candidateJSON), "target_count": cfg.TargetRows, "repair": attempt > 0}
			if attempt > 0 {
				payload["validation_error"] = validationError
			}
			content, err := call(ctx, 2, payload)
			if err != nil {
				return err
			}
			selected, err = selectIndexedRows(content, rows, cfg.TargetRows)
			slog.Info("indexed_table selection validation", "repair", attempt > 0, "selected_count", len(selected), "target_count", cfg.TargetRows, "success", err == nil)
			if err == nil {
				break
			}
			if attempt >= cfg.MaxRepairs {
				return err
			}
			validationError = err.Error()
		}
		columns := make([]map[string]any, len(cfg.Columns))
		for i, col := range cfg.Columns {
			columns[i] = map[string]any{"header": col}
		}
		snapshot, _ := json.Marshal(map[string]any{"metadata": map[string]any{"original_request": original}, "sheets": []any{map[string]any{"name": cfg.SheetName, "columns": columns, "rows": selected, "freeze_header": true}}})
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- llmcall.Chunk{Delta: string(snapshot)}:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- llmcall.Chunk{Done: true}:
			return nil
		}
	}
}

// expandIndexedBatches bounds both outstanding requests and total work. Workers
// write disjoint result slots, preserving batch order regardless of finish order.
// Any provider or validation failure cancels peers; all workers exit before return.
func expandIndexedBatches(parent context.Context, cfg soyapack.IndexedTable, count int, base map[string]any, call func(context.Context, int, map[string]any) (string, error)) ([][]string, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	size, concurrency := cfg.BatchSize, cfg.MaxConcurrency
	if size == 0 {
		size = count
		concurrency = 1
	}
	batches := (count + size - 1) / size
	if concurrency > batches {
		concurrency = batches
	}
	results := make([][][]string, batches)
	jobs := make(chan int)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error
	fail := func(err error) { once.Do(func() { firstErr = err; cancel() }) }
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				target := size
				if remaining := count - index*size; remaining < target {
					target = remaining
				}
				payload := make(map[string]any, len(base)+3)
				for k, v := range base {
					payload[k] = v
				}
				payload["batch_index"] = index + 1
				payload["batch_count"] = batches
				payload["target_count"] = target
				partition := ""
				if len(cfg.BatchValues) > 0 && base["repair"] != true {
					partition = cfg.BatchValues[index]
					payload["partition_value"] = partition
				}
				content, err := call(ctx, 1, payload)
				if err != nil {
					fail(err)
					return
				}
				var candidates struct {
					Rows [][]string `json:"rows"`
				}
				if err := strictTableJSON(content, &candidates); err != nil {
					fail(fmt.Errorf("indexed_table candidates batch %d: %w", index+1, err))
					return
				}
				if len(candidates.Rows) > target {
					slog.Info("indexed_table candidate surplus", "batch_index", index+1, "discarded_count", len(candidates.Rows)-target)
					candidates.Rows = candidates.Rows[:target]
				}
				validRows := make([][]string, 0, len(candidates.Rows))
				for i, row := range candidates.Rows {
					if len(row) != len(cfg.Columns) {
						fail(fmt.Errorf("indexed_table batch %d row %d has %d columns, want %d", index+1, i+1, len(row), len(cfg.Columns)))
						return
					}
					for _, cell := range row {
						if strings.TrimSpace(cell) == "" {
							fail(fmt.Errorf("indexed_table batch %d row %d contains empty cell", index+1, i+1))
							return
						}
					}
					if matchesIndexedColumnRules(row, cfg.ColumnRules) && (partition == "" || row[cfg.BatchColumn] == partition) {
						validRows = append(validRows, row)
					}
				}
				results[index] = validRows
				slog.Info("indexed_table batch validation", "batch_index", index+1, "batch_count", batches, "received_count", len(candidates.Rows), "rejected_count", len(candidates.Rows)-len(validRows), "target_count", target, "repair", base["repair"])
			}
		}()
	}
dispatch:
	for index := 0; index < batches; index++ {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- index:
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rows [][]string
	for _, batch := range results {
		rows = append(rows, batch...)
	}
	return rows, nil
}

func matchesIndexedColumnRules(row []string, rules []soyapack.IndexedColumnRule) bool {
	for _, rule := range rules {
		value := row[rule.Column]
		for _, forbidden := range rule.ForbiddenSubstrings {
			if strings.Contains(value, forbidden) {
				return false
			}
		}
		if !strings.HasPrefix(value, rule.Prefix) || !strings.HasSuffix(value, rule.Suffix) {
			return false
		}
		if len(rule.AllowedValues) > 0 {
			allowed := false
			for _, candidate := range rule.AllowedValues {
				if candidate == value {
					allowed = true
					break
				}
			}
			if !allowed {
				return false
			}
		}
	}
	return true
}

func strictTableJSON(content string, dst any) error {
	dec := json.NewDecoder(strings.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON object")
	}
	return nil
}

func selectIndexedRows(content string, rows [][]string, target int) ([][]string, error) {
	var selection struct {
		Indices []int `json:"indices"`
	}
	if err := strictTableJSON(content, &selection); err != nil {
		return nil, fmt.Errorf("indexed_table selection: %w", err)
	}
	if len(selection.Indices) != target {
		return nil, fmt.Errorf("indexed_table requires exactly %d indices, got %d", target, len(selection.Indices))
	}
	seen := map[int]bool{}
	result := make([][]string, 0, target)
	for _, index := range selection.Indices {
		if index < 1 || index > len(rows) || seen[index] {
			return nil, fmt.Errorf("indexed_table invalid or duplicate index %d (candidate count %d)", index, len(rows))
		}
		seen[index] = true
		result = append(result, rows[index-1])
	}
	return result, nil
}
