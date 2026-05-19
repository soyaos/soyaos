package artifact

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// findChromeForTests mirrors PDFRenderer.resolveChromePath but without
// referencing the renderer's receiver, so the integration tests can decide
// up-front whether Chrome is available and t.Skip cleanly otherwise.
func findChromeForTests() (string, bool) {
	if env := os.Getenv(chromeEnvVar); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, true
		}
	}
	for _, name := range []string{"chromium-browser", "google-chrome", "Google Chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, true
		}
	}
	for _, hint := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	} {
		if _, err := os.Stat(hint); err == nil {
			return hint, true
		}
	}
	return "", false
}

const chromeSkipHint = "Chrome not found; install Google Chrome / Chromium or set " +
	chromeEnvVar + "=/path/to/chrome (e.g. " +
	"SOYAOS_CHROME='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')."

func TestPDFRenderer_ProducesValidPDFFile(t *testing.T) {
	chrome, ok := findChromeForTests()
	if !ok {
		t.Skip(chromeSkipHint)
	}

	r := PDFRenderer{
		Template:   "<h1>{{.Title}}</h1>\n<p>{{.Body}}</p>",
		Schema:     "guide.v1",
		ChromePath: chrome,
		Timeout:    30 * time.Second,
	}

	var buf bytes.Buffer
	art, err := r.Render(context.Background(), map[string]any{
		"Title": "Title",
		"Body":  "Body",
	}, &buf)
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	out := buf.Bytes()

	if art.Kind != KindPDF {
		t.Errorf("Artifact.Kind = %q, want %q", art.Kind, KindPDF)
	}
	if art.MIMEType != "application/pdf" {
		t.Errorf("Artifact.MIMEType = %q, want application/pdf", art.MIMEType)
	}
	if art.Schema != "guide.v1" {
		t.Errorf("Artifact.Schema = %q, want guide.v1", art.Schema)
	}
	if art.Size <= 0 || art.Size != int64(len(out)) {
		t.Errorf("Artifact.Size = %d, want %d", art.Size, len(out))
	}
	if art.CreatedAt.IsZero() {
		t.Error("Artifact.CreatedAt is zero")
	}

	// PDFs always begin with "%PDF-" and conclude with "%%EOF". Tolerate
	// trailing whitespace/newlines from the printer.
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		head := out
		if len(head) > 16 {
			head = head[:16]
		}
		t.Errorf("output does not start with %%PDF-: %q", head)
	}
	trimmed := bytes.TrimRight(out, " \r\n\t")
	if !bytes.HasSuffix(trimmed, []byte("%%EOF")) {
		tail := trimmed
		if len(tail) > 16 {
			tail = tail[len(tail)-16:]
		}
		t.Errorf("output does not end with %%EOF: %q", tail)
	}

	// PDFs typically embed visible text as ASCII inside content streams.
	if !bytes.Contains(out, []byte("Title")) {
		t.Errorf("PDF output does not contain visible text %q", "Title")
	}
}

func TestPDFRenderer_HonorsTimeout(t *testing.T) {
	chrome, ok := findChromeForTests()
	if !ok {
		t.Skip(chromeSkipHint)
	}

	r := PDFRenderer{
		Template:   "<h1>hello</h1>",
		Schema:     "guide.v1",
		ChromePath: chrome,
		Timeout:    1 * time.Millisecond,
	}

	var buf bytes.Buffer
	_, err := r.Render(context.Background(), nil, &buf)
	if err == nil {
		t.Fatal("Render: expected non-nil error for 1ms timeout")
	}

	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context deadline") {
		t.Errorf("Render: error %q does not mention context deadline", err)
	}
}

func TestPDFRenderer_NoChromePath_ErrorIsFriendly(t *testing.T) {
	t.Parallel()

	// Point ChromePath at a path guaranteed not to exist so resolveChromePath
	// fails on the first branch, before any environment-variable lookup.
	bogus := filepath.Join(t.TempDir(), "definitely-not-chrome")

	r := PDFRenderer{
		Template:   "<h1>x</h1>",
		Schema:     "guide.v1",
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
