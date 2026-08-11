package runtime

import "errors"

// ErrDeniedByCapability is the sentinel returned (wrapped) when the
// capability gate refuses an operation. Callers use
// `errors.Is(err, runtime.ErrDeniedByCapability)` to distinguish a
// policy denial from infrastructure errors or ErrNotImplemented.
var ErrDeniedByCapability = errors.New("denied by capability")

// DeniedError carries the structured details of a capability-gate
// rejection: which capability tripped, which resource the sandbox
// asked for, and a short human-readable reason. It unwraps to
// ErrDeniedByCapability so the sentinel check above keeps working.
//
// Capability is one of: "network_out" | "fs_read" | "fs_write" | "exec".
// Resource is the gate-specific identifier — host:port for network_out,
// an absolute path for fs_*, argv[0] for exec.
// Reason is short and stable (e.g. "not in allowlist", "explicitly denied").
type DeniedError struct {
	Capability string
	Resource   string
	Reason     string
}

// Error returns a single-line message of the form
// `runtime: denied by capability <cap> (<resource>): <reason>`.
func (e *DeniedError) Error() string {
	return "runtime: denied by capability " + e.Capability + " (" + e.Resource + "): " + e.Reason
}

// Unwrap exposes ErrDeniedByCapability so callers can match the denial
// class without depending on the concrete *DeniedError type.
func (e *DeniedError) Unwrap() error { return ErrDeniedByCapability }
