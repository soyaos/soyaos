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
	Entry     string         `yaml:"entry,omitempty" json:"entry,omitempty"`
	Expose    *Expose        `yaml:"expose,omitempty" json:"expose,omitempty"`
	Inputs    []Input        `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Outputs   []any          `yaml:"outputs,omitempty" json:"outputs,omitempty"`
	Prompt    *Prompt        `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	Artifacts []ArtifactDecl `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
	Schedules []ScheduleDecl `yaml:"schedules,omitempty" json:"schedules,omitempty"`
	Channels  []ChannelDecl  `yaml:"channels,omitempty" json:"channels,omitempty"`
	Actions   []ActionDecl   `yaml:"actions,omitempty" json:"actions,omitempty"`
	State     *StateDecl     `yaml:"state,omitempty" json:"state,omitempty"`
	Sandbox   *SandboxDecl   `yaml:"sandbox,omitempty" json:"sandbox,omitempty"`
	Uses      []string       `yaml:"uses,omitempty" json:"uses,omitempty"`

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
type Prompt struct {
	Scaffold string   `yaml:"scaffold,omitempty" json:"scaffold,omitempty"`
	Tools    []string `yaml:"tools,omitempty" json:"tools,omitempty"`
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
type ChannelDecl struct {
	Kind            string `yaml:"kind" json:"kind"`
	BindingTemplate string `yaml:"binding_template,omitempty" json:"binding_template,omitempty"`
}

// ActionDecl describes a row / button / api action trigger (DD-010).
type ActionDecl struct {
	ID        string   `yaml:"id" json:"id"`
	On        string   `yaml:"on" json:"on"` // per_row / button / api
	Handler   string   `yaml:"handler" json:"handler"`
	Timeout   string   `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Artifacts []string `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
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
type Capabilities struct {
	NetworkOut      []EgressRule   `yaml:"network_out,omitempty" json:"network_out,omitempty"`
	FSRead          []string       `yaml:"fs_read,omitempty" json:"fs_read,omitempty"`
	FSWrite         []string       `yaml:"fs_write,omitempty" json:"fs_write,omitempty"`
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
	Model         string       `yaml:"model,omitempty" json:"model,omitempty"`
	Temperature   *FloatBounds `yaml:"temperature,omitempty" json:"temperature,omitempty"`
	MaxTokens     int          `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
	QuotaPerCall  int          `yaml:"quota_per_call,omitempty" json:"quota_per_call,omitempty"`
	QuotaPerDay   int          `yaml:"quota_per_day,omitempty" json:"quota_per_day,omitempty"`
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

// NASMount declares a NAS storage target (DD-011).
type NASMount struct {
	Kind   string `yaml:"kind" json:"kind"` // smb / nfs / webdav / s3
	Mount  string `yaml:"mount" json:"mount"`
	Access string `yaml:"access" json:"access"` // ro / rw
}

// Resources caps the compute budget per Pack invocation.
type Resources struct {
	CPU      int `yaml:"cpu,omitempty" json:"cpu,omitempty"`           // vCPU
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
