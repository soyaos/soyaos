package soyapack

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// SpecVersionV0 is the only spec_version this implementation accepts.
const SpecVersionV0 = "soyapack.v0"

// Kind enumerates the three SoyaPack archetypes (manifest.md §"Three KINDs").
type Kind string

const (
	KindSkill  Kind = "Skill"
	KindAgent  Kind = "Agent"
	KindMemory Kind = "Memory"
)

// Determinism levels (manifest.md §"Determinism axis").
type Determinism string

const (
	DeterminismPure     Determinism = "pure"
	DeterminismReadOnly Determinism = "read-only"
	DeterminismStateful Determinism = "stateful"
)

// Affinity hints which node role should host the Pack at runtime.
type Affinity string

const (
	AffinityPlanet Affinity = "planet"
	AffinityMoon   Affinity = "moon"
	AffinityComet  Affinity = "comet"
	AffinityAny    Affinity = "any"
)

// Manifest is the canonical SoyaPack v0 manifest. Field shapes mirror
// specs/soyapack/v0/manifest.md verbatim; do not drift without a spec bump.
type Manifest struct {
	// Common (all KINDs)
	SpecVersion string        `yaml:"spec_version" json:"spec_version"`
	Kind        Kind          `yaml:"kind" json:"kind"`
	Name        string        `yaml:"name" json:"name"`
	Version     string        `yaml:"version" json:"version"`
	Description string        `yaml:"description" json:"description"`
	Authors     []Author      `yaml:"authors" json:"authors"`
	License     string        `yaml:"license" json:"license"`
	Homepage    string        `yaml:"homepage,omitempty" json:"homepage,omitempty"`
	Runtime     RuntimeCompat `yaml:"runtime" json:"runtime"`
	Determinism Determinism   `yaml:"determinism" json:"determinism"`
	Affinity    Affinity      `yaml:"affinity,omitempty" json:"affinity,omitempty"`
	Deps        *Deps         `yaml:"deps,omitempty" json:"deps,omitempty"`
	SBOM        string        `yaml:"sbom,omitempty" json:"sbom,omitempty"`
	Signatures  []Signature   `yaml:"signatures,omitempty" json:"signatures,omitempty"`

	// Agent-specific (also valid in Skill subsets)
	Entry      string           `yaml:"entry,omitempty" json:"entry,omitempty"`
	Expose     *Expose          `yaml:"expose,omitempty" json:"expose,omitempty"`
	Inputs     []Input          `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Outputs    []any            `yaml:"outputs,omitempty" json:"outputs,omitempty"`
	Prompt     *Prompt          `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	Artifacts  []ArtifactDecl   `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
	Schedules  []ScheduleDecl   `yaml:"schedules,omitempty" json:"schedules,omitempty"`
	Channels   []ChannelDecl    `yaml:"channels,omitempty" json:"channels,omitempty"`
	Actions    []ActionDecl     `yaml:"actions,omitempty" json:"actions,omitempty"`
	State      *StateDecl       `yaml:"state,omitempty" json:"state,omitempty"`
	StorageNAS []StorageNASDecl `yaml:"storage_nas,omitempty" json:"storage_nas,omitempty"`
	DataPlane  *DataPlaneDecl   `yaml:"data_plane,omitempty" json:"data_plane,omitempty"`
	Sandbox    *SandboxDecl     `yaml:"sandbox,omitempty" json:"sandbox,omitempty"`
	Uses       []string         `yaml:"uses,omitempty" json:"uses,omitempty"`

	// Skill-specific
	Capabilities *Capabilities `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`

	// Memory-specific
	Mount    *Mount    `yaml:"mount,omitempty" json:"mount,omitempty"`
	Contents []Content `yaml:"contents,omitempty" json:"contents,omitempty"`

	// Extensions captures any `x-`-prefixed top-level fields verbatim. The
	// SoyaPack spec reserves the `x-` prefix for forward-compatible
	// extensions; unknown fields without that prefix are rejected by the
	// loader.
	Extensions map[string]any `yaml:"-" json:"-"`
}

// Author is one entry under `authors:`.
type Author struct {
	Name  string `yaml:"name" json:"name"`
	Email string `yaml:"email,omitempty" json:"email,omitempty"`
}

// RuntimeCompat declares the compatible SoyaOS SemVer range.
type RuntimeCompat struct {
	Compat string `yaml:"compat" json:"compat"`
}

// Deps holds dependency lockfile bookkeeping.
type Deps struct {
	Lockfile string `yaml:"lockfile,omitempty" json:"lockfile,omitempty"`
}

// Signature is a COSE_Sign1 entry attached by the publishing pipeline. Pack
// authors do not write these by hand.
type Signature struct {
	Type      string `yaml:"type,omitempty" json:"type,omitempty"`
	KeyID     string `yaml:"key_id,omitempty" json:"key_id,omitempty"`
	Algorithm string `yaml:"algorithm,omitempty" json:"algorithm,omitempty"`
	Value     string `yaml:"value,omitempty" json:"value,omitempty"`
}

// --- Agent-side declarations -------------------------------------------------

// Expose controls the OpenAI-Compat virtual-model identity (DD-005).
type Expose struct {
	OpenAICompat   string `yaml:"openai_compat,omitempty" json:"openai_compat,omitempty"` // chat / responses / both
	VirtualModelID string `yaml:"virtual_model_id,omitempty" json:"virtual_model_id,omitempty"`
}

// Input describes one parameter the Agent accepts at invocation.
type Input struct {
	Name     string         `yaml:"name" json:"name"`
	Type     string         `yaml:"type" json:"type"`
	Optional bool           `yaml:"optional,omitempty" json:"optional,omitempty"`
	Items    map[string]any `yaml:"items,omitempty" json:"items,omitempty"`
}

// Prompt holds prompt-scaffolding hints.
//
// Two prompt-body shapes are supported and mutually exclusive with
// the top-level `entry`:
//
//   - `entry` (top-level) — single system prompt file (the v0 default).
//   - `prompt.steps[]`    — ordered chain of N prompt files; each step's
//     full response is fed as the user input of the next. The kernel
//     streams only the final step back to the caller. (APP-550 Compo
//     Phase B)
type Prompt struct {
	IndexedTable *IndexedTable `yaml:"indexed_table,omitempty" json:"indexed_table,omitempty"`
	Scaffold     string        `yaml:"scaffold,omitempty" json:"scaffold,omitempty"`
	Tools        []string      `yaml:"tools,omitempty" json:"tools,omitempty"`
	Steps        []PromptStep  `yaml:"steps,omitempty" json:"steps,omitempty"`
	Upstream     *UpstreamDecl `yaml:"upstream,omitempty" json:"upstream,omitempty"`
}

// IndexedTable opts a three-stage chain into validated, programmatic table assembly.
type IndexedTable struct {
	TargetRows      int                 `yaml:"target_rows" json:"target_rows"`
	CandidateRows   int                 `yaml:"candidate_rows" json:"candidate_rows"`
	Columns         []string            `yaml:"columns" json:"columns"`
	SheetName       string              `yaml:"sheet_name" json:"sheet_name"`
	MaxRepairs      int                 `yaml:"max_repairs" json:"max_repairs"`
	TimeoutSeconds  int                 `yaml:"timeout_seconds" json:"timeout_seconds"`
	BatchSize       int                 `yaml:"batch_size,omitempty" json:"batch_size,omitempty"`
	MaxConcurrency  int                 `yaml:"max_concurrency,omitempty" json:"max_concurrency,omitempty"`
	ColumnRules     []IndexedColumnRule `yaml:"column_rules,omitempty" json:"column_rules,omitempty"`
	BatchColumn     int                 `yaml:"batch_column,omitempty" json:"batch_column,omitempty"`
	BatchValues     []string            `yaml:"batch_values,omitempty" json:"batch_values,omitempty"`
	MinPerPartition int                 `yaml:"min_per_partition,omitempty" json:"min_per_partition,omitempty"`
}

// IndexedColumnRule is a deterministic opt-in constraint, not semantic review.
// Column is zero-based; all configured constraints must match exactly.
type IndexedColumnRule struct {
	Column              int      `yaml:"column" json:"column"`
	AllowedValues       []string `yaml:"allowed_values,omitempty" json:"allowed_values,omitempty"`
	ForbiddenSubstrings []string `yaml:"forbidden_substrings,omitempty" json:"forbidden_substrings,omitempty"`
	Prefix              string   `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	Suffix              string   `yaml:"suffix,omitempty" json:"suffix,omitempty"`
}

