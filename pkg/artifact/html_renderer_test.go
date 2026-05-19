package artifact

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestHTMLRenderer_RendersTemplate(t *testing.T) {
	t.Parallel()

	r := HTMLRenderer{Template: `<h1>{{.Title}}</h1>`, Schema: "guide.v1"}
	var buf bytes.Buffer

	art, err := r.Render(context.Background(), map[string]any{"Title": "Hello"}, &buf)
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	if got := buf.String(); !strings.Contains(got, "Hello") {
		t.Errorf("rendered HTML missing %q; got %q", "Hello", got)
	}
	if art.Kind != KindHTML {
		t.Errorf("Artifact.Kind = %q, want %q", art.Kind, KindHTML)
	}
	if art.MIMEType != "text/html; charset=utf-8" {
		t.Errorf("Artifact.MIMEType = %q, want text/html; charset=utf-8", art.MIMEType)
	}
	if art.Schema != "guide.v1" {
		t.Errorf("Artifact.Schema = %q, want guide.v1", art.Schema)
	}
	if art.Size <= 0 || art.Size != int64(buf.Len()) {
		t.Errorf("Artifact.Size = %d, want %d", art.Size, buf.Len())
	}
	if art.CreatedAt.IsZero() {
		t.Error("Artifact.CreatedAt is zero")
	}
}

func TestHTMLRenderer_InjectsPrintCSS(t *testing.T) {
	t.Parallel()

	r := HTMLRenderer{Template: `<p>body</p>`}
	var buf bytes.Buffer

	if _, err := r.Render(context.Background(), nil, &buf); err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	got := buf.String()
	for _, want := range []string{
		"@media print",
		"break-inside",
		"break-after",
		"-webkit-print-color-adjust: exact",
		"print-color-adjust: exact",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q", want)
		}
	}

	// Print CSS must appear before the template body so PDF tooling
	// finds it on its first pass.
	if idxCSS, idxBody := strings.Index(got, "@media print"), strings.Index(got, "<p>body</p>"); idxCSS == -1 || idxBody == -1 || idxCSS > idxBody {
		t.Errorf("print CSS not injected ahead of body: cssIdx=%d bodyIdx=%d", idxCSS, idxBody)
	}
}

func TestHTMLRenderer_EscapesUserInput(t *testing.T) {
	t.Parallel()

	r := HTMLRenderer{Template: `<h1>{{.Title}}</h1>`}
	var buf bytes.Buffer

	if _, err := r.Render(context.Background(), map[string]any{"Title": "<script>"}, &buf); err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	got := buf.String()
	// Restrict the search to the rendered body so the <style> wrapper
	// we ourselves emit can't satisfy the assertion.
	body := got[strings.Index(got, "<h1>"):]
	if strings.Contains(body, "<script>") {
		t.Errorf("user input not escaped; body=%q", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected escaped &lt;script&gt; in body; got %q", body)
	}
}

func TestHTMLRenderer_EmptyTemplateErrors(t *testing.T) {
	t.Parallel()

	r := HTMLRenderer{}
	var buf bytes.Buffer

	art, err := r.Render(context.Background(), nil, &buf)
	if err == nil {
		t.Fatal("Render: expected non-nil error for empty Template")
	}
	if buf.Len() != 0 {
		t.Errorf("Render: dst should be untouched on error; wrote %d bytes", buf.Len())
	}
	if art.Kind != "" || art.Size != 0 {
		t.Errorf("Render: expected zero Artifact on error; got %+v", art)
	}
}
