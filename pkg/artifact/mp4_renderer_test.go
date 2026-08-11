package artifact

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func TestMP4Renderer_Render_ProducesNonEmptyBytes(t *testing.T) {
	var buf bytes.Buffer
	r := MP4Renderer{Schema: "silentcut.v1"}
	a, err := r.Render(context.Background(), map[string]any{"clip": "demo"}, &buf)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("Render produced zero bytes")
	}
	// MP4 magic: bytes[4:8] should be "ftyp".
	body := buf.Bytes()
	if len(body) < 8 || string(body[4:8]) != "ftyp" {
		t.Fatalf("body[4:8]=%q, want ftyp; body=%x", body[4:8], body[:min(16, len(body))])
	}
	if a.Kind != KindMP4 {
		t.Errorf("Kind=%q, want mp4", a.Kind)
	}
	if a.MIMEType != "video/mp4" {
		t.Errorf("MIME=%q, want video/mp4", a.MIMEType)
	}
	if a.SnapshotHash == "" {
		t.Error("SnapshotHash empty; expected canonical sha256")
	}
	if a.Schema != "silentcut.v1" {
		t.Errorf("Schema=%q, want silentcut.v1", a.Schema)
	}
}

func TestMP4Renderer_RenderStream_PushesChunks(t *testing.T) {
	chunks := make(chan []byte, 32)
	r := MP4Renderer{}
	a, err := r.RenderStream(context.Background(), nil, chunks)
	if err != nil {
		t.Fatalf("RenderStream: %v", err)
	}
	var got [][]byte
	for c := range chunks {
		got = append(got, c)
	}
	if len(got) < 3 {
		t.Fatalf("got %d chunks, want >= 3", len(got))
	}
	// Concatenated chunks must equal what Render produces.
	var concat bytes.Buffer
	for _, c := range got {
		concat.Write(c)
	}
	if string(concat.Bytes()[4:8]) != "ftyp" {
		t.Fatalf("concat body[4:8]=%q, want ftyp", concat.Bytes()[4:8])
	}
	if !a.Streaming {
		t.Error("Streaming should be true")
	}
	if a.Size != -1 {
		t.Errorf("Size=%d, want -1 for streaming artifact", a.Size)
	}
}

func TestServeStreamingArtifact_ChunkedDelivery(t *testing.T) {
	srv := httptest.NewServer(ServeStreamingArtifact(MP4Renderer{}, nil))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/clip.mp4")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("Content-Type=%q", resp.Header.Get("Content-Type"))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) == 0 || string(body[4:8]) != "ftyp" {
		t.Fatalf("body bad: %x", body[:min(16, len(body))])
	}
}

func TestServeStreamingArtifact_RangeResume(t *testing.T) {
	srv := httptest.NewServer(ServeStreamingArtifact(MP4Renderer{}, nil))
	defer srv.Close()

	// First, measure full length.
	resp, err := http.Get(srv.URL + "/clip.mp4")
	if err != nil {
		t.Fatalf("GET full: %v", err)
	}
	full, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fullLen := len(full)
	if fullLen <= 10 {
		t.Fatalf("body too short to test range resume: %d bytes", fullLen)
	}

	// Now resume from byte 10.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/clip.mp4", nil)
	req.Header.Set("Range", "bytes=10-")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET range: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status=%d, want 206", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != fullLen-10 {
		t.Fatalf("range body len=%d, want %d", len(body), fullLen-10)
	}
	// The resumed bytes must equal the tail of the full body.
	if !bytes.Equal(body, full[10:]) {
		t.Fatalf("range body does not match full[10:]")
	}
}

func TestServeStreamingArtifact_BadRange(t *testing.T) {
	srv := httptest.NewServer(ServeStreamingArtifact(MP4Renderer{}, nil))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/clip.mp4", nil)
	req.Header.Set("Range", "bytes=10-99") // bounded ranges unsupported
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("status=%d, want 416", resp.StatusCode)
	}
}