// Validate rejects unbounded or ambiguous indexed-table configurations.
func (c *IndexedTable) Validate(steps int) error {
	if steps != 3 {
		return fmt.Errorf("indexed_table requires exactly 3 prompt steps")
	}
	if c.TargetRows < 1 || c.CandidateRows < c.TargetRows || c.CandidateRows > 10000 {
		return fmt.Errorf("indexed_table requires 1 <= target_rows <= candidate_rows <= 10000")
	}
	if c.BatchSize != 0 || c.MaxConcurrency != 0 {
		if c.BatchSize < 1 || c.BatchSize > c.CandidateRows || c.MaxConcurrency < 1 || c.MaxConcurrency > 8 {
			return fmt.Errorf("indexed_table batch_size and max_concurrency must both be set: 1 <= batch_size <= candidate_rows, 1 <= max_concurrency <= 8")
		}
		if (c.CandidateRows+c.BatchSize-1)/c.BatchSize > 100 {
			return fmt.Errorf("indexed_table permits at most 100 candidate batches")
		}
	}
	if c.MaxRepairs < 0 || c.MaxRepairs > 3 || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 3600 {
		return fmt.Errorf("indexed_table requires max_repairs in 0..3 and timeout_seconds in 1..3600")
	}
	if len(c.BatchValues) > 0 {
		if c.BatchSize < 1 || c.BatchColumn < 0 || c.BatchColumn >= len(c.Columns) || len(c.BatchValues) != (c.CandidateRows+c.BatchSize-1)/c.BatchSize {
			return fmt.Errorf("indexed_table batch_values require one value per batch and an in-range batch_column")
		}
		seenValues := map[string]bool{}
		for _, value := range c.BatchValues {
			if strings.TrimSpace(value) == "" || seenValues[value] {
				return fmt.Errorf("indexed_table batch_values must be nonempty and unique")
			}
			seenValues[value] = true
		}
	}
	if c.MinPerPartition < 0 || (c.MinPerPartition > 0 && (len(c.BatchValues) == 0 || c.MinPerPartition > c.TargetRows/len(c.BatchValues))) {
		return fmt.Errorf("indexed_table min_per_partition exceeds target or lacks partitions")
	}
	if len(c.Columns) == 0 || len(c.Columns) > 100 {
		return fmt.Errorf("indexed_table requires 1..100 columns")
	}
	seen := map[string]bool{}
	for _, col := range c.Columns {
		if strings.TrimSpace(col) == "" || seen[col] {
			return fmt.Errorf("indexed_table columns must be nonempty and unique")
		}
		seen[col] = true
	}
	if strings.TrimSpace(c.SheetName) == "" || len([]rune(c.SheetName)) > 31 || strings.ContainsAny(c.SheetName, "[]:*?/\\") || strings.HasPrefix(c.SheetName, "'") || strings.HasSuffix(c.SheetName, "'") {
		return fmt.Errorf("indexed_table sheet_name is invalid")
	}
	ruleColumns := map[int]bool{}
	for _, rule := range c.ColumnRules {
		if rule.Column < 0 || rule.Column >= len(c.Columns) || ruleColumns[rule.Column] {
			return fmt.Errorf("indexed_table column_rules require unique in-range column indices")
		}
		if len(rule.AllowedValues) == 0 && len(rule.ForbiddenSubstrings) == 0 && rule.Prefix == "" && rule.Suffix == "" {
			return fmt.Errorf("indexed_table column_rules require at least one constraint")
		}
		for _, value := range rule.AllowedValues {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("indexed_table allowed_values must be nonempty")
			}
		}
		for _, value := range rule.ForbiddenSubstrings {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("indexed_table forbidden_substrings must be nonempty")
			}
		}
		ruleColumns[rule.Column] = true
	}
	return nil
}

