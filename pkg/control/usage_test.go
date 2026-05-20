package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/kernel"
	"github.com/soyaos/soyaos/pkg/scope"
)

func TestUsage_EmptyWhenNoAggregator(t *testing.T) {
	srv := httptest.NewServer(NewServer(kernel.New()).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/control/v0/usage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var out usageResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Object != "list" || len(out.Data) != 0 {
		t.Errorf("expected empty list, got %+v", out)
	}
}

func TestUsage_ReturnsRowsFromAggregator(t *testing.T) {
	agg := scope.NewUsageAggregator(nil)
	now := time.Now().UTC()
	agg.Tick(scope.UsageSample{
		APIKeyPrefix: "sk_abc",
		AgentSlug:    "silentcut",
		SandboxImage: "video-base@0.1.0",
		At:           now,
		VCPUSeconds:  1.5,
		BytesOut:     1024,
	})

	srv := httptest.NewServer(NewServer(kernel.New()).WithUsage(agg).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/control/v0/usage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var out usageResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Data) != 1 {
		t.Fatalf("got %d rows, want 1", len(out.Data))
	}
	if out.Data[0].APIKeyPrefix != "sk_abc" || out.Data[0].VCPUSeconds != 1.5 {
		t.Errorf("row content wrong: %+v", out.Data[0])
	}
}

func TestUsage_RejectsBadWindow(t *testing.T) {
	srv := httptest.NewServer(NewServer(kernel.New()).WithUsage(scope.NewUsageAggregator(nil)).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/control/v0/usage?window=lol")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
}

func TestUsage_FiltersByAgent(t *testing.T) {
	agg := scope.NewUsageAggregator(nil)
	now := time.Now().UTC()
	agg.Tick(scope.UsageSample{APIKeyPrefix: "sk_a", AgentSlug: "silentcut", SandboxImage: "video-base@0.1.0", At: now, VCPUSeconds: 1})
	agg.Tick(scope.UsageSample{APIKeyPrefix: "sk_a", AgentSlug: "other", SandboxImage: "video-base@0.1.0", At: now, VCPUSeconds: 1})

	srv := httptest.NewServer(NewServer(kernel.New()).WithUsage(agg).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/control/v0/usage?agent_slug=silentcut")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var out usageResp
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Data) != 1 {
		t.Fatalf("got %d rows, want 1 (filter by agent_slug=silentcut)", len(out.Data))
	}
}

func TestUsage_RejectsNonGet(t *testing.T) {
	srv := httptest.NewServer(NewServer(kernel.New()).Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/control/v0/usage", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status=%d, want 405", resp.StatusCode)
	}
}
