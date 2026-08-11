// SilentCut reverse-pressure: DD-011 wants per-second billing across
// (API Key prefix, Agent slug, sandbox image). The CometProvider.Cost() RPC
// returns a snapshot every 100ms; this file owns the type the aggregator
// rolls those samples into and persists.

package scope

// KindUsage is the Event.Kind value used by the usage aggregator. Distinct
// from "log" / "span" / "audit" so a downstream Recorder can route the
// usage stream to a separate sink (typical: billing pipeline vs. logs).
const KindUsage = "usage"

// UsagePayload is one (key, agent, image, minute) row. The fields are flat
// so the same struct can be serialized to JSON, billing CSV or OTel
// without restructuring.
//
// Window is the RFC3339Nano timestamp at the *start* of the bucket
// (minute-aligned in the alpha aggregator). The aggregator groups samples
// by Window so down-stream consumers see one row per minute per key/agent
// /image triple.
type UsagePayload struct {
	APIKeyPrefix string  `json:"api_key_prefix"`
	AgentSlug    string  `json:"agent_slug"`
	SandboxImage string  `json:"sandbox_image"`
	Window       string  `json:"window"`
	VCPUSeconds  float64 `json:"vcpu_seconds"`
	GPUSeconds   float64 `json:"gpu_seconds"`
	BytesIn      int64   `json:"bytes_in"`
	BytesOut     int64   `json:"bytes_out"`
}
