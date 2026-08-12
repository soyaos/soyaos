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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	internalpack "github.com/soyaos/soyaos/pkg/control/internal/pack"
	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/kernel"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/scope"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

// DefaultListenAddr matches specs/cli/v0.md — localhost loopback on 7475.
const DefaultListenAddr = "127.0.0.1:7475"

// MaxPackUploadBytes is the upper bound on a single POST /control/v0/packs
// body (the multipart envelope including the file part). 32 MiB is the
// alpha cap: Pack source trees are mostly prompt files + a few templates
// so the typical .spk is well under 1 MiB; bigger sandbox images / model
// weights belong on a separate channel. Larger archives are reserved for
// v0.1.1 once we wire in chunked / resumable uploads.
const MaxPackUploadBytes int64 = 32 << 20

// Server is the control-plane HTTP handler.
type Server struct {
	Kernel *kernel.Kernel
	// Usage is the optional per-second metering aggregator. When non-nil,
	// /control/v0/usage exposes its Query view; when nil, that route
	// reports an empty list (so the control API stays uniform across
	// builds that have not wired metering yet).
	Usage *scope.UsageAggregator
	// DataDir is the on-disk root under which uploaded Packs are
	// materialised (each Pack lands in <DataDir>/packs/<name>-<version>/).
	// When empty, POST /control/v0/packs responds 503 — that endpoint is
	// opt-in for callers that wire a persistent data directory. The Solo
	// `soyaos start` path always sets it; raw `NewServer(k)` in unit
	// tests deliberately does not, so the deploy surface stays disabled
	// in tests that don't need it.
	DataDir string
}

// NewServer wires a control server backed by the given kernel.
func NewServer(k *kernel.Kernel) *Server { return &Server{Kernel: k} }

// WithUsage attaches a UsageAggregator to s; returns s for chaining.
func (s *Server) WithUsage(u *scope.UsageAggregator) *Server {
	s.Usage = u
	return s
}

// WithDataDir enables the pack deploy endpoint by pointing it at an on-disk
// root. Pack archives uploaded through POST /control/v0/packs are unpacked
// under <dir>/packs/<name>-<version>/. Returns s for chaining.
func (s *Server) WithDataDir(dir string) *Server {
	s.DataDir = dir
	return s
}

// Handler returns the http.Handler that owns /control/v0/* paths.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/control/v0/healthz", s.handleHealthz)
	mux.HandleFunc("/control/v0/agents", s.handleAgents)
	mux.HandleFunc("/control/v0/agents/", s.handleAgentChild)
	mux.HandleFunc("/control/v0/usage", s.handleUsage)
	mux.HandleFunc("/control/v0/packs", s.handleDeployPack)
	return loopbackOnly(mux)
}

// --- agents -----------------------------------------------------------------

