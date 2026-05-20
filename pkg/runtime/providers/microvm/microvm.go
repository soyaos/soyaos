// Package microvm is the ProfileMicroVM CometProvider — the strongest of the
// three isolation tiers, backed by Firecracker microVMs. Each invocation
// gets its own kernel and rootfs; egress, FS and exec are all mediated by
// the VM boundary on top of the SoyaPack capability gate.
//
// SilentCut (DD-011) reverse-pressure: SilentCut is the original reason the
// microvm tier exists. Per-second billing requires ≤ 125ms cold-start and
// per-tenant isolation strong enough to host third-party code paths
// (custom subtitle transforms, future plugin renderers). Firecracker's
// snapshot-restore is the only option that hits both numbers on commodity
// hosts; the same five-method CometProvider contract makes it
// substitutable with process / container for cheaper steps.
//
// alpha shape: this file is intentionally a stub. Every method returns
// runtime.ErrNotImplemented. The Stage 5 implementation will manage a pool
// of pre-snapshotted VMs and dispatch jobs over a vsock control channel.
package microvm

import (
	"context"

	"github.com/soyaos/soyaos/pkg/runtime"
)

// Provider is the ProfileMicroVM CometProvider.
type Provider struct{}

// New returns the stub Provider. No firecracker process is spawned. The real
// build will accept paths to the firecracker binary, the jailer binary, and
// the pre-built kernel + rootfs templates.
func New() *Provider { return &Provider{} }

// Capabilities advertises full surface — the microvm tier is the most
// permissive in terms of intent (it can do everything a container can) but
// the most restrictive in terms of trust (each task gets its own kernel).
func (*Provider) Capabilities() runtime.Capabilities {
	return runtime.Capabilities{
		Profile: runtime.ProfileMicroVM,
		Network: true,
		FS:      true,
		Exec:    true,
	}
}

// Provision will boot or restore a Firecracker VM for this task. alpha: stub.
//
// TODO(Stage5): Firecracker via /run/firecracker/firecracker.sock —
// PUT /machine-config, /boot-source, /drives/rootfs, /network-interfaces/eth0,
// /snapshot/load (preferred for cold-start ≤ 125ms), /actions InstanceStart.
func (*Provider) Provision(context.Context, runtime.ProvisionRequest) (runtime.Handle, error) {
	return "", runtime.ErrNotImplemented
}

// Execute will dispatch the command over the VM's vsock agent. alpha: stub.
//
// TODO(Stage5): in-VM agent on a known vsock port that accepts protobuf
// ExecuteRequest and streams back stdout/stderr.
func (*Provider) Execute(context.Context, runtime.Handle, runtime.ExecuteRequest) (runtime.ExecuteResult, error) {
	return runtime.ExecuteResult{}, runtime.ErrNotImplemented
}

// Terminate will SIGTERM the in-VM agent then InstanceHalt. alpha: stub.
//
// TODO(Stage5): graceful agent shutdown, then PUT /actions InstanceHalt;
// release tap device + reclaim memory snapshot.
func (*Provider) Terminate(context.Context, runtime.Handle) error {
	return runtime.ErrNotImplemented
}

// Cost will report VMM-side counters. alpha: stub.
//
// TODO(Stage5): GET /metrics returns Firecracker's own counters
// (vcpu_count × wall-clock, balloon stats, net bytes); GPUs land via a
// separate sidecar passthrough metric.
func (*Provider) Cost(context.Context, runtime.Handle) (runtime.CostSnapshot, error) {
	return runtime.CostSnapshot{}, runtime.ErrNotImplemented
}

// Compile-time assertion that *Provider satisfies the CometProvider contract.
var _ runtime.CometProvider = (*Provider)(nil)