// PromptStep is one stage in a `prompt.steps[]` chain. Prompt is a path
// relative to the Pack root; ID is a short stable handle used in logs
// and trace spans so operators can pin which stage failed.
type PromptStep struct {
	ID     string `yaml:"id" json:"id"`
	Prompt string `yaml:"prompt" json:"prompt"`
}

// UpstreamDecl is the per-Agent BYOK upstream override declared under
// `prompt.upstream`. When set, it wins over the operator-level
// SOYA_MODEL_* env vars at dispatch time; see pkg/llmcall.ResolveConfig.
//
// Inline secrets are forbidden: APIKeyRef must be of the form ${ENV_NAME}
// referencing an environment variable on the SoyaOS host. The strict YAML
// decoder rejects unknown fields under this struct, so a literal
// `api_key:` line in soyapack.yaml fails to load. (APP-543)
type UpstreamDecl struct {
	Provider  string `yaml:"provider" json:"provider"`
	BaseURL   string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	Model     string `yaml:"model,omitempty" json:"model,omitempty"`
	APIKeyRef string `yaml:"api_key_ref,omitempty" json:"api_key_ref,omitempty"`
}

// ArtifactDecl declares an output form. (Proposed DD-012.)
type ArtifactDecl struct {
	Kind   string `yaml:"kind" json:"kind"`     // html / pdf / long_image / markdown / xlsx / mp4
	Schema string `yaml:"schema" json:"schema"` // schema id + SemVer
}

