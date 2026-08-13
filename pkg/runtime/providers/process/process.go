// Package process is the ProfileProcess CometProvider — the lightest of the
// three isolation tiers. It executes commands directly on the host with
// os/exec, scoped only by argv allowlists from the SoyaPack capability gate.
//
// SilentCut (DD-011) reverse-pressure: SilentCut's 30s clip pipeline must
// land at < 5 min wall-clock and many of its steps (ffmpeg probe, subtitle
// extraction, tiny ML inferences) do not need a microVM. The process tier
// exists so those cheap steps avoid Firecracker cold-start tax while still
// going through the same CometProvider five-method contract Stage 2 froze.
//
// alpha shape:
//   - Provision allocates an in-memory sandbox descriptor (uuid + image +
//     started-at) — no chroot, no cgroups, no Landlock yet.
//   - Execute shells out via os.exec.CommandContext. The TODOs below mark
//     where cgroups v2 (Linux) and Landlock / Seatbelt (macOS) integration
//     will plug in before this tier ships to operators.
//   - Cost reports VCPUSeconds derived from wall-clock since Provision; the
//     metering aggregator (APP-514) refines this to 100ms ticks.
package process

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os/exec"
	"sync"
	"time"

	"github.com/soyaos/soyaos/pkg/runtime"
	"github.com/soyaos/soyaos/pkg/scope"
)

// Provider is the ProfileProcess CometProvider.
type Provider struct {
	mu        sync.Mutex
	sandboxes map[runtime.Handle]*sandbox
	usage     *scope.UsageAggregator // optional; nil ⇒ no metering hook
}

type sandbox struct {
	image        string
	started      time.Time
	lastMetered  time.Time
	gate         *runtime.Gate
	apiKeyPrefix string
	agentSlug    string
	stopTick     chan struct{}
}

// New returns an empty Provider. Handles are issued by Provision; nothing is
// scheduled until then.
func New() *Provider {
	return &Provider{sandboxes: map[runtime.Handle]*sandbox{}}
}

// WithUsage wires a UsageAggregator into the provider; subsequent Execute
// calls record per-100ms tick samples into u. Returns p for chaining.
//
// SilentCut (DD-011) reverse-pressure: per-second billing for SilentCut
// means cost samples must hit the aggregator at finer than per-second
// granularity. Hook the aggregator here so all three tiers
// (process / container / microvm) share the same Tick call shape.
func (p *Provider) WithUsage(u *scope.UsageAggregator) *Provider {
	p.usage = u
	return p
}

// Capabilities advertises the process tier's surface: no network egress (the
// gate decides per-call), filesystem access yes, exec yes.
func (*Provider) Capabilities() runtime.Capabilities {
	return runtime.Capabilities{
		Profile: runtime.ProfileProcess,
		Network: false,
		FS:      true,
		Exec:    true,
	}
}

// Provision records a new sandbox descriptor and returns its opaque Handle.
// The Image field is informational — the process tier has no image registry.
func (p *Provider) Provision(_ context.Context, req runtime.ProvisionRequest) (runtime.Handle, error) {
	if req.Profile != "" && req.Profile != runtime.ProfileProcess {
		return "", errors.New("process: ProvisionRequest.Profile must be process or empty")
	}
	now := time.Now()
	h := runtime.Handle(newID())
	p.mu.Lock()
	p.sandboxes[h] = &sandbox{
		image:       req.Image,
		started:     now,
		lastMetered: now,
		gate:        runtime.NewGate(req.Caps),
	}
	p.mu.Unlock()
	return h, nil
}

// LabelHandle attaches metering labels (api_key_prefix, agent_slug) to a
// previously-provisioned Handle. The CometProvider contract has no place
// for these labels — they are call-site policy data, not sandbox data —
// so callers attach them in a follow-up call.
func (p *Provider) LabelHandle(h runtime.Handle, apiKeyPrefix, agentSlug string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sandboxes[h]
	if !ok {
		return errors.New("process: unknown handle")
	}
	s.apiKeyPrefix = apiKeyPrefix
	s.agentSlug = agentSlug
	return nil
}

