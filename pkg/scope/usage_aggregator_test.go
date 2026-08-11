package scope

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/store"
)

func TestUsageAggregator_TickAccumulatesSameMinute(t *testing.T) {
	a := NewUsageAggregator(nil)
	t0 := time.Date(2025, 5, 19, 10, 15, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		a.Tick(UsageSample{
			APIKeyPrefix: "sk_abc",
			AgentSlug:    "silentcut",
			SandboxImage: "video-base@0.1.0",
			At:           t0.Add(time.Duration(i) * 100 * time.Millisecond),
			VCPUSeconds:  0.1,
			BytesOut:     1024,
		})
	}
	got, err := a.Query(context.Background(), UsageQuery{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1 (all samples in same minute bucket)", len(got))
	}
	if got[0].VCPUSeconds < 0.49 || got[0].VCPUSeconds > 0.51 {
		t.Errorf("VCPUSeconds=%v, want ~0.5", got[0].VCPUSeconds)
	}
	if got[0].BytesOut != 5*1024 {
		t.Errorf("BytesOut=%d, want %d", got[0].BytesOut, 5*1024)
	}
}

func TestUsageAggregator_FlushPersistsClosedBuckets(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	a := NewUsageAggregator(s)
	// Pin "now" so Flush considers prior-minute buckets closed.
	now := time.Date(2025, 5, 19, 10, 16, 0, 0, time.UTC)
	a.SetNow(func() time.Time { return now })

	// Sample lands in the 10:15 bucket (closed when "now" is 10:16).
	a.Tick(UsageSample{
		APIKeyPrefix: "sk_abc",
		AgentSlug:    "silentcut",
		SandboxImage: "video-base@0.1.0",
		At:           time.Date(2025, 5, 19, 10, 15, 30, 0, time.UTC),
		VCPUSeconds:  1.0,
		BytesOut:     2048,
	})
	if err := a.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// After Flush, live should be empty; the value must be readable via Query.
	got, err := a.Query(context.Background(), UsageQuery{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Query returned %d rows, want 1", len(got))
	}
	if got[0].APIKeyPrefix != "sk_abc" || got[0].AgentSlug != "silentcut" {
		t.Errorf("payload identity wrong: %+v", got[0])
	}
	if got[0].VCPUSeconds != 1.0 {
		t.Errorf("VCPUSeconds=%v, want 1.0", got[0].VCPUSeconds)
	}
	_ = filepath.Join // keep import live for future Bolt-path assertions
}

func TestUsageAggregator_QueryFiltersByKeyAndAgent(t *testing.T) {
	a := NewUsageAggregator(nil)
	t0 := time.Date(2025, 5, 19, 10, 15, 0, 0, time.UTC)
	a.Tick(UsageSample{APIKeyPrefix: "sk_a", AgentSlug: "silentcut", SandboxImage: "video-base@0.1.0", At: t0, VCPUSeconds: 1})
	a.Tick(UsageSample{APIKeyPrefix: "sk_b", AgentSlug: "silentcut", SandboxImage: "video-base@0.1.0", At: t0, VCPUSeconds: 1})
	a.Tick(UsageSample{APIKeyPrefix: "sk_a", AgentSlug: "other", SandboxImage: "video-base@0.1.0", At: t0, VCPUSeconds: 1})

	got, err := a.Query(context.Background(), UsageQuery{APIKeyPrefix: "sk_a", AgentSlug: "silentcut"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one (sk_a, silentcut) bucket, got %d", len(got))
	}
}

func TestUsageAggregator_FlushKeepsCurrentMinuteLive(t *testing.T) {
	a := NewUsageAggregator(nil)
	now := time.Date(2025, 5, 19, 10, 15, 30, 0, time.UTC)
	a.SetNow(func() time.Time { return now })
	a.Tick(UsageSample{APIKeyPrefix: "sk_a", AgentSlug: "x", SandboxImage: "y", At: now, VCPUSeconds: 0.5})
	if err := a.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// The live bucket should still be queryable.
	got, _ := a.Query(context.Background(), UsageQuery{})
	if len(got) != 1 {
		t.Fatalf("got %d rows after flushing current-minute bucket, want 1", len(got))
	}
}
