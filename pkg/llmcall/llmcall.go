// Package llmcall is the LLM call layer behind the OpenAI-Compat Gateway.
//
// The architecture spec calls out three modes: BYOK, platform-managed, and
// private vLLM. v0.1.0-alpha.0 ships only an echo Provider used by the
// reference Agent (`soya:echo`) so that the OpenAI-Compat smoke test works
// without any external LLM credentials.
//
// Real Providers (OpenAI, Anthropic, vLLM, local Ollama, etc.) land in
// later milestones as the dispatcher grows real model-call paths.
package llmcall

import (
	"context"
	"errors"
)

// Message is one entry in a chat conversation.
type Message struct {
	Role    string // "system" / "user" / "assistant" / "tool"
	Content string
	Name    string // tool name or speaker label, optional
}

// Request is a chat-completion call.
type Request struct {
	Model       string // canonical virtual model id, e.g. "soya:echo"
	Messages    []Message
	Temperature float32
	MaxTokens   int
	Stream      bool
}

// Response is a non-streamed chat-completion result.
type Response struct {
	Model        string
	Content      string
	InputTokens  int
	OutputTokens int
}

// Chunk is a streamed delta. Done==true marks the end-of-stream sentinel
// (per llmcall convention; OpenAI uses an explicit `finish_reason` field on
// the last chunk, which we surface here so callers can distinguish a
// natural stop from "length" / "content_filter" / "error".
//
// When Done is true, FinishReason holds the upstream's value verbatim
// (`stop` / `length` / `content_filter` / `error` / ...) — or the empty
// string when the upstream did not provide one (e.g. the Echo Provider).
type Chunk struct {
	Delta        string
	Done         bool
	FinishReason string
}

// Provider executes a single Request.
type Provider interface {
	Name() string
	Generate(ctx context.Context, req Request) (Response, error)
	GenerateStream(ctx context.Context, req Request, out chan<- Chunk) error
}

// ErrUnknownModel is returned when no Provider is registered for a model id.
var ErrUnknownModel = errors.New("llmcall: unknown model")

// Echo is the smoke-test Provider: it returns the user's last message
// reversed-prefixed with "echo: ". Useful for verifying the OpenAI-Compat
// path end-to-end without external dependencies.
type Echo struct{}

// Name implements Provider.
func (Echo) Name() string { return "echo" }

// Generate returns the last user message with a fixed prefix.
func (Echo) Generate(_ context.Context, req Request) (Response, error) {
	var last string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			last = req.Messages[i].Content
			break
		}
	}
	if last == "" {
		last = "(no user message)"
	}
	return Response{Model: req.Model, Content: "echo: " + last}, nil
}

// GenerateStream emits the echoed message as a single chunk followed by Done.
func (e Echo) GenerateStream(ctx context.Context, req Request, out chan<- Chunk) error {
	resp, err := e.Generate(ctx, req)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case out <- Chunk{Delta: resp.Content}:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case out <- Chunk{Done: true, FinishReason: "stop"}:
	}
	return nil
}
