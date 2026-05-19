// Package factory is the Agent Factory — natural language → Agent manifest
// translation (DD-009, NewsBeam).
//
// v0.1.0-alpha.0 exposes only the descriptor types. The real translator is a
// kernel-internal Agent that is statically deployed (to avoid the chicken-and-
// egg problem of bootstrapping the Factory through itself); it lands with the
// NewsBeam milestone.
package factory

// Manifest is the structured form an Agent Factory produces from NL input.
//
// Shape matches the SoyaPack `soyapack.yaml` for `kind: Agent` per the
// alignment checklist's required schema extension (DD-005/006/007/010/011).
type Manifest struct {
	Kind    string `json:"kind"`              // always "Agent"
	Name    string `json:"name"`              // slug, unique within owner scope
	Version string `json:"version"`           // SemVer, default "0.1.0"
	Expose  Expose `json:"expose,omitempty"`  // OpenAI-Compat surface

	Artifacts []ArtifactDecl `json:"artifacts,omitempty"`
	Schedules []ScheduleDecl `json:"schedules,omitempty"`
	Channels  []ChannelDecl  `json:"channels,omitempty"`
	Actions   []ActionDecl   `json:"actions,omitempty"`
	State     *StateDecl     `json:"state,omitempty"`
	Sandbox   *SandboxDecl   `json:"sandbox,omitempty"`
}

// Expose controls the OpenAI-Compat virtual-model identity (DD-005).
type Expose struct {
	OpenAICompat    string `json:"openai_compat,omitempty"` // "chat" / "responses" / "both"
	VirtualModelID  string `json:"virtual_model_id,omitempty"`
}

// ArtifactDecl declares an output form. (Proposed DD-012.)
type ArtifactDecl struct {
	Kind   string `json:"kind"`   // html / pdf / long_image / markdown / xlsx / mp4
	Schema string `json:"schema"` // schema id + SemVer
}

// ScheduleDecl declares a cron / one-shot trigger (DD-007).
type ScheduleDecl struct {
	Cron           string         `json:"cron,omitempty"`
	Once           string         `json:"once,omitempty"`
	TZ             string         `json:"tz,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	MissedFire     string         `json:"missed_fire,omitempty"` // skip / once / backfill
}

// ChannelDecl binds the Agent to an external channel (DD-006).
type ChannelDecl struct {
	Kind            string `json:"kind"`
	BindingTemplate string `json:"binding_template,omitempty"`
}

// ActionDecl describes a row-scoped or button-scoped action (DD-010).
type ActionDecl struct {
	ID        string   `json:"id"`
	On        string   `json:"on"` // "per_row" / "button" / "api"
	Handler   string   `json:"handler"`
	Timeout   string   `json:"timeout,omitempty"`
	Artifacts []string `json:"artifacts,omitempty"`
}

// StateDecl declares Stateful Agent storage (DD-010).
type StateDecl struct {
	Scope string `json:"scope"` // agent / user / tenant
	Store string `json:"store"` // memory / kv / db
}

// SandboxDecl declares the Comet sandbox profile (DD-011).
type SandboxDecl struct {
	Image            string `json:"image"`
	BudgetSecondsMax int    `json:"budget_seconds_max,omitempty"`
	ColdStartTargetMS int   `json:"cold_start_target_ms,omitempty"`
}
