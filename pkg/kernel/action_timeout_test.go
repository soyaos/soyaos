package kernel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

func TestActionTimeoutHandlerCancellation(t *testing.T) {
	for _, lateNil := range []bool{false, true} {
		k := New()
		k.Register(Agent{Slug: "timed", Manifest: &soyapack.Manifest{Actions: []soyapack.ActionDecl{{ID: "run", Timeout: "10ms"}}}})
		canceled := false
		k.SetActionHandler(func(ctx context.Context, _ soyapack.ActionDecl, _ ActionRequest) (ActionResult, error) {
			if lateNil {
				time.Sleep(30 * time.Millisecond) // deliberately ignores cancellation while working
				canceled = ctx.Err() != nil
				return ActionResult{Status: "done"}, nil
			}
			<-ctx.Done()
			canceled = true
			return ActionResult{}, ctx.Err()
		})
		result, err := k.InvokeAction(context.Background(), auth.Identity{}, "timed", "run", "row-1", nil)
		if !canceled || !errors.Is(err, context.DeadlineExceeded) || result.Status != "" {
			t.Fatalf("lateNil=%v canceled=%v result=%#v err=%v", lateNil, canceled, result, err)
		}
	}
}

func TestActionTimeoutPreservesNoTimeoutAndParentDeadline(t *testing.T) {
	k := New()
	m := &soyapack.Manifest{Actions: []soyapack.ActionDecl{{ID: "run"}}}
	k.Register(Agent{Slug: "timed", Manifest: m})
	original := context.WithValue(context.Background(), struct{}{}, "sentinel")
	k.SetActionHandler(func(ctx context.Context, _ soyapack.ActionDecl, _ ActionRequest) (ActionResult, error) {
		if ctx != original {
			t.Error("no-timeout context changed")
		}
		return ActionResult{Status: "done"}, nil
	})
	if _, err := k.InvokeAction(original, auth.Identity{}, "timed", "run", "row-1", nil); err != nil {
		t.Fatal(err)
	}
	m.Actions[0].Timeout = "1h"
	parent, parentCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer parentCancel()
	parentDeadline, _ := parent.Deadline()
	k.SetActionHandler(func(ctx context.Context, _ soyapack.ActionDecl, _ ActionRequest) (ActionResult, error) {
		deadline, ok := ctx.Deadline()
		if !ok || !deadline.Equal(parentDeadline) {
			t.Error("action extended parent deadline")
		}
		<-ctx.Done()
		return ActionResult{}, ctx.Err()
	})
	if _, err := k.InvokeAction(parent, auth.Identity{}, "timed", "run", "row-1", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("parent deadline lost: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	k.SetActionHandler(func(ctx context.Context, _ soyapack.ActionDecl, _ ActionRequest) (ActionResult, error) {
		return ActionResult{}, ctx.Err()
	})
	if _, err := k.InvokeAction(ctx, auth.Identity{}, "timed", "run", "row-1", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancellation lost: %v", err)
	}
}

func TestActionTimeoutRejectsInvalidRuntimeManifest(t *testing.T) {
	for _, timeout := range []string{"invalid", "0s", "-1s"} {
		k := New()
		k.Register(Agent{Slug: "timed", Manifest: &soyapack.Manifest{Actions: []soyapack.ActionDecl{{ID: "run", Timeout: timeout}}}})
		called := false
		k.SetActionHandler(func(context.Context, soyapack.ActionDecl, ActionRequest) (ActionResult, error) {
			called = true
			return ActionResult{}, nil
		})
		if _, err := k.InvokeAction(context.Background(), auth.Identity{}, "timed", "run", "row-1", nil); err == nil || called {
			t.Fatalf("timeout=%s err=%v called=%v", timeout, err, called)
		}
	}
}

func TestActionTimeoutCancelsPackProvider(t *testing.T) {
	m := minimalAgentManifest("timed", "timed")
	m.Actions = []soyapack.ActionDecl{{ID: "run", On: "per_row", Handler: m.Entry, Timeout: "10ms"}}
	dir := writePack(t, m, "action prompt")
	provider := &indexedCancelProvider{canceled: make(chan struct{})}
	k := New()
	if err := k.registerFromPack(m, dir, func(llmcall.Config) llmcall.Provider { return provider }); err != nil {
		t.Fatal(err)
	}
	if _, err := k.InvokeAction(context.Background(), auth.Identity{}, "timed", "run", "row-1", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	select {
	case <-provider.canceled:
	default:
		t.Fatal("provider not canceled")
	}
}