type agentRow struct {
	ID          string `json:"id"`   // canonical "soya:<slug>"
	Slug        string `json:"slug"` // bare slug
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

// --- packs ------------------------------------------------------------------

// deployPackResp is the wire shape of a successful POST /control/v0/packs.
type deployPackResp struct {
	Slug           string `json:"slug"`             // bare slug after "soya:"
	VirtualModelID string `json:"virtual_model_id"` // canonical "soya:<slug>"
	Files          int    `json:"files"`            // regular files written
	Size           int64  `json:"size"`             // total bytes written under packDir
}

// handleDeployPack implements POST /control/v0/packs.
//
// Wire shape:
//
//	multipart/form-data
//	  pack: <.spk file>                         (required, regular file part)
//	  X-Spk-Sha256: <64-char lowercase hex>     (optional header)
//
// Flow:
//  1. Reject when DataDir is unset (deploy disabled, 503).
//  2. ParseMultipartForm with MaxPackUploadBytes cap. The form's "pack"
//     part lands in a temp file on disk (default multipart behaviour for
//     parts above the in-memory threshold).
//  3. If the caller supplied X-Spk-Sha256, verify it matches the upload's
//     sha256 (rejecting tampered transport with 400).
//  4. Stream-extract the .spk into <DataDir>/packs/<name>-<version>/ via
//     internal/pack.Extract, which enforces zip-slip + entry-size caps.
//  5. Load + validate the unpacked soyapack.yaml and register the Agent
//     against the kernel. A pre-existing Agent of the same slug is
//     overwritten — kernel.Register is idempotent by ModelID().
//
// Errors are wire-shaped via writeError (envelope: {error: {type, code, message}}).
func (s *Server) handleDeployPack(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method)
		return
	}
	if s.DataDir == "" {
		writeError(w, http.StatusServiceUnavailable, "pack_deploy_disabled",
			"pack deploy disabled: server has no DataDir configured")
		return
	}

	// Cap the *entire* request body (envelope + part) before any work. We
	// also wrap r.Body with MaxBytesReader so a misbehaving client can't
	// stream more bytes than the cap even past ParseMultipartForm's hint.
	r.Body = http.MaxBytesReader(w, r.Body, MaxPackUploadBytes)
	if err := r.ParseMultipartForm(MaxPackUploadBytes); err != nil {
		// MaxBytesReader returns "http: request body too large" when the
		// cap is exceeded; map it to a 413 so callers can disambiguate
		// "too big" from "wrong shape" without parsing the message.
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) || strings.Contains(err.Error(), "too large") {
			writeError(w, http.StatusRequestEntityTooLarge, "pack_too_large",
				fmt.Sprintf("pack upload exceeds %d bytes", MaxPackUploadBytes))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_multipart", err.Error())
		return
	}

	file, hdr, err := r.FormFile("pack")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_pack_field",
			"multipart body must contain a 'pack' file part")
		return
	}
	defer file.Close()
	if hdr.Size > MaxPackUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "pack_too_large",
			fmt.Sprintf("pack file declares %d bytes (max %d)", hdr.Size, MaxPackUploadBytes))
		return
	}

	// Drain the part to a temp file so we can both (a) hash it without
	// holding 32 MiB in RAM and (b) hand the bytes to Extract afterwards.
	tmp, err := os.CreateTemp("", "soyaos-spk-*.spk")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tempfile_error", err.Error())
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	h := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, h), file)
	if cerr := tmp.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "upload_read_error", err.Error())
		return
	}
	if written == 0 {
		writeError(w, http.StatusBadRequest, "empty_pack", "uploaded pack is empty")
		return
	}
	digest := hex.EncodeToString(h.Sum(nil))

	if expect := strings.TrimSpace(r.Header.Get("X-Spk-Sha256")); expect != "" {
		if !strings.EqualFold(expect, digest) {
			writeError(w, http.StatusBadRequest, "sha256_mismatch",
				fmt.Sprintf("X-Spk-Sha256 mismatch: header=%s computed=%s", expect, digest))
			return
		}
	}

	// Stage 1: peek the manifest by extracting into a *staging* temp dir.
	// We need name+version from the manifest to derive the canonical
	// destination directory; doing this in a temp area first lets us
	// reject malformed packs without leaving partial bytes under DataDir.
	staging, err := os.MkdirTemp("", "soyaos-pack-staging-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tempdir_error", err.Error())
		return
	}
	defer os.RemoveAll(staging)

	src, err := os.Open(tmpPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tempfile_reopen", err.Error())
		return
	}
	res, err := internalpack.Extract(src, staging)
	_ = src.Close()
	if err != nil {
		status := http.StatusBadRequest
		code := "pack_extract_error"
		switch {
		case errors.Is(err, internalpack.ErrUnsafePath):
			code = "pack_unsafe_path"
		case errors.Is(err, internalpack.ErrUnsupportedEntry):
			code = "pack_unsupported_entry"
		case errors.Is(err, internalpack.ErrEntryTooLarge):
			code = "pack_entry_too_large"
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, code, err.Error())
		return
	}

	manifestPath := filepath.Join(staging, "soyapack.yaml")
	m, err := soyapack.LoadFromFile(manifestPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "manifest_load_error", err.Error())
		return
	}
	if err := soyapack.Validate(m); err != nil {
		writeError(w, http.StatusBadRequest, "manifest_invalid", err.Error())
		return
	}

	// Stage 2: promote staging → <DataDir>/packs/<name>-<version>/.
	// We delete any pre-existing dir of the same identity so an upgrade
	// is a clean swap rather than a half-merged sprawl.
	finalDir := filepath.Join(s.DataDir, "packs", m.Name+"-"+m.Version)
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "packs_dir_mkdir", err.Error())
		return
	}
	if err := os.RemoveAll(finalDir); err != nil {
		writeError(w, http.StatusInternalServerError, "stale_pack_remove", err.Error())
		return
	}
	if err := os.Rename(staging, finalDir); err != nil {
		// Rename across devices fails on some setups; fall back to a copy.
		if copyErr := copyTree(staging, finalDir); copyErr != nil {
			writeError(w, http.StatusInternalServerError, "promote_error",
				fmt.Sprintf("rename=%v copy=%v", err, copyErr))
			return
		}
	}

	if err := s.Kernel.RegisterFromPack(m, finalDir); err != nil {
		writeError(w, http.StatusBadRequest, "register_failed", err.Error())
		return
	}

	slug := strings.TrimPrefix(m.Expose.VirtualModelID, "soya:")
	writeJSON(w, http.StatusOK, deployPackResp{
		Slug:           slug,
		VirtualModelID: m.Expose.VirtualModelID,
		Files:          res.Files,
		Size:           res.Bytes,
	})
}

// copyTree is the cross-device fallback for os.Rename when src and dst
// live on different filesystems (e.g. /tmp on tmpfs, DataDir on a real
// disk). Symlinks / specials cannot appear here — internal/pack.Extract
// only emits regular files + their parent dirs.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
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
