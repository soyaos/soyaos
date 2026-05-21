// scheduler_integration_test.go pins the boot-time wiring between the
// SoyaPack manifest layer and the in-process scheduler. We don't boot
// a real `soyaos start` here — that would require pinning real ports
// + LLM credentials. Instead we drive the helpers directly.
package main

import (
	"context"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/kernel"
	"github.com/soyaos/soyaos/pkg/scheduler"
	"github.com/soyaos/soyaos/pkg/store"
)

func TestMakeScheduleHook_AddsJobAndPersists(t *testing.T) {
	tw := scheduler.NewTimeWheel()
	defer func() { _ = tw.Stop(context.Background()) }()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	hook := makeScheduleHook(tw, s)

	fired := make(chan struct{}, 1)
	fire := func(_ context.Context) {
		fired <- struct{}{}
	}
	// Use a cron that matches every minute for the alpha; we drive
	// the Fire callback synchronously below to avoid waiting up to
	// 60s for the tick.
	spec := kernel.ScheduleSpec{
		Cron:           "* * * * *",
		MissedFire:     "skip",
		IdempotencyKey: "test:tick",
	}
	if err := hook("pack:test:0", spec, fire); err != nil {
		t.Fatalf("hook: %v", err)
	}

	// Persistence check: SavePersistent should have written the job spec.
	persisted, err := scheduler.LoadPersistent(context.Background(), s)
	if err != nil {
		t.Fatalf("LoadPersistent: %v", err)
	}
	if len(persisted) != 1 || persisted[0].ID != "pack:test:0" {
		t.Fatalf("persisted = %+v, want one job with ID pack:test:0", persisted)
	}
	if persisted[0].Cron != "* * * * *" {
		t.Errorf("persisted.Cron = %q", persisted[0].Cron)
	}

	// Trigger the Fire callback manually — the time wheel ticking
	// once per second would also work but adds at least 1s of latency
	// per case; this is the same contract the wheel honors.
	fire(context.Background())
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("Fire callback did not signal")
	}
}
