// Command soyaos-planet is the control-plane-only entrypoint for SoyaOS.
//
// A Planet node owns the long-lived, public-facing surface: auth, agent
// discovery, scheduling decisions, and capability-token signing. It never
// reaches into a customer's network — Moons reverse-dial Planet, never the
// other way round.
//
// This binary is a Day-1 stub. The Solo edition ships everything inside the
// `soyaos` binary (cmd/soyaos), where Planet, Moon and Comet share one Go
// process. soyaos-planet exists so that:
//
//   - Cluster / Cloud / Hybrid / Enterprise editions can run a Planet
//     process on its own host without dragging Moon/Comet startup code in.
//   - The release matrix (.github/workflows/matrix-build.yml) can already
//     reference the three distinct entry binaries promised by spec §05.
//
// Real Planet startup wiring lands in the cluster-edition milestone; for
// now the binary prints its identity and exits 0 so smoke tests can
// confirm the build target is present.
package main

import (
	"fmt"
	"os"

	"github.com/soyaos/soyaos/pkg/version"
)

func main() {
	fmt.Printf("soyaos-planet %s (%s) — Planet control plane stub\n", version.Version, version.GitSHA)
	fmt.Fprintln(os.Stderr, "Planet startup is not wired in v0.1.0-alpha.0. Run `soyaos start` for the Solo all-in-one binary.")
}