// ScheduleDecl declares a cron / one-shot trigger (DD-007).
type ScheduleDecl struct {
	Cron           string         `yaml:"cron,omitempty" json:"cron,omitempty"`
	Once           string         `yaml:"once,omitempty" json:"once,omitempty"`
	TZ             string         `yaml:"tz,omitempty" json:"tz,omitempty"`
	Payload        map[string]any `yaml:"payload,omitempty" json:"payload,omitempty"`
	IdempotencyKey string         `yaml:"idempotency_key,omitempty" json:"idempotency_key,omitempty"`
	MissedFire     string         `yaml:"missed_fire,omitempty" json:"missed_fire,omitempty"` // skip / once / backfill
}

// ChannelDecl binds the Agent to an external channel (DD-006).
//
// Two forms coexist for alpha:
//
//   - BindingTemplate: a free-form placeholder filled at deploy time
//     (the original v0 shape; kept for forward compat).
//   - BindingID + Secrets: a deploy-time-locked binding plus an
//     env-var-ref secret map. Used by NewsBeam (APP-552) so a Pack
//     can declare *which* DingTalk robot it pushes to without
//     embedding creds in the manifest.
//
// Secret values must be of the form `${ENV_NAME}`; inline secrets are
// forbidden by Validate.
type ChannelDecl struct {
	Kind            string            `yaml:"kind" json:"kind"`
	BindingTemplate string            `yaml:"binding_template,omitempty" json:"binding_template,omitempty"`
	BindingID       string            `yaml:"binding_id,omitempty" json:"binding_id,omitempty"`
	Secrets         map[string]string `yaml:"secrets,omitempty" json:"secrets,omitempty"`
}

// ActionDecl describes a row / button / api action trigger (DD-010).
type ActionDecl struct {
	ID             string          `yaml:"id" json:"id"`
	On             string          `yaml:"on" json:"on"` // per_row / button / api
	Handler        string          `yaml:"handler" json:"handler"`
	Timeout        string          `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Artifacts      []string        `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
	TextValidation *TextValidation `yaml:"text_validation,omitempty" json:"text_validation,omitempty"`
}

