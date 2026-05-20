// Package control implements the Solo control-plane RPC.
//
// The control RPC is what `soyaos agent list` / `agent run` / `agent deploy`
// talk to. It is intentionally separate from the OpenAI-Compat gateway:
//
//   - The OpenAI-Compat gateway (pkg/openaicompat) is the *data plane* —
//     it accepts user prompts and returns Agent output. Its auth is the
//     user's API key.
//   - The control RPC (this package) is the *control plane* — it
//     enumerates Agents, deploys new Packs, manages bindings. Its auth is
//     a localhost loopback assumption: in Solo it binds to 127.0.0.1:7475
//     and trusts the OS user.
//
// As Cluster / Cloud editions land, this server will gain mTLS + capability
// tokens; the Solo loopback-only stance is the alpha shape.
//
// Wire format is JSON over HTTP under /control/v0/* paths. Spec lives in
// soyaos/specs.
package control

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
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/scope"
)

// DefaultListenAddr matches specs/cli/v0.md — localhost loopback on 7475.
const DefaultListenAddr = "127.0.0.1:7475"

// Server is the control-plane HTTP handler.
type Server struct {
	Kernel *kernel.Kernel
	// Usage is the optional per-second metering aggregator. When non-nil,
	// /control/v0/usage exposes its Query view; when nil, that route
	// reports an empty list (so the control API stays uniform across
	// builds that have not wired metering yet).
	Usage *scope.UsageAggregator
}

// NewServer wires a control server backed by the given kernel.
func NewServer(k *kernel.Kernel) *Server { return &Server{Kernel: k} }

// WithUsage attaches a UsageAggregator to s; returns s for chaining.
func (s *Server) WithUsage(u *scope.UsageAggregator) *Server {
	s.Usage = u
	return s
}

// Handler returns the http.Handler that owns /control/v0/* paths.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/control/v0/healthz", s.handleHealthz)
	mux.HandleFunc("/control/v0/agents", s.handleAgents)
	mux.HandleFunc("/control/v0/agents/", s.handleAgentChild)
	mux.HandleFunc("/control/v0/usage", s.handleUsage)
	return loopbackOnly(mux)
}

// --- agents -----------------------------------------------------------------

type agentRow struct {
	ID          string `json:"id"`           // canonical "soya:<slug>"
	Slug        string `json:"slug"`         // bare slug
	Description string `json:"description"`
}

type agentsResp struct {
	Object string     `json:"object"`
	Data   []agentRow `json:"data"`
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method)
		return
	}
	rows := make([]agentRow, 0)
	for _, a := range s.Kernel.List() {
		rows = append(rows, agentRow{ID: a.ModelID(), Slug: a.Slug, Description: a.Description})
	}
	writeJSON(w, http.StatusOK, agentsResp{Object: "list", Data: rows})
}

// handleAgentChild routes /control/v0/agents/{slug}/{verb}.
func (s *Server) handleAgentChild(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/control/v0/agents/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "expected /control/v0/agents/{slug}/{verb}")
		return
	}
	slug, verb := parts[0], parts[1]
	switch verb {
	case "invoke":
		s.invokeAgent(w, r, slug)
	default:
		writeError(w, http.StatusNotFound, "unknown_verb", verb)
	}
}

type invokeReq struct {
	Prompt string `json:"prompt"`
}

type invokeResp struct {
	Slug    string `json:"slug"`
	Model   string `json:"model"`
	Content string `json:"content"`
}

func (s *Server) invokeAgent(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method)
		return
	}
	var req invokeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "missing_prompt", "prompt is required")
		return
	}
	resp, err := s.Kernel.ChatCompletion(r.Context(), auth.Identity{Subject: "control-rpc"}, llmcall.Request{
		Model:    "soya:" + slug,
		Messages: []llmcall.Message{{Role: "user", Content: req.Prompt}},
	})
	if err != nil {
		if errors.Is(err, kernel.ErrUnknownAgent) {
			writeError(w, http.StatusNotFound, "unknown_agent", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "kernel_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, invokeResp{Slug: slug, Model: resp.Model, Content: resp.Content})
}

// --- usage ------------------------------------------------------------------

// usageResp is the wire shape of GET /control/v0/usage. We follow the same
// {object, data} envelope the /agents endpoint uses so clients can write
// one decoder for both lists.
type usageResp struct {
	Object string               `json:"object"`
	Data   []scope.UsagePayload `json:"data"`
}

// handleUsage exposes the UsageAggregator's Query view over HTTP. Query
// parameters:
//
//   - window=today | 7d | 30d (default: today)
//   - api_key_prefix=...      (optional, exact match)
//   - agent_slug=...          (optional)
//   - sandbox_image=...       (optional)
//
// `group_by` is accepted but ignored in the alpha — the aggregator already
// stores at the finest grain (api_key, agent, image, minute) so callers can
// fold client-side.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method)
		return
	}
	if s.Usage == nil {
		writeJSON(w, http.StatusOK, usageResp{Object: "list", Data: []scope.UsagePayload{}})
		return
	}
	since, err := parseUsageWindow(r.URL.Query().Get("window"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_window", err.Error())
		return
	}
	q := scope.UsageQuery{
		APIKeyPrefix: r.URL.Query().Get("api_key_prefix"),
		AgentSlug:    r.URL.Query().Get("agent_slug"),
		SandboxImage: r.URL.Query().Get("sandbox_image"),
		Since:        since,
	}
	rows, err := s.Usage.Query(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "usage_query_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, usageResp{Object: "list", Data: rows})
}

// parseUsageWindow turns the `window=` query param into a Since bound.
// Empty / "today" → start of current UTC day. "7d" / "30d" → that many
// days back. Anything else → error.
func parseUsageWindow(s string) (time.Time, error) {
	now := time.Now().UTC()
	switch s {
	case "", "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), nil
	case "7d":
		return now.Add(-7 * 24 * time.Hour), nil
	case "30d":
		return now.Add(-30 * 24 * time.Hour), nil
	default:
		return time.Time{}, fmt.Errorf("window must be one of today|7d|30d (got %q)", s)
	}
}

// --- healthz ----------------------------------------------------------------

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

// --- helpers ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type errBody struct {
	Error struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	e := errBody{}
	e.Error.Type = "soyaos.control_error"
	e.Error.Code = code
	e.Error.Message = message
	writeJSON(w, status, e)
}

// loopbackOnly refuses requests whose remote address is not the loopback
// interface. The Solo edition assumes the control plane is talked to only
// by the local OS user; rejecting non-loopback callers makes that assumption
// enforceable.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.RemoteAddr
		if i := strings.LastIndex(host, ":"); i > 0 {
			host = host[:i]
		}
		host = strings.Trim(host, "[]")
		if host != "127.0.0.1" && host != "::1" && host != "localhost" {
			writeError(w, http.StatusForbidden, "non_loopback_forbidden", "control RPC only accepts loopback connections in Solo edition")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ShutdownTimeout is the default time a caller should give the control
// server to drain in-flight requests before forcing close. Exported so
// callers don't reinvent it.
const ShutdownTimeout = 5 * time.Second

// Ensure context is used (for future cancelable wiring).
var _ = context.Background
