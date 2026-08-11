package microvm

import (
	"context"
	"errors"
	"testing"

	"github.com/soyaos/soyaos/pkg/runtime"
)

func TestMicroVMProvider_StubReturnsErrNotImplemented(t *testing.T) {
	// alpha stub: every operation must return ErrNotImplemented so the
	// scheduler can fall back from microvm → container → process cleanly
	// in environments without Firecracker.
	p := New()
	ctx := context.Background()

	if caps := p.Capabilities(); caps.Profile != runtime.ProfileMicroVM {
		t.Fatalf("Profile=%q, want microvm", caps.Profile)
	}

	if _, err := p.Provision(ctx, runtime.ProvisionRequest{}); !errors.Is(err, runtime.ErrNotImplemented) {
		t.Errorf("Provision err=%v, want ErrNotImplemented", err)
	}
	if _, err := p.Execute(ctx, "h", runtime.ExecuteRequest{}); !errors.Is(err, runtime.ErrNotImplemented) {
		t.Errorf("Execute err=%v, want ErrNotImplemented", err)
	}
	if err := p.Terminate(ctx, "h"); !errors.Is(err, runtime.ErrNotImplemented) {
		t.Errorf("Terminate err=%v, want ErrNotImplemented", err)
	}
	if _, err := p.Cost(ctx, "h"); !errors.Is(err, runtime.ErrNotImplemented) {
		t.Errorf("Cost err=%v, want ErrNotImplemented", err)
	}
}

func TestMicroVMProvider_SkippedIntegration(t *testing.T) {
	t.Skip("Firecracker integration is an alpha stub; real /run/firecracker/firecracker.sock wiring lands in Stage 5")
}
