package soyapack

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrInvalidManifest is the sentinel returned when Validate fails. Callers
// can `errors.Is(err, ErrInvalidManifest)` to detect validation failures
// without parsing the message.
var ErrInvalidManifest = errors.New("soyapack: invalid manifest")

// reName matches a SoyaPack name slug: lowercase, hyphens, 1–48 chars,
// first / last must be alphanumeric.
var reName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,46}[a-z0-9]$|^[a-z0-9]$`)

// reSemVer is a SemVer 2.0 regular expression (simplified — covers the cases
// we expect from SoyaPack authors).
var reSemVer = regexp.MustCompile(
	`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
		`(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)` +
		`(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?` +
		`(?:\+[0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*)?$`,
)

// reVirtualModelID matches the locked `soya:<slug>` form for Agent exposes.
var reVirtualModelID = regexp.MustCompile(`^soya:[a-z][a-z0-9-]{0,46}[a-z0-9]$|^soya:[a-z0-9]$`)

// reSHA256Hex matches a 64-character lowercase hex string.
var reSHA256Hex = regexp.MustCompile(`^[a-f0-9]{64}$`)

// reAPIKeyRef matches the locked-in ${ENV_NAME} form for prompt.upstream
// API key references. Inline secrets are forbidden — the env name must be
// upper-case, start with letter/underscore, and contain only the standard
// shell-identifier character class. (APP-543)
var reAPIKeyRef = regexp.MustCompile(`^\$\{[A-Z_][A-Z0-9_]+\}$`)

// ErrUnsupportedProvider is returned when prompt.upstream.provider is not
// "openai-compat". Other providers are reserved for later milestones.
var ErrUnsupportedProvider = errors.New("soyapack: unsupported upstream provider")

// ErrBadAPIKeyRef is returned when prompt.upstream.api_key_ref does not
// match the ${ENV_NAME} form. Inline secrets are forbidden.
var ErrBadAPIKeyRef = errors.New("soyapack: bad api_key_ref")