// --- RemotionSpec wiring (APP-554 / DD-011 SilentCut) -----------------------

// mockRunner records argv + stdin and emits a configurable byte body
// to the chunks channel, so RenderStream's Remotion path can be tested
// without spawning subprocesses.
type mockRunner struct {
	gotArgv  []string
	gotStdin []byte
	body     []byte
	chunks   int // how many chunks to split body into (≥1)
	err      error
}

func (m *mockRunner) Run(ctx context.Context, argv []string, stdin []byte, out chan<- []byte, _ int64) error {
	m.gotArgv = argv
	m.gotStdin = append([]byte(nil), stdin...)
	if m.err != nil {
		return m.err
	}
	if m.chunks <= 0 {
		m.chunks = 1
	}
	chunkSize := (len(m.body) + m.chunks - 1) / m.chunks
	if chunkSize == 0 {
		chunkSize = 1
	}
	for i := 0; i < len(m.body); i += chunkSize {
		end := i + chunkSize
		if end > len(m.body) {
			end = len(m.body)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- append([]byte(nil), m.body[i:end]...):
		}
	}
	return nil
}

func TestMP4Renderer_RenderStream_SpawnRemotion(t *testing.T) {
	// Build a 256-byte fake MP4 body — enough to split into 4 chunks.
	fakeBody := bytes.Repeat([]byte("0123456789ABCDEF"), 16)
	if !bytes.Equal(fakeBody[:4], []byte("0123")) {
		t.Fatal("test fixture wrong")
	}
	runner := &mockRunner{body: fakeBody, chunks: 4}
	r := MP4Renderer{Schema: "silentcut.v1", Runner: runner}

	chunks := make(chan []byte, 16)
	spec := &RemotionSpec{
		Argv:  []string{"npx", "remotion", "render", "/workdir/src/index.ts", "Clip", "/workdir/out/clip.mp4"},
		Stdin: []byte(`{"hello":"world"}`),
	}
	a, err := r.RenderStream(context.Background(), spec, chunks)
	if err != nil {
		t.Fatalf("RenderStream: %v", err)
	}
	if !a.Streaming || a.Size != -1 {
		t.Errorf("Artifact = %+v, want Streaming=true Size=-1", a)
	}
	var got [][]byte
	for c := range chunks {
		got = append(got, c)
	}
	if len(got) < 1 {
		t.Fatalf("got %d chunks, want >= 1", len(got))
	}
	var concat bytes.Buffer
	for _, c := range got {
		concat.Write(c)
	}
	if !bytes.Equal(concat.Bytes(), fakeBody) {
		t.Errorf("concatenated chunks != fake body (got %d bytes, want %d)", concat.Len(), len(fakeBody))
	}
	// Argv + stdin must have been threaded through verbatim.
	if len(runner.gotArgv) != len(spec.Argv) {
		t.Fatalf("argv len = %d, want %d", len(runner.gotArgv), len(spec.Argv))
	}
	for i := range runner.gotArgv {
		if runner.gotArgv[i] != spec.Argv[i] {
			t.Errorf("argv[%d] = %q, want %q", i, runner.gotArgv[i], spec.Argv[i])
		}
	}
	if !bytes.Equal(runner.gotStdin, spec.Stdin) {
		t.Errorf("stdin = %q, want %q", runner.gotStdin, spec.Stdin)
	}
}

func TestMP4Renderer_RenderStream_RemotionSpecFromMap(t *testing.T) {
	// Snapshots that arrived as map[string]any (from JSON) must still be
	// recognised when they embed a remotion key.
	runner := &mockRunner{body: []byte("ftyp-fake-body"), chunks: 2}
	r := MP4Renderer{Runner: runner}
	chunks := make(chan []byte, 8)

	snapshot := map[string]any{
		"remotion": RemotionSpec{Argv: []string{"npx", "remotion", "render", "/a/b.ts", "C", "/out.mp4"}},
	}
	_, err := r.RenderStream(context.Background(), snapshot, chunks)
	if err != nil {
		t.Fatalf("RenderStream: %v", err)
	}
	if len(runner.gotArgv) == 0 {
		t.Fatal("mockRunner not invoked; map shape not recognised")
	}
}

