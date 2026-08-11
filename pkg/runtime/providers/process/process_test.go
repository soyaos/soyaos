package process

import (
	"context"
	"strings"
	"testing"

	"github.com/soyaos/soyaos/pkg/runtime"
)

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
	h, err := p.Provision(ctx, runtime.ProvisionRequest{Profile: runtime.ProfileProcess, Image: ""})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if h == "" {
		t.Fatal("Provision returned empty handle")
	}

	res, err := p.Execute(ctx, h, runtime.ExecuteRequest{Cmd: []string{"echo", "hello"}})
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

func TestProvider_TerminateThenExecuteFails(t *testing.T) {
	p := New()
	ctx := context.Background()
	h, _ := p.Provision(ctx, runtime.ProvisionRequest{})
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
	h, _ := p.Provision(ctx, runtime.ProvisionRequest{})
	res, err := p.Execute(ctx, h, runtime.ExecuteRequest{Cmd: []string{"sh", "-c", "exit 7"}})
	if err != nil {
		// non-zero exit should be reported via ExitCode, not a Go error.
		// (sh is universally present on macOS + linux test runners.)
		t.Fatalf("Execute returned err for non-zero exit: %v", err)
	}
	if res.ExitCode != 7 {
		t.Errorf("ExitCode=%d, want 7", res.ExitCode)
	}
}
