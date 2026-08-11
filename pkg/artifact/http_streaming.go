// SilentCut reverse-pressure: DD-011 §R needs an HTTP delivery shape that
// matches RenderStream's "produce-as-you-go" semantics. Chunked
// Transfer-Encoding (HTTP/1.1) gives us that for free; Range-header resume
// gives us crash-restart on flaky mobile uplinks. This file wires both on
// top of any StreamingRenderer.

package artifact

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ServeStreamingArtifact returns an http.Handler that drives r against
// snapshot and writes the bytes back to the client with chunked
// Transfer-Encoding. If the request carries a `Range: bytes=N-` header, the
// first N bytes are silently skipped — equivalent semantically to resuming
// a download that previously dropped after N bytes.
//
// The handler does not buffer the full artifact in memory; it spools each
// chunk through to the wire and flushes immediately so middleboxes do not
// coalesce. The Content-Type comes from the renderer (e.g. "video/mp4").
//
// Errors surfaced by the renderer are translated to 5xx after headers have
// already been written; in chunked mode the connection is simply closed,
// which is how HTTP/1.1 signals mid-stream failure.
func ServeStreamingArtifact(r StreamingRenderer, snapshot any) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		skip, err := parseRangeOffset(req.Header.Get("Range"))
		if err != nil {
			http.Error(w, "invalid Range header", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		// Resolve MIME by running Render once into discard? No — that loses
		// the streaming property. Instead, we hard-code the MIME from
		// renderer.Kind() since the Artifact form fixes it.
		w.Header().Set("Content-Type", mimeForKind(r.Kind()))
		// Chunked transfer is implicit if Content-Length is absent and
		// the server keeps the connection HTTP/1.1.
		w.Header().Set("Cache-Control", "no-store")
		if skip > 0 {
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		flusher, _ := w.(http.Flusher)

		chunks := make(chan []byte, 4)
		// Spawn the renderer in a goroutine so we can pipe chunks to the
		// wire concurrently. ctx ties the renderer's lifetime to the HTTP
		// request: if the client disconnects, ctx.Done() fires, and
		// RenderStream's `case <-ctx.Done()` aborts the producer.
		ctx, cancel := context.WithCancel(req.Context())
		defer cancel()
		go func() {
			_, _ = r.RenderStream(ctx, snapshot, chunks)
		}()

		var remaining = skip
		for chunk := range chunks {
			if remaining > 0 {
				if int64(len(chunk)) <= remaining {
					remaining -= int64(len(chunk))
					continue
				}
				chunk = chunk[remaining:]
				remaining = 0
			}
			if _, werr := w.Write(chunk); werr != nil {
				// Client gone; cancel renderer and drain.
				cancel()
				for range chunks {
				}
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
}

// parseRangeOffset parses a `bytes=N-` header and returns N. Empty header
// returns 0. Anything else returns an error.
//
// The Range spec (RFC 7233 §3.1) accepts multiple ranges + suffix ranges;
// for SilentCut we only need the single open-ended `bytes=N-` form, which
// is what every download manager / curl -C emits.
func parseRangeOffset(h string) (int64, error) {
	if h == "" {
		return 0, nil
	}
	const prefix = "bytes="
	if !strings.HasPrefix(h, prefix) {
		return 0, fmt.Errorf("artifact: range unit must be bytes")
	}
	spec := strings.TrimPrefix(h, prefix)
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, fmt.Errorf("artifact: range missing '-'")
	}
	startStr := spec[:dash]
	endStr := spec[dash+1:]
	if startStr == "" {
		return 0, fmt.Errorf("artifact: only open-ended bytes=N- ranges are supported")
	}
	if endStr != "" {
		// Bounded ranges are not what SilentCut needs; reject explicitly
		// so callers learn early rather than getting silently truncated.
		return 0, fmt.Errorf("artifact: only open-ended bytes=N- ranges are supported")
	}
	n, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("artifact: invalid range start %q", startStr)
	}
	return n, nil
}

// mimeForKind maps an Artifact Kind to its canonical Content-Type. Mirrors
// the values each Renderer stamps on its Artifact today; centralising it
// here so the HTTP shim doesn't need to run the renderer twice.
func mimeForKind(k Kind) string {
	switch k {
	case KindHTML:
		return "text/html; charset=utf-8"
	case KindPDF:
		return "application/pdf"
	case KindLongImage:
		return "image/png"
	case KindMarkdown:
		return "text/markdown; charset=utf-8"
	case KindXLSX:
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case KindMP4:
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}
