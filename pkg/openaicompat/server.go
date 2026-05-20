// Package openaicompat implements the SoyaOS OpenAI-Compat Gateway (DD-005).
//
// It exposes the three endpoints required for "paste base_url and it works"
// onboarding:
//
//   GET  /v1/models                — lists registered Agents as virtual models
//   POST /v1/chat/completions     — non-stream + SSE streaming
//   POST /v1/responses            — minimal Responses API (echoes the chat path)
//
// Auth is by Bearer token in the canonical "sk-soya-..." format, resolved by
// pkg/auth. The kernel handles the actual completion.
//
// What this gateway intentionally does NOT do in v0.1.0-alpha.0:
//   - tool_calls / function calling (DD-005 marks it for v0.1.1)
//   - usage accounting / quota enforcement
//   - rate limiting
//   - Responses API beyond the simple "send a message → get text back" shape
package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/kernel"
	"github.com/soyaos/soyaos/pkg/llmcall"
)

// Server is an http.Handler wiring kernel + auth into the /v1/* surface.
type Server struct {
	Kernel   *kernel.Kernel
	Verifier auth.Verifier

	// RowTokens is an optional signer that accepts row-scoped JWTs on the
	// per-row Action endpoint (DD-019 / APP-503). When nil, only standard
	// sk-soya keys are accepted.
	RowTokens *auth.RowTokenSigner
}

// NewServer constructs a gateway handler.
func NewServer(k *kernel.Kernel, v auth.Verifier) *Server {
	return &Server{Kernel: k, Verifier: v}
}

// Handler returns an http.Handler that owns /v1/*.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("/v1/responses", s.handleResponses)
	// Per-row Action trigger (DD-010 / APP-502). The fall-through
	// "/v1/agents/" prefix catches the parameterised path; the dispatcher
	// parses {slug} and {action_id} out of the URL.
	mux.HandleFunc("/v1/agents/", s.handleAgentAction)
	return mux
}

// --- Models ----------------------------------------------------------------

type modelRow struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
	Created int64  `json:"created"`
}

type modelsResp struct {
	Object string     `json:"object"`
	Data   []modelRow `json:"data"`
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if _, err := s.authorize(r); err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_api_key", err.Error())
		return
	}
	rows := make([]modelRow, 0)
	for _, a := range s.Kernel.List() {
		rows = append(rows, modelRow{
			ID:      a.ModelID(),
			Object:  "model",
			OwnedBy: "soyaos",
			Created: time.Now().Unix(),
		})
	}
	writeJSON(w, http.StatusOK, modelsResp{Object: "list", Data: rows})
}

// --- Chat completions ------------------------------------------------------

