// Package runtime is the Comet runtime façade.
//
// Comet is a task-scoped sandbox (microVM / container / process). Any node
// in the SoyaOS topology may host Comet tasks if its config sets
// `hosts-comet=true`. This package owns the descriptors for sandbox profiles
// and a placeholder Local executor used by Solo.
//
// Real microVM / container backends arrive alongside the SilentCut milestone
// (DD-011) where per-second lifecycle and pre-warmed image pools are
// required.
package runtime

import (
	"context"
	"errors"
)

// Profile names a sandbox image preset. Architecture spec § "Comet 镜像分级"
// and proposed DD-018.
type Profile string

const (
	ProfileTextOnly  Profile = "text-only"
	ProfileHTMLBase  Profile = "html-base"
	ProfileVideoBase Profile = "video-base"
)

// Task is a single sandboxed invocation.
type Task struct {
	ID             string
	Profile        Profile
	Image          string // image identifier, e.g. "video-base@0.1.0"
	BudgetSeconds  int    // hard timeout
	ColdStartMSMax int    // SLA target for cold-start; informational
	Command        []string
	Env            map[string]string
}

// Result captures the terminal state of a Task.
type Result struct {
	TaskID   string
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Err      error
}

// ErrNotImplemented is returned by the v0.1.0-alpha.0 Local executor for
// operations that the Solo edition does not yet support.
var ErrNotImplemented = errors.New("runtime: not implemented in solo alpha")

// Executor runs sandboxed Tasks.
type Executor interface {
	Run(ctx context.Context, t Task) (Result, error)
}

// Local is a placeholder Executor; SilentCut (DD-011) will replace this with
// a real per-second microVM backend.
type Local struct{}

// Run currently returns ErrNotImplemented — Solo alpha cannot spawn sandboxes.
func (Local) Run(context.Context, Task) (Result, error) {
	return Result{}, ErrNotImplemented
}
