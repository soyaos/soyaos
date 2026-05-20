// episodic_test.go pins the Episodic Append/Recent contract: events
// come out newest-first, only the requested tail size is returned,
// monotonic ordering survives identical-nano appends, and the (scope,
// owner) key isolates tenants. APP-505.
package memory_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/memory"
)

func TestEpisodic_AppendRecent_NewestFirstTail(t *testing.T) {
	e := memory.NewBoltEpisodic(openTempStore(t))
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := e.Append(ctx, memory.ScopeAgent, "estate-muse", memory.Event{
			Payload: []byte("event-" + strconv.Itoa(i)),
		}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	got, err := e.Recent(ctx, memory.ScopeAgent, "estate-muse", 5)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}
	// Expect newest first: event-9, event-8, ... event-5.
	for i, ev := range got {
		want := "event-" + strconv.Itoa(9-i)
		if string(ev.Payload) != want {
			t.Fatalf("Recent[%d].Payload = %q, want %q", i, ev.Payload, want)
		}
	}
	// Timestamps must be strictly decreasing across the returned slice.
	for i := 1; i < len(got); i++ {
		if !got[i-1].Timestamp.After(got[i].Timestamp) {
			t.Fatalf("Recent not newest-first at i=%d: %v then %v", i, got[i-1].Timestamp, got[i].Timestamp)
		}
	}
}

func TestEpisodic_Recent_ReturnsEmptySliceForUnknownOwner(t *testing.T) {
	e := memory.NewBoltEpisodic(openTempStore(t))
	got, err := e.Recent(context.Background(), memory.ScopeUser, "nobody", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %d", len(got))
	}
}

func TestEpisodic_OwnerIsolation(t *testing.T) {
	e := memory.NewBoltEpisodic(openTempStore(t))
	ctx := context.Background()
	_ = e.Append(ctx, memory.ScopeUser, "alice", memory.Event{Payload: []byte("a1")})
	_ = e.Append(ctx, memory.ScopeUser, "alice", memory.Event{Payload: []byte("a2")})
	_ = e.Append(ctx, memory.ScopeUser, "bob", memory.Event{Payload: []byte("b1")})

	alice, _ := e.Recent(ctx, memory.ScopeUser, "alice", 10)
	if len(alice) != 2 {
		t.Fatalf("alice events = %d, want 2", len(alice))
	}
	bob, _ := e.Recent(ctx, memory.ScopeUser, "bob", 10)
	if len(bob) != 1 || string(bob[0].Payload) != "b1" {
		t.Fatalf("bob events = %+v, want [b1]", bob)
	}
}

func TestEpisodic_MonotonicEvenOnSameNano(t *testing.T) {
	e := memory.NewBoltEpisodic(openTempStore(t))
	ctx := context.Background()
	// Force three events to share the same timestamp.
	now := time.Now()
	for i := 0; i < 3; i++ {
		if err := e.Append(ctx, memory.ScopeAgent, "a", memory.Event{
			Timestamp: now,
			Payload:   []byte("e" + strconv.Itoa(i)),
		}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	got, err := e.Recent(ctx, memory.ScopeAgent, "a", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// Newest-first ordering must still hold; payloads inserted last
	// must come back first.
	if string(got[0].Payload) != "e2" || string(got[2].Payload) != "e0" {
		t.Fatalf("ordering wrong: %+v", got)
	}
}

func TestEpisodic_RecentNonPositiveN(t *testing.T) {
	e := memory.NewBoltEpisodic(openTempStore(t))
	got, err := e.Recent(context.Background(), memory.ScopeAgent, "a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty for n=0")
	}
	got, err = e.Recent(context.Background(), memory.ScopeAgent, "a", -3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty for n<0")
	}
}

// Sanity test: KV already has List + TTL fields (APP-461). Re-assert
// it here so APP-505's contract surface is checked end-to-end in one
// file.
func TestKV_HasListWithTTL(t *testing.T) {
	m := memory.NewInMemory()
	ctx := context.Background()
	_ = m.Put(ctx, memory.Entry{Scope: memory.ScopeUser, Owner: "u", Key: "p/a", Value: []byte("1"), TTL: time.Hour})
	_ = m.Put(ctx, memory.Entry{Scope: memory.ScopeUser, Owner: "u", Key: "p/b", Value: []byte("2")})
	got, err := m.List(ctx, memory.ScopeUser, "u", "p/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("List = %d, want 2", len(got))
	}
}
