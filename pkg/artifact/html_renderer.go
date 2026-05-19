package artifact

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"time"
)

// printCSS is the @media print rule set mandated by DESIGN.md §9. It is
// auto-injected into every HTML artifact so downstream PDF renderers
// (Chrome headless, APP-463) produce output where images, tables, code
// blocks and cards never break across pages, and where headings never
// end up orphaned at the bottom of a page.
const printCSS = `<style>
@media print {
  img, table, pre, code, figure, .card, blockquote {
    break-inside: avoid;
  }
  h1, h2, h3, h4, h5, h6 {
    break-after: avoid;
  }
  * {
    -webkit-print-color-adjust: exact;
    print-color-adjust: exact;
  }
}
</style>`

// HTMLRenderer renders a snapshot to HTML via html/template, then prepends
// the DESIGN.md §9 @media print CSS block so the result is ready for PDF
// export without any further processing.
//
// Template is the raw html/template source. Schema is the schema id
// (e.g. "guide.v1") stamped onto the produced Artifact.
type HTMLRenderer struct {
	Template string
	Schema   string
}

// Kind reports KindHTML; HTMLRenderer is the canonical renderer for the
// "html" Artifact form.
func (r HTMLRenderer) Kind() Kind { return KindHTML }

// Render parses r.Template, executes it against snapshot, prepends the
// @media print CSS block, and writes the combined HTML to dst.
//
// An empty Template is treated as a programming error and surfaced via
// a typed errors.New value rather than producing an empty document.
func (r HTMLRenderer) Render(ctx context.Context, snapshot any, dst io.Writer) (Artifact, error) {
	if r.Template == "" {
		return Artifact{}, errors.New("html: empty template")
	}

	tmpl, err := template.New("artifact").Parse(r.Template)
	if err != nil {
		return Artifact{}, fmt.Errorf("html: parse template: %w", err)
	}

	var body bytes.Buffer
	if err := tmpl.Execute(&body, snapshot); err != nil {
		return Artifact{}, fmt.Errorf("html: execute template: %w", err)
	}

	// Prepend the print stylesheet so it lives at the very top of the
	// document, ahead of any <head>/<body> content from the template.
	// Downstream PDF tooling reads the first @media print block it sees.
	var out bytes.Buffer
	out.WriteString(printCSS)
	out.WriteString("\n")
	if _, err := body.WriteTo(&out); err != nil {
		return Artifact{}, fmt.Errorf("html: assemble document: %w", err)
	}

	n, err := dst.Write(out.Bytes())
	if err != nil {
		return Artifact{}, fmt.Errorf("html: write output: %w", err)
	}

	return Artifact{
		Kind:      KindHTML,
		Schema:    r.Schema,
		MIMEType:  "text/html; charset=utf-8",
		Size:      int64(n),
		CreatedAt: time.Now(),
	}, nil
}
