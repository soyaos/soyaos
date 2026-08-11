package studio

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndexAtRoot(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<!doctype html>") && !strings.Contains(string(body), "<!DOCTYPE html>") {
		t.Errorf("index.html body does not look like HTML: %s", body[:min(100, len(body))])
	}
}

func TestHandlerServesHashedAssetWithLongCache(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	// Find the actual hashed asset name from the index.
	idxResp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	idxBody, _ := io.ReadAll(idxResp.Body)
	idxResp.Body.Close()

	// Pull the first /assets/... reference out of the HTML.
	const marker = `/assets/`
	i := strings.Index(string(idxBody), marker)
	if i < 0 {
		t.Skip("dist tree empty or contains no /assets/ reference — skipping cache header check")
	}
	tail := string(idxBody)[i:]
	j := strings.IndexAny(tail, `"'`)
	if j < 0 {
		t.Fatal("could not parse asset URL from index.html")
	}
	assetPath := tail[:j]

	resp, err := http.Get(srv.URL + assetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status for %s = %d", assetPath, resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("hashed asset Cache-Control = %q, want immutable", cc)
	}
}

func TestHandlerSPAFallbackForUnknownPath(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	// Hard-reload on a client-side route must return index.html (200), not 404.
	for _, route := range []string{"/chat", "/agents", "/keys", "/trace", "/whatever/deep/path"} {
		resp, err := http.Get(srv.URL + route)
		if err != nil {
			t.Fatalf("GET %s: %v", route, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", route, resp.StatusCode)
			continue
		}
		if !strings.Contains(strings.ToLower(string(body)), "<!doctype html>") {
			t.Errorf("%s: body does not contain doctype, got: %s…", route, body[:min(80, len(body))])
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s: index Cache-Control = %q, want no-cache", route, cc)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
