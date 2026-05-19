package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

// boltStore is the Bolt-backed Store. Each namespace maps to a bbolt
// bucket; buckets are created lazily on first Put. Read operations on a
// non-existent bucket return ErrNotFound or empty results rather than
// surfacing the bolt-level "bucket not found" sentinel.
type boltStore struct {
	db *bolt.DB
}

// openBolt opens (or creates) a bolt database file at path with sensible
// Solo defaults. Implementation detail of Open(); not exported.
func openBolt(path string) (Store, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{
		Timeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("store: open bolt %s: %w", path, err)
	}
	return &boltStore{db: db}, nil
}

// Get implements Store.
func (s *boltStore) Get(_ context.Context, namespace string, key []byte) ([]byte, error) {
	var out []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(namespace))
		if b == nil {
			return ErrNotFound
		}
		v := b.Get(key)
		if v == nil {
			return ErrNotFound
		}
		// Copy: bolt's slice is only valid within the tx.
		out = append(out, v...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Put implements Store.
func (s *boltStore) Put(_ context.Context, namespace string, key []byte, value []byte) error {
	if namespace == "" {
		return errors.New("store: empty namespace")
	}
	if len(key) == 0 {
		return errors.New("store: empty key")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(namespace))
		if err != nil {
			return err
		}
		return b.Put(key, value)
	})
}

// Delete implements Store.
func (s *boltStore) Delete(_ context.Context, namespace string, key []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(namespace))
		if b == nil {
			return nil // bucket doesn't exist → nothing to delete
		}
		return b.Delete(key)
	})
}

// List implements Store. The returned slice's keys and values are owned by
// the caller (copied out of the bolt tx).
func (s *boltStore) List(_ context.Context, namespace string, prefix []byte) ([]Pair, error) {
	var out []Pair
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(namespace))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		if len(prefix) == 0 {
			for k, v := c.First(); k != nil; k, v = c.Next() {
				out = append(out, copyPair(k, v))
			}
			return nil
		}
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			out = append(out, copyPair(k, v))
		}
		return nil
	})
	return out, err
}

// Close implements Store.
func (s *boltStore) Close() error {
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func copyPair(k, v []byte) Pair {
	return Pair{
		Key:   append([]byte(nil), k...),
		Value: append([]byte(nil), v...),
	}
}
