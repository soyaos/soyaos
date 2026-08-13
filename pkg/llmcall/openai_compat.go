package llmcall

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// OpenAICompat is the BYOK upstream Provider used when the operator sets
// SOYA_MODEL_API_KEY (plus optionally BASE_URL / DEFAULT). It speaks the
// OpenAI /v1/chat/completions surface verbatim, which lets one Provider
// cover the entire compatible ecosystem — OpenAI itself, Azure OpenAI,
// DeepSeek, Moonshot, Groq, Together, Fireworks, Ollama, vLLM, LM Studio.
//
// Stage 5 will add per-Agent overrides via SoyaPack `prompt.upstream`; for
// alpha the env three-tuple is the single source of truth.
type OpenAICompat struct {
	Cfg    Config
	Client *http.Client // nil → defaultClient
}

// defaultClient is reused across calls to amortize TLS handshakes. We
// deliberately do NOT set http.Client.Timeout — that field caps the entire
// request including body read, which truncates long generations (e.g. a
// 5000-token HTML report from qwen3.6-plus easily blows past 90s). Caller
// owns the deadline via context.Context, propagated by NewRequestWithContext.
// The connection-level safety nets below keep stuck TCP from leaking
// forever.
var defaultClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second, // connect timeout
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// ResponseHeaderTimeout is the wall clock between request send and
		// the upstream's first response byte. Streaming endpoints answer in
		// 1-2s, but a non-streaming /chat/completions on DashScope qwen-plus
		// can wait until the *whole* 2-3 KB JSON is generated before
		// flushing headers — that's 60-120s. The chain runner runs all
		// intermediate stages streaming-internally (pack_agent.streamCollect)
		// to avoid this, but we keep a generous header timeout as the
		// belt-and-braces fallback for direct non-streaming Generate calls.
		ResponseHeaderTimeout: 180 * time.Second,
	},
}

// Name implements Provider. The host of the base URL is informative for logs
// without leaking the API key.
func (p OpenAICompat) Name() string {
	if p.Cfg.BaseURL == "" {
		return "openai-compat"
	}
	return "openai-compat(" + hostOf(p.Cfg.BaseURL) + ")"
}

// Generate posts a non-stream chat completion to <base_url>/chat/completions.
// The request body is the standard OpenAI shape; only Model / Messages /
// Temperature / MaxTokens / ResponseFormat are forwarded since those are
// what pkg/llmcall.Request exposes today.
func (p OpenAICompat) Generate(ctx context.Context, req Request) (Response, error) {
	body, err := p.buildBody(req, false)
	if err != nil {
		return Response{}, err
	}
	httpReq, err := p.buildRequest(ctx, body)
	if err != nil {
		return Response{}, err
	}
	resp, err := p.client().Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("llmcall: upstream request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Response{}, fmt.Errorf("llmcall: upstream %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var decoded struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Response{}, fmt.Errorf("llmcall: decode upstream response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return Response{}, fmt.Errorf("llmcall: upstream returned no choices")
	}
	return Response{
		Model:        firstNonEmpty(decoded.Model, p.resolvedModel(req)),
		Content:      decoded.Choices[0].Message.Content,
		InputTokens:  decoded.Usage.PromptTokens,
		OutputTokens: decoded.Usage.CompletionTokens,
	}, nil
}

// GenerateStream walks the OpenAI SSE format line by line, forwarding each
// `choices[0].delta.content` chunk to `out` and closing with {Done:true} when
// the upstream emits `data: [DONE]`. Errors from the network bubble up; SSE
// frames that fail to parse are skipped (matches OpenAI SDK behavior).
func (p OpenAICompat) GenerateStream(ctx context.Context, req Request, out chan<- Chunk) error {
	body, err := p.buildBody(req, true)
	if err != nil {
		return err
	}
	httpReq, err := p.buildRequest(ctx, body)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := p.client().Do(httpReq)
	if err != nil {
		return fmt.Errorf("llmcall: upstream stream request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("llmcall: upstream %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var finishReason string
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		var frame struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &frame); err != nil {
			continue
		}
		if len(frame.Choices) == 0 {
			continue
		}
		choice := frame.Choices[0]
		// Content chunks come first; the terminal chunk has empty delta.content
		// AND a non-null finish_reason — capture both so the gateway can
		// forward the upstream's true reason ("length" / "content_filter" /
		// "error") to the client instead of masking it as "stop".
		if choice.Delta.Content != "" {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case out <- Chunk{Delta: choice.Delta.Content}:
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			finishReason = *choice.FinishReason
		}
	}
	if err := scanner.Err(); err != nil {
		// Wrap io.ErrUnexpectedEOF specifically so callers (chain runner,
		// action handler) can errors.Is() and retry it as a transient
		// network condition. Long SSE streams against DashScope reasoning
		// models occasionally get torn down by an intermediate hop while
		// the model is still thinking — there's no application-level
		// signal, just a half-closed TCP. Retry is the right answer.
		if errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "unexpected EOF") {
			return fmt.Errorf("llmcall: read upstream stream: %w", io.ErrUnexpectedEOF)
		}
		return fmt.Errorf("llmcall: read upstream stream: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case out <- Chunk{Done: true, FinishReason: finishReason}:
	}
	return nil
}

func (p OpenAICompat) buildBody(req Request, stream bool) ([]byte, error) {
	msgs := make([]map[string]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}
	payload := map[string]any{
		"model":    p.resolvedModel(req),
		"messages": msgs,
		"stream":   stream,
	}
	if req.Temperature != 0 {
		payload["temperature"] = req.Temperature
	}
	if req.MaxTokens != 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	if req.ResponseFormat != "" {
		payload["response_format"] = map[string]string{"type": req.ResponseFormat}
	}
	// enable_thinking is intentionally opt-in. It is supported by several
	// OpenAI-compatible vendors but is not part of the OpenAI standard; an
	// unset config must therefore omit the field rather than send false.
	if p.Cfg.EnableThinking != nil {
		payload["enable_thinking"] = *p.Cfg.EnableThinking
	}
	return json.Marshal(payload)
}

func (p OpenAICompat) buildRequest(ctx context.Context, body []byte) (*http.Request, error) {
	url := strings.TrimRight(p.Cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.Cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// resolvedModel picks the upstream model id. SoyaOS virtual model ids look
// like `soya:llm`; the upstream needs a real id (`gpt-4o-mini`,
// `deepseek-chat`, …). When the caller's model is a soya:* virtual id we
// substitute Cfg.Model; otherwise we pass the caller's choice through, so a
// future manifest override or test rig can target a specific upstream model.
func (p OpenAICompat) resolvedModel(req Request) string {
	if strings.HasPrefix(req.Model, "soya:") || req.Model == "" {
		return p.Cfg.Model
	}
	return req.Model
}

func (p OpenAICompat) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return defaultClient
}

func hostOf(rawurl string) string {
	s := strings.TrimPrefix(rawurl, "https://")
	s = strings.TrimPrefix(s, "http://")
	if i := strings.IndexAny(s, "/?"); i >= 0 {
		s = s[:i]
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
