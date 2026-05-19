// Package parseinput implements the "tool.parse_input" Kernel built-in Tool.
//
// It normalizes multi-modal user input (image / PDF / text / audio) into a
// structured {text, metadata} JSON payload that downstream LLM agents can
// consume directly. Compo (flagship story DD-008) is the primary consumer:
// a parent uploads a photo of their child's essay, the kernel routes it
// through tool.parse_input, and the LLM sees plain text.
//
// Alpha scope:
//
//   - text/plain  → returned verbatim.
//   - application/pdf → naive "BT ... ET" text-object scan using stdlib only.
//     This intentionally does NOT handle CID fonts, encrypted PDFs, or
//     compressed content streams. Real PDF support lands in a later
//     milestone (S2 Compo PDF parser).
//   - image/png|jpeg|gif → typed error pointing at the future gosseract
//     build tag (Compo S2 真实 OCR).
//   - audio/wav|mp3 → typed error pointing at the SilentCut audio stage.
//
// The tool is stdlib-only so it can be linked into every kernel build
// without dragging Tesseract or ffmpeg into the alpha binary.
package parseinput

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/soyaos/soyaos/pkg/tooling"
)

// Tool returns the tool.parse_input Tool definition.
func Tool() tooling.Tool {
	return tooling.Tool{
		Name:        "tool.parse_input",
		Description: "Normalize multi-modal user input (image/PDF/text/audio) into {text, metadata} JSON.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"uri": map[string]any{
					"type":        "string",
					"description": "Source URI: file://path, data:<mime>;base64,<...>, or plain path.",
				},
				"bytes": map[string]any{
					"type":        "string",
					"description": "Base64-encoded raw bytes. Mutually exclusive with uri.",
				},
				"hint": map[string]any{
					"type":        "string",
					"enum":        []any{"image", "pdf", "text", "audio"},
					"description": "Optional MIME family hint to disambiguate sniffing.",
				},
			},
			"oneOf": []any{
				map[string]any{"required": []any{"uri"}},
				map[string]any{"required": []any{"bytes"}},
			},
		},
		OutputType: "application/json",
		Handler:    handle,
	}
}

// handle is the parse_input Handler.
func handle(_ context.Context, input map[string]any) (any, error) {
	uriRaw, hasURI := input["uri"]
	bytesRaw, hasBytes := input["bytes"]

	uri, _ := uriRaw.(string)
	bytesStr, _ := bytesRaw.(string)

	hasURI = hasURI && uri != ""
	hasBytes = hasBytes && bytesStr != ""

	switch {
	case hasURI && hasBytes:
		return nil, errors.New("parseinput: exactly one of uri or bytes must be set, got both")
	case !hasURI && !hasBytes:
		return nil, errors.New("parseinput: exactly one of uri or bytes must be set, got neither")
	}

	hint, _ := input["hint"].(string)

	var (
		data    []byte
		extHint string
		err     error
	)
	if hasURI {
		data, extHint, err = loadURI(uri)
	} else {
		data, err = base64.StdEncoding.DecodeString(bytesStr)
	}
	if err != nil {
		return nil, fmt.Errorf("parseinput: load input: %w", err)
	}

	mime := sniffMIME(data, extHint, hint)

	switch {
	case strings.HasPrefix(mime, "text/"):
		return map[string]any{
			"text": string(data),
			"metadata": map[string]any{
				"mime":             mime,
				"length":           len(data),
				"extracted_method": "text",
			},
		}, nil
	case mime == "application/pdf":
		text := extractPDFText(data)
		return map[string]any{
			"text": text,
			"metadata": map[string]any{
				"mime":             mime,
				"length":           len(text),
				"extracted_method": "pdf-bt-scan",
			},
		}, nil
	case strings.HasPrefix(mime, "image/"):
		return nil, errors.New("parseinput: image OCR requires gosseract/tesseract build tag — not enabled in alpha")
	case strings.HasPrefix(mime, "audio/"):
		return nil, errors.New("parseinput: audio not yet supported (SilentCut stage)")
	default:
		return nil, fmt.Errorf("parseinput: unsupported MIME type: %s", mime)
	}
}

