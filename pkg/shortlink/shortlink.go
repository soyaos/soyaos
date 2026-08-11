// Package shortlink is the SoyaOS in-process URL shortener used by the
// degradation path (DD-006). When a Channel can only carry plain text
// (WeChat Public Account passive reply, SMS), the connector layer
// rewrites the outgoing Message to fit, appending a short URL the
// recipient can tap to reach the full artifact.
//
// The codepath is deliberately stdlib-only:
//
//   - Deterministic short codes (base62 of sha256(longURL)) means
//     repeated Shorten() calls for the same URL yield the same code,
//     so a recipient who tapped a stale message still gets there.
//   - The Store namespace lives in the same pkg/store the rest of Solo
//     uses, so a single bolt file holds the entire SoyaOS state.
package shortlink

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/soyaos/soyaos/pkg/store"
)

// Namespace is the bbolt bucket used for short-code → long-URL records.
const Namespace = "shortlink"

// CodeLen is the length of the generated short code (base62 chars).
const CodeLen = 8

// base62 is the alphabet used by encodeBase62.
const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Store mints + resolves short codes against an underlying pkg/store.
//
// Base is the public-facing prefix prepended to every code returned by
// Shorten ("https://s.soya.os/" → "https://s.soya.os/AbCdEfGh"). Empty
// Base is allowed for test convenience.
type Store struct {
	KV   store.Store
	Base string
}

// Shorten returns a stable short URL for longURL. The same long URL
// always returns the same code (within the lifetime of the underlying
// store); collisions are vanishingly improbable at 8 base62 chars but
// would be detected by the round-trip check below.
func (s *Store) Shorten(ctx context.Context, longURL string) (string, error) {
	if s == nil || s.KV == nil {
		return "", errors.New("shortlink: nil store")
	}
	if longURL == "" {
		return "", errors.New("shortlink: empty URL")
	}
	code := codeFor(longURL)

	// Collision check: if a previous Put recorded a *different* URL
	// under this code, surface that as an error rather than silently
	// overwriting. (Genuine duplicates re-write the same value, which
	// is a no-op.)
	if existing, err := s.KV.Get(ctx, Namespace, []byte(code)); err == nil {
		if string(existing) != longURL {
			return "", fmt.Errorf("shortlink: collision under code %q", code)
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", err
	}

	if err := s.KV.Put(ctx, Namespace, []byte(code), []byte(longURL)); err != nil {
		return "", err
	}
	return s.Base + code, nil
}

// Resolve returns the long URL recorded under code. Code may carry the
// Base prefix; we strip it before lookup. Missing codes surface as
// ErrNotFound from pkg/store, which callers can errors.Is against.
func (s *Store) Resolve(ctx context.Context, code string) (string, error) {
	if s == nil || s.KV == nil {
		return "", errors.New("shortlink: nil store")
	}
	if s.Base != "" {
		code = strings.TrimPrefix(code, s.Base)
	}
	if code == "" {
		return "", errors.New("shortlink: empty code")
	}
	raw, err := s.KV.Get(ctx, Namespace, []byte(code))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// codeFor returns the deterministic base62 short code for longURL.
//
// We hash the full URL, then encode the first 6 bytes (48 bits) of the
// digest in base62. Six bytes → up to 9 base62 chars; we keep the first
// CodeLen (8). The collision space is 62^8 ≈ 2.18e14 → safe for billions
// of unique URLs.
func codeFor(longURL string) string {
	sum := sha256.Sum256([]byte(longURL))
	// Pack the first 6 bytes (48 bits) into a uint64 and encode.
	var v uint64
	for i := 0; i < 6; i++ {
		v = v<<8 | uint64(sum[i])
	}
	out := encodeBase62(v, CodeLen)
	return out
}

// encodeBase62 renders v as a left-padded base62 string of exactly width
// characters.
func encodeBase62(v uint64, width int) string {
	if v == 0 {
		return strings.Repeat(string(base62[0]), width)
	}
	buf := make([]byte, 0, width)
	for v > 0 {
		buf = append(buf, base62[v%62])
		v /= 62
	}
	// Reverse.
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	for len(buf) < width {
		buf = append([]byte{base62[0]}, buf...)
	}
	if len(buf) > width {
		buf = buf[len(buf)-width:]
	}
	return string(buf)
}
