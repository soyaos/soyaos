package store

import (
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
)

// ErrNotFound is returned by Get when the key is unknown. Use
// errors.Is(err, ErrNotFound) to detect — implementations may wrap it.
var ErrNotFound = errors.New("store: not found")

// Pair is one key/value pair yielded by List.
type Pair struct {
	Key   []byte
	Value []byte
}

// Store is the minimal byte-in / byte-out persistence contract. All
// implementations must be safe for concurrent use by independent goroutines.
type Store interface {
	// Get returns the value at (namespace, key). ErrNotFound if absent.
	Get(ctx context.Context, namespace string, key []byte) ([]byte, error)

	// Put writes value at (namespace, key). Implementations create the
	// namespace on demand.
	Put(ctx context.Context, namespace string, key []byte, value []byte) error

	// Delete removes (namespace, key). Missing keys are a no-op.
	Delete(ctx context.Context, namespace string, key []byte) error

	// List iterates the namespace and returns every key/value pair whose
	// key starts with the given prefix. A nil prefix matches all keys.
	List(ctx context.Context, namespace string, prefix []byte) ([]Pair, error)

	// Close releases resources. Subsequent calls return an error.
	Close() error
}

// Open returns the canonical Solo Store backed by Bolt at
// <dataDir>/soyaos.bolt. The directory is expected to exist; callers should
// MkdirAll before calling.
func Open(dataDir string) (Store, error) {
	return openBolt(filepath.Join(dataDir, "soyaos.bolt"))
}

// CompositeKey concatenates byte parts with a length-prefix so the encoded
// key is unambiguous regardless of the part contents. Use this when a
// caller wants a multi-segment key like `<scope>·<owner>·<entry-key>` that
// must be injection-safe (i.e. no separator character a caller could smuggle
// in to collide with another tenant's keyspace).
//
// Format: each part = `uvarint(len(part)) || part`, concatenated in order.
func CompositeKey(parts ...[]byte) []byte {
	total := 0
	hdr := make([]byte, binary.MaxVarintLen64)
	for _, p := range parts {
		n := binary.PutUvarint(hdr, uint64(len(p)))
		total += n + len(p)
	}
	out := make([]byte, 0, total)
	for _, p := range parts {
		n := binary.PutUvarint(hdr, uint64(len(p)))
		out = append(out, hdr[:n]...)
		out = append(out, p...)
	}
	return out
}

// CompositeKeyString is a convenience for callers that have string parts.
// It is exactly equivalent to converting each part to []byte first.
func CompositeKeyString(parts ...string) []byte {
	bparts := make([][]byte, len(parts))
	for i, p := range parts {
		bparts[i] = []byte(p)
	}
	return CompositeKey(bparts...)
}
