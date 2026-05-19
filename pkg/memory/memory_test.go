package memory_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/memory"
	"github.com/soyaos/soyaos/pkg/store"
)

func TestInMemory_PutGetDelete_RoundTrip(t *testing.T) {
	m := memory.NewInMemory()
	ctx := context.Background()
	if err := m.Put(ctx, memory.Entry{Scope: memory.ScopeAgent, Owner: "a1", Key: "k", Value: []byte("v")}); err != nil {
		t.Fatal(err)
	}
	got, err := m.Get(ctx, memory.ScopeAgent, "a1", "k")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "v" {
		t.Fatalf("Value = %q", got.Value)
	}
	if got.Sensitivity != memory.SensitivityLow {
		t.Fatalf("Sensitivity defaulted to %q, want low", got.Sensitivity)
	}
	if err := m.Delete(ctx, memory.ScopeAgent, "a1", "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(ctx, memory.ScopeAgent, "a1", "k"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("Get(deleted) = %v", err)
	}
}

func TestInMemory_TTLExpires(t *testing.T) {
	m := memory.NewInMemory()
	ctx := context.Background()
	_ = m.Put(ctx, memory.Entry{Scope: memory.ScopeUser, Owner: "u", Key: "k", Value: []byte("v"), TTL: 1 * time.Millisecond})
	time.Sleep(5 * time.Millisecond)
	if _, err := m.Get(ctx, memory.ScopeUser, "u", "k"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("Get(expired) = %v", err)
	}
}

func TestInMemory_ListFilterByScopeOwnerPrefix(t *testing.T) {
	m := memory.NewInMemory()
	ctx := context.Background()
	for _, kv := range []struct{ s memory.Scope; o, k string }{
		{memory.ScopeAgent, "a1", "task/1"},
		{memory.ScopeAgent, "a1", "task/2"},
		{memory.ScopeAgent, "a1", "log/1"},
		{memory.ScopeAgent, "a2", "task/1"},
		{memory.ScopeUser, "a1", "task/1"},
	} {
		_ = m.Put(ctx, memory.Entry{Scope: kv.s, Owner: kv.o, Key: kv.k, Value: []byte("x")})
	}
	got, err := m.List(ctx, memory.ScopeAgent, "a1", "task/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("List = %d, want 2", len(got))
	}
}

// --- StoreBacked tests ------------------------------------------------------

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

func TestStoreBacked_RoundTrip(t *testing.T) {
	m := memory.NewStoreBacked(openTempStore(t))
	ctx := context.Background()
	_ = m.Put(ctx, memory.Entry{Scope: memory.ScopeAgent, Owner: "estate-muse", Key: "topic/42", Value: []byte("starred")})
	got, err := m.Get(ctx, memory.ScopeAgent, "estate-muse", "topic/42")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "starred" {
		t.Fatalf("Value = %q", got.Value)
	}
}

func TestStoreBacked_PersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	{
		s, err := store.Open(dir)
		if err != nil {
			t.Fatalf("Open#1: %v", err)
		}
		m := memory.NewStoreBacked(s)
		_ = m.Put(context.Background(), memory.Entry{Scope: memory.ScopeAgent, Owner: "p", Key: "k", Value: []byte("persist")})
		_ = s.Close()
	}

	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open#2: %v", err)
	}
	defer s.Close()
	m := memory.NewStoreBacked(s)
	got, err := m.Get(context.Background(), memory.ScopeAgent, "p", "k")
	if err != nil {
		t.Fatalf("Get(after reopen) = %v", err)
	}
	if string(got.Value) != "persist" {
		t.Fatalf("Value = %q", got.Value)
	}
}

func TestStoreBacked_TTLExpires(t *testing.T) {
	m := memory.NewStoreBacked(openTempStore(t))
	_ = m.Put(context.Background(), memory.Entry{Scope: memory.ScopeAgent, Owner: "o", Key: "k", Value: []byte("v"), TTL: 1 * time.Millisecond})
	time.Sleep(5 * time.Millisecond)
	if _, err := m.Get(context.Background(), memory.ScopeAgent, "o", "k"); !errors.Is(err, memory.ErrNotFound) {
		t.Fatalf("Get(expired) = %v", err)
	}
}

func TestStoreBacked_ListFilters(t *testing.T) {
	m := memory.NewStoreBacked(openTempStore(t))
	ctx := context.Background()
	for _, kv := range []struct{ owner, key string }{
		{"a", "task/1"},
		{"a", "task/2"},
		{"a", "log/1"},
		{"b", "task/1"},
	} {
		_ = m.Put(ctx, memory.Entry{Scope: memory.ScopeAgent, Owner: kv.owner, Key: kv.key})
	}
	got, err := m.List(ctx, memory.ScopeAgent, "a", "task/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("List = %d, want 2", len(got))
	}
}

// Sanity test: compositeKey scheme survives a path-like owner string. If
// the encoding regressed to the old `|` separator the keys would collide.
func TestStoreBacked_OwnerWithPathSeparator(t *testing.T) {
	m := memory.NewStoreBacked(openTempStore(t))
	ctx := context.Background()
	_ = m.Put(ctx, memory.Entry{Scope: memory.ScopeAgent, Owner: "team/a", Key: "k", Value: []byte("A")})
	_ = m.Put(ctx, memory.Entry{Scope: memory.ScopeAgent, Owner: "team", Key: "/a/k", Value: []byte("B")})
	got, err := m.Get(ctx, memory.ScopeAgent, "team/a", "k")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "A" {
		t.Fatalf("Owner=team/a Key=k → %q, want A", got.Value)
	}
	got2, err := m.Get(ctx, memory.ScopeAgent, "team", "/a/k")
	if err != nil {
		t.Fatal(err)
	}
	if string(got2.Value) != "B" {
		t.Fatalf("Owner=team Key=/a/k → %q, want B", got2.Value)
	}
	_ = filepath.Separator // silence import
}
