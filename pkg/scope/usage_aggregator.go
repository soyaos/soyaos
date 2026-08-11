// SilentCut reverse-pressure: per-second billing for SilentCut means the
// CometProvider.Cost() RPC ticks every 100ms (DD-011 §metering). Each tick
// produces a UsageSample; the aggregator rolls those into minute-aligned
// (api_key, agent, image) buckets so the billing pipeline downstream
// reads at most "1 row per minute per tenant per agent per image".
//
// alpha shape: in-memory + Bolt-backed Store persistence. The Flush()
// method moves "closed" minute buckets (minute < current minute) into
// store.Store; Query() reads them back out. Live (current-minute) data
// stays in memory until the next tick crosses a minute boundary.

package scope

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/soyaos/soyaos/pkg/store"
)

// usageStoreNamespace is the Bolt namespace the aggregator writes to.
const usageStoreNamespace = "scope.usage.v0"

// UsageSample is one 100ms-tick sample from a CometProvider.
//
// At is the wall-clock time the sample was taken. The aggregator uses
// At.Truncate(time.Minute).Format(RFC3339Nano) as the bucket Window key.
type UsageSample struct {
	APIKeyPrefix string
	AgentSlug    string
	SandboxImage string
	At           time.Time
	VCPUSeconds  float64
	GPUSeconds   float64
	BytesIn      int64
	BytesOut     int64
}

// UsageQuery filters Query results. Empty fields match all values. Since
// is inclusive; Until is exclusive. Zero-valued time means "no bound".
type UsageQuery struct {
	APIKeyPrefix string
	AgentSlug    string
	SandboxImage string
	Since        time.Time
	Until        time.Time
}

// UsageAggregator buckets UsageSamples per minute and per
// (api_key_prefix, agent_slug, sandbox_image). Live buckets stay in
// memory; Flush moves closed buckets to the backing store.
type UsageAggregator struct {
	store store.Store
	now   func() time.Time
	mu    sync.Mutex
	live  map[string]*UsagePayload // key: bucketKey
}

// NewUsageAggregator returns an aggregator backed by s. Nil s is permitted
// for callers that only want in-memory tallies (tests, Solo dev).
func NewUsageAggregator(s store.Store) *UsageAggregator {
	return &UsageAggregator{
		store: s,
		now:   time.Now,
		live:  map[string]*UsagePayload{},
	}
}

// Tick records one 100ms sample. Samples whose At lands in the same
// (key, agent, image, minute) bucket accumulate; values are added.
func (a *UsageAggregator) Tick(s UsageSample) {
	window := s.At.UTC().Truncate(time.Minute).Format(time.RFC3339Nano)
	k := bucketKey(s.APIKeyPrefix, s.AgentSlug, s.SandboxImage, window)
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.live[k]
	if !ok {
		p = &UsagePayload{
			APIKeyPrefix: s.APIKeyPrefix,
			AgentSlug:    s.AgentSlug,
			SandboxImage: s.SandboxImage,
			Window:       window,
		}
		a.live[k] = p
	}
	p.VCPUSeconds += s.VCPUSeconds
	p.GPUSeconds += s.GPUSeconds
	p.BytesIn += s.BytesIn
	p.BytesOut += s.BytesOut
}

// Flush moves every bucket whose Window is strictly before the current
// minute into the backing store. Live (current-minute) buckets remain in
// memory so subsequent Ticks keep accumulating.
//
// If the aggregator has no store, Flush is a no-op for closed buckets;
// they simply get dropped after being snapshotted. (This is fine for
// tests but not for production — callers must wire a real Store.)
func (a *UsageAggregator) Flush(ctx context.Context) error {
	currentMin := a.now().UTC().Truncate(time.Minute).Format(time.RFC3339Nano)
	a.mu.Lock()
	closed := make([]*UsagePayload, 0, len(a.live))
	for k, p := range a.live {
		if p.Window < currentMin {
			closed = append(closed, p)
			delete(a.live, k)
		}
	}
	a.mu.Unlock()

	if a.store == nil {
		return nil
	}
	for _, p := range closed {
		raw, err := json.Marshal(p)
		if err != nil {
			return fmt.Errorf("scope/usage: marshal: %w", err)
		}
		key := store.CompositeKeyString(p.Window, p.APIKeyPrefix, p.AgentSlug, p.SandboxImage)
		if err := a.store.Put(ctx, usageStoreNamespace, key, raw); err != nil {
			return fmt.Errorf("scope/usage: store put: %w", err)
		}
	}
	return nil
}

// Query returns every persisted UsagePayload that matches q, plus any
// in-memory buckets that match. Results are sorted by Window ascending,
// then by (APIKeyPrefix, AgentSlug, SandboxImage) for deterministic
// callers.
func (a *UsageAggregator) Query(ctx context.Context, q UsageQuery) ([]UsagePayload, error) {
	out := make([]UsagePayload, 0)
	// In-memory live buckets first.
	a.mu.Lock()
	for _, p := range a.live {
		if matchesQuery(*p, q) {
			out = append(out, *p)
		}
	}
	a.mu.Unlock()
	// Persisted buckets.
	if a.store != nil {
		pairs, err := a.store.List(ctx, usageStoreNamespace, nil)
		if err != nil {
			return nil, fmt.Errorf("scope/usage: list: %w", err)
		}
		for _, kv := range pairs {
			var p UsagePayload
			if err := json.Unmarshal(kv.Value, &p); err != nil {
				continue
			}
			if matchesQuery(p, q) {
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Window != out[j].Window {
			return out[i].Window < out[j].Window
		}
		if out[i].APIKeyPrefix != out[j].APIKeyPrefix {
			return out[i].APIKeyPrefix < out[j].APIKeyPrefix
		}
		if out[i].AgentSlug != out[j].AgentSlug {
			return out[i].AgentSlug < out[j].AgentSlug
		}
		return out[i].SandboxImage < out[j].SandboxImage
	})
	return out, nil
}

// SetNow overrides the time source. Test-only; real callers must not
// touch this.
func (a *UsageAggregator) SetNow(f func() time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.now = f
}

// bucketKey produces a stable map key for the in-memory live buckets.
// Format: window|api_key|agent|image — '|' is illegal in RFC3339 so no
// collision risk with Window text.
func bucketKey(api, agent, image, window string) string {
	return window + "|" + api + "|" + agent + "|" + image
}

func matchesQuery(p UsagePayload, q UsageQuery) bool {
	if q.APIKeyPrefix != "" && q.APIKeyPrefix != p.APIKeyPrefix {
		return false
	}
	if q.AgentSlug != "" && q.AgentSlug != p.AgentSlug {
		return false
	}
	if q.SandboxImage != "" && q.SandboxImage != p.SandboxImage {
		return false
	}
	// Time bounds compare against Window (RFC3339Nano). Since RFC3339 sorts
	// lexicographically iff zone is fixed (always UTC here), string compare
	// is correct.
	if !q.Since.IsZero() {
		bound := q.Since.UTC().Truncate(time.Minute).Format(time.RFC3339Nano)
		if p.Window < bound {
			return false
		}
	}
	if !q.Until.IsZero() {
		bound := q.Until.UTC().Truncate(time.Minute).Format(time.RFC3339Nano)
		if p.Window >= bound {
			return false
		}
	}
	return true
}
