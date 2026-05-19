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
	"github.com/soyaos/soyaos/pkg/modelgw"
)

// Server is an http.Handler wiring kernel + auth into the /v1/* surface.
type Server struct {
	Kernel   *kernel.Kernel
	Verifier auth.Verifier
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
	Delta        *chatReqMessage `json:"delta,omitempty"`
	FinishReason *string         `json:"finish_reason"`
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

	gwReq := modelgw.Request{
		Model:       req.Model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
	}
	for _, m := range req.Messages {
		gwReq.Messages = append(gwReq.Messages, modelgw.Message{Role: m.Role, Content: m.Content, Name: m.Name})
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

func (s *Server) streamChat(w http.ResponseWriter, ctx context.Context, id auth.Identity, req modelgw.Request) {
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

	out := make(chan modelgw.Chunk, 8)
	errCh := make(chan error, 1)
	go func() { errCh <- s.Kernel.ChatCompletionStream(ctx, id, req, out); close(out) }()

	first := true
	for c := range out {
		if c.Done {
			break
		}
		var role string
		if first {
			role = "assistant"
			first = false
		}
		frame := chatResp{
			ID:      chunkID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   req.Model,
			Choices: []chatChoice{{
				Index: 0,
				Delta: &chatReqMessage{Role: role, Content: c.Delta},
			}},
		}
		if err := writeSSE(w, flusher, frame); err != nil {
			return
		}
	}
	finish := "stop"
	tail := chatResp{
		ID:      chunkID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   req.Model,
		Choices: []chatChoice{{Index: 0, Delta: &chatReqMessage{}, FinishReason: &finish}},
	}
	_ = writeSSE(w, flusher, tail)
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()

	if err := <-errCh; err != nil {
		// Stream already started — log via trailer-style data frame.
		_ = writeSSE(w, flusher, map[string]any{"error": err.Error()})
	}
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
	gwReq := modelgw.Request{
		Model:    req.Model,
		Messages: []modelgw.Message{{Role: "user", Content: req.Input}},
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

// MustListenAddr returns the canonical default OpenAI-Compat listen address.
// The 6473 port reads as "SO-Y-S" on a phone keypad.
const DefaultListenAddr = ":6473"

// SupportedPaths is the canonical list of HTTP paths this server owns —
// useful for callers that want to mount it alongside their own routes.
var SupportedPaths = []string{"/v1/models", "/v1/chat/completions", "/v1/responses"}

// PathsString is the SupportedPaths list joined with ", " for human display.
func PathsString() string { return strings.Join(SupportedPaths, ", ") }
