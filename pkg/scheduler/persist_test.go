package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/scheduler"
	"github.com/soyaos/soyaos/pkg/store"
)

func openTempStore(t *testing.T) store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestPersist_SaveLoadRoundTrip(t *testing.T) {
	s := openTempStore(t)
	ctx := context.Background()

	jobs := []scheduler.Job{
		{ID: "newsbeam-daily", Cron: "0 9 * * *", IdempotencyKey: "newsbeam:{date}"},
		{ID: "compo-oneshot", RunAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)},
	}
	for _, j := range jobs {
		if err := scheduler.SavePersistent(ctx, s, j, scheduler.MissedFireSkip); err != nil {
			t.Fatalf("SavePersistent: %v", err)
		}
	}

	loaded, err := scheduler.LoadPersistent(ctx, s)
	if err != nil {
		t.Fatalf("LoadPersistent: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded = %d, want 2", len(loaded))
	}

	byID := map[string]scheduler.PersistedJob{}
	for _, j := range loaded {
		byID[j.ID] = j
	}
	if byID["newsbeam-daily"].Cron != "0 9 * * *" {
		t.Fatalf("Cron not round-tripped: %+v", byID["newsbeam-daily"])
	}
	if byID["newsbeam-daily"].MissedFire != string(scheduler.MissedFireSkip) {
		t.Fatalf("MissedFire = %q, want skip", byID["newsbeam-daily"].MissedFire)
	}
}

func TestPersist_DeleteIsNoOpOnMissing(t *testing.T) {
	s := openTempStore(t)
	if err := scheduler.DeletePersistent(context.Background(), s, "ghost"); err != nil {
		t.Fatalf("DeletePersistent(ghost): %v", err)
	}
}

func TestPersist_DeleteRemoves(t *testing.T) {
	s := openTempStore(t)
	ctx := context.Background()
	_ = scheduler.SavePersistent(ctx, s, scheduler.Job{ID: "x", Cron: "* * * * *"}, scheduler.MissedFireSkip)
	if err := scheduler.DeletePersistent(ctx, s, "x"); err != nil {
		t.Fatalf("DeletePersistent: %v", err)
	}
	loaded, _ := scheduler.LoadPersistent(ctx, s)
	if len(loaded) != 0 {
		t.Fatalf("after delete: %d jobs, want 0", len(loaded))
	}
}

func TestPersist_SaveRejectsNilStore(t *testing.T) {
	if err := scheduler.SavePersistent(context.Background(), nil, scheduler.Job{ID: "x", Cron: "* * * * *"}, scheduler.MissedFireSkip); err == nil {
		t.Fatal("SavePersistent(nil store) should error")
	}
}

func TestPersist_AcrossReopen(t *testing.T) {
	dir := t.TempDir()
	{
		s, err := store.Open(dir)
		if err != nil {
			t.Fatalf("Open#1: %v", err)
		}
		_ = scheduler.SavePersistent(context.Background(), s, scheduler.Job{
			ID: "newsbeam-daily", Cron: "0 9 * * *",
		}, scheduler.MissedFireBackfill)
		_ = s.Close()
	}
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open#2: %v", err)
	}
	defer s.Close()
	loaded, _ := scheduler.LoadPersistent(context.Background(), s)
	if len(loaded) != 1 || loaded[0].MissedFire != string(scheduler.MissedFireBackfill) {
		t.Fatalf("after reopen: %+v", loaded)
	}
}
