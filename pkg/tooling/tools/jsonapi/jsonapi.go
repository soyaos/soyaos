// Package jsonapi implements the "tool.json_api" Kernel built-in Tool.
//
// This is the generic HTTP/JSON escape hatch every Agent eventually
// needs — calling a service the SoyaOS connector framework hasn't grown
// a dedicated channel for yet. NewsBeam uses it to call internal news
// scoring APIs; downstream Agents reuse it as a transport for any JSON
// REST endpoint declared in a Pack's network_out capability.
//
// The tool enforces the boring-but-important hygiene that ad-hoc
// net/http callers tend to skip:
//
//   - Content-Type is always application/json on egress (and Accept too).
//   - Timeout defaults to 30s; context cancellation always wins.
//   - Body is read with a hard cap so a runaway endpoint can't pin RAM.
//   - The raw bytes are returned as json.RawMessage so callers decide
//     how to unmarshal — this keeps the tool schema-free at the kernel
//     layer (real schema enforcement is the Agent's job).
package jsonapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/soyaos/soyaos/pkg/tooling"
)

// ToolName is the canonical registry name.
const ToolName = "tool.json_api"

// defaultTimeout is applied when the caller's context has no deadline.
const defaultTimeout = 30 * time.Second

// maxResponseBytes caps the body Read budget — 4 MiB suffices for any
// realistic JSON response and prevents pathological endpoints from
// exhausting Kernel memory.
const maxResponseBytes = 4 << 20

// Tool is the json_api tool handle.
type Tool struct {
	Client *http.Client
}

// Input describes one JSON HTTP request.
type Input struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    any
}

// Output is the parsed response. Body is raw JSON; callers Unmarshal
// it themselves so this tool stays schema-agnostic.
type Output struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

// Name implements the informal name() contract used by the tooling layer.
func (t *Tool) Name() string { return ToolName }

// Invoke executes the call described by in.
func (t *Tool) Invoke(ctx context.Context, in Input) (*Output, error) {
	if in.URL == "" {
		return nil, errors.New("jsonapi: empty URL")
	}
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
	default:
		return nil, fmt.Errorf("jsonapi: unsupported method %q", method)
	}

	client := t.Client
	if client == nil {
		client = http.DefaultClient
	}

	// Only impose our own deadline if the caller didn't supply one.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	var bodyReader io.Reader
	if in.Body != nil {
		buf, err := json.Marshal(in.Body)
		if err != nil {
			return nil, fmt.Errorf("jsonapi: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, in.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("jsonapi: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range in.Headers {
		// Caller cannot override Content-Type — that's a kernel invariant.
		if strings.EqualFold(k, "content-type") {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jsonapi: %s %s: %w", method, in.URL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("jsonapi: read body: %w", err)
	}

	// Empty body is allowed (DELETE 204 etc.) — leave Body as nil-equivalent.
	out := &Output{Status: resp.StatusCode}
	if len(bytes.TrimSpace(raw)) > 0 {
		// Validate it's well-formed JSON so callers can rely on the
		// raw field always being parseable.
		if !json.Valid(raw) {
			return nil, fmt.Errorf("jsonapi: response not valid JSON (status %d)", resp.StatusCode)
		}
		out.Body = raw
	}
	return out, nil
}

// Builtin returns the tooling.Tool descriptor used by the kernel registry.
func Builtin() tooling.Tool {
	t := &Tool{}
	return tooling.Tool{
		Name:        ToolName,
		Description: "Generic JSON HTTP call. application/json on egress; 30s timeout; body capped at 4 MiB.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"method":  map[string]any{"type": "string", "enum": []any{"GET", "POST", "PUT", "DELETE", "PATCH"}},
				"url":     map[string]any{"type": "string"},
				"headers": map[string]any{"type": "object"},
				"body":    map[string]any{},
			},
			"required": []any{"url"},
		},
		OutputType: "application/json",
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in := Input{}
			if v, ok := input["method"].(string); ok {
				in.Method = v
			}
			if v, ok := input["url"].(string); ok {
				in.URL = v
			}
			if v, ok := input["headers"].(map[string]any); ok {
				in.Headers = map[string]string{}
				for k, val := range v {
					if s, ok := val.(string); ok {
						in.Headers[k] = s
					}
				}
			}
			if v, ok := input["body"]; ok {
				in.Body = v
			}
			return t.Invoke(ctx, in)
		},
	}
}
