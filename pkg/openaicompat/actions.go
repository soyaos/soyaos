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

	"github.com/soyaos/soyaos/pkg/kernel"
)

// actionRequestBody is the JSON shape callers POST.
type actionRequestBody struct {
	RowID   string         `json:"row_id"`
	Payload map[string]any `json:"payload,omitempty"`
}

// handleAgentAction parses `/v1/agents/{slug}/actions/{action_id}` and
// dispatches the named action through the kernel.
func (s *Server) handleAgentAction(w http.ResponseWriter, r *http.Request) {
	id, err := s.authorize(r)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_api_key", err.Error())
		return
	}
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
