package parseinput

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// invoke runs the Tool's handler directly. Going through the Registry is
// covered by tooling's own tests; here we exercise the handler in isolation.
func invoke(t *testing.T, input map[string]any) (any, error) {
	t.Helper()
	return Tool().Handler(context.Background(), input)
}

func TestTool_HandlesPlainText(t *testing.T) {
	t.Parallel()
	body := "hello kernel"
	out, err := invoke(t, map[string]any{
		"bytes": base64.StdEncoding.EncodeToString([]byte(body)),
		"hint":  "text",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any output, got %T", out)
	}
	if got := result["text"]; got != body {
		t.Fatalf("text mismatch: got %q want %q", got, body)
	}
	meta, _ := result["metadata"].(map[string]any)
	if meta["extracted_method"] != "text" {
		t.Fatalf("extracted_method = %v, want text", meta["extracted_method"])
	}
	if meta["length"] != len(body) {
		t.Fatalf("length = %v, want %d", meta["length"], len(body))
	}
}

func TestTool_HandlesFileURI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	body := "essay paragraph one"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out, err := invoke(t, map[string]any{"uri": "file://" + path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := out.(map[string]any)
	if result["text"] != body {
		t.Fatalf("text mismatch: got %q want %q", result["text"], body)
	}
	meta := result["metadata"].(map[string]any)
	if !strings.HasPrefix(meta["mime"].(string), "text/") {
		t.Fatalf("mime = %v, want text/*", meta["mime"])
	}
}

func TestTool_RejectsBothUriAndBytes(t *testing.T) {
	t.Parallel()
	_, err := invoke(t, map[string]any{
		"uri":   "file:///tmp/nope",
		"bytes": base64.StdEncoding.EncodeToString([]byte("x")),
	})
	if err == nil {
		t.Fatal("expected error when both uri and bytes are set")
	}
	if !strings.Contains(err.Error(), "both") {
		t.Fatalf("error %q should mention 'both'", err)
	}
}

func TestTool_RejectsNeither(t *testing.T) {
	t.Parallel()
	_, err := invoke(t, map[string]any{})
	if err == nil {
		t.Fatal("expected error when neither uri nor bytes are set")
	}
	if !strings.Contains(err.Error(), "neither") {
		t.Fatalf("error %q should mention 'neither'", err)
	}
}

func TestTool_ImageReturnsTypedError(t *testing.T) {
	t.Parallel()
	// PNG magic: 89 50 4E 47 0D 0A 1A 0A
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x01, 0x02}
	_, err := invoke(t, map[string]any{
		"bytes": base64.StdEncoding.EncodeToString(png),
	})
	if err == nil {
		t.Fatal("expected typed error for image input")
	}
	if !strings.Contains(err.Error(), "gosseract") {
		t.Fatalf("error %q should mention gosseract build tag", err)
	}
}

func TestTool_AudioReturnsTypedError(t *testing.T) {
	t.Parallel()
	// RIFF....WAVE header.
	wav := append([]byte("RIFF"), 0, 0, 0, 0)
	wav = append(wav, []byte("WAVE")...)
	wav = append(wav, make([]byte, 8)...)
	_, err := invoke(t, map[string]any{
		"bytes": base64.StdEncoding.EncodeToString(wav),
	})
	if err == nil {
		t.Fatal("expected typed error for audio input")
	}
	if !strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("error %q should mention 'not yet supported'", err)
	}
}

// TestTool_PDFExtractsBTBlock feeds a minimal handcrafted PDF byte stream
// containing a single text object with a "(hello)" literal. We are NOT
// generating a fully spec-conformant PDF — the extractor only needs the
// "%PDF" magic for sniffing plus a "BT ... ET" block to walk.
func TestTool_PDFExtractsBTBlock(t *testing.T) {
	t.Parallel()
	pdf := []byte("%PDF-1.4\n" +
		"1 0 obj <<>> stream\n" +
		"BT /F1 12 Tf 72 720 Td (hello) Tj ET\n" +
		"endstream endobj\n" +
		"%%EOF\n")
	out, err := invoke(t, map[string]any{
		"bytes": base64.StdEncoding.EncodeToString(pdf),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := out.(map[string]any)
	text, _ := result["text"].(string)
	if !strings.Contains(text, "hello") {
		t.Fatalf("extracted text %q should contain 'hello'", text)
	}
	meta := result["metadata"].(map[string]any)
	if meta["extracted_method"] != "pdf-bt-scan" {
		t.Fatalf("extracted_method = %v, want pdf-bt-scan", meta["extracted_method"])
	}
	if meta["mime"] != "application/pdf" {
		t.Fatalf("mime = %v, want application/pdf", meta["mime"])
	}
}
