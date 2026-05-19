package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/soyaos/soyaos/pkg/store"
)

func openTempStore(t *testing.T) (store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func TestStore_PutGetDelete_RoundTrip(t *testing.T) {
	s, _ := openTempStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, "ns", []byte("k1"), []byte("v1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, "ns", []byte("k1"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "v1" {
		t.Fatalf("Get value = %q, want v1", got)
	}
	if err := s.Delete(ctx, "ns", []byte("k1")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "ns", []byte("k1")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get(deleted) = %v, want ErrNotFound", err)
	}
}

func TestStore_GetUnknownNamespace_ReturnsNotFound(t *testing.T) {
	s, _ := openTempStore(t)
	if _, err := s.Get(context.Background(), "never-written", []byte("k")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get(empty ns) = %v, want ErrNotFound", err)
	}
}

func TestStore_List_RespectsPrefix(t *testing.T) {
	s, _ := openTempStore(t)
	ctx := context.Background()

	seed := map[string]string{
		"users/alice":   "1",
		"users/bob":     "2",
		"users/charlie": "3",
		"sessions/x":    "99",
	}
	for k, v := range seed {
		if err := s.Put(ctx, "ns", []byte(k), []byte(v)); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	all, err := s.List(ctx, "ns", nil)
	if err != nil {
		t.Fatalf("List(nil): %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("List(nil) returned %d, want 4", len(all))
	}

	users, err := s.List(ctx, "ns", []byte("users/"))
	if err != nil {
		t.Fatalf("List(prefix): %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("List(users/) returned %d, want 3", len(users))
	}
	for _, p := range users {
		if string(p.Key)[:6] != "users/" {
			t.Fatalf("List(users/) yielded non-matching key %q", p.Key)
		}
	}
}

func TestStore_PersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	{
		s, err := store.Open(dir)
		if err != nil {
			t.Fatalf("Open#1: %v", err)
		}
		if err := s.Put(context.Background(), "ns", []byte("survives"), []byte("yes")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	// Re-open the same directory simulating a process restart.
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open#2: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	got, err := s.Get(context.Background(), "ns", []byte("survives"))
	if err != nil {
		t.Fatalf("Get(after reopen): %v", err)
	}
	if string(got) != "yes" {
		t.Fatalf("Get(after reopen) = %q, want yes", got)
	}
}

func TestStore_PutRejectsEmptyKey(t *testing.T) {
	s, _ := openTempStore(t)
	if err := s.Put(context.Background(), "ns", nil, []byte("v")); err == nil {
		t.Fatal("Put(empty key) should error")
	}
}

func TestStore_PutRejectsEmptyNamespace(t *testing.T) {
	s, _ := openTempStore(t)
	if err := s.Put(context.Background(), "", []byte("k"), []byte("v")); err == nil {
		t.Fatal("Put(empty ns) should error")
	}
}

func TestCompositeKey_DistinguishesAmbiguousParts(t *testing.T) {
	// `["ab", "c"]` and `["a", "bc"]` would collide under a simple `|`
	// separator scheme. Length-prefixed encoding must keep them distinct.
	a := store.CompositeKey([]byte("ab"), []byte("c"))
	b := store.CompositeKey([]byte("a"), []byte("bc"))
	if string(a) == string(b) {
		t.Fatalf("CompositeKey collided: %x == %x", a, b)
	}
}

func TestCompositeKey_HandlesNULAndSpecialChars(t *testing.T) {
	// A naive `<scope>\0<owner>\0<key>` scheme breaks if a caller smuggles
	// a NUL byte into one part. Length prefixing has no such failure mode.
	a := store.CompositeKey([]byte("scope\x00with-nul"), []byte("owner"), []byte("k"))
	b := store.CompositeKey([]byte("scope"), []byte("with-nul\x00owner"), []byte("k"))
	if string(a) == string(b) {
		t.Fatalf("CompositeKey collided on NUL injection: %x == %x", a, b)
	}
}

func TestCompositeKeyString_EquivalentToBytesForm(t *testing.T) {
	a := store.CompositeKeyString("a", "bc", "def")
	b := store.CompositeKey([]byte("a"), []byte("bc"), []byte("def"))
	if string(a) != string(b) {
		t.Fatalf("CompositeKeyString diverged from CompositeKey: %x vs %x", a, b)
	}
}

func TestStore_OpenLockedFileSecondTime(t *testing.T) {
	dir := t.TempDir()
	s1, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open#1: %v", err)
	}
	defer s1.Close()

	// Opening a second handle while the first is still open should fail
	// fast (bbolt enforces an exclusive file lock; our 5s timeout means
	// this returns within a few seconds rather than hanging forever).
	if _, err := store.Open(dir); err == nil {
		t.Fatal("Open#2 should fail while #1 holds the lock")
	}

	_ = filepath.Base(dir) // silence unused warning when filepath dropped later
}
