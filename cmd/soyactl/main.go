// Command soyactl is the fine-grained operator CLI for a running SoyaOS.
//
// Where `soyaos` is the user-facing CLI (start, agent create / list / run),
// `soyactl` is the operator-facing CLI — it talks to a running soyaos via
// the control RPC surface (control.v0) and exposes verbs that are too
// noisy or too dangerous to put in the user CLI:
//
//   soyactl nodes ls / drain / cordon         — orbit registry inspection
//   soyactl scheduler jobs ls / pause / resume — pkg/scheduler control
//   soyactl scope events tail                  — live event log stream
//   soyactl auth keys ls / revoke              — API key management
//   soyactl runtime gate dryrun                — Capability-gate decision trace
//
// This binary is a Day-1 stub: the verbs above are reserved (locked by the
// cli.v0 spec) but not yet implemented. Running it prints the surface and
// exits 0 so operators / docs can already reference the binary path.
package main

import (
	"fmt"

	"github.com/soyaos/soyaos/pkg/version"
)

func main() {
	fmt.Printf("soyactl %s (%s) — operator CLI stub\n", version.Version, version.GitSHA)
	fmt.Println("Verb surface (reserved by cli.v0, not implemented in v0.1.0-alpha.0):")
	fmt.Println("  nodes      ls | drain | cordon")
	fmt.Println("  scheduler  jobs ls | pause | resume")
	fmt.Println("  scope      events tail")
	fmt.Println("  auth       keys ls | revoke")
	fmt.Println("  runtime    gate dryrun")
}