// loadURI resolves a uri into raw bytes and an extension hint.
//
// Supported forms:
//   - data:<mime>;base64,<payload>
//   - file:///abs/path  (also accepts file://relative for convenience)
//   - plain filesystem path (no scheme)
func loadURI(uri string) ([]byte, string, error) {
	if strings.HasPrefix(uri, "data:") {
		comma := strings.Index(uri, ",")
		if comma < 0 {
			return nil, "", errors.New("malformed data URI: missing comma")
		}
		header := uri[5:comma]
		payload := uri[comma+1:]
		if !strings.Contains(header, ";base64") {
			// Treat as URL-encoded text.
			decoded, err := url.QueryUnescape(payload)
			if err != nil {
				return nil, "", err
			}
			return []byte(decoded), "", nil
		}
		raw, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, "", err
		}
		return raw, "", nil
	}

	path := uri
	if strings.HasPrefix(uri, "file://") {
		// Tolerate file:// (two slashes, relative) and file:/// (absolute).
		path = strings.TrimPrefix(uri, "file://")
		// file:///abs/path → path becomes "/abs/path" after the trim above
		// only when the URI had three slashes. If it had two, path may be
		// "relative". Both are acceptable for our local-only use.
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return data, strings.ToLower(filepath.Ext(path)), nil
}

// sniffMIME picks a MIME type using (in order): caller hint, file extension,
// magic-byte signature, then a text/plain fallback.
func sniffMIME(data []byte, extHint, hint string) string {
	switch hint {
	case "text":
		return "text/plain"
	case "pdf":
		return "application/pdf"
	case "image":
		return "image/octet-stream"
	case "audio":
		return "audio/octet-stream"
	}

	switch extHint {
	case ".txt", ".md", ".log":
		return "text/plain"
	case ".pdf":
		return "application/pdf"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".wav":
		return "audio/wav"
	case ".mp3":
		return "audio/mpeg"
	}

	// Magic-byte sniff on first 512 bytes.
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	switch {
	case bytes.HasPrefix(head, []byte("%PDF")):
		return "application/pdf"
	case bytes.HasPrefix(head, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case bytes.HasPrefix(head, []byte("GIF87a")) || bytes.HasPrefix(head, []byte("GIF89a")):
		return "image/gif"
	case bytes.HasPrefix(head, []byte("RIFF")) && len(head) >= 12 && bytes.Equal(head[8:12], []byte("WAVE")):
		return "audio/wav"
	case bytes.HasPrefix(head, []byte{0xFF, 0xFB}) ||
		bytes.HasPrefix(head, []byte{0xFF, 0xF3}) ||
		bytes.HasPrefix(head, []byte{0xFF, 0xF2}) ||
		bytes.HasPrefix(head, []byte("ID3")):
		return "audio/mpeg"
	}

	if isLikelyText(head) {
		return "text/plain"
	}
	return "application/octet-stream"
}

// isLikelyText returns true if the buffer looks like UTF-8 text (no NULs and
// printable / whitespace characters dominate).
func isLikelyText(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, c := range b {
		if c == 0 {
			return false
		}
	}
	return true
}

// extractPDFText walks the PDF byte stream and concatenates every string
// literal found inside a "BT ... ET" (Begin Text / End Text) block.
//
// This is intentionally simplistic: it only handles ASCII literal strings in
// uncompressed content streams, ignores font CMaps, and does nothing with
// encrypted PDFs. The intent is to unblock the Compo alpha demo where the
// fixture PDFs we author ourselves are trivially well-formed.
func extractPDFText(data []byte) string {
	var out strings.Builder
	i := 0
	for i < len(data) {
		btIdx := bytes.Index(data[i:], []byte("BT"))
		if btIdx < 0 {
			break
		}
		blockStart := i + btIdx + 2
		etRel := bytes.Index(data[blockStart:], []byte("ET"))
		if etRel < 0 {
			break
		}
		block := data[blockStart : blockStart+etRel]
		extractStringLiterals(block, &out)
		i = blockStart + etRel + 2
	}
	return out.String()
}

// extractStringLiterals scans a PDF text-object block for "(...)" literals and
// appends their (un-escaped) contents to out, separated by spaces. Balanced
// parentheses inside the literal are honored, as is the "\(" escape.
func extractStringLiterals(block []byte, out *strings.Builder) {
	i := 0
	for i < len(block) {
		if block[i] != '(' {
			i++
			continue
		}
		// Found a literal start. Walk forward, tracking depth.
		depth := 1
		i++
		var lit strings.Builder
		for i < len(block) && depth > 0 {
			c := block[i]
			switch c {
			case '\\':
				if i+1 < len(block) {
					nxt := block[i+1]
					switch nxt {
					case 'n':
						lit.WriteByte('\n')
					case 'r':
						lit.WriteByte('\r')
					case 't':
						lit.WriteByte('\t')
					case '(', ')', '\\':
						lit.WriteByte(nxt)
					default:
						lit.WriteByte(nxt)
					}
					i += 2
					continue
				}
				i++
			case '(':
				depth++
				lit.WriteByte(c)
				i++
			case ')':
				depth--
				if depth == 0 {
					i++
					if out.Len() > 0 {
						out.WriteByte(' ')
					}
					out.WriteString(lit.String())
					break
				}
				lit.WriteByte(c)
				i++
			default:
				lit.WriteByte(c)
				i++
			}
		}
	}
}