// Execute runs req.Cmd inside the sandbox identified by h and returns its
// terminal state. Stdin is piped if non-empty.
//
// TODO(SilentCut): wire cgroups v2 (cpu.max / memory.max) on Linux and
// Landlock LSM where available; on Darwin use Seatbelt
// (sandbox_init / sandbox-exec) to clamp fs + network. The gate already
// authorizes the *intent*; the OS-level enforcement is the next hardening
// pass and intentionally absent from the alpha to keep the contract small.
func (p *Provider) Execute(ctx context.Context, h runtime.Handle, req runtime.ExecuteRequest) (runtime.ExecuteResult, error) {
	p.mu.Lock()
	s, ok := p.sandboxes[h]
	p.mu.Unlock()
	if !ok {
		return runtime.ExecuteResult{}, errors.New("process: unknown handle")
	}
	if len(req.Cmd) == 0 {
		return runtime.ExecuteResult{}, errors.New("process: empty Cmd")
	}
	if req.Access == nil {
		return runtime.ExecuteResult{}, errors.Join(runtime.ErrInvalidTask, errors.New("access declaration is required"))
	}
	if err := s.gate.Authorize(req.Cmd[0], *req.Access); err != nil {
		return runtime.ExecuteResult{}, err
	}
	cmd := exec.CommandContext(ctx, req.Cmd[0], req.Cmd[1:]...)
	if len(req.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(req.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Per-100ms metering tick. The goroutine stops when this Execute
	// returns (close(done)). Each tick records the delta since the last
	// tick so consumption never double-counts across ticks or Execute
	// invocations.
	done := make(chan struct{})
	if p.usage != nil {
		go p.tickUsage(s, done)
	}
	defer close(done)

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			// non-exit error (couldn't start, ctx cancel, etc) surfaces both
			// as ExitCode -1 and via err semantics for the caller.
			return runtime.ExecuteResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: -1}, err
		}
	}
	return runtime.ExecuteResult{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		ExitCode: exitCode,
	}, nil
}

// Terminate releases the sandbox descriptor. The process tier has no
// long-lived child to kill; subsequent Execute calls on h return an error.
func (p *Provider) Terminate(_ context.Context, h runtime.Handle) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.sandboxes[h]; !ok {
		return errors.New("process: unknown handle")
	}
	delete(p.sandboxes, h)
	return nil
}

// Cost reports a monotonic counter snapshot since Provision. VCPUSeconds is
// approximated by wall-clock seconds; APP-514's 100ms aggregator refines this.
func (p *Provider) Cost(_ context.Context, h runtime.Handle) (runtime.CostSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sandboxes[h]
	if !ok {
		return runtime.CostSnapshot{}, errors.New("process: unknown handle")
	}
	elapsed := time.Since(s.started).Seconds()
	return runtime.CostSnapshot{
		VCPUSeconds: int64(elapsed),
	}, nil
}

// tickUsage emits a UsageSample every 100ms until done is closed. Each
// sample carries the delta since the prior tick so the aggregator never
// double-counts. Safe to call with usage==nil — caller already gated on
// that.
func (p *Provider) tickUsage(s *sandbox, done <-chan struct{}) {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-done:
			p.emitTick(s)
			return
		case <-t.C:
			p.emitTick(s)
		}
	}
}

// emitTick computes the delta since lastMetered and pushes one sample.
func (p *Provider) emitTick(s *sandbox) {
	p.mu.Lock()
	now := time.Now()
	delta := now.Sub(s.lastMetered).Seconds()
	s.lastMetered = now
	sample := scope.UsageSample{
		APIKeyPrefix: s.apiKeyPrefix,
		AgentSlug:    s.agentSlug,
		SandboxImage: s.image,
		At:           now,
		VCPUSeconds:  delta,
	}
	p.mu.Unlock()
	p.usage.Tick(sample)
}

// newID returns a 32-hex-char opaque identifier. We avoid pulling a uuid
// dependency for a value that callers must treat as opaque anyway.
func newID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Cannot happen on supported platforms; fall back to a time-based
		// best-effort identifier rather than panicking.
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(buf[:])
}

// Compile-time assertion that *Provider satisfies the CometProvider contract.
var _ runtime.CometProvider = (*Provider)(nil)
