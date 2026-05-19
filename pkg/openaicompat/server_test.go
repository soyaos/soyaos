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
