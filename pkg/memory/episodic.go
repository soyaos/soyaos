// episodic.go layers the SoyaMemory Episodic store on top of pkg/store
// (EstateMuse Aha #4: "Agents remember what they did, per scope+owner,
// in time order"). DD-010 distinguishes user-scoped memory (the regular
// KV) from agent-scoped state — Episodic is the chronological log axis
// that complements both: Append(scope, owner, event), Recent(scope,
// owner, n).
//
// Keys are stored as `episodic/<scope>/<owner>/<unix-nano>` so a single
// prefix scan on (scope, owner) yields events in insertion order. Recent
// reverses the scan and returns the last n entries newest-first.
package memory

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/soyaos/soyaos/pkg/store"
)

// EpisodicNamespace is the bbolt bucket name used by BoltEpisodic.
const EpisodicNamespace = "memory.episodic"

// Event is one record in the episodic log.
type Event struct {
	Timestamp time.Time `json:"timestamp"`
	Payload   []byte    `json:"payload"`
}

// Episodic is the contract for the chronological log axis.
type Episodic interface {
	Append(ctx context.Context, scope Scope, owner string, event Event) error
	Recent(ctx context.Context, scope Scope, owner string, n int) ([]Event, error)
}

// nowFunc is overridable in tests so two Appends in the same nanosecond
// can be unit-tested deterministically. Production code calls time.Now.
var nowFunc = time.Now

// BoltEpisodic is the store-backed Episodic implementation. Each event
// occupies a unique key under namespace `memory.episodic`, composed as
// `<scope>\x00<owner>\x00<be-uint64 unix nano>`. The trailing uint64 is
// big-endian so bytewise sort matches chronological order.
type BoltEpisodic struct {
	store store.Store
	mu    sync.Mutex
	// nextNano guarantees monotonicity even when two appends hit the
	// same nanosecond on machines whose clock granularity is coarser
	// than ns. We only advance forward; we never go back.
	lastNano int64
}

// NewBoltEpisodic returns an Episodic backed by s.
func NewBoltEpisodic(s store.Store) *BoltEpisodic { return &BoltEpisodic{store: s} }

// Append writes one event under (scope, owner). Timestamp is filled with
// nowFunc when zero. Two appends in the same nanosecond are sequenced
// by bumping the embedded clock so the byte-sort still reflects insert
// order.
func (b *BoltEpisodic) Append(ctx context.Context, scope Scope, owner string, ev Event) error {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = nowFunc()
	}
	b.mu.Lock()
	nano := ev.Timestamp.UnixNano()
	if nano <= b.lastNano {
		nano = b.lastNano + 1
		ev.Timestamp = time.Unix(0, nano)
	}
	b.lastNano = nano
	b.mu.Unlock()

	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("memory: encode event: %w", err)
	}
	return b.store.Put(ctx, EpisodicNamespace, episodicKey(scope, owner, nano), body)
}

// Recent returns the last n events under (scope, owner), newest-first.
// n<=0 returns an empty slice.
func (b *BoltEpisodic) Recent(ctx context.Context, scope Scope, owner string, n int) ([]Event, error) {
	if n <= 0 {
		return []Event{}, nil
	}
	prefix := episodicPrefix(scope, owner)
	pairs, err := b.store.List(ctx, EpisodicNamespace, prefix)
	if err != nil {
		return nil, err
	}
	// pairs are returned in key-sorted (ascending nanos) order; sort
	// defensively in case a backend changes ordering rules.
	sort.Slice(pairs, func(i, j int) bool {
		return decodeNano(pairs[i].Key, len(prefix)) < decodeNano(pairs[j].Key, len(prefix))
	})
	// Take the tail n, then reverse for newest-first.
	if len(pairs) > n {
		pairs = pairs[len(pairs)-n:]
	}
	out := make([]Event, 0, len(pairs))
	for i := len(pairs) - 1; i >= 0; i-- {
		var ev Event
		if err := json.Unmarshal(pairs[i].Value, &ev); err != nil {
			// Skip individually-malformed records; the log is append-only
			// elsewhere so this is real corruption, not normal flow.
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

// episodicKey is the storage key for one event.
func episodicKey(scope Scope, owner string, nano int64) []byte {
	prefix := episodicPrefix(scope, owner)
	out := make([]byte, 0, len(prefix)+8)
	out = append(out, prefix...)
	var nbuf [8]byte
	binary.BigEndian.PutUint64(nbuf[:], uint64(nano))
	out = append(out, nbuf[:]...)
	return out
}

// episodicPrefix is the (scope, owner) prefix shared by every event for
// the pair. NUL bytes separate fields so neither scope nor owner can
// tunnel into another tenant's keyspace.
func episodicPrefix(scope Scope, owner string) []byte {
	return []byte(string(scope) + "\x00" + owner + "\x00")
}

// decodeNano extracts the trailing big-endian uint64 (interpreted as
// int64) from an event key.
func decodeNano(key []byte, prefixLen int) int64 {
	if len(key) < prefixLen+8 {
		// Should not happen, but if a non-Episodic record slipped in
		// we sort it to the front so callers see something stable.
		v, _ := strconv.ParseInt(string(key), 10, 64)
		return v
	}
	return int64(binary.BigEndian.Uint64(key[prefixLen:]))
}
