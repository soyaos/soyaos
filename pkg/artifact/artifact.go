// Package artifact defines the SoyaOS Artifact abstraction (proposed DD-012).
//
// Every Agent run can emit one or more Artifacts. v0.1.0 admits six forms:
//
//   HTML / PDF / long_image / Markdown / Excel / MP4
//
// Each Artifact carries a schema identifier with SemVer (e.g. "guide.v1") so
// downstream readers can validate compatibility. Multiple Artifacts produced
// from the same underlying snapshot share a "canonical snapshot hash" so the
// rendered forms can be cross-validated (DD-009 / DD-017).
//
// This package owns only the abstraction and the in-memory store used by Solo.
// Renderers (html / pdf / long_image / xlsx / mp4) live alongside their
// runtime dependencies and register themselves via Register().
package artifact

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Kind enumerates the six Artifact forms permitted in v0.1.0.
type Kind string

const (
	KindHTML      Kind = "html"
	KindPDF       Kind = "pdf"
	KindLongImage Kind = "long_image"
	KindMarkdown  Kind = "markdown"
	KindXLSX      Kind = "xlsx"
	KindMP4       Kind = "mp4"
)

// Valid reports whether k is one of the six recognised forms.
func (k Kind) Valid() bool {
	switch k {
	case KindHTML, KindPDF, KindLongImage, KindMarkdown, KindXLSX, KindMP4:
		return true
	}
	return false
}

// Artifact is the descriptor for a single rendered product.
type Artifact struct {
	ID           string            // unique within the producing Agent run
	Kind         Kind              // one of the six recognised forms
	Schema       string            // schema id + SemVer, e.g. "guide.v1"
	SnapshotHash string            // canonical snapshot sha256 (DD-017)
	MIMEType     string            // canonical mime type
	Size         int64             // bytes; -1 if streaming and unknown
	CreatedAt    time.Time         // when rendering finished
	Streaming    bool              // true if the body is appended over time (DD-011)
	Metadata     map[string]string // free-form metadata (filename hints, etc.)
}

// Renderer transforms a snapshot (caller-supplied opaque data) into an
// Artifact body written to dst.
type Renderer interface {
	Kind() Kind
	Render(ctx context.Context, snapshot any, dst io.Writer) (Artifact, error)
}

// Store persists artifacts. Solo uses an in-memory store; other editions
// substitute object storage.
type Store interface {
	Put(ctx context.Context, a Artifact, body io.Reader) error
	Get(ctx context.Context, id string) (Artifact, io.ReadCloser, error)
	List(ctx context.Context) ([]Artifact, error)
}

// Registry tracks the Renderers wired into a SoyaOS process.
type Registry struct {
	mu        sync.RWMutex
	renderers map[Kind]Renderer
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry { return &Registry{renderers: map[Kind]Renderer{}} }

// Register adds a Renderer. The Renderer's Kind() determines its slot.
// Subsequent registrations of the same Kind replace the prior entry.
func (r *Registry) Register(rend Renderer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renderers[rend.Kind()] = rend
}

// Lookup returns the Renderer for a Kind, if any.
func (r *Registry) Lookup(k Kind) (Renderer, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rend, ok := r.renderers[k]
	return rend, ok
}

// Kinds lists every Kind with a registered Renderer.
func (r *Registry) Kinds() []Kind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Kind, 0, len(r.renderers))
	for k := range r.renderers {
		out = append(out, k)
	}
	return out
}

// ErrUnknownKind is returned when a Kind has no registered Renderer.
var ErrUnknownKind = errors.New("artifact: no renderer registered for kind")

// Render is a convenience that finds the Renderer for kind and runs it.
func (r *Registry) Render(ctx context.Context, kind Kind, snapshot any, dst io.Writer) (Artifact, error) {
	rend, ok := r.Lookup(kind)
	if !ok {
		return Artifact{}, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
	return rend.Render(ctx, snapshot, dst)
}
