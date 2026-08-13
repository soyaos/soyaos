// Package runtime declares the CometProvider contract: every comet
// (process / container / microvm) implementation must satisfy this
// interface. The interface is intentionally stable across Stage 2-5;
// concrete process / container / microvm implementations will land in
// subsequent stages (APP-507 et al).
package runtime

import "context"

// CometProvider is the unified contract every Comet backend must satisfy.
// Spec §07 ("统一契约") fixes the five-method shape — Capabilities /
// Provision / Execute / Terminate / Cost — across all three isolation
// profiles so the scheduler can address them uniformly.
//
// stub: this file is a Stage 2 stub; concrete implementations
// (LocalProcessComet, ContainerComet, SilentCutMicroVM) land in Stage 5.
type CometProvider interface {
	Capabilities() Capabilities
	Provision(ctx context.Context, req ProvisionRequest) (Handle, error)
	Execute(ctx context.Context, h Handle, req ExecuteRequest) (ExecuteResult, error)
	Terminate(ctx context.Context, h Handle) error
	Cost(ctx context.Context, h Handle) (CostSnapshot, error)
}

// Capabilities advertises what a CometProvider can do. The scheduler
// reads this once at registration time to decide eligibility.
//
// stub: Stage 5 will likely extend this with GPU / arch / locality fields.
type Capabilities struct {
	Profile Profile
	Network bool
	FS      bool
	Exec    bool
}

// Handle is an opaque, provider-issued identifier for a provisioned
// sandbox. It is meaningful only to the issuing provider; callers must
// treat it as opaque and never parse it.
type Handle string

// ProvisionRequest asks a provider to prepare a sandbox.
//
// stub: Image is provider-interpreted — for process it may be empty,
// for container it is an OCI reference, for microvm it is a rootfs id.
type ProvisionRequest struct {
	Profile Profile
	Image   string
	Caps    Caps // capability snapshot copied into the provisioned sandbox
}

// ExecuteRequest carries one command invocation inside a provisioned
// sandbox. Stage 5 will add streaming variants; the synchronous shape
// here is the lowest common denominator.
type ExecuteRequest struct {
	Cmd    []string
	Stdin  []byte
	Access *Access // required declaration of network/filesystem side effects
}

// ExecuteResult is the terminal state of one Execute call.
type ExecuteResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// CostSnapshot reports the consumption of one Handle since Provision.
// Returned values are monotonic counters; callers may diff snapshots
// to bill per-window. Stage 5 will wire this to per-second microVM
// metering (DD-011).
type CostSnapshot struct {
	VCPUSeconds int64
	GPUSeconds  int64
	BytesIn     int64
	BytesOut    int64
}
