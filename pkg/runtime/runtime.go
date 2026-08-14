// Package runtime is the Comet runtime façade.
//
// Comet is a task-scoped sandbox (microVM / container / process). Any node
// in the SoyaOS topology may host Comet tasks if its config sets
// `hosts-comet=true`. This package owns the descriptors for sandbox profiles
// and the capability-gated Local process executor used by Solo.
//
// Real microVM / container backends arrive alongside the SilentCut milestone
// (DD-011) where per-second lifecycle and pre-warmed image pools are
// required.
package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Profile is the runtime isolation axis: process | container | microvm.
//
// Spec §3.4 separates the *isolation* tier (this type) from the *image*
// tier (Task.Image / sandbox.image), which historically rode the same
// dimension as Profile. The image-preset concept (e.g. "video-base@0.1.0")
// now lives entirely in Task.Image / SandboxDecl.Image.
type Profile string

const (
	ProfileProcess   Profile = "process"
	ProfileContainer Profile = "container"
	ProfileMicroVM   Profile = "microvm"
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
	Caps           Caps    // immutable capability snapshot for this invocation
	Access         *Access // required declaration of network/filesystem side effects
}

// Result captures the terminal state of a Task.
type Result struct {
	TaskID   string
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Err      error
}

// ErrNotImplemented is returned by provider stubs for isolation tiers the
// Solo edition does not yet support (currently container and microVM).
var ErrNotImplemented = errors.New("runtime: not implemented in solo alpha")

// ErrInvalidTask identifies a task rejected before capability evaluation or
// process creation because its execution shape is incomplete.
var ErrInvalidTask = errors.New("runtime: invalid task")

// Executor runs sandboxed Tasks.
type Executor interface {
	Run(ctx context.Context, t Task) (Result, error)
}

// Local is the Solo process executor. It performs capability admission before
// process creation; stronger OS-level confinement belongs to the container and
// microVM providers.
type Local struct{}

// Run authorizes all declared side effects, then executes the task locally.
// Capability denial is returned directly as a typed *DeniedError and copied to
// Result.Err; no child process is created and nothing is logged by runtime.
func (Local) Run(ctx context.Context, t Task) (Result, error) {
	result := Result{TaskID: t.ID}
	if len(t.Command) == 0 {
		err := errors.Join(ErrInvalidTask, errors.New("command is required"))
		result.Err = err
		return result, err
	}
	if t.Access == nil {
		err := errors.Join(ErrInvalidTask, errors.New("access declaration is required"))
		result.Err = err
		return result, err
	}

	if err := NewGate(t.Caps).Authorize(t.Command[0], *t.Access); err != nil {
		result.Err = err
		return result, err
	}

	runCtx := ctx
	cancel := func() {}
	if t.BudgetSeconds > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(t.BudgetSeconds)*time.Second)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, t.Command[0], t.Command[1:]...)
	if len(t.Env) > 0 {
		cmd.Env = mergedEnv(t.Env)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result.Stdout = stdout.Bytes()
	result.Stderr = stderr.Bytes()
	if err == nil {
		return result, nil
	}
	if runCtx.Err() != nil {
		result.ExitCode = -1
		result.Err = runCtx.Err()
		return result, runCtx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		result.Err = err
		return result, nil
	}
	result.ExitCode = -1
	result.Err = err
	return result, err
}

func mergedEnv(overrides map[string]string) []string {
	env := make(map[string]string, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	for key, value := range overrides {
		env[key] = value
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+env[key])
	}
	return result
}
