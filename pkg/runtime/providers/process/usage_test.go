package process

import (
	"context"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/runtime"
	"github.com/soyaos/soyaos/pkg/scope"
)

func TestProvider_WithUsage_TicksAggregator(t *testing.T) {
	agg := scope.NewUsageAggregator(nil)
	p := New().WithUsage(agg)
	ctx := context.Background()
	h, _ := p.Provision(ctx, runtime.ProvisionRequest{
		Image: "video-base@0.1.0",
		Caps:  runtime.Caps{Exec: []string{"sh"}},
	})
	if err := p.LabelHandle(h, "sk_abc", "silentcut"); err != nil {
		t.Fatalf("LabelHandle: %v", err)
	}
	// Sleep long enough for at least two 100ms ticks.
	if _, err := p.Execute(ctx, h, runtime.ExecuteRequest{Cmd: []string{"sh", "-c", "sleep 0.3"}, Access: &runtime.Access{}}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	rows, err := agg.Query(ctx, scope.UsageQuery{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("aggregator received no samples")
	}
	// Total should be > 0.2s vCPU since the child ran ~300ms.
	var total float64
	for _, r := range rows {
		total += r.VCPUSeconds
		if r.APIKeyPrefix != "sk_abc" || r.AgentSlug != "silentcut" || r.SandboxImage != "video-base@0.1.0" {
			t.Errorf("row labels wrong: %+v", r)
		}
	}
	if total < 0.2 {
		t.Errorf("total VCPUSeconds=%v, want > 0.2 (child ran ~300ms)", total)
	}
	// Sanity: aggregator never sees a negative tick.
	for _, r := range rows {
		if r.VCPUSeconds < 0 {
			t.Errorf("negative VCPUSeconds: %v", r.VCPUSeconds)
		}
	}
	_ = time.Now // keep import
}
