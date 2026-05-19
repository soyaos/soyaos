// Package modelgw is the Model Gateway — the LLM-call collection layer.
//
// The architecture spec calls out three modes: BYOK, platform-managed, and
// private vLLM. v0.1.0-alpha.0 ships only an echo Provider used by the
// reference Agent (`soya:echo`) so that the OpenAI-Compat smoke test works
// without any external LLM credentials.
//
// Real Providers (OpenAI, Anthropic, vLLM, local Ollama, etc.) land in
// later milestones as the dispatcher grows real model-call paths.
package modelgw

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

// Chunk is a streamed delta.
type Chunk struct {
	Delta string
	Done  bool
}

// Provider executes a single Request.
type Provider interface {
	Name() string
	Generate(ctx context.Context, req Request) (Response, error)
	GenerateStream(ctx context.Context, req Request, out chan<- Chunk) error
}

// ErrUnknownModel is returned when no Provider is registered for a model id.
var ErrUnknownModel = errors.New("modelgw: unknown model")

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
	case out <- Chunk{Done: true}:
	}
	return nil
}
