// Package container is the ProfileContainer CometProvider — the middle
// isolation tier. It will speak directly to containerd (no Docker daemon)
// via /run/containerd/containerd.sock so SoyaOS deployments need only the
// runtime, not a higher-level engine.
//
// SilentCut (DD-011) reverse-pressure: SilentCut's Remotion + Chromium
// rendering needs > 1 GiB RAM and a curated set of system libraries
// (ffmpeg, fonts). A container with the video-base image (APP-508) gives
// us OCI portability and ≤ 10s warm starts while keeping the same five-
// method CometProvider contract the scheduler already knows.
//
// alpha shape: this file is intentionally a stub. Every method returns
// runtime.ErrNotImplemented. Stage 5 brings the live containerd client
// (gRPC over the local UNIX socket) and OCI image-pulling. Shipping the
// contract surface now lets the scheduler and tests reference the type
// without pulling in containerd dependencies.
package container

import (
	"context"

	"github.com/soyaos/soyaos/pkg/runtime"
)

// Provider is the ProfileContainer CometProvider.
type Provider struct{}

// New returns the stub Provider. No connection is established yet; the real
// build will accept a containerd-socket path and a namespace.
func New() *Provider { return &Provider{} }

// Capabilities advertises full surface (Network/FS/Exec) because containers
// are the default tier for "general workloads with external I/O".
func (*Provider) Capabilities() runtime.Capabilities {
	return runtime.Capabilities{
		Profile: runtime.ProfileContainer,
		Network: true,
		FS:      true,
		Exec:    true,
	}
}

// Provision will create a containerd container from the OCI image referenced
// by req.Image. alpha: stub.
//
// TODO(Stage5): containerd API via /run/containerd/containerd.sock —
// github.com/containerd/containerd/v2/client.New(socket) + NewContainer with
// OCI runtime spec.
func (*Provider) Provision(context.Context, runtime.ProvisionRequest) (runtime.Handle, error) {
	return "", runtime.ErrNotImplemented
}

// Execute will run a command inside the container. alpha: stub.
//
// TODO(Stage5): containerd task.Exec with a fresh process-spec; stream
// stdout/stderr through fifoset; honor ctx cancellation.
func (*Provider) Execute(context.Context, runtime.Handle, runtime.ExecuteRequest) (runtime.ExecuteResult, error) {
	return runtime.ExecuteResult{}, runtime.ErrNotImplemented
}

// Terminate will kill the task and delete the container. alpha: stub.
//
// TODO(Stage5): SIGTERM → grace period → SIGKILL → delete; reclaim snapshot.
func (*Provider) Terminate(context.Context, runtime.Handle) error {
	return runtime.ErrNotImplemented
}

// Cost will report cgroup-derived counters. alpha: stub.
//
// TODO(Stage5): read cpu.stat + memory.current + per-task net counters from
// the containerd-managed cgroup tree.
func (*Provider) Cost(context.Context, runtime.Handle) (runtime.CostSnapshot, error) {
	return runtime.CostSnapshot{}, runtime.ErrNotImplemented
}

// Compile-time assertion that *Provider satisfies the CometProvider contract.
var _ runtime.CometProvider = (*Provider)(nil)