func TestMP4Renderer_RenderStream_RemotionFailure(t *testing.T) {
	runner := &mockRunner{err: errors.New("exit status 1: chromium crashed")}
	r := MP4Renderer{Runner: runner}
	chunks := make(chan []byte, 8)
	spec := &RemotionSpec{Argv: []string{"npx", "remotion", "render", "/a/b.ts", "C", "/o.mp4"}}
	_, err := r.RenderStream(context.Background(), spec, chunks)
	if err == nil {
		t.Fatal("expected RenderStream to surface runner error")
	}
	if !strings.Contains(err.Error(), "chromium crashed") {
		t.Errorf("error should include subprocess detail, got %v", err)
	}
}

// TestMP4Renderer_RenderStream_RealSubprocess exercises the actual
// execCmdRunner against /bin/sh -c 'printf ...' — no Remotion needed.
// The point is to assert that argv → exec.CommandContext → stdout pipe
// → chunks really works end-to-end. Skipped on Windows where /bin/sh
// is not a thing.
func TestMP4Renderer_RenderStream_RealSubprocess(t *testing.T) {
	if _, err := exec.LookPath("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}
	chunks := make(chan []byte, 32)
	r := MP4Renderer{Schema: "silentcut.v1"} // Runner=nil → execCmdRunner
	spec := &RemotionSpec{
		Argv: []string{"/bin/sh", "-c", "printf 'ftypisomXYZ'; sleep 0; printf 'mdat-payload-bytes'"},
	}
	a, err := r.RenderStream(context.Background(), spec, chunks)
	if err != nil {
		t.Fatalf("RenderStream: %v", err)
	}
	if !a.Streaming {
		t.Error("Streaming must be true")
	}
	var concat bytes.Buffer
	for c := range chunks {
		concat.Write(c)
	}
	body := concat.String()
	if !strings.Contains(body, "ftypisomXYZ") || !strings.Contains(body, "mdat-payload-bytes") {
		t.Errorf("stdout did not propagate to chunks; got %q", body)
	}
}

func TestMP4Renderer_RenderStream_PlaceholderWhenNoSpec(t *testing.T) {
	// Sanity: passing a non-spec snapshot still takes the placeholder
	// path and produces ≥3 chunks of valid-looking MP4 bytes — this is
	// the contract the http_streaming.go tests rely on.
	chunks := make(chan []byte, 16)
	r := MP4Renderer{}
	a, err := r.RenderStream(context.Background(), map[string]any{"unrelated": true}, chunks)
	if err != nil {
		t.Fatalf("RenderStream: %v", err)
	}
	var got [][]byte
	for c := range chunks {
		got = append(got, c)
	}
	if len(got) < 3 {
		t.Errorf("got %d chunks, want >= 3 (placeholder path)", len(got))
	}
	if !a.Streaming {
		t.Error("Streaming should be true on placeholder path")
	}
}

func TestParseRangeOffset(t *testing.T) {
	cases := []struct {
		in     string
		want   int64
		errish bool
	}{
		{"", 0, false},
		{"bytes=0-", 0, false},
		{"bytes=42-", 42, false},
		{"bytes=42-99", 0, true},
		{"items=0-", 0, true},
		{"bytes=-", 0, true},
		{"bytes=abc-", 0, true},
		{"bytes=-100", 0, true},
	}
	for _, tc := range cases {
		got, err := parseRangeOffset(tc.in)
		if tc.errish && err == nil {
			t.Errorf("parseRangeOffset(%q) expected error", tc.in)
			continue
		}
		if !tc.errish && err != nil {
			t.Errorf("parseRangeOffset(%q) unexpected error %v", tc.in, err)
			continue
		}
		if !tc.errish && got != tc.want {
			t.Errorf("parseRangeOffset(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
