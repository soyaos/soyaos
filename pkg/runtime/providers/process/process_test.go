package process

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/soyaos/soyaos/pkg/runtime"
)

func execCaps(names ...string) runtime.Caps {
	return runtime.Caps{Exec: names}
}

func TestProvider_Capabilities(t *testing.T) {
	p := New()
	caps := p.Capabilities()
	if caps.Profile != runtime.ProfileProcess {
		t.Fatalf("Profile=%q, want process", caps.Profile)
	}
	if !caps.FS || !caps.Exec {
		t.Fatalf("process tier must report FS+Exec, got %+v", caps)
	}
}

func TestProvider_ProvisionExecuteEcho(t *testing.T) {
	p := New()
	ctx := context.Background()
	h, err := p.Provision(ctx, runtime.ProvisionRequest{
		Profile: runtime.ProfileProcess,
		Caps:    execCaps("echo"),
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if h == "" {
		t.Fatal("Provision returned empty handle")
	}

	res, err := p.Execute(ctx, h, runtime.ExecuteRequest{Cmd: []string{"echo", "hello"}, Access: &runtime.Access{}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode=%d, want 0", res.ExitCode)
	}
	if !strings.Contains(string(res.Stdout), "hello") {
		t.Errorf("stdout=%q, want to contain 'hello'", res.Stdout)
	}
}

func TestProvider_ExecuteUnknownHandle(t *testing.T) {
	p := New()
	_, err := p.Execute(context.Background(), runtime.Handle("bogus"), runtime.ExecuteRequest{Cmd: []string{"true"}})
	if err == nil {
		t.Fatal("Execute on unknown handle must error")
	}
}

func TestProvider_ExecuteEmptyCmd(t *testing.T) {
	p := New()
	h, _ := p.Provision(context.Background(), runtime.ProvisionRequest{})
	_, err := p.Execute(context.Background(), h, runtime.ExecuteRequest{})
	if err == nil {
		t.Fatal("Execute with empty Cmd must error")
	}
}

func TestProvider_ExecuteFailClosedAndTyped(t *testing.T) {
	p := New()
	h, err := p.Provision(context.Background(), runtime.ProvisionRequest{})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	_, err = p.Execute(context.Background(), h, runtime.ExecuteRequest{Cmd: []string{"definitely-not-started"}, Access: &runtime.Access{}})
	if !errors.Is(err, runtime.ErrDeniedByCapability) {
		t.Fatalf("Execute error = %v, want ErrDeniedByCapability", err)
	}
	var denied *runtime.DeniedError
	if !errors.As(err, &denied) || denied.Capability != "exec" {
		t.Fatalf("Execute error = %#v, want exec *DeniedError", err)
	}
}

func TestProvider_ExecuteAuthorizesDeclaredSideEffects(t *testing.T) {
	p := New()
	caps := runtime.Caps{
		Exec:       []string{"true"},
		NetworkOut: []runtime.NetRule{{Host: "api.example.com", Port: 443, Proto: "https"}},
		FSRead:     []string{"/work/input"},
		FSWrite:    []string{"/work/output"},
	}
	h, err := p.Provision(context.Background(), runtime.ProvisionRequest{Caps: caps})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	access := runtime.Access{
		NetworkOut: []runtime.EgressAccess{{Host: "api.example.com", Port: 443, Proto: "https"}},
		FSRead:     []string{"/work/input/source.txt"},
		FSWrite:    []string{"/work/output/result.txt"},
	}
	if _, err := p.Execute(context.Background(), h, runtime.ExecuteRequest{Cmd: []string{"true"}, Access: &access}); err != nil {
		t.Fatalf("Execute allowed request: %v", err)
	}
}

func TestProvider_ExecuteDeniesEachDeclaredSideEffect(t *testing.T) {
	tests := []struct {
		name       string
		access     runtime.Access
		capability string
	}{
		{
			name:       "network",
			access:     runtime.Access{NetworkOut: []runtime.EgressAccess{{Host: "blocked.example.com", Port: 443, Proto: "https"}}},
			capability: "network_out",
		},
		{name: "fs read", access: runtime.Access{FSRead: []string{"/blocked/input"}}, capability: "fs_read"},
		{name: "fs write", access: runtime.Access{FSWrite: []string{"/blocked/output"}}, capability: "fs_write"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := New()
			h, err := p.Provision(context.Background(), runtime.ProvisionRequest{Caps: execCaps("true")})
			if err != nil {
				t.Fatalf("Provision: %v", err)
			}
			access := test.access
			_, err = p.Execute(context.Background(), h, runtime.ExecuteRequest{Cmd: []string{"true"}, Access: &access})
			var denied *runtime.DeniedError
			if !errors.Is(err, runtime.ErrDeniedByCapability) || !errors.As(err, &denied) {
				t.Fatalf("Execute error = %v, want typed capability denial", err)
			}
			if denied.Capability != test.capability {
				t.Fatalf("Capability = %q, want %q", denied.Capability, test.capability)
			}
		})
	}
}

func TestProvider_TerminateThenExecuteFails(t *testing.T) {
	p := New()
	ctx := context.Background()
	h, _ := p.Provision(ctx, runtime.ProvisionRequest{Caps: execCaps("sh")})
	if err := p.Terminate(ctx, h); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if _, err := p.Execute(ctx, h, runtime.ExecuteRequest{Cmd: []string{"echo"}}); err == nil {
		t.Fatal("Execute after Terminate must error")
	}
	if err := p.Terminate(ctx, h); err == nil {
		t.Fatal("double Terminate must error")
	}
}

func TestProvider_ExecuteRejectsMissingAccessDeclaration(t *testing.T) {
	p := New()
	h, err := p.Provision(context.Background(), runtime.ProvisionRequest{Caps: execCaps("true")})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	_, err = p.Execute(context.Background(), h, runtime.ExecuteRequest{Cmd: []string{"true"}})
	if !errors.Is(err, runtime.ErrInvalidTask) {
		t.Fatalf("Execute error = %v, want ErrInvalidTask", err)
	}
}

func TestProvider_CostMonotonic(t *testing.T) {
	p := New()
	ctx := context.Background()
	h, _ := p.Provision(ctx, runtime.ProvisionRequest{})
	c, err := p.Cost(ctx, h)
	if err != nil {
		t.Fatalf("Cost: %v", err)
	}
	if c.VCPUSeconds < 0 {
		t.Errorf("VCPUSeconds=%d, want >= 0", c.VCPUSeconds)
	}
}

func TestProvider_RejectsWrongProfile(t *testing.T) {
	p := New()
	_, err := p.Provision(context.Background(), runtime.ProvisionRequest{Profile: runtime.ProfileMicroVM})
	if err == nil {
		t.Fatal("Provision with microvm profile must be rejected by process provider")
	}
}

func TestProvider_ExecuteNonZeroExit(t *testing.T) {
	p := New()
	ctx := context.Background()
	h, _ := p.Provision(ctx, runtime.ProvisionRequest{Caps: execCaps("sh")})
	res, err := p.Execute(ctx, h, runtime.ExecuteRequest{Cmd: []string{"sh", "-c", "exit 7"}, Access: &runtime.Access{}})
	if err != nil {
		// non-zero exit should be reported via ExitCode, not a Go error.
		// (sh is universally present on macOS + linux test runners.)
		t.Fatalf("Execute returned err for non-zero exit: %v", err)
	}
	if res.ExitCode != 7 {
		t.Errorf("ExitCode=%d, want 7", res.ExitCode)
	}
}
