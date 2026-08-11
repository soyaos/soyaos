// state_test.go covers the BoltStore round-trip, MVCC CompareAndSwap
// (happy + conflict), prefix List, and ErrNotFound semantics. These are
// the contract guarantees the Stateful Agent runtime (EstateMuse and
// any later per-row agents) relies on (APP-501).
package state_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/soyaos/soyaos/pkg/state"
	"github.com/soyaos/soyaos/pkg/store"
)

func openTempStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestBoltStore_GetPut_RoundTrip(t *testing.T) {
	b := state.NewBoltStore(openTempStore(t))
	ctx := context.Background()

	if _, err := b.Get(ctx, state.ScopeRow, "row-42", "tier"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("Get(missing) = %v, want ErrNotFound", err)
	}

	put, err := b.Put(ctx, state.ScopeRow, "row-42", "tier", []byte("A"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if put.Version != 1 {
		t.Fatalf("first Put Version = %d, want 1", put.Version)
	}

	got, err := b.Get(ctx, state.ScopeRow, "row-42", "tier")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Value) != "A" {
		t.Fatalf("Value = %q, want A", got.Value)
	}
	if got.Version != 1 {
		t.Fatalf("Version = %d, want 1", got.Version)
	}

	put2, err := b.Put(ctx, state.ScopeRow, "row-42", "tier", []byte("B"))
	if err != nil {
		t.Fatalf("Put#2: %v", err)
	}
	if put2.Version != 2 {
		t.Fatalf("second Put Version = %d, want 2", put2.Version)
	}
}

func TestBoltStore_CompareAndSwap_Happy(t *testing.T) {
	b := state.NewBoltStore(openTempStore(t))
	ctx := context.Background()

	// Initial insert via CAS with baseVersion=0.
	cur, err := b.CompareAndSwap(ctx, state.ScopeAgent, "estatemuse", "selected_topic", 0, []byte("AI rules"))
	if err != nil {
		t.Fatalf("CAS#1: %v", err)
	}
	if cur.Version != 1 {
		t.Fatalf("CAS#1 Version = %d, want 1", cur.Version)
	}

	// Subsequent update with correct base version.
	cur2, err := b.CompareAndSwap(ctx, state.ScopeAgent, "estatemuse", "selected_topic", cur.Version, []byte("Verge story"))
	if err != nil {
		t.Fatalf("CAS#2: %v", err)
	}
	if cur2.Version != 2 || string(cur2.Value) != "Verge story" {
		t.Fatalf("CAS#2 = %+v", cur2)
	}
}

func TestBoltStore_CompareAndSwap_Conflict(t *testing.T) {
	b := state.NewBoltStore(openTempStore(t))
	ctx := context.Background()

	if _, err := b.Put(ctx, state.ScopeRow, "r1", "k", []byte("v1")); err != nil {
		t.Fatalf("Put seed: %v", err)
	}

	// Stale base version (0) when entry already exists → conflict.
	if _, err := b.CompareAndSwap(ctx, state.ScopeRow, "r1", "k", 0, []byte("v2")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("CAS(staleBase=0) = %v, want ErrConflict", err)
	}

	// Wrong base version (5 when actual=1) → conflict.
	if _, err := b.CompareAndSwap(ctx, state.ScopeRow, "r1", "k", 5, []byte("v3")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("CAS(wrongBase=5) = %v, want ErrConflict", err)
	}

	// Confirm stored value never changed.
	got, _ := b.Get(ctx, state.ScopeRow, "r1", "k")
	if string(got.Value) != "v1" {
		t.Fatalf("Value after conflicts = %q, want v1", got.Value)
	}
	if got.Version != 1 {
		t.Fatalf("Version after conflicts = %d, want 1", got.Version)
	}
}

func TestBoltStore_CompareAndSwap_InitialInsertRejectsExistingKey(t *testing.T) {
	b := state.NewBoltStore(openTempStore(t))
	ctx := context.Background()
	if _, err := b.Put(ctx, state.ScopeUser, "u1", "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	// baseVersion=0 means "expect not exist"; we already inserted → conflict.
	if _, err := b.CompareAndSwap(ctx, state.ScopeUser, "u1", "k", 0, []byte("v2")); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("CAS(base=0, key exists) = %v, want ErrConflict", err)
	}
}

func TestBoltStore_CompareAndSwap_ConcurrentRetryLosesNoUpdates(t *testing.T) {
	b := state.NewBoltStore(openTempStore(t))
	ctx := context.Background()

	const writers = 16
	const bumpsPerWriter = 10

	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range bumpsPerWriter {
				for {
					cur, err := b.Get(ctx, state.ScopeAgent, "estatemuse", "generated_total")
					var base int64
					value := 0
					if err == nil {
						base = cur.Version
						if _, err := fmt.Sscanf(string(cur.Value), "%d", &value); err != nil {
							errs <- fmt.Errorf("parse counter: %w", err)
							return
						}
					} else if !errors.Is(err, state.ErrNotFound) {
						errs <- fmt.Errorf("get counter: %w", err)
						return
					}

					_, err = b.CompareAndSwap(ctx, state.ScopeAgent, "estatemuse", "generated_total", base, fmt.Appendf(nil, "%d", value+1))
					if err == nil {
						break
					}
					if !errors.Is(err, state.ErrConflict) {
						errs <- fmt.Errorf("compare and swap: %w", err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		return
	}

	final, err := b.Get(ctx, state.ScopeAgent, "estatemuse", "generated_total")
	if err != nil {
		t.Fatalf("final Get: %v", err)
	}
	want := writers * bumpsPerWriter
	if string(final.Value) != fmt.Sprint(want) {
		t.Fatalf("counter = %s, want %d", final.Value, want)
	}
	if final.Version != int64(want) {
		t.Fatalf("version = %d, want %d", final.Version, want)
	}
}

func TestBoltStore_PutConcurrentVersionBumpsAreAtomic(t *testing.T) {
	b := state.NewBoltStore(openTempStore(t))
	ctx := context.Background()

	const writers = 32
	versions := make(chan int64, writers)
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for writer := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry, err := b.Put(ctx, state.ScopeRow, "row-42", "tier", fmt.Appendf(nil, "%d", writer))
			if err != nil {
				errs <- err
				return
			}
			versions <- entry.Version
		}()
	}
	wg.Wait()
	close(errs)
	close(versions)
	for err := range errs {
		t.Errorf("Put: %v", err)
	}

	seen := make(map[int64]bool, writers)
	for version := range versions {
		seen[version] = true
	}
	for version := int64(1); version <= writers; version++ {
		if !seen[version] {
			t.Errorf("version %d was not assigned; got %v", version, seen)
		}
	}
	final, err := b.Get(ctx, state.ScopeRow, "row-42", "tier")
	if err != nil {
		t.Fatalf("final Get: %v", err)
	}
	if final.Version != writers {
		t.Fatalf("final version = %d, want %d", final.Version, writers)
	}
}

func TestBoltStore_List_PrefixAndScopeFilter(t *testing.T) {
	b := state.NewBoltStore(openTempStore(t))
	ctx := context.Background()
	entries := []struct {
		scope state.Scope
		owner string
		key   string
	}{
		{state.ScopeRow, "r1", "tier/A"},
		{state.ScopeRow, "r1", "tier/B"},
		{state.ScopeRow, "r1", "tag/foo"},
		{state.ScopeRow, "r2", "tier/A"},
		{state.ScopeAgent, "r1", "tier/A"},
	}
	for _, e := range entries {
		if _, err := b.Put(ctx, e.scope, e.owner, e.key, []byte("x")); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	got, err := b.List(ctx, state.ScopeRow, "r1", "tier/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("List(scope=row owner=r1 prefix=tier/) = %d entries, want 2: %+v", len(got), got)
	}
}

func TestBoltStore_Delete(t *testing.T) {
	b := state.NewBoltStore(openTempStore(t))
	ctx := context.Background()
	_, _ = b.Put(ctx, state.ScopeAgent, "a", "k", []byte("v"))
	if err := b.Delete(ctx, state.ScopeAgent, "a", "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, state.ScopeAgent, "a", "k"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("after Delete: %v, want ErrNotFound", err)
	}
}

func TestBoltStore_RejectsUnknownScope(t *testing.T) {
	b := state.NewBoltStore(openTempStore(t))
	if _, err := b.Get(context.Background(), state.Scope("bogus"), "x", "k"); !errors.Is(err, state.ErrInvalidScope) {
		t.Fatalf("Get(bogus scope) = %v, want ErrInvalidScope", err)
	}
}
