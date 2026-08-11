// actions_test.go covers the per-row Action trigger endpoint
// (DD-010 / APP-502): registered Agent + manifest → 200, unknown
// action id → 404, malformed body → 400.
package openaicompat

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/kernel"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

// newActionTestServer registers an Agent backed by EchoAgent's handler
// plus a manifest with two ActionDecls.
func newActionTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	k := kernel.New()
	agent := kernel.EchoAgent
	agent.Manifest = &soyapack.Manifest{
		Kind: soyapack.KindAgent,
		Name: "echo",
		Actions: []soyapack.ActionDecl{
			{ID: "star", On: "per_row", Handler: "prompts/star.md"},
			{ID: "refresh", On: "per_row", Handler: "prompts/refresh.md"},
		},
	}
	k.Register(agent)
	store := auth.NewMemoryStore()
	key := store.SeedDevKey()
	srv := httptest.NewServer(NewServer(k, store).Handler())
	t.Cleanup(srv.Close)
	return srv, key
}

func TestActions_DispatchesKnownAction(t *testing.T) {
	srv, key := newActionTestServer(t)

	body := strings.NewReader(`{"row_id":"row-42","payload":{"hint":"trending"}}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/agents/echo/actions/star", body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
	var out kernel.ActionResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.TaskID == "" {
		t.Fatalf("expected non-empty TaskID, got %+v", out)
	}
	if out.AgentSlug != "echo" || out.ActionID != "star" || out.RowID != "row-42" {
		t.Fatalf("wrong identity in response: %+v", out)
	}
	if out.Status != "queued" {
		t.Fatalf("Status = %q, want queued", out.Status)
	}
}

func TestActions_UnknownActionReturns404(t *testing.T) {
	srv, key := newActionTestServer(t)
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/agents/echo/actions/does-not-exist",
		bytes.NewBufferString(`{"row_id":"r1"}`))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
}

func TestActions_UnknownAgentReturns404(t *testing.T) {
	srv, key := newActionTestServer(t)
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/agents/no-such-agent/actions/star",
		bytes.NewBufferString(`{"row_id":"r1"}`))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
}

func TestActions_MissingRowIDReturns400(t *testing.T) {
	srv, key := newActionTestServer(t)
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/agents/echo/actions/star",
		bytes.NewBufferString(`{"payload":{}}`))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
}

func TestActions_RequiresAuth(t *testing.T) {
	srv, _ := newActionTestServer(t)
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/agents/echo/actions/star",
		bytes.NewBufferString(`{"row_id":"r1"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}

func TestActions_MalformedPath(t *testing.T) {
	srv, key := newActionTestServer(t)
	// `/v1/agents/echo/foo/bar` is structurally wrong (no "actions" segment).
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/agents/echo/foo/bar",
		bytes.NewBufferString(`{"row_id":"r1"}`))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
}

func TestActions_AcceptsRowToken(t *testing.T) {
	// Build a server with a RowTokens signer wired up.
	k := kernel.New()
	a := kernel.EchoAgent
	a.Manifest = &soyapack.Manifest{
		Kind:    soyapack.KindAgent,
		Name:    "echo",
		Actions: []soyapack.ActionDecl{{ID: "star", On: "per_row", Handler: "p"}},
	}
	k.Register(a)
	store := auth.NewMemoryStore()
	signer := auth.NewRowTokenSigner([]byte("test-secret-padding-padding-pad"))
	s := &Server{Kernel: k, Verifier: store, RowTokens: signer}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	tok, err := signer.Mint("echo", "star", "row-42", "sk-soya-abcd1234", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/agents/echo/actions/star",
		bytes.NewBufferString(`{"row_id":"row-42"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}
}

func TestActions_RowTokenForDifferentRowRejected(t *testing.T) {
	k := kernel.New()
	a := kernel.EchoAgent
	a.Manifest = &soyapack.Manifest{
		Kind:    soyapack.KindAgent,
		Name:    "echo",
		Actions: []soyapack.ActionDecl{{ID: "star", On: "per_row", Handler: "p"}},
	}
	k.Register(a)
	store := auth.NewMemoryStore()
	signer := auth.NewRowTokenSigner([]byte("test-secret-padding-padding-pad"))
	s := &Server{Kernel: k, Verifier: store, RowTokens: signer}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// Token issued for row-42 but caller posts row-99 → 401.
	tok, _ := signer.Mint("echo", "star", "row-42", "sk-soya-abcd", time.Hour)
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/v1/agents/echo/actions/star",
		bytes.NewBufferString(`{"row_id":"row-99"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}

func TestParseAgentActionPath(t *testing.T) {
	tests := []struct {
		path   string
		ok     bool
		slug   string
		action string
	}{
		{"/v1/agents/foo/actions/bar", true, "foo", "bar"},
		{"/v1/agents/foo/actions/bar/baz", true, "foo", "bar/baz"},
		{"/v1/agents/foo/bar/baz", false, "", ""},
		{"/v1/agents//actions/bar", false, "", ""},
		{"/v1/agents/foo/actions/", false, "", ""},
		{"/v1/something/else", false, "", ""},
	}
	for _, tt := range tests {
		s, a, ok := parseAgentActionPath(tt.path)
		if ok != tt.ok || s != tt.slug || a != tt.action {
			t.Errorf("parseAgentActionPath(%q) = (%q, %q, %v); want (%q, %q, %v)", tt.path, s, a, ok, tt.slug, tt.action, tt.ok)
		}
	}
}
