package control

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/soyaos/soyaos/pkg/kernel"
)

// buildTestSPK assembles a minimal in-memory .spk (gzip+tar) containing
// a soyapack.yaml + the prompt file it references. Caller controls the
// virtual_model_id so individual cases can assert deduplication / slug
// uniqueness without colliding.
func buildTestSPK(t *testing.T, name, slug string, extras ...tarEntry) []byte {
	t.Helper()
	manifest := fmt.Sprintf(`spec_version: soyapack.v0
kind: Agent
name: %s
version: 0.1.0
description: test pack for deploy endpoint
authors:
  - name: tester
license: MIT
runtime:
  compat: ">=0.1.0"
determinism: read-only
entry: prompts/main.md
expose:
  openai_compat: chat
  virtual_model_id: soya:%s
`, name, slug)

	entries := []tarEntry{
		{name: "soyapack.yaml", body: manifest},
		{name: "prompts/main.md", body: "You are a test agent."},
	}
	entries = append(entries, extras...)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0o644,
			Size:     int64(len(e.body)),
			Typeflag: tf,
			Format:   tar.FormatPAX,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if tf == tar.TypeReg && e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("tar body: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz.Close: %v", err)
	}
	return buf.Bytes()
}

type tarEntry struct {
	name     string
	body     string
	typeflag byte
}

// postPack POSTs a multipart body wrapping the .spk under field name "pack".
// The caller can set a custom X-Spk-Sha256 to exercise the validation path;
// passing the empty string omits the header entirely.
func postPack(t *testing.T, baseURL string, spk []byte, sha string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("pack", "test.spk")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(spk); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("mw.Close: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/control/v0/packs", &body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if sha != "" {
		req.Header.Set("X-Spk-Sha256", sha)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

func newDeployServer(t *testing.T) (*httptest.Server, *kernel.Kernel) {
	t.Helper()
	k := kernel.New()
	k.Register(kernel.EchoAgent)
	srv := httptest.NewServer(NewServer(k).WithDataDir(t.TempDir()).Handler())
	t.Cleanup(srv.Close)
	return srv, k
}

func TestDeploy_HappyPath_RegistersAgent(t *testing.T) {
	srv, k := newDeployServer(t)
	spk := buildTestSPK(t, "hello-pack", "hello-pack")
	sum := sha256.Sum256(spk)
	resp := postPack(t, srv.URL, spk, hex.EncodeToString(sum[:]))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var out deployPackResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Slug != "hello-pack" || out.VirtualModelID != "soya:hello-pack" {
		t.Errorf("identity wrong: %+v", out)
	}
	if out.Files < 2 {
		t.Errorf("Files=%d, want >=2", out.Files)
	}
	if out.Size == 0 {
		t.Errorf("Size=0, want >0")
	}
	// Kernel must now list the new agent alongside echo.
	found := false
	for _, a := range k.List() {
		if a.ModelID() == "soya:hello-pack" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("kernel.List() does not contain the deployed agent")
	}
}

func TestDeploy_DisabledWhenNoDataDir(t *testing.T) {
	k := kernel.New()
	srv := httptest.NewServer(NewServer(k).Handler()) // no WithDataDir
	defer srv.Close()
	spk := buildTestSPK(t, "p", "p")
	resp := postPack(t, srv.URL, spk, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", resp.StatusCode)
	}
}

func TestDeploy_SHA256Mismatch_400(t *testing.T) {
	srv, _ := newDeployServer(t)
	spk := buildTestSPK(t, "mismatch", "mismatch")
	wrong := strings.Repeat("0", 64)
	resp := postPack(t, srv.URL, spk, wrong)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s, want 400", resp.StatusCode, b)
	}
	var env errBody
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Error.Code != "sha256_mismatch" {
		t.Errorf("code=%q, want sha256_mismatch", env.Error.Code)
	}
}

func TestDeploy_ZipSlip_Rejected(t *testing.T) {
	srv, _ := newDeployServer(t)
	// Inject a malicious tar entry alongside a real manifest. Extract
	// must refuse the whole archive before any file lands on disk —
	// even a syntactically valid manifest in the same archive does not
	// rescue a slip attempt.
	spk := buildTestSPK(t, "evil", "evil",
		tarEntry{name: "../etc/passwd", body: "root:x:0:0"},
	)
	resp := postPack(t, srv.URL, spk, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s, want 400", resp.StatusCode, b)
	}
	var env errBody
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env.Error.Code != "pack_unsafe_path" {
		t.Errorf("code=%q, want pack_unsafe_path", env.Error.Code)
	}
}

func TestDeploy_PackTooLarge_413(t *testing.T) {
	srv, _ := newDeployServer(t)
	// Build a body larger than MaxPackUploadBytes. We do not need a real
	// .spk — the MaxBytesReader gate trips before ParseMultipartForm
	// finishes reading the body.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("pack", "big.spk")
	// 33 MiB of zero bytes overflows the 32 MiB cap.
	junk := make([]byte, 33<<20)
	_, _ = part.Write(junk)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/control/v0/packs", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s, want 413", resp.StatusCode, b)
	}
}

func TestDeploy_MissingPackField_400(t *testing.T) {
	srv, _ := newDeployServer(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	// Wrong field name — server expects "pack".
	part, _ := mw.CreateFormFile("blob", "x.spk")
	_, _ = part.Write([]byte("x"))
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/control/v0/packs", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
}

func TestDeploy_NonPostMethod_405(t *testing.T) {
	srv, _ := newDeployServer(t)
	resp, err := http.Get(srv.URL + "/control/v0/packs")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d, want 405", resp.StatusCode)
	}
}

// ensure errors.Is path stays imported even when test set narrows; keep
// canary so future trimming doesn't drop the import silently.
var _ = errors.Is
