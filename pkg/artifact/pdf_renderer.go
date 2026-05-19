package artifact

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// chromeEnvVar names the environment variable that overrides Chrome binary
// discovery. Exposed as a constant so error messages and tests can refer to
// the same string.
const chromeEnvVar = "SOYAOS_CHROME"

// defaultPDFTimeout is applied when PDFRenderer.Timeout is the zero value.
const defaultPDFTimeout = 30 * time.Second

// A4 paper dimensions in inches, used for page.PrintToPDF.
const (
	a4WidthInches  = 8.27
	a4HeightInches = 11.69
)

// PDFRenderer renders a snapshot to PDF by first invoking HTMLRenderer to
// produce HTML (with the DESIGN.md §9 @media print rules) and then driving
// a headless Chrome via chromedp to print the page to PDF.
//
// Output is A4, with background colors preserved and no header / footer.
type PDFRenderer struct {
	Template   string        // forwarded to HTMLRenderer
	Schema     string        // artifact schema id (e.g. "guide.v1")
	ChromePath string        // optional explicit chrome binary; empty = auto-detect
	Timeout    time.Duration // default 30s if zero
}

// Kind reports KindPDF; PDFRenderer is the canonical renderer for the
// "pdf" Artifact form.
func (r PDFRenderer) Kind() Kind { return KindPDF }

// Render produces a PDF document.
//
// The pipeline is:
//
//  1. Run HTMLRenderer{r.Template, r.Schema} against snapshot to obtain HTML.
//  2. Write the HTML to a temporary file with a .html suffix so Chrome can
//     load it via file:// without inventing a local HTTP server.
//  3. Boot a headless Chrome process via chromedp.NewExecAllocator pointed at
//     the binary resolved by resolveChromePath.
//  4. Navigate to the file, wait for body to be ready and document.fonts.ready
//     to resolve, then invoke page.PrintToPDF with A4 paper size,
//     PrintBackground=true and no header/footer.
//  5. Write the returned PDF bytes to dst.
//
// The returned Artifact carries Kind=KindPDF and MIMEType="application/pdf".
func (r PDFRenderer) Render(ctx context.Context, snapshot any, dst io.Writer) (Artifact, error) {
	html := HTMLRenderer{Template: r.Template, Schema: r.Schema}

	var htmlBuf bytes.Buffer
	if _, err := html.Render(ctx, snapshot, &htmlBuf); err != nil {
		return Artifact{}, fmt.Errorf("pdf: render html: %w", err)
	}

	chromePath, err := r.resolveChromePath()
	if err != nil {
		return Artifact{}, err
	}

	tmpFile, err := os.CreateTemp("", "soyaos-artifact-*.html")
	if err != nil {
		return Artifact{}, fmt.Errorf("pdf: create temp html: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(htmlBuf.Bytes()); err != nil {
		tmpFile.Close()
		return Artifact{}, fmt.Errorf("pdf: write temp html: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return Artifact{}, fmt.Errorf("pdf: close temp html: %w", err)
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultPDFTimeout
	}

	runCtx, cancelTimeout := context.WithTimeout(ctx, timeout)
	defer cancelTimeout()

	allocOpts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	allocOpts = append(allocOpts, chromedp.ExecPath(chromePath))

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(runCtx, allocOpts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	var pdfBytes []byte
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate("file://"+tmpPath),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools("document.fonts.ready", nil),
		chromedp.ActionFunc(func(ctx context.Context) error {
			buf, _, perr := page.PrintToPDF().
				WithPaperWidth(a4WidthInches).
				WithPaperHeight(a4HeightInches).
				WithPrintBackground(true).
				WithPreferCSSPageSize(false).
				WithDisplayHeaderFooter(false).
				Do(ctx)
			if perr != nil {
				return perr
			}
			pdfBytes = buf
			return nil
		}),
	); err != nil {
		// chromedp swallows the underlying context.DeadlineExceeded when
		// chrome itself fails to start mid-boot (it reports an opaque
		// "chrome failed to start" instead). Re-attach the timeout error
		// when our runCtx fired so callers can errors.Is(...,
		// context.DeadlineExceeded) and see "context deadline" in the
		// formatted message.
		if ctxErr := runCtx.Err(); errors.Is(ctxErr, context.DeadlineExceeded) {
			return Artifact{}, fmt.Errorf("pdf: chromedp run: %w: %v", ctxErr, err)
		}
		return Artifact{}, fmt.Errorf("pdf: chromedp run: %w", err)
	}

	n, err := dst.Write(pdfBytes)
	if err != nil {
		return Artifact{}, fmt.Errorf("pdf: write output: %w", err)
	}

	return Artifact{
		Kind:      KindPDF,
		Schema:    r.Schema,
		MIMEType:  "application/pdf",
		Size:      int64(n),
		CreatedAt: time.Now(),
	}, nil
}

// resolveChromePath returns the Chrome binary path to use for this Render
// invocation.
//
// Resolution order:
//
//  1. r.ChromePath, if non-empty.
//  2. The SOYAOS_CHROME environment variable.
//  3. exec.LookPath for "chromium-browser", "google-chrome", "Google Chrome".
//  4. macOS application bundle hints
//     ("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" and the
//     Chromium equivalent).
//
// On miss, the returned error explicitly mentions SOYAOS_CHROME so the
// operator knows how to override the search.
func (r PDFRenderer) resolveChromePath() (string, error) {
	if r.ChromePath != "" {
		if _, err := os.Stat(r.ChromePath); err != nil {
			return "", fmt.Errorf("pdf: chrome binary %q not usable: %w (set %s to override)", r.ChromePath, err, chromeEnvVar)
		}
		return r.ChromePath, nil
	}

	if env := os.Getenv(chromeEnvVar); env != "" {
		if _, err := os.Stat(env); err != nil {
			return "", fmt.Errorf("pdf: %s=%q not usable: %w", chromeEnvVar, env, err)
		}
		return env, nil
	}

	for _, name := range []string{"chromium-browser", "google-chrome", "Google Chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}

	for _, hint := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	} {
		if _, err := os.Stat(hint); err == nil {
			return hint, nil
		}
	}

	return "", errors.New("pdf: chrome binary not found in PATH or macOS hints; set " + chromeEnvVar + " to an absolute path")
}
