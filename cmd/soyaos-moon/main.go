// Command soyaos-moon is the intranet-side data-plane entrypoint for SoyaOS.
//
// A Moon node lives inside the customer's network (LAN, on-device, VPC) and
// reverse-dials a Planet. Moon owns persistent state, credentials, and the
// hand-off point between Planet control RPC and Comet sandbox execution.
// Large payloads (videos, artifacts) travel Moon ↔ Comet direct and never
// transit Planet.
//
// This binary is a Day-1 stub. The Solo edition ships everything inside the
// `soyaos` binary (cmd/soyaos), where Planet, Moon and Comet share one Go
// process. soyaos-moon exists so that:
//
//   - Cluster / Cloud / Hybrid / Enterprise editions can run a Moon process
//     on a customer device without dragging Planet startup code in.
//   - The release matrix can already produce a distinct moon binary.
//
// Real Moon startup wiring (reverse-dial registration, sandbox handoff,
// keystore) lands in the cluster-edition milestone.
package main

import (
	"fmt"
	"os"

	"github.com/soyaos/soyaos/pkg/version"
)

func main() {
	fmt.Printf("soyaos-moon %s (%s) — Moon data-plane stub\n", version.Version, version.GitSHA)
	fmt.Fprintln(os.Stderr, "Moon reverse-dial is not wired in v0.1.0-alpha.0. Run `soyaos start` for the Solo all-in-one binary.")
}
