package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// captured holds whatever the fake control server pulled off the request so
// the test can assert multipart shape, sha header, and method.
type captured struct {
	mu       sync.Mutex
	method   string
	path     string
	sha      string
	field    string
	filename string
	body     []byte
}

func (c *captured) snapshot() captured {
	c.mu.Lock()
	defer c.mu.Unlock()
	return captured{
		method: c.method, path: c.path, sha: c.sha,
		field: c.field, filename: c.filename, body: append([]byte(nil), c.body...),
	}
}

// fakeControlServer accepts POST /control/v0/packs, captures the request,
// and answers with a canned deployPackResp shape. cmdAgentDeploy must hit
// exactly that path, send the .spk under field name "pack", and set
// X-Spk-Sha256 to the lowercase hex digest of the file's bytes.
func fakeControlServer(t *testing.T, cap *captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.mu.Lock()
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.sha = r.Header.Get("X-Spk-Sha256")
		cap.mu.Unlock()

		if err := r.ParseMultipartForm(64 << 20); err != nil {
			http.Error(w, "parse multipart: "+err.Error(), http.StatusBadRequest)
			return
		}
		// Find the first form file part (whatever the field name was).
		for field, fhdrs := range r.MultipartForm.File {
			if len(fhdrs) == 0 {
				continue
			}
			cap.mu.Lock()
			cap.field = field
			cap.filename = fhdrs[0].Filename
			cap.mu.Unlock()
			f, err := fhdrs[0].Open()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			body, _ := io.ReadAll(f)
			_ = f.Close()
			cap.mu.Lock()
			cap.body = body
			cap.mu.Unlock()
			break
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"slug":             "hello",
			"virtual_model_id": "soya:hello",
			"files":            3,
			"size":             1234,
		})
	}))
}

// writeFakeSPK writes a tiny non-empty file to disk and returns its path.
// We deliberately do not build a real .spk — cmdAgentDeploy only computes
// the sha and streams bytes; the fake server's contract is just that it
// gets back what was sent.
func writeFakeSPK(t *testing.T, dir, name string) (string, []byte, string) {
	t.Helper()
	body := []byte("fake-spk-bytes-" + name)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write spk: %v", err)
	}
	sum := sha256.Sum256(body)
	return path, body, hex.EncodeToString(sum[:])
}

func TestCmdAgentDeploy_PostsMultipartAndShaHeader(t *testing.T) {
	cap := &captured{}
	srv := fakeControlServer(t, cap)
	defer srv.Close()

	tmp := t.TempDir()
	path, body, sum := writeFakeSPK(t, tmp, "hello-0.1.0.spk")

	// rpc flag arg is whatever httptest gives us.
	u, _ := url.Parse(srv.URL)
	if err := cmdAgentDeploy([]string{path, "--rpc", u.String()}); err != nil {
		t.Fatalf("cmdAgentDeploy: %v", err)
	}

	got := cap.snapshot()
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != "/control/v0/packs" {
		t.Errorf("path = %q, want /control/v0/packs", got.path)
	}
	if got.sha != sum {
		t.Errorf("X-Spk-Sha256 = %q, want %q", got.sha, sum)
	}
	if got.field != "pack" {
		t.Errorf("multipart field = %q, want pack", got.field)
	}
	if got.filename != "hello-0.1.0.spk" {
		t.Errorf("filename = %q, want hello-0.1.0.spk", got.filename)
	}
	if string(got.body) != string(body) {
		t.Errorf("uploaded body did not roundtrip: got %q want %q", got.body, body)
	}
}

func TestCmdAgentDeploy_RPCFlagAfterPositional(t *testing.T) {
	// Regression: reorderForFlagSet should let --rpc appear AFTER the
	// positional .spk path. The reorder helper is shared with `agent
	// list` / `agent run`; this case is a smoke that deploy plays along.
	cap := &captured{}
	srv := fakeControlServer(t, cap)
	defer srv.Close()

	tmp := t.TempDir()
	path, _, _ := writeFakeSPK(t, tmp, "p.spk")

	if err := cmdAgentDeploy([]string{path, "--rpc", srv.URL}); err != nil {
		t.Fatalf("cmdAgentDeploy: %v", err)
	}
	if cap.snapshot().path != "/control/v0/packs" {
		t.Fatalf("server did not see /control/v0/packs — flag order broke routing")
	}
}

func TestCmdAgentDeploy_PropagatesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"sha256_mismatch","message":"bad sha"}}`))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	path, _, _ := writeFakeSPK(t, tmp, "x.spk")
	err := cmdAgentDeploy([]string{path, "--rpc", srv.URL})
	if err == nil {
		t.Fatal("cmdAgentDeploy did not surface a 400 error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("err = %v, want it to mention 400", err)
	}
}

func TestCmdAgentDeploy_RejectsMissingFile(t *testing.T) {
	err := cmdAgentDeploy([]string{"/this/path/does/not/exist.spk"})
	if err == nil {
		t.Fatal("cmdAgentDeploy accepted a non-existent file")
	}
}

func TestCmdAgentDeploy_RejectsDirectory(t *testing.T) {
	err := cmdAgentDeploy([]string{t.TempDir()})
	if err == nil {
		t.Fatal("cmdAgentDeploy accepted a directory as the .spk")
	}
}

func TestCmdAgentDeploy_RejectsMissingPositional(t *testing.T) {
	err := cmdAgentDeploy([]string{})
	if err == nil {
		t.Fatal("cmdAgentDeploy accepted no args")
	}
}

// Smoke that the multipart writer's boundary doesn't leak into the body
// bytes — symptom would be the fake server receiving more bytes than the
// underlying file. Hash equality on the captured part already covers this
// but we keep the explicit length check for a clearer failure message.
func TestCmdAgentDeploy_PartBytesEqualFile(t *testing.T) {
	cap := &captured{}
	srv := fakeControlServer(t, cap)
	defer srv.Close()

	tmp := t.TempDir()
	path, body, _ := writeFakeSPK(t, tmp, "len.spk")
	if err := cmdAgentDeploy([]string{path, "--rpc", srv.URL}); err != nil {
		t.Fatalf("cmdAgentDeploy: %v", err)
	}
	got := cap.snapshot()
	if len(got.body) != len(body) {
		t.Errorf("uploaded len=%d, file len=%d", len(got.body), len(body))
	}
}

// Compile-time anchor so unused imports don't slip in if the test set
// trims later (multipart is exercised end-to-end through cmdAgentDeploy,
// but having the symbol referenced here is a clearer signal of intent
// when reading the test in isolation).
var _ = multipart.NewWriter