// TextValidation is an opt-in, deterministic text contract, not factual review.
type TextValidation struct {
	Section          string   `yaml:"section,omitempty" json:"section,omitempty"`
	MinChars         int      `yaml:"min_chars,omitempty" json:"min_chars,omitempty"`
	MaxChars         int      `yaml:"max_chars" json:"max_chars"`
	MaxRepairs       int      `yaml:"max_repairs,omitempty" json:"max_repairs,omitempty"`
	ForbiddenPhrases []string `yaml:"forbidden_phrases,omitempty" json:"forbidden_phrases,omitempty"`
}

func (v *TextValidation) Validate() error {
	if v.MinChars < 0 || v.MaxChars < 1 || v.MaxChars < v.MinChars || v.MaxChars > 100000 || v.MaxRepairs < 0 || v.MaxRepairs > 2 {
		return fmt.Errorf("text_validation requires 0 <= min_chars <= max_chars <= 100000, max_chars > 0, and max_repairs in 0..2")
	}
	for _, phrase := range v.ForbiddenPhrases {
		if strings.TrimSpace(phrase) == "" {
			return fmt.Errorf("text_validation forbidden_phrases must be nonempty")
		}
	}
	return nil
}

// StateDecl declares Stateful Agent storage (DD-010).
type StateDecl struct {
	Scope string `yaml:"scope" json:"scope"` // agent / user / tenant
	Store string `yaml:"store" json:"store"` // memory / kv / db
}

// SandboxDecl declares the Comet sandbox profile (DD-011 + S2-B2-capabilities).
type SandboxDecl struct {
	Isolation         string        `yaml:"isolation,omitempty" json:"isolation,omitempty"` // process / container / microvm
	Image             string        `yaml:"image" json:"image"`
	BudgetSecondsMax  int           `yaml:"budget_seconds_max,omitempty" json:"budget_seconds_max,omitempty"`
	ColdStartTargetMS int           `yaml:"cold_start_target_ms,omitempty" json:"cold_start_target_ms,omitempty"`
	Capabilities      *Capabilities `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
}

// Capabilities is the default-deny capability declaration. Mirrors
// specs/soyapack/v0/capabilities.md. Implementation enforcement lives in
// pkg/runtime.Gate; this type only carries the declaration.
//
// R0 P0 fail-closed triad: NetworkOut + FSRead + FSWrite + Exec form the
// minimum gate surface; anything not listed here must be denied at the
// runtime layer. Exec restricts which argv[0] entries the sandbox may
// invoke (Stage 5 will enforce; see DeniedError.Capability="exec").
type Capabilities struct {
	NetworkOut      []EgressRule   `yaml:"network_out,omitempty" json:"network_out,omitempty"`
	FSRead          []string       `yaml:"fs_read,omitempty" json:"fs_read,omitempty"`
	FSWrite         []string       `yaml:"fs_write,omitempty" json:"fs_write,omitempty"`
	Exec            []string       `yaml:"exec,omitempty" json:"exec,omitempty"`
	Syscalls        []string       `yaml:"syscalls,omitempty" json:"syscalls,omitempty"`
	LLM             *LLMCapability `yaml:"llm,omitempty" json:"llm,omitempty"`
	MCPTools        []string       `yaml:"mcp_tools,omitempty" json:"mcp_tools,omitempty"`
	Memory          []MemoryMount  `yaml:"memory,omitempty" json:"memory,omitempty"`
	StorageNAS      []NASMount     `yaml:"storage_nas,omitempty" json:"storage_nas,omitempty"`
	Secrets         []string       `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	Resources       *Resources     `yaml:"resources,omitempty" json:"resources,omitempty"`
	DeterminismTier string         `yaml:"determinism_tier,omitempty" json:"determinism_tier,omitempty"`
}

