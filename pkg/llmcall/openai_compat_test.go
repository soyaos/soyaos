package llmcall

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoadConfigFromEnvDefaults(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
	cfg := LoadConfigFromEnv()
	if cfg.Configured() {
		t.Errorf("empty env must yield Configured=false")
	}
	if cfg.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL default = %q, want %q", cfg.BaseURL, DefaultBaseURL)
	}
	if cfg.Model != DefaultModel {
		t.Errorf("Model default = %q, want %q", cfg.Model, DefaultModel)
	}

	t.Setenv(EnvAPIKey, "sk-test")
	t.Setenv(EnvBaseURL, "https://api.deepseek.com/v1")
	t.Setenv(EnvModel, "deepseek-chat")
	cfg = LoadConfigFromEnv()
	if !cfg.Configured() {
		t.Errorf("API key set must yield Configured=true")
	}
	if cfg.BaseURL != "https://api.deepseek.com/v1" || cfg.Model != "deepseek-chat" {
		t.Errorf("env overrides not applied: %+v", cfg)
	}
}

func TestOpenAICompatGenerateRoundtrip(t *testing.T) {
	var seenAuth, seenPath, seenModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenPath = r.URL.Path
		var body struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role, Content string
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		seenModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
            "model":"`+body.Model+`",
            "choices":[{"message":{"content":"pong"}}],
            "usage":{"prompt_tokens":3,"completion_tokens":1}
        }`)
	}))
	defer srv.Close()

	p := OpenAICompat{Cfg: Config{APIKey: "sk-test", BaseURL: srv.URL, Model: "gpt-4o-mini"}}
	resp, err := p.Generate(context.Background(), Request{
		Model:    "soya:llm",
		Messages: []Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Content != "pong" {
		t.Errorf("content = %q, want pong", resp.Content)
	}
	if seenAuth != "Bearer sk-test" {
		t.Errorf("auth header = %q", seenAuth)
	}
	if seenPath != "/chat/completions" {
		t.Errorf("path = %q", seenPath)
	}
	if seenModel != "gpt-4o-mini" {
		t.Errorf("virtual model id should be rewritten to upstream model, got %q", seenModel)
	}
	if resp.InputTokens != 3 || resp.OutputTokens != 1 {
		t.Errorf("usage = (%d,%d)", resp.InputTokens, resp.OutputTokens)
	}
}

func TestOpenAICompatGenerateNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := OpenAICompat{Cfg: Config{APIKey: "sk-bad", BaseURL: srv.URL, Model: "x"}}
	_, err := p.Generate(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("Generate must return error on non-2xx")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("error must include status + body, got %v", err)
	}
}

func TestOpenAICompatGenerateStreamSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, payload := range []string{
			`{"choices":[{"delta":{"content":"hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
			`{"choices":[{"delta":{}}]}`,
			`{"choices":[{"delta":{"content":"!"}}]}`,
		} {
			_, _ = io.WriteString(w, "data: "+payload+"\n\n")
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := OpenAICompat{Cfg: Config{APIKey: "sk-test", BaseURL: srv.URL, Model: "x"}}
	out := make(chan Chunk, 8)
	go func() {
		defer close(out)
		if err := p.GenerateStream(context.Background(), Request{
			Messages: []Message{{Role: "user", Content: "say hi"}},
		}, out); err != nil {
			t.Errorf("GenerateStream: %v", err)
		}
	}()

	var got strings.Builder
	var sawDone bool
	for c := range out {
		if c.Done {
			sawDone = true
			continue
		}
		got.WriteString(c.Delta)
	}
	if !sawDone {
		t.Error("must emit Done:true at end of stream")
	}
	if got.String() != "hello!" {
		t.Errorf("assembled stream = %q, want %q", got.String(), "hello!")
	}
}

func TestOpenAICompatResolvedModelPassesThroughRealNames(t *testing.T) {
	p := OpenAICompat{Cfg: Config{Model: "default-fallback"}}
	cases := []struct {
		in, want string
	}{
		{"", "default-fallback"},
		{"soya:llm", "default-fallback"},
		{"soya:compo", "default-fallback"},
		{"gpt-4o-mini", "gpt-4o-mini"},
		{"deepseek-chat", "deepseek-chat"},
	}
	for _, c := range cases {
		if got := p.resolvedModel(Request{Model: c.in}); got != c.want {
			t.Errorf("resolvedModel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOpenAICompatBuildBodyOptionalThinking(t *testing.T) {
	request := Request{
		Messages:       []Message{{Role: "user", Content: "return JSON"}},
		ResponseFormat: "json_object",
	}

	plain := OpenAICompat{Cfg: Config{Model: "x"}}
	body, err := plain.buildBody(request, false)
	if err != nil {
		t.Fatalf("buildBody plain: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode plain body: %v", err)
	}
	if _, exists := payload["enable_thinking"]; exists {
		t.Fatalf("unset thinking config must omit vendor extension: %s", body)
	}
	format, ok := payload["response_format"].(map[string]any)
	if !ok || format["type"] != "json_object" {
		t.Fatalf("response_format = %#v, want json_object", payload["response_format"])
	}

	disabled := false
	withFlag := OpenAICompat{Cfg: Config{Model: "x", EnableThinking: &disabled}}
	body, err = withFlag.buildBody(request, true)
	if err != nil {
		t.Fatalf("buildBody flagged: %v", err)
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode flagged body: %v", err)
	}
	got, exists := payload["enable_thinking"]
	if !exists || got != false {
		t.Fatalf("enable_thinking = %#v (exists=%v), want false", got, exists)
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://api.openai.com/v1":   "api.openai.com",
		"http://localhost:11434/v1":   "localhost:11434",
		"https://api.deepseek.com/v1": "api.deepseek.com",
		"https://x.com":               "x.com",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}