type chatReq struct {
	Model       string           `json:"model"`
	Messages    []chatReqMessage `json:"messages"`
	Stream      bool             `json:"stream,omitempty"`
	Temperature float32          `json:"temperature,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
}

type chatReqMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

type chatChoice struct {
	Index        int             `json:"index"`
	Message      *chatReqMessage `json:"message,omitempty"`
	Delta        *chatDelta      `json:"delta,omitempty"`
	FinishReason *string         `json:"finish_reason"`
}

// chatDelta is the streaming-only counterpart to chatReqMessage. The OpenAI
// SSE contract emits `role: "assistant"` only on the FIRST chunk, omits both
// role and content fields on subsequent chunks (sending only the new content
// delta), and emits an empty delta `{}` on the final chunk that carries
// finish_reason. Strict clients (Cherry Studio's Zod validator, the OpenAI
// Node SDK type guards) reject `role: ""` because the schema's role enum is
// `["assistant","user","system","tool","developer"]` — empty string is not
// a member. Keeping all fields as ,omitempty so a zero-value chatDelta
// marshals to `{}` and only populated fields appear on the wire.
type chatDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type chatResp struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	id, err := s.authorize(r)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_api_key", err.Error())
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method)
		return
	}
	var req chatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", err.Error())
		return
	}
	if req.Model == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_model", "request.model is required")
		return
	}

	gwReq := llmcall.Request{
		Model:       req.Model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
	}
	for _, m := range req.Messages {
		gwReq.Messages = append(gwReq.Messages, llmcall.Message{Role: m.Role, Content: m.Content, Name: m.Name})
	}

	if req.Stream {
		s.streamChat(w, r.Context(), id, gwReq)
		return
	}

	resp, err := s.Kernel.ChatCompletion(r.Context(), id, gwReq)
	if err != nil {
		s.handleKernelError(w, err)
		return
	}
	finish := "stop"
	writeJSON(w, http.StatusOK, chatResp{
		ID:      newID("chatcmpl"),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   resp.Model,
		Choices: []chatChoice{{
			Index:        0,
			Message:      &chatReqMessage{Role: "assistant", Content: resp.Content},
			FinishReason: &finish,
		}},
	})
}

func (s *Server) streamChat(w http.ResponseWriter, ctx context.Context, id auth.Identity, req llmcall.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "stream_unsupported", "response writer does not support streaming")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	chunkID := newID("chatcmpl")
	created := time.Now().Unix()

	out := make(chan llmcall.Chunk, 8)
	errCh := make(chan error, 1)
	go func() { errCh <- s.Kernel.ChatCompletionStream(ctx, id, req, out); close(out) }()

	first := true
	upstreamFinishReason := ""
	for c := range out {
		if c.Done {
			upstreamFinishReason = c.FinishReason
			break
		}
		delta := &chatDelta{Content: c.Delta}
		if first {
			delta.Role = "assistant"
			first = false
		}
		frame := chatResp{
			ID:      chunkID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   req.Model,
			Choices: []chatChoice{{
				Index: 0,
				Delta: delta,
			}},
		}
		if err := writeSSE(w, flusher, frame); err != nil {
			return
		}
	}

	// The goroutine closes `out` AFTER writing to errCh, so the err is ready
	// by the time the loop above exits. Read it before deciding what tail
	// frame to send: upstream errors must reach the client (via a structured
	// error frame) instead of being masked by a finish_reason="stop" tail.
	streamErr := <-errCh

	if streamErr != nil {
		// Error path: send a structured error frame compatible with OpenAI
		// streaming conventions (the error object lives at the top level so
		// SDKs that check `frame.error` see it), then a finish_reason="error"
		// tail, then [DONE] so well-behaved SSE readers terminate cleanly.
		_ = writeSSE(w, flusher, map[string]any{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   req.Model,
			"error": map[string]any{
				"message": streamErr.Error(),
				"type":    "soyaos_error",
				"code":    "upstream_stream_error",
			},
		})
		finish := "error"
		tail := chatResp{
			ID:      chunkID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   req.Model,
			Choices: []chatChoice{{Index: 0, Delta: &chatDelta{}, FinishReason: &finish}},
		}
		_ = writeSSE(w, flusher, tail)
	} else {
		finish := upstreamFinishReason
		if finish == "" {
			finish = "stop"
		}
		tail := chatResp{
			ID:      chunkID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   req.Model,
			Choices: []chatChoice{{Index: 0, Delta: &chatDelta{}, FinishReason: &finish}},
		}
		_ = writeSSE(w, flusher, tail)
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// --- Responses API (minimal) ----------------------------------------------

type respReq struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type respResp struct {
	ID         string           `json:"id"`
	Object     string           `json:"object"`
	Model      string           `json:"model"`
	Output     []respOutputItem `json:"output"`
	Created    int64            `json:"created"`
}

type respOutputItem struct {
	Type    string           `json:"type"`    // "message"
	Role    string           `json:"role"`    // "assistant"
	Content []respOutputText `json:"content"`
}

type respOutputText struct {
	Type string `json:"type"` // "output_text"
	Text string `json:"text"`
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	id, err := s.authorize(r)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_api_key", err.Error())
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method)
		return
	}
	var req respReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", err.Error())
		return
	}
	if req.Model == "" || req.Input == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_field", "responses requires model + input")
		return
	}
	gwReq := llmcall.Request{
		Model:    req.Model,
		Messages: []llmcall.Message{{Role: "user", Content: req.Input}},
	}
	resp, err := s.Kernel.ChatCompletion(r.Context(), id, gwReq)
	if err != nil {
		s.handleKernelError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, respResp{
		ID:      newID("resp"),
		Object:  "response",
		Model:   resp.Model,
		Created: time.Now().Unix(),
		Output: []respOutputItem{{
			Type:    "message",
			Role:    "assistant",
			Content: []respOutputText{{Type: "output_text", Text: resp.Content}},
		}},
	})
}

// --- helpers ---------------------------------------------------------------

func (s *Server) authorize(r *http.Request) (auth.Identity, error) {
	raw := auth.ExtractBearer(r.Header.Get("Authorization"))
	if raw == "" {
		return auth.Identity{}, errors.New("missing or malformed Authorization header")
	}
	return s.Verifier.Verify(r.Context(), raw)
}

func (s *Server) handleKernelError(w http.ResponseWriter, err error) {
	if errors.Is(err, kernel.ErrUnknownAgent) {
		writeAPIError(w, http.StatusNotFound, "unknown_model", err.Error())
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "kernel_error", err.Error())
}

type apiError struct {
	Error apiErrorBody `json:"error"`
}
type apiErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{Error: apiErrorBody{Message: message, Type: "soyaos_error", Code: code}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeSSE(w http.ResponseWriter, f http.Flusher, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
		return err
	}
	f.Flush()
	return nil
}

// newID returns a short request id with the given prefix. Not crypto-grade —
// it's a correlation aid, not a security token.
func newID(prefix string) string {
	now := time.Now().UnixNano()
	return fmt.Sprintf("%s-%x", prefix, now)
}

// DefaultListenAddr is the canonical default address for the OpenAI-Compat
// gateway. Locked by specs/cli/v0.md: localhost-by-default for Solo so the
// gateway is not reachable from other machines on the LAN without explicit
// reconfiguration. The 7474 port matches Studio's default (specs/cli/v0.md).
const DefaultListenAddr = "127.0.0.1:7474"

// SupportedPaths is the canonical list of HTTP paths this server owns —
// useful for callers that want to mount it alongside their own routes.
var SupportedPaths = []string{
	"/v1/models",
	"/v1/chat/completions",
	"/v1/responses",
	"/v1/agents/{slug}/actions/{action_id}",
}

// PathsString is the SupportedPaths list joined with ", " for human display.
func PathsString() string { return strings.Join(SupportedPaths, ", ") }