// EgressRule is one entry under network_out.
type EgressRule struct {
	Host         string `yaml:"host" json:"host"`
	Port         int    `yaml:"port" json:"port"`
	Proto        string `yaml:"proto" json:"proto"`
	Pin          string `yaml:"pin,omitempty" json:"pin,omitempty"`
	QuotaPerCall int    `yaml:"quota_per_call,omitempty" json:"quota_per_call,omitempty"`
}

// LLMCapability bounds the LLM calls a Pack may make.
type LLMCapability struct {
	Model        string       `yaml:"model,omitempty" json:"model,omitempty"`
	Temperature  *FloatBounds `yaml:"temperature,omitempty" json:"temperature,omitempty"`
	MaxTokens    int          `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
	QuotaPerCall int          `yaml:"quota_per_call,omitempty" json:"quota_per_call,omitempty"`
	QuotaPerDay  int          `yaml:"quota_per_day,omitempty" json:"quota_per_day,omitempty"`
}

// FloatBounds is a closed range [Min, Max].
type FloatBounds struct {
	Min float32 `yaml:"min" json:"min"`
	Max float32 `yaml:"max" json:"max"`
}

// MemoryMount declares a dependency on a Memory Pack.
type MemoryMount struct {
	Name   string `yaml:"name" json:"name"`
	Access string `yaml:"access" json:"access"` // ro / rw
}

// NASMount declares a NAS storage target referenced by a capability
// allowlist. Kept for the legacy `sandbox.capabilities.storage_nas`
// surface; new code should prefer the top-level `storage_nas:` block
// (StorageNASDecl) which carries the protocol-specific connection
// details Comet needs to actually write the artifact.
type NASMount struct {
	Kind   string `yaml:"kind" json:"kind"` // smb / nfs / webdav / s3
	Mount  string `yaml:"mount" json:"mount"`
	Access string `yaml:"access" json:"access"` // ro / rw
}

// StorageNASDecl is one entry under the top-level `storage_nas:` block
// (DD-011 SilentCut). Where NASMount only names a capability slot,
// StorageNASDecl carries the full connection recipe the kernel hands
// to Comet's NAS connector layer:
//
//   - Protocol — one of "smb", "nfs", "webdav", "s3"; the four
//     pkg/connectors/nas drivers ride here.
//   - HostRef  — the network address. Must be of the form ${ENV_NAME}
//     so the operator's deployment env supplies it; inline hosts are
//     allowed (no secret leak risk) but the SilentCut alpha
//     recommendation is to keep them in env so the manifest is
//     deploy-environment-agnostic.
//   - Share    — protocol-specific path inside HostRef (SMB share
//     name, NFS export path, WebDAV root, S3 bucket).
//   - Access   — "ro" / "rw".
//   - Secrets  — env-var refs ({"username_ref": "${SOYA_NAS_USER}", ...}).
//     The same ${ENV_NAME} rule that governs channels.secrets[*]
//     governs these.
//   - ID       — optional stable identifier so multiple NAS targets
//     can coexist; defaults to "primary" when omitted.
type StorageNASDecl struct {
	ID       string            `yaml:"id,omitempty" json:"id,omitempty"`
	Protocol string            `yaml:"protocol" json:"protocol"`
	HostRef  string            `yaml:"host_ref" json:"host_ref"`
	Share    string            `yaml:"share" json:"share"`
	Access   string            `yaml:"access,omitempty" json:"access,omitempty"`
	Secrets  map[string]string `yaml:"secrets,omitempty" json:"secrets,omitempty"`
}

// DataPlaneDecl controls whether large Pack artifacts are allowed to fall
// back through Planet. Direct=true is the DD-011 SilentCut posture: Planet
// may coordinate and audit the run, but MP4 bytes must flow directly between
// Comet and Moon/NAS. A host that cannot establish a direct path must fail the
// artifact delivery instead of silently proxying it through the control
// plane.
type DataPlaneDecl struct {
	Direct bool `yaml:"direct" json:"direct"`
}

// Resources caps the compute budget per Pack invocation.
type Resources struct {
	CPU      int `yaml:"cpu,omitempty" json:"cpu,omitempty"` // vCPU
	RAMMB    int `yaml:"ram_mb,omitempty" json:"ram_mb,omitempty"`
	TimeoutS int `yaml:"timeout_s,omitempty" json:"timeout_s,omitempty"`
	GPU      int `yaml:"gpu,omitempty" json:"gpu,omitempty"`
}

// --- Memory-side declarations ------------------------------------------------

// Mount describes how a Memory Pack is mounted.
type Mount struct {
	Partition string          `yaml:"partition" json:"partition"`
	Access    string          `yaml:"access" json:"access"` // ro / rw
	Format    string          `yaml:"format" json:"format"` // embeddings / kv / json-lines
	Embedding *EmbeddingMount `yaml:"embedding,omitempty" json:"embedding,omitempty"`
}

// EmbeddingMount pins the embedding model used to build a Memory Pack.
type EmbeddingMount struct {
	Model       string `yaml:"model" json:"model"`
	Fingerprint string `yaml:"fingerprint" json:"fingerprint"`
	Dim         int    `yaml:"dim" json:"dim"`
}

// Content is one file entry in a Memory Pack.
type Content struct {
	Path   string `yaml:"path" json:"path"`
	SHA256 string `yaml:"sha256" json:"sha256"`
	Size   int64  `yaml:"size" json:"size"`
}

// --- top-level strict decoder ------------------------------------------------

// knownTopLevelFields mirrors the YAML keys defined as top-level on Manifest
// above. Used by UnmarshalYAML to enforce SoyaPack's "reject unknown fields
// not prefixed with x-" rule.
var knownTopLevelFields = map[string]struct{}{
	"spec_version": {},
	"kind":         {},
	"name":         {},
	"version":      {},
	"description":  {},
	"authors":      {},
	"license":      {},
	"homepage":     {},
	"runtime":      {},
	"determinism":  {},
	"affinity":     {},
	"deps":         {},
	"sbom":         {},
	"signatures":   {},
	"entry":        {},
	"expose":       {},
	"inputs":       {},
	"outputs":      {},
	"prompt":       {},
	"artifacts":    {},
	"schedules":    {},
	"channels":     {},
	"actions":      {},
	"state":        {},
	"storage_nas":  {},
	"data_plane":   {},
	"sandbox":      {},
	"uses":         {},
	"capabilities": {},
	"mount":        {},
	"contents":     {},
}

// UnmarshalYAML implements the strict top-level rule:
//
//   - Keys prefixed with `x-` are captured in Manifest.Extensions verbatim.
//   - Any other unknown key returns an error referencing the bad field name.
//
// Nested fields use yaml.v3's default behavior (lenient on extras); strictness
// is intentionally only enforced at the top level, matching spec §"Manifest
// canonicalization".
func (m *Manifest) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("soyapack: manifest must be a YAML mapping at top level (got kind=%d)", node.Kind)
	}

	m.Extensions = map[string]any{}
	pruned := make([]*yaml.Node, 0, len(node.Content))

	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]
		key := keyNode.Value

		if strings.HasPrefix(key, "x-") {
			var raw any
			if err := valNode.Decode(&raw); err != nil {
				return fmt.Errorf("soyapack: decode extension %q: %w", key, err)
			}
			m.Extensions[key] = raw
			continue
		}

		if _, ok := knownTopLevelFields[key]; !ok {
			return fmt.Errorf("soyapack: unknown top-level field %q (use the x- prefix for extensions)", key)
		}
		pruned = append(pruned, keyNode, valNode)
	}

	// Re-decode pruned mapping through an alias type so we don't recurse
	// into our own UnmarshalYAML.
	type alias Manifest
	var a alias
	prunedNode := &yaml.Node{Kind: yaml.MappingNode, Tag: node.Tag, Content: pruned}
	if err := prunedNode.Decode(&a); err != nil {
		return err
	}

	ext := m.Extensions
	*m = Manifest(a)
	m.Extensions = ext
	return nil
}