// Validate enforces the semantic constraints from
// specs/soyapack/v0/manifest.md. Returns an error wrapping ErrInvalidManifest
// when the manifest is malformed; nil otherwise.
func Validate(m *Manifest) error {
	if m == nil {
		return wrap("nil manifest")
	}

	// --- common required fields ---------------------------------------------
	if m.SpecVersion != SpecVersionV0 {
		return wrap("spec_version must be %q, got %q", SpecVersionV0, m.SpecVersion)
	}
	switch m.Kind {
	case KindSkill, KindAgent, KindMemory:
	default:
		return wrap("kind must be one of Skill/Agent/Memory, got %q", m.Kind)
	}
	if !reName.MatchString(m.Name) {
		return wrap("name %q does not match ^[a-z][a-z0-9-]{0,46}[a-z0-9]$", m.Name)
	}
	if !reSemVer.MatchString(m.Version) {
		return wrap("version %q is not valid SemVer 2.0", m.Version)
	}
	if m.Description == "" {
		return wrap("description is required")
	}
	if len(m.Description) > 280 {
		return wrap("description exceeds 280 chars (%d)", len(m.Description))
	}
	if len(m.Authors) == 0 {
		return wrap("authors[] must have at least one entry")
	}
	for i, a := range m.Authors {
		if a.Name == "" {
			return wrap("authors[%d].name is required", i)
		}
	}
	if m.License == "" {
		return wrap("license is required (use an SPDX identifier, e.g. MIT)")
	}
	if m.Runtime.Compat == "" {
		return wrap("runtime.compat is required (SemVer range, e.g. \">=0.1.0 <0.2.0\")")
	}
	switch m.Determinism {
	case DeterminismPure, DeterminismReadOnly, DeterminismStateful:
	default:
		return wrap("determinism must be pure/read-only/stateful, got %q", m.Determinism)
	}
	if m.Affinity != "" {
		switch m.Affinity {
		case AffinityPlanet, AffinityMoon, AffinityComet, AffinityAny:
		default:
			return wrap("affinity must be planet/moon/comet/any, got %q", m.Affinity)
		}
	}
	if m.Deps != nil && m.Deps.Lockfile != "" && !reSHA256Hex.MatchString(m.Deps.Lockfile) {
		return wrap("deps.lockfile must be 64-char lowercase hex, got %q", m.Deps.Lockfile)
	}

	// --- per-Kind constraints -----------------------------------------------
	switch m.Kind {
	case KindAgent:
		if err := validateAgent(m); err != nil {
			return err
		}
	case KindSkill:
		if err := validateSkill(m); err != nil {
			return err
		}
	case KindMemory:
		if err := validateMemory(m); err != nil {
			return err
		}
	}

	// --- artifacts ----------------------------------------------------------
	for i, a := range m.Artifacts {
		switch a.Kind {
		case "html", "pdf", "long_image", "markdown", "xlsx", "mp4":
		default:
			return wrap("artifacts[%d].kind %q not in {html,pdf,long_image,markdown,xlsx,mp4}", i, a.Kind)
		}
		if a.Schema == "" {
			return wrap("artifacts[%d].schema is required", i)
		}
	}

	// --- schedules ----------------------------------------------------------
	for i, s := range m.Schedules {
		if s.Cron == "" && s.Once == "" {
			return wrap("schedules[%d] must have either cron or once", i)
		}
		if s.MissedFire != "" {
			switch s.MissedFire {
			case "skip", "once", "backfill":
			default:
				return wrap("schedules[%d].missed_fire must be skip/once/backfill", i)
			}
		}
	}

	// --- channels -----------------------------------------------------------
	for i, c := range m.Channels {
		if c.Kind == "" {
			return wrap("channels[%d].kind is required", i)
		}
		switch c.Kind {
		case "dingtalk", "feishu", "wework", "wechat-mp", "wechat-cs", "webhook":
		default:
			return wrap("channels[%d].kind %q not in {dingtalk,feishu,wework,wechat-mp,wechat-cs,webhook}", i, c.Kind)
		}
	}

	// --- actions ------------------------------------------------------------
	for i, a := range m.Actions {
		if a.ID == "" || a.Handler == "" {
			return wrap("actions[%d] requires id + handler", i)
		}
		switch a.On {
		case "per_row", "button", "api":
		default:
			return wrap("actions[%d].on must be per_row/button/api, got %q", i, a.On)
		}
	}

	// --- state --------------------------------------------------------------
	if m.State != nil {
		switch m.State.Scope {
		case "agent", "user", "tenant":
		default:
			return wrap("state.scope must be agent/user/tenant, got %q", m.State.Scope)
		}
		switch m.State.Store {
		case "memory", "kv", "db":
		default:
			return wrap("state.store must be memory/kv/db, got %q", m.State.Store)
		}
	}

	// --- sandbox ------------------------------------------------------------
	if m.Sandbox != nil {
		if m.Sandbox.Image == "" {
			return wrap("sandbox.image is required")
		}
		if m.Sandbox.Isolation != "" {
			switch m.Sandbox.Isolation {
			case "process", "container", "microvm":
			default:
				return wrap("sandbox.isolation must be process/container/microvm, got %q", m.Sandbox.Isolation)
			}
		}
		if m.Sandbox.Capabilities != nil {
			if err := validateCapabilities(m.Sandbox.Capabilities); err != nil {
				return err
			}
		}
	}

	// --- top-level Skill capabilities --------------------------------------
	if m.Capabilities != nil {
		if err := validateCapabilities(m.Capabilities); err != nil {
			return err
		}
	}

	return nil
}

// validateCapabilities enforces the fail-closed gate surface: any
// network_out entry must declare a real host, a port in [1, 65535] and
// one of the canonical protos. Empty fields would be silently treated
// as "deny all", but a present-but-malformed entry is almost always an
// authoring bug — reject it loudly here so it never reaches the gate.
func validateCapabilities(c *Capabilities) error {
	for i, r := range c.NetworkOut {
		if r.Host == "" {
			return wrap("capabilities.network_out[%d].host is required", i)
		}
		if r.Port < 1 || r.Port > 65535 {
			return wrap("capabilities.network_out[%d].port must be in [1,65535], got %d", i, r.Port)
		}
		switch r.Proto {
		case "http", "https", "grpc", "quic":
		default:
			return wrap("capabilities.network_out[%d].proto must be one of http/https/grpc/quic, got %q", i, r.Proto)
		}
	}
	return nil
}

