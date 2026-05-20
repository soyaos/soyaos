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

	"github.com/chromedp/chromedp"
)

// defaultLongImageTimeout is applied when LongImageRenderer.Timeout is the
// zero value. Long-image rendering does the same boot-Chrome → load-HTML →
// rasterize trip the PDF renderer does, so the budget matches.
const defaultLongImageTimeout = 30 * time.Second

// LongImageRenderer renders a snapshot to a single tall PNG by reusing
// HTMLRenderer's output and asking headless Chrome for a full-page
// screenshot. NewsBeam (DD-009) pushes the result to DingTalk / Feishu
// groups every morning: chat platforms render embedded images far more
// reliably than fancy rich cards, and a "long picture" is the cultural
// default in 中文 social/work apps for daily digests.
//
// The renderer pins viewport width to 1080 px and deviceScaleFactor to 2
// so the resulting PNG is 2160 px wide — high-DPI on phone screens with
// no extra storage cost over a 1080 px @1× shot.
type LongImageRenderer struct {
	Template   string        // forwarded to HTMLRenderer
	Schema     string        // artifact schema id (e.g. "newsbeam.v1")
	ChromePath string        // optional explicit chrome binary; empty = auto-detect
	Timeout    time.Duration // default 30s if zero
}

// Kind reports KindLongImage; LongImageRenderer is the canonical renderer
// for the "long_image" Artifact form.
func (r LongImageRenderer) Kind() Kind { return KindLongImage }

// MIME is exposed as a constant on the renderer so callers building
// Attachments without going through Render() (e.g. while streaming) can
// stay aligned with what Render() writes into Artifact.MIMEType.
func (r LongImageRenderer) MIME() string { return "image/png" }

// Render produces a long PNG screenshot of the rendered template.
//
// Pipeline:
//
//  1. Run HTMLRenderer{r.Template, r.Schema} against snapshot to obtain HTML.
//  2. Write the HTML to a temporary file so Chrome can load it via file://.
//  3. Boot a headless Chrome and emulate a 1080 px wide viewport at 2×
//     deviceScaleFactor (final image = 2160 px wide).
//  4. Wait for document.fonts.ready to resolve so web-font glyphs are
//     baked in rather than blank-boxed at the moment of capture.
//  5. chromedp.FullScreenshot at quality 100 → write PNG bytes to dst.
//
// The returned Artifact carries Kind=KindLongImage and
// MIMEType="image/png".
func (r LongImageRenderer) Render(ctx context.Context, snapshot any, dst io.Writer) (Artifact, error) {
	html := HTMLRenderer{Template: r.Template, Schema: r.Schema}

	var htmlBuf bytes.Buffer
	if _, err := html.Render(ctx, snapshot, &htmlBuf); err != nil {
		return Artifact{}, fmt.Errorf("long_image: render html: %w", err)
	}

	chromePath, err := r.resolveChromePath()
	if err != nil {
		return Artifact{}, err
	}

	tmpFile, err := os.CreateTemp("", "soyaos-artifact-*.html")
	if err != nil {
		return Artifact{}, fmt.Errorf("long_image: create temp html: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(htmlBuf.Bytes()); err != nil {
		tmpFile.Close()
		return Artifact{}, fmt.Errorf("long_image: write temp html: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return Artifact{}, fmt.Errorf("long_image: close temp html: %w", err)
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultLongImageTimeout
	}

	runCtx, cancelTimeout := context.WithTimeout(ctx, timeout)
	defer cancelTimeout()

	allocOpts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	allocOpts = append(allocOpts, chromedp.ExecPath(chromePath))

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(runCtx, allocOpts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	var pngBytes []byte
	if err := chromedp.Run(browserCtx,
		// 1080 viewport × 2 device scale → 2160 px physical width.
		chromedp.EmulateViewport(1080, 1, chromedp.EmulateScale(2)),
		chromedp.Navigate("file://"+tmpPath),
		chromedp.WaitReady("body", chromedp.ByQuery),
		// Wait for web-font load. chromedp.Poll evaluates the supplied
		// expression every animation frame until truthy. We accept any
		// of (a) fonts.ready resolved to status=loaded, or (b) no
		// document.fonts at all — so headless pages with no web fonts
		// don't hang waiting for an API that's effectively a no-op.
		chromedp.Poll("(typeof document.fonts === 'undefined') || document.fonts.status === 'loaded'", nil),
		chromedp.FullScreenshot(&pngBytes, 100),
	); err != nil {
		if ctxErr := runCtx.Err(); errors.Is(ctxErr, context.DeadlineExceeded) {
			return Artifact{}, fmt.Errorf("long_image: chromedp run: %w: %v", ctxErr, err)
		}
		return Artifact{}, fmt.Errorf("long_image: chromedp run: %w", err)
	}

	n, err := dst.Write(pngBytes)
	if err != nil {
		return Artifact{}, fmt.Errorf("long_image: write output: %w", err)
	}

	return Artifact{
		Kind:      KindLongImage,
		Schema:    r.Schema,
		MIMEType:  "image/png",
		Size:      int64(n),
		CreatedAt: time.Now(),
	}, nil
}

// resolveChromePath mirrors PDFRenderer.resolveChromePath but lives on
// LongImageRenderer so the renderers stay independent (no shared
// package-level state). Resolution order matches PDFRenderer exactly so
// operators only have to set SOYAOS_CHROME once.
func (r LongImageRenderer) resolveChromePath() (string, error) {
	if r.ChromePath != "" {
		if _, err := os.Stat(r.ChromePath); err != nil {
			return "", fmt.Errorf("long_image: chrome binary %q not usable: %w (set %s to override)", r.ChromePath, err, chromeEnvVar)
		}
		return r.ChromePath, nil
	}

	if env := os.Getenv(chromeEnvVar); env != "" {
		if _, err := os.Stat(env); err != nil {
			return "", fmt.Errorf("long_image: %s=%q not usable: %w", chromeEnvVar, env, err)
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

	return "", errors.New("long_image: chrome binary not found in PATH or macOS hints; set " + chromeEnvVar + " to an absolute path")
}
