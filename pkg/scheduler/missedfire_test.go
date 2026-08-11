// missedfire_test.go pins the three missed-fire policies (skip / once /
// backfill) and the idempotency-key dedup window against regression.
// All tests use a deterministic fake clock and the bbolt-backed store
// so the on-disk shape stays under test alongside the in-memory logic.
package scheduler_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/scheduler"
	"github.com/soyaos/soyaos/pkg/store"
)

// helper to make a resolver that hands back the same Fire for any ID.
func staticResolver(fire scheduler.Fire) scheduler.HandlerFor {
	return func(_ scheduler.PersistedJob) (scheduler.Fire, bool) { return fire, true }
}

func TestLoadFromStore_SkipPolicy_NoBackfill(t *testing.T) {
	s := openTempStore(t)
	ctx := context.Background()

	// Persist a daily-9am job whose last run was 3 days ago.
	last := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	j := scheduler.Job{
		ID:          "skip-job",
		Cron:        "0 9 * * *",
		LastFiredAt: last,
	}
	if err := scheduler.SavePersistent(ctx, s, j, scheduler.MissedFireSkip); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var fires int32
	tw := scheduler.NewTimeWheel()
	defer tw.Stop(ctx)

	now := time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC)
	err := scheduler.LoadFromStore(ctx, s, tw, staticResolver(func(context.Context) {
		atomic.AddInt32(&fires, 1)
	}), now)
	if err != nil {
		t.Fatalf("LoadFromStore: %v", err)
	}
	if got := atomic.LoadInt32(&fires); got != 0 {
		t.Errorf("skip policy fired %d times, want 0", got)
	}
}

func TestLoadFromStore_OncePolicy_FiresExactlyOnce(t *testing.T) {
	s := openTempStore(t)
	ctx := context.Background()

	last := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	if err := scheduler.SavePersistent(ctx, s, scheduler.Job{
		ID: "once-job", Cron: "0 9 * * *", LastFiredAt: last,
	}, scheduler.MissedFireOnce); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var fires int32
	tw := scheduler.NewTimeWheel()
	defer tw.Stop(ctx)

	now := time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC)
	err := scheduler.LoadFromStore(ctx, s, tw, staticResolver(func(context.Context) {
		atomic.AddInt32(&fires, 1)
	}), now)
	if err != nil {
		t.Fatalf("LoadFromStore: %v", err)
	}
	if got := atomic.LoadInt32(&fires); got != 1 {
		t.Fatalf("once policy fired %d times, want 1", got)
	}
}

func TestLoadFromStore_BackfillPolicy_FiresEveryMissed(t *testing.T) {
	s := openTempStore(t)
	ctx := context.Background()

	// LastFiredAt = 3 days ago at 9:01 (one minute past the last fire).
	// Daily 0 9 * * * → 3 missed firings (today, yesterday, day-before).
	last := time.Date(2026, 5, 16, 9, 1, 0, 0, time.UTC)
	if err := scheduler.SavePersistent(ctx, s, scheduler.Job{
		ID: "backfill-job", Cron: "0 9 * * *", LastFiredAt: last,
	}, scheduler.MissedFireBackfill); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var fires int32
	tw := scheduler.NewTimeWheel()
	defer tw.Stop(ctx)

	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	err := scheduler.LoadFromStore(ctx, s, tw, staticResolver(func(context.Context) {
		atomic.AddInt32(&fires, 1)
	}), now)
	if err != nil {
		t.Fatalf("LoadFromStore: %v", err)
	}
	if got := atomic.LoadInt32(&fires); got != 3 {
		t.Errorf("backfill policy fired %d times, want 3", got)
	}
}

func TestLoadFromStore_BackfillPolicy_RateLimited(t *testing.T) {
	s := openTempStore(t)
	ctx := context.Background()

	// 60 missed minute matches in one hour → with 10/s cap should take
	// roughly 5s (gap = 100ms between fires).
	last := time.Date(2026, 5, 19, 8, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 19, 9, 0, 0, 0, time.UTC)
	if err := scheduler.SavePersistent(ctx, s, scheduler.Job{
		ID: "rl-job", Cron: "* * * * *", LastFiredAt: last,
	}, scheduler.MissedFireBackfill); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var fires int32
	tw := scheduler.NewTimeWheel()
	defer tw.Stop(ctx)

	start := time.Now()
	// Bound by ctx so the test fails fast if the rate limiter is broken.
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := scheduler.LoadFromStore(cctx, s, tw, staticResolver(func(context.Context) {
		atomic.AddInt32(&fires, 1)
	}), now); err != nil {
		t.Fatalf("LoadFromStore: %v", err)
	}
	elapsed := time.Since(start)
	if got := atomic.LoadInt32(&fires); got != 60 {
		t.Errorf("backfill fired %d times, want 60", got)
	}
	// 60 fires at 10/s → minimum ~5.9s of sleeping (gaps between 59).
	if elapsed < 5*time.Second {
		t.Errorf("backfill took %s; expected ≥5s due to rate limit", elapsed)
	}
}