func validateAgent(m *Manifest) error {
	if m.Entry == "" {
		return wrap("Agent: entry is required")
	}
	if m.Expose == nil {
		return wrap("Agent: expose is required")
	}
	if m.Expose.OpenAICompat != "" {
		switch m.Expose.OpenAICompat {
		case "chat", "responses", "both":
		default:
			return wrap("Agent: expose.openai_compat must be chat/responses/both, got %q", m.Expose.OpenAICompat)
		}
	}
	if m.Expose.VirtualModelID != "" && !reVirtualModelID.MatchString(m.Expose.VirtualModelID) {
		return wrap("Agent: expose.virtual_model_id must match ^soya:<slug>$, got %q", m.Expose.VirtualModelID)
	}
	if m.Prompt != nil && m.Prompt.Upstream != nil {
		if err := validateUpstream(m.Prompt.Upstream); err != nil {
			return err
		}
	}
	return nil
}

// validateUpstream enforces the prompt.upstream rules (APP-543):
//
//   - provider must be "openai-compat" (other values reserved).
//   - api_key_ref, if set, must match ${ENV_NAME}; inline secrets are
//     forbidden (the strict YAML decoder additionally rejects unknown
//     fields under UpstreamDecl, so an `api_key:` literal fails to load).
func validateUpstream(u *UpstreamDecl) error {
	if u.Provider != "openai-compat" {
		return fmt.Errorf("%w: prompt.upstream.provider must be %q, got %q",
			ErrUnsupportedProvider, "openai-compat", u.Provider)
	}
	if u.APIKeyRef != "" && !reAPIKeyRef.MatchString(u.APIKeyRef) {
		return fmt.Errorf("%w: prompt.upstream.api_key_ref must match ${ENV_NAME} (pattern %s), got %q",
			ErrBadAPIKeyRef, reAPIKeyRef.String(), u.APIKeyRef)
	}
	return nil
}

func validateSkill(m *Manifest) error {
	if m.Entry == "" {
		return wrap("Skill: entry is required")
	}
	return nil
}

func validateMemory(m *Manifest) error {
	if m.Mount == nil {
		return wrap("Memory: mount is required")
	}
	if m.Mount.Partition == "" {
		return wrap("Memory: mount.partition is required")
	}
	switch m.Mount.Access {
	case "ro", "rw":
	default:
		return wrap("Memory: mount.access must be ro/rw, got %q", m.Mount.Access)
	}
	switch m.Mount.Format {
	case "embeddings", "kv", "json-lines":
	default:
		return wrap("Memory: mount.format must be embeddings/kv/json-lines, got %q", m.Mount.Format)
	}
	if m.Mount.Format == "embeddings" {
		if m.Mount.Embedding == nil {
			return wrap("Memory: embeddings format requires mount.embedding{model,fingerprint,dim}")
		}
		if !reSHA256Hex.MatchString(m.Mount.Embedding.Fingerprint) {
			return wrap("Memory: mount.embedding.fingerprint must be 64-char hex")
		}
		if m.Mount.Embedding.Dim <= 0 {
			return wrap("Memory: mount.embedding.dim must be > 0")
		}
	}
	if len(m.Contents) == 0 {
		return wrap("Memory: contents[] must have at least one entry")
	}
	for i, c := range m.Contents {
		if c.Path == "" {
			return wrap("Memory: contents[%d].path is required", i)
		}
		if !reSHA256Hex.MatchString(c.SHA256) {
			return wrap("Memory: contents[%d].sha256 must be 64-char hex, got %q", i, c.SHA256)
		}
		if c.Size < 0 {
			return wrap("Memory: contents[%d].size must be >= 0", i)
		}
	}
	return nil
}

func wrap(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidManifest, fmt.Sprintf(format, args...))
}
