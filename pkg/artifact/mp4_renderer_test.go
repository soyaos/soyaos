package artifact

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