func TestLoadFromStore_IdempotencyKey_DedupsWithinWindow(t *testing.T) {
	s := openTempStore(t)
	ctx := context.Background()

	last := time.Date(2026, 5, 16, 9, 1, 0, 0, time.UTC)
	if err := scheduler.SavePersistent(ctx, s, scheduler.Job{
		ID:             "idemp-job",
		Cron:           "0 9 * * *",
		LastFiredAt:    last,
		IdempotencyKey: "newsbeam:daily",
	}, scheduler.MissedFireBackfill); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var fires int32
	tw := scheduler.NewTimeWheel()
	defer tw.Stop(ctx)

	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	resolver := staticResolver(func(context.Context) { atomic.AddInt32(&fires, 1) })

	if err := scheduler.LoadFromStore(ctx, s, tw, resolver, now); err != nil {
		t.Fatalf("LoadFromStore #1: %v", err)
	}
	first := atomic.LoadInt32(&fires)
	if first == 0 {
		t.Fatal("first hydration should have fired at least once")
	}

	// Reset the persisted LastFiredAt back so the loader would otherwise
	// replay again, then re-hydrate. The dedup key should suppress all
	// re-fires inside the 24-hour window.
	if err := scheduler.SavePersistent(ctx, s, scheduler.Job{
		ID:             "idemp-job",
		Cron:           "0 9 * * *",
		LastFiredAt:    last,
		IdempotencyKey: "newsbeam:daily",
	}, scheduler.MissedFireBackfill); err != nil {
		t.Fatalf("re-Save: %v", err)
	}
	if err := scheduler.LoadFromStore(ctx, s, tw, resolver, now); err != nil {
		t.Fatalf("LoadFromStore #2: %v", err)
	}
	if got := atomic.LoadInt32(&fires); got != first {
		t.Errorf("dedup key should block re-fire; saw %d after first %d", got, first)
	}
}

func TestLoadFromStore_OneShot_SkipFiresOnceIfNeverFired(t *testing.T) {
	s := openTempStore(t)
	ctx := context.Background()

	past := time.Date(2026, 5, 19, 8, 0, 0, 0, time.UTC)
	if err := scheduler.SavePersistent(ctx, s, scheduler.Job{
		ID: "oneshot-job", RunAt: past,
	}, scheduler.MissedFireOnce); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var fires int32
	tw := scheduler.NewTimeWheel()
	defer tw.Stop(ctx)

	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	if err := scheduler.LoadFromStore(ctx, s, tw, staticResolver(func(context.Context) {
		atomic.AddInt32(&fires, 1)
	}), now); err != nil {
		t.Fatalf("LoadFromStore: %v", err)
	}
	if got := atomic.LoadInt32(&fires); got != 1 {
		t.Errorf("one-shot once policy fired %d times, want 1", got)
	}
}

func TestLoadFromStore_NilResolver_Errors(t *testing.T) {
	s := openTempStore(t)
	if err := scheduler.LoadFromStore(context.Background(), s, scheduler.NewTimeWheel(), nil, time.Now()); err == nil {
		t.Fatal("expected error on nil resolver")
	}
}

func TestLoadFromStore_UnknownHandler_Skipped(t *testing.T) {
	s := openTempStore(t)
	ctx := context.Background()
	if err := scheduler.SavePersistent(ctx, s, scheduler.Job{
		ID: "no-handler", Cron: "0 9 * * *",
	}, scheduler.MissedFireOnce); err != nil {
		t.Fatalf("Save: %v", err)
	}
	tw := scheduler.NewTimeWheel()
	defer tw.Stop(ctx)
	called := false
	resolver := func(_ scheduler.PersistedJob) (scheduler.Fire, bool) {
		called = true
		return nil, false
	}
	if err := scheduler.LoadFromStore(ctx, s, tw, resolver, time.Now()); err != nil {
		t.Fatalf("LoadFromStore: %v", err)
	}
	if !called {
		t.Error("resolver was never consulted")
	}
}

// silence unused-import linter — store is used transitively via openTempStore.
var _ = store.ErrNotFound
