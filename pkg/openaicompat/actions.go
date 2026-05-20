// actions.go wires the OpenAI-Compat gateway's third Agent entry point —
// per-row Actions (DD-010, EstateMuse Aha #3, APP-502). Conversation
// (handled by /v1/chat/completions) and scheduled triggers (pkg/scheduler)
// are the other two.
//
// Route shape:
//
//	POST /v1/agents/{slug}/actions/{action_id}
//	    body: { "row_id": "...", "payload": {...} }
//
// The handler resolves the agent's manifest, locates the ActionDecl by
// id, and asks kernel.InvokeAction to dispatch it. 404s are returned for
// unknown agents / unknown action ids; 400 for malformed bodies.
package openaicompat

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/kernel"
)

// actionRequestBody is the JSON shape callers POST.
type actionRequestBody struct {
	RowID   string         `json:"row_id"`
	Payload map[string]any `json:"payload,omitempty"`
}

// agentRow is one entry returned by GET /v1/agents. Unlike modelRow (which
// follows the OpenAI /v1/models shape verbatim), this carries the human-
// readable description from kernel.Agent so a UI can render a "what does
// this Agent do" column without a second round-trip.
type agentRow struct {
	ID          string `json:"id"`          // "soya:echo"
	Slug        string `json:"slug"`        // "echo"
	Description string `json:"description"` // free-form
	Object      string `json:"object"`      // "agent"
}

// agentsResp is the {object, data} envelope OpenAI-style clients expect.
type agentsResp struct {
	Object string     `json:"object"`
	Data   []agentRow `json:"data"`
}

// handleAgentList implements GET /v1/agents — the SoyaOS-superset companion
// to /v1/models. Like /v1/models it requires a bearer key; unlike
// /v1/models it includes the kernel.Agent description, so an in-binary UI
// can paint an agent table without reaching the loopback-only control RPC.
func (s *Server) handleAgentList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method)
		return
	}
	if _, err := s.authorize(r); err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_api_key", err.Error())
		return
	}
	rows := make([]agentRow, 0)
	for _, a := range s.Kernel.List() {
		rows = append(rows, agentRow{
			ID:          a.ModelID(),
			Slug:        a.Slug,
			Description: a.Description,
			Object:      "agent",
		})
	}
	writeJSON(w, http.StatusOK, agentsResp{Object: "list", Data: rows})
}

// handleAgentAction parses `/v1/agents/{slug}/actions/{action_id}` and
// dispatches the named action through the kernel.
//
// Auth accepts EITHER:
//   - a standard sk-soya bearer key (resolves to auth.Identity); or
//   - a row-scoped JWT (DD-019 / APP-503) bound to exactly this
//     (slug, action_id, row_id) triple. The token's claims must match
//     the path + body, otherwise the request is rejected 401.
func (s *Server) handleAgentAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method)
		return
	}

	slug, actionID, ok := parseAgentActionPath(r.URL.Path)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "expected /v1/agents/{slug}/actions/{action_id}")
		return
	}

	var body actionRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", err.Error())
		return
	}
	if body.RowID == "" {
		writeAPIError(w, http.StatusBadRequest, "missing_field", "row_id is required")
		return
	}

	id, authErr := s.authorizeAction(r, slug, actionID, body.RowID)
	if authErr != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", authErr.Error())
		return
	}

	if _, ok := s.Kernel.GetAgentManifest(slug); !ok {
		writeAPIError(w, http.StatusNotFound, "unknown_agent", slug)
		return
	}

	result, err := s.Kernel.InvokeAction(r.Context(), id, slug, actionID, body.RowID, body.Payload)
	if err != nil {
		switch {
		case errors.Is(err, kernel.ErrUnknownAgent):
			writeAPIError(w, http.StatusNotFound, "unknown_agent", err.Error())
		case errors.Is(err, kernel.ErrNoManifest):
			writeAPIError(w, http.StatusNotFound, "no_manifest", err.Error())
		case errors.Is(err, kernel.ErrUnknownAction):
			writeAPIError(w, http.StatusNotFound, "unknown_action", err.Error())
		default:
			writeAPIError(w, http.StatusInternalServerError, "kernel_error", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// authorizeAction resolves the request's Bearer credential to an
// auth.Identity. It first tries the standard sk-soya verifier; if that
// fails AND the server has a RowTokens signer configured, it tries to
// parse the credential as a row-scoped JWT and asserts its claims
// match the (slug, action_id, row_id) the caller is asking for.
//
// Returning an error here always means 401 — we never lookup-by-path
// because that would leak agent enumeration through auth.
func (s *Server) authorizeAction(r *http.Request, slug, actionID, rowID string) (auth.Identity, error) {
	raw := auth.ExtractBearer(r.Header.Get("Authorization"))
	if raw == "" {
		return auth.Identity{}, errors.New("missing or malformed Authorization header")
	}

	// Try the standard verifier first.
	if id, err := s.Verifier.Verify(r.Context(), raw); err == nil {
		return id, nil
	}

	// Fall back to row token. Skip if no signer configured.
	if s.RowTokens == nil {
		return auth.Identity{}, errors.New("invalid api key")
	}
	claims, err := s.RowTokens.Verify(raw)
	if err != nil {
		return auth.Identity{}, errors.New("invalid api key or row token")
	}
	if claims.AgentSlug != slug || claims.ActionID != actionID || claims.RowID != rowID {
		return auth.Identity{}, errors.New("row token does not match this action / row")
	}
	// Synthesise an Identity from the token's owner key prefix. KeyID
	// is the token-bound prefix; Subject is "row-token" so downstream
	// audit can tell row-token traffic apart from sk-soya traffic.
	return auth.Identity{
		KeyID:   claims.OwnerKey,
		Subject: "row-token:" + claims.OwnerKey,
		Scopes:  []string{"agents:invoke:row"},
	}, nil
}

// parseAgentActionPath extracts (slug, action_id) from a URL of the form
// `/v1/agents/{slug}/actions/{action_id}`. Returns ok=false for any
// shape mismatch so callers can return a clean 404.
func parseAgentActionPath(path string) (slug, actionID string, ok bool) {
	const prefix = "/v1/agents/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := path[len(prefix):]
	// Expected: <slug>/actions/<action_id>
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 {
		return "", "", false
	}
	if parts[1] != "actions" {
		return "", "", false
	}
	if parts[0] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}
