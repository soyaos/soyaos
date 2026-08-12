// Package studio embeds the soyaos/studio SPA bundle into the soyaos binary
// and exposes it as an http.Handler that the data-plane mux mounts at "/".
//
// The bundle ships from the soyaos/studio repo as a pure static dist/ tree:
// one index.html plus hashed JS/CSS under assets/. We copy that tree under
// dist/ (committed to source control so `go build` is hermetic) and use
// //go:embed to fold it into the binary at compile time.
//
// SPA fallback: react-router-dom uses BrowserRouter, so client-side routes
// like /chat, /agents, /keys, /trace need the server to return index.html on
// hard reload — otherwise the user sees 404. Handler() does this by checking
// whether the requested path is a real file in the embedded tree; if not, it
// returns index.html with status 200 so the client-side router can take
// over. Paths matched earlier in the mux (/v1/*, /control/*, /healthz) are
// not reached, so the fallback is scoped strictly to the SPA surface.
package studio

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist
var distFS embed.FS

// Handler returns the embedded Studio SPA handler with index.html fallback.
//
// Behavior:
//   - GET / or any path that resolves to a file inside dist/ → serve the file
//     with the right Content-Type and a long Cache-Control for hashed assets.
//   - GET any other path (client-side route) → serve index.html with
//     Cache-Control: no-cache so updates ship immediately.
//
// If the embedded tree is empty (e.g. the build pipeline forgot to copy
// studio/dist into internal/studio/dist), the handler returns 503 with a
// short hint instead of a misleading 404.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "studio: embedded dist tree missing — see soyaos/studio README", http.StatusServiceUnavailable)
		})
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			serveIndex(w, sub)
			return
		}
		if _, statErr := fs.Stat(sub, path); statErr == nil {
			// Hashed assets under assets/ can cache forever (filename changes
			// on every build); the index.html caches no — handled separately.
			if strings.HasPrefix(path, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		// Client-side route — let react-router-dom take over.
		serveIndex(w, sub)
	})
}

func serveIndex(w http.ResponseWriter, sub fs.FS) {
	body, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.Error(w, "studio: index.html missing from embedded dist", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}
