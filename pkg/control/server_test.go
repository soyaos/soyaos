package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/soyaos/soyaos/pkg/kernel"
)

func newTestControl(t *testing.T) *httptest.Server {
	t.Helper()
	k := kernel.New()
	k.Register(kernel.EchoAgent)
	srv := httptest.NewServer(NewServer(k).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestHealthz_ReturnsPlainOK(t *testing.T) {
	srv := newTestControl(t)
	resp, err := http.Get(srv.URL + "/control/v0/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != "ok" {
		t.Fatalf("body=%q, want plain 'ok'", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type=%q, want text/plain", ct)
	}
}

func TestAgents_ListsEcho(t *testing.T) {
	srv := newTestControl(t)
	resp, err := http.Get(srv.URL + "/control/v0/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var out agentsResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Object != "list" || len(out.Data) != 1 || out.Data[0].ID != "soya:echo" {
		t.Fatalf("unexpected payload: %+v", out)
	}
	if out.Data[0].Slug != "echo" {
		t.Fatalf("expected slug=echo, got %q", out.Data[0].Slug)
	}
}

func TestInvoke_RoundTripsEcho(t *testing.T) {
	srv := newTestControl(t)
	body := strings.NewReader(`{"prompt":"ping"}`)
	resp, err := http.Post(srv.URL+"/control/v0/agents/echo/invoke", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var out invokeResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "echo: ping") {
		t.Fatalf("content=%q", out.Content)
	}
	if out.Slug != "echo" || out.Model != "soya:echo" {
		t.Fatalf("identity wrong: %+v", out)
	}
}

func TestInvoke_UnknownAgent(t *testing.T) {
	srv := newTestControl(t)
	body := strings.NewReader(`{"prompt":"x"}`)
	resp, err := http.Post(srv.URL+"/control/v0/agents/missing/invoke", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
}

func TestInvoke_MissingPrompt(t *testing.T) {
	srv := newTestControl(t)
	body := strings.NewReader(`{}`)
	resp, err := http.Post(srv.URL+"/control/v0/agents/echo/invoke", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
}

func TestAgentChildPath_BadShape(t *testing.T) {
	srv := newTestControl(t)
	// Missing verb
	resp, err := http.Get(srv.URL + "/control/v0/agents/echo/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
}
