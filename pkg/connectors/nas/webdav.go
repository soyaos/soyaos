// SilentCut reverse-pressure: WebDAV is the lowest-common-denominator NAS
// protocol — Nextcloud, ownCloud, Apache mod_dav, even some Synology
// configurations — and it's the only one whose wire format we can drive
// with the stdlib alone. That makes it the alpha go-to for "give SilentCut
// at least one working end-to-end NAS target without dragging extra deps".

package nas

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// webdavHandle drives PUT requests against the configured server. The HTTP
// client is reused across Writes so connection pooling works.
type webdavHandle struct {
	mu     sync.Mutex
	cfg    Config // mutated by Close (Password wipe)
	closed bool
	client *http.Client
}

func openWebDAV(_ context.Context, cfg Config) (NAS, error) {
	if cfg.Host == "" {
		return nil, errors.New("webdav: Host is required")
	}
	return &webdavHandle{
		cfg:    cfg,
		client: &http.Client{},
	}, nil
}

// Write performs an HTTP PUT against cfg.Host + path with Basic auth.
func (h *webdavHandle) Write(ctx context.Context, path string, r io.Reader) (int64, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return 0, errors.New("webdav: handle closed")
	}
	cfg := h.cfg // snapshot under lock — protects against Close racing with Write
	h.mu.Unlock()

	url := joinURL(cfg.Host, path)
	// We need to know how many bytes were written; tee through a counter.
	counter := &countingReader{r: r}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, counter)
	if err != nil {
		return 0, fmt.Errorf("webdav: build request: %w", err)
	}
	if cfg.Username != "" || cfg.Password != "" {
		req.SetBasicAuth(cfg.Username, cfg.Password)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return counter.n, fmt.Errorf("webdav: do request: %w", err)
	}
	defer resp.Body.Close()
	// WebDAV RFC 4918 §9.7: 200/201/204 are all success codes for PUT.
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return counter.n, nil
	default:
		return counter.n, fmt.Errorf("webdav: unexpected status %d", resp.StatusCode)
	}
}

// Close marks the handle unusable and wipes the credential bytes the
// Moon-issued bundle left in memory. The HTTP client itself has no
// long-lived secrets so we only need to drop the password.
func (h *webdavHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	wipe(&h.cfg)
	h.client = nil
	return nil
}

// joinURL stitches base + path with exactly one slash between them.
func joinURL(base, path string) string {
	if base == "" {
		return path
	}
	if path == "" {
		return base
	}
	if strings.HasSuffix(base, "/") && strings.HasPrefix(path, "/") {
		return base + path[1:]
	}
	if !strings.HasSuffix(base, "/") && !strings.HasPrefix(path, "/") {
		return base + "/" + path
	}
	return base + path
}

// countingReader wraps an io.Reader and tallies the bytes that flow
// through it. http.Request.Body needs the body once; we read everything,
// so counter.n is the final byte count.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// wipe zeroes the secret-bearing fields of cfg in place. It must be called
// under whatever lock the handle uses to serialize Close against Write.
func wipe(cfg *Config) {
	// Overwrite with zero-length strings so the original backing array is
	// no longer reachable from the Config. Go strings are immutable, so
	// we can't scrub the bytes themselves — the best we can do is drop
	// the only reference we kept.
	cfg.Password = ""
	cfg.Username = ""
}
