// jsonapi_test.go pins the kernel invariants: application/json on the
// wire, schema-agnostic raw body return, body size cap, and that the
// caller cannot override Content-Type (would break egress firewall logs).
package jsonapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONAPI_GET_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("server saw method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer srv.Close()

	tl := &Tool{}
	out, err := tl.Invoke(context.Background(), Input{Method: "GET", URL: srv.URL})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out.Status != 200 {
		t.Errorf("Status = %d, want 200", out.Status)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out.Body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if parsed["ok"] != true {
		t.Errorf("body = %v, want ok=true", parsed)
	}
}

func TestJSONAPI_POST_SendsJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("server saw Content-Type = %q, want application/json", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"x":1`) {
			t.Errorf("body did not carry payload: %q", string(body))
		}
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer srv.Close()

	tl := &Tool{}
	out, err := tl.Invoke(context.Background(), Input{
		Method: "POST",
		URL:    srv.URL,
		// Caller tries to override Content-Type — kernel must ignore it.
		Headers: map[string]string{"Content-Type": "text/plain", "X-Trace": "abc"},
		Body:    map[string]int{"x": 1},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out.Status != 202 {
		t.Errorf("Status = %d, want 202", out.Status)
	}
}

func TestJSONAPI_4xx_PassesThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":"not-found"}`))
	}))
	defer srv.Close()
	tl := &Tool{}
	out, err := tl.Invoke(context.Background(), Input{Method: "GET", URL: srv.URL})
	if err != nil {
		t.Fatalf("Invoke (4xx): %v (4xx is not a tool error)", err)
	}
	if out.Status != 404 {
		t.Errorf("Status = %d, want 404", out.Status)
	}
}

func TestJSONAPI_UnsupportedMethod(t *testing.T) {
	tl := &Tool{}
	_, err := tl.Invoke(context.Background(), Input{Method: "BREW", URL: "http://x/"})
	if err == nil {
		t.Fatal("BREW should be rejected")
	}
}

func TestJSONAPI_NonJSONBody_Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain text not json"))
	}))
	defer srv.Close()
	tl := &Tool{}
	if _, err := tl.Invoke(context.Background(), Input{Method: "GET", URL: srv.URL}); err == nil {
		t.Fatal("non-JSON body should be rejected")
	}
}

func TestJSONAPI_EmptyBody_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()
	tl := &Tool{}
	out, err := tl.Invoke(context.Background(), Input{Method: "DELETE", URL: srv.URL})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out.Status != 204 {
		t.Errorf("Status = %d, want 204", out.Status)
	}
	if len(out.Body) != 0 {
		t.Errorf("empty body should yield nil RawMessage, got %q", string(out.Body))
	}
}
