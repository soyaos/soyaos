package artifact

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLongImageRenderer_KindAndMIME pins the cheap, non-browser
// invariants. Running this test does not boot Chrome — if the renderer
// drifts on MIME/Kind we want to catch it on every CI run, not only when
// Chrome happens to be installed.
func TestLongImageRenderer_KindAndMIME(t *testing.T) {
	r := LongImageRenderer{}
	if r.Kind() != KindLongImage {
		t.Errorf("Kind = %q, want %q", r.Kind(), KindLongImage)
	}
	if r.MIME() != "image/png" {
		t.Errorf("MIME = %q, want image/png", r.MIME())
	}
}

func TestLongImageRenderer_NoChromePath_ErrorIsFriendly(t *testing.T) {
	t.Parallel()
	bogus := filepath.Join(t.TempDir(), "definitely-not-chrome")
	r := LongImageRenderer{
		Template:   "<h1>x</h1>",
		Schema:     "newsbeam.v1",
		ChromePath: bogus,
	}
	var buf bytes.Buffer
	_, err := r.Render(context.Background(), nil, &buf)
	if err == nil {
		t.Fatal("Render: expected non-nil error for bogus ChromePath")
	}
	if !strings.Contains(err.Error(), chromeEnvVar) {
		t.Errorf("Render: error %q does not mention %s", err, chromeEnvVar)
	}
	if buf.Len() != 0 {
		t.Errorf("Render: dst should be untouched on chrome-resolution error; wrote %d bytes", buf.Len())
	}
}

func TestLongImageRenderer_RegistersAsLongImage(t *testing.T) {
	reg := NewRegistry()
	reg.Register(LongImageRenderer{Template: "<p>x</p>", Schema: "newsbeam.v1"})
	rend, ok := reg.Lookup(KindLongImage)
	if !ok {
		t.Fatal("registry.Lookup(KindLongImage) = (_, false)")
	}
	if rend.Kind() != KindLongImage {
		t.Errorf("registered renderer Kind = %q", rend.Kind())
	}
}

func TestLongImageRenderer_ProducesPNGFile(t *testing.T) {
	chrome, ok := findChromeForTests()
	if !ok {
		t.Skip(chromeSkipHint)
	}

	r := LongImageRenderer{
		Template:   "<h1>{{.Title}}</h1><p>{{.Body}}</p>",
		Schema:     "newsbeam.v1",
		ChromePath: chrome,
		Timeout:    30 * time.Second,
	}

	var buf bytes.Buffer
	art, err := r.Render(context.Background(), map[string]any{
		"Title": "Daily AI Digest",
		"Body":  "Bloomberg + Verge + Ars",
	}, &buf)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if art.Kind != KindLongImage {
		t.Errorf("Kind = %q, want %q", art.Kind, KindLongImage)
	}
	if art.MIMEType != "image/png" {
		t.Errorf("MIME = %q, want image/png", art.MIMEType)
	}
	// PNG magic header: 89 50 4E 47 0D 0A 1A 0A.
	pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if !bytes.HasPrefix(buf.Bytes(), pngMagic) {
		head := buf.Bytes()
		if len(head) > 8 {
			head = head[:8]
		}
		t.Errorf("output does not start with PNG magic; head=% x", head)
	}
	if art.Size != int64(buf.Len()) {
		t.Errorf("Artifact.Size = %d, want %d", art.Size, buf.Len())
	}
}
