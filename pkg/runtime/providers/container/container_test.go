package container

import (
	"context"
	"errors"
	"testing"

	"github.com/soyaos/soyaos/pkg/runtime"
)

func TestContainerProvider_StubReturnsErrNotImplemented(t *testing.T) {
	// alpha stub: every operation must return ErrNotImplemented so callers
	// can branch on errors.Is and fall back to other providers cleanly.
	p := New()
	ctx := context.Background()

	if caps := p.Capabilities(); caps.Profile != runtime.ProfileContainer {
		t.Fatalf("Profile=%q, want container", caps.Profile)
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

func TestContainerProvider_SkippedIntegration(t *testing.T) {
	t.Skip("containerd integration is an alpha stub; real /run/containerd/containerd.sock wiring lands in Stage 5")
}
