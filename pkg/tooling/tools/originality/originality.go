// Package originality implements the SoyaOS originality precheck Tool
// (EstateMuse Aha #6: "Refuse to publish copies of stuff we already
// published"). Drafts produced from the multi-platform template pool
// (APP-504) flow through this Tool before they leave the agent. The
// alpha index is in-memory and based on character-shingle Jaccard
// similarity — cheap, deterministic, no model dependency. A future
// revision will swap in an embedding-vector backend without touching
// the Tool interface.
package originality

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/soyaos/soyaos/pkg/tooling"
)

// ToolName is the canonical registry name for this Tool.
const ToolName = "tool.originality_check"

// DefaultThreshold is the cutoff at which two texts are considered
// "duplicates" (Similar=true). Tuned for Chinese drafts where a
// score of 0.85 typically corresponds to "less than a paragraph
// rewritten".
const DefaultThreshold = 0.85

// Index is the storage contract for previously-seen drafts.
type Index interface {
	Add(ctx context.Context, text string) error
	NearestSim(ctx context.Context, text string) (float64, error)
}

// Tool is the registry-facing wrapper around an Index.
type Tool struct {
	Index Index
}

// Input is the Tool's input payload.
type Input struct {
	Text      string  `json:"text"`
	Threshold float64 `json:"threshold,omitempty"` // 0 → DefaultThreshold
}

// Output is the Tool's response.
type Output struct {
	Similar bool    `json:"similar"`
	Score   float64 `json:"score"`
}

// Invoke runs the precheck. Returns Similar=true when the nearest
// previously-seen text exceeds the threshold.
func (t *Tool) Invoke(ctx context.Context, in Input) (Output, error) {
	if in.Text == "" {
		return Output{}, errors.New("originality: input.Text required")
	}
	if t.Index == nil {
		return Output{}, errors.New("originality: index not configured")
	}
	threshold := in.Threshold
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	score, err := t.Index.NearestSim(ctx, in.Text)
	if err != nil {
		return Output{}, fmt.Errorf("originality: nearest sim: %w", err)
	}
	return Output{Similar: score >= threshold, Score: score}, nil
}

// --- InMemoryIndex ---------------------------------------------------------

// InMemoryIndex is the alpha backend. It stores each Add()'d text as a
// 3-char shingle set; NearestSim returns the highest Jaccard similarity
// over every stored set.
//
// Trade-offs:
//   - Cheap and deterministic; no model dependency.
//   - Memory grows linearly with corpus size. Production deployments
//     should swap in an embedding-vector backend.
//   - "Shingle" granularity is per-rune (works for Chinese and English
//     equally; emoji are passed through as single runes).
type InMemoryIndex struct {
	mu      sync.RWMutex
	corpus  []map[string]struct{}
	shingle int // shingle size in runes; defaults to 3 via NewInMemoryIndex
}

// NewInMemoryIndex returns an empty index using 3-rune shingles.
func NewInMemoryIndex() *InMemoryIndex { return &InMemoryIndex{shingle: 3} }

// NewInMemoryIndexWithShingle lets callers tune the shingle width. Use
// 2 for very short drafts, 4-5 for long-form English.
func NewInMemoryIndexWithShingle(width int) *InMemoryIndex {
	if width < 1 {
		width = 1
	}
	return &InMemoryIndex{shingle: width}
}

// Add stores the shingle set for text. Empty texts are a no-op.
func (m *InMemoryIndex) Add(_ context.Context, text string) error {
	set := shinglesOf(text, m.shingle)
	if len(set) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.corpus = append(m.corpus, set)
	return nil
}

// NearestSim returns the maximum Jaccard similarity between text and any
// previously-Added text. Returns 0 when the corpus is empty.
func (m *InMemoryIndex) NearestSim(_ context.Context, text string) (float64, error) {
	candidate := shinglesOf(text, m.shingle)
	if len(candidate) == 0 {
		return 0, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	best := 0.0
	for _, stored := range m.corpus {
		sim := jaccard(candidate, stored)
		if sim > best {
			best = sim
		}
	}
	return best, nil
}

// shinglesOf returns the set of distinct n-rune substrings of text.
// Runes (not bytes) so Chinese characters are treated as one token.
func shinglesOf(text string, n int) map[string]struct{} {
	runes := []rune(text)
	out := make(map[string]struct{})
	if len(runes) < n {
		if len(runes) > 0 {
			out[string(runes)] = struct{}{}
		}
		return out
	}
	for i := 0; i+n <= len(runes); i++ {
		out[string(runes[i:i+n])] = struct{}{}
	}
	return out
}

// jaccard is |A ∩ B| / |A ∪ B|.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// Iterate the smaller set for the intersection count.
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}
	inter := 0
	for k := range small {
		if _, ok := large[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	return float64(inter) / float64(union)
}

// --- builtin registry integration ------------------------------------------

// Builtin returns the tooling.Tool descriptor used by the kernel
// registry. The default index is an empty InMemoryIndex; callers that
// want pre-seeded corpora should construct a Tool directly.
func Builtin() tooling.Tool {
	tool := &Tool{Index: NewInMemoryIndex()}
	return tooling.Tool{
		Name:        ToolName,
		Description: "Originality precheck — refuse to publish text too similar to recent drafts.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":      map[string]any{"type": "string"},
				"threshold": map[string]any{"type": "number"},
			},
			"required": []any{"text"},
		},
		OutputType: "application/json",
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in := Input{}
			if v, ok := input["text"].(string); ok {
				in.Text = v
			}
			if v, ok := input["threshold"].(float64); ok {
				in.Threshold = v
			} else if v, ok := input["threshold"].(int); ok {
				in.Threshold = float64(v)
			}
			return tool.Invoke(ctx, in)
		},
	}
}
