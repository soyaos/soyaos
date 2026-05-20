package openaicompat

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/kernel"
)

func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	k := kernel.New()
	k.Register(kernel.EchoAgent)
	store := auth.NewMemoryStore()
	key := store.SeedDevKey()
	srv := httptest.NewServer(NewServer(k, store).Handler())
	t.Cleanup(srv.Close)
	return srv, key
}

func TestModels_Lists_RegisteredAgents(t *testing.T) {
	srv, key := newTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var out modelsResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 1 || out.Data[0].ID != "soya:echo" {
		t.Fatalf("unexpected models response: %+v", out)
	}
}

func TestModels_RejectsMissingAuth(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}

func TestChatCompletions_NonStream_EchoesUserMessage(t *testing.T) {
	srv, key := newTestServer(t)
	body := strings.NewReader(`{"model":"soya:echo","messages":[{"role":"user","content":"hello world"}]}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var out chatResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message == nil {
		t.Fatalf("no message in response: %+v", out)
	}
	if !strings.Contains(out.Choices[0].Message.Content, "echo: hello world") {
		t.Fatalf("response content = %q", out.Choices[0].Message.Content)
	}
}

func TestChatCompletions_Stream_EmitsSSE(t *testing.T) {
	srv, key := newTestServer(t)
	body := strings.NewReader(`{"model":"soya:echo","stream":true,"messages":[{"role":"user","content":"ping"}]}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, resp.Body); err != nil {
		t.Fatal(err)
	}
	text := buf.String()
	if !strings.Contains(text, "echo: ping") {
		t.Fatalf("stream missing echoed text: %q", text)
	}
	if !strings.Contains(text, "data: [DONE]") {
		t.Fatalf("stream missing [DONE] sentinel: %q", text)
	}
}

// TestChatCompletions_Stream_DeltaSchemaMatchesOpenAI guards against the
// regression Cherry Studio's Zod validator flagged: chunk deltas were
// serializing `"role":""` on every frame, which fails the OpenAI streaming
// schema (role enum is ["assistant","user","system","tool","developer"]).
// The contract:
//   - first chunk's delta carries `"role":"assistant"` exactly once,
//   - subsequent chunks omit the role field entirely (only `"content"`),
//   - the terminal chunk has an empty delta `{}` plus finish_reason.
func TestChatCompletions_Stream_DeltaSchemaMatchesOpenAI(t *testing.T) {
	srv, key := newTestServer(t)
	body := strings.NewReader(`{"model":"soya:echo","stream":true,"messages":[{"role":"user","content":"abc"}]}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	_, _ = io.Copy(buf, resp.Body)

	type frame struct {
		Choices []struct {
			Delta        map[string]any `json:"delta"`
			FinishReason *string        `json:"finish_reason"`
		} `json:"choices"`
	}

	var frames []frame
	for _, line := range strings.Split(buf.String(), "\n") {
		line = strings.TrimPrefix(line, "data: ")
		line = strings.TrimSpace(line)
		if line == "" || line == "[DONE]" {
			continue
		}
		var f frame
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			t.Fatalf("bad frame %q: %v", line, err)
		}
		frames = append(frames, f)
	}
	if len(frames) < 2 {
		t.Fatalf("expected at least 2 frames (content + tail), got %d: %s", len(frames), buf.String())
	}

	// First frame: role must be "assistant"; never the empty string.
	firstDelta := frames[0].Choices[0].Delta
	if role, ok := firstDelta["role"]; !ok || role != "assistant" {
		t.Errorf("first frame delta.role = %v (ok=%v), want \"assistant\"", role, ok)
	}

	// Subsequent content frames must NOT carry a `role` key at all.
	// The strict client rejects role:"" because "" is not in the enum.
	for i := 1; i < len(frames)-1; i++ {
		d := frames[i].Choices[0].Delta
		if _, has := d["role"]; has {
			t.Errorf("frame[%d] delta unexpectedly carries role: %v", i, d)
		}
	}

	// Last frame: empty delta `{}` plus finish_reason="stop".
	lastChoice := frames[len(frames)-1].Choices[0]
	if lastChoice.FinishReason == nil || *lastChoice.FinishReason != "stop" {
		t.Errorf("tail frame missing finish_reason=stop, got %v", lastChoice.FinishReason)
	}
	if len(lastChoice.Delta) != 0 {
		t.Errorf("tail frame delta must be empty object, got %v", lastChoice.Delta)
	}
}

func TestChatCompletions_UnknownModel(t *testing.T) {
	srv, key := newTestServer(t)
	body := strings.NewReader(`{"model":"soya:missing","messages":[{"role":"user","content":"x"}]}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s, want 404", resp.StatusCode, b)
	}
}

func TestResponses_MinimalShape(t *testing.T) {
	srv, key := newTestServer(t)
	body := strings.NewReader(`{"model":"soya:echo","input":"hi"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/responses", body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var out respResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Output) == 0 || len(out.Output[0].Content) == 0 || !strings.Contains(out.Output[0].Content[0].Text, "echo: hi") {
		t.Fatalf("unexpected /v1/responses payload: %+v", out)
	}
}
