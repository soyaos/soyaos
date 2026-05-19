package runtime

import (
	"path/filepath"
	"strings"
)

// Caps is the runtime-side view of a SoyaPack's capability declaration.
// It mirrors the four fail-closed axes from soyapack.Capabilities
// (network_out / fs_read / fs_write / exec). Callers translate from the
// manifest type into Caps when constructing a Gate; runtime stays
// independent of pkg/soyapack so the gate is reusable from tests and
// from non-manifest call sites.
//
// A zero Caps denies everything — that is the fail-closed contract.
type Caps struct {
	NetworkOut []NetRule
	FSRead     []string
	FSWrite    []string
	Exec       []string
}

// NetRule is one entry in NetworkOut. Host "*" matches any host; Port 0
// matches any port; Proto is matched case-insensitively against the
// caller's argument. Pin / quota / other higher-level constraints are
// out of scope for the gate.
type NetRule struct {
	Host  string
	Port  int
	Proto string
}

// Gate enforces the fail-closed capability checks. Construct one per task
// invocation via NewGate; every outbound action the sandbox attempts goes
// through one of the four Check methods. A denial returns a *DeniedError
// that unwraps to ErrDeniedByCapability.
type Gate struct {
	caps Caps
}

// NewGate builds a Gate from a Caps snapshot. The snapshot is copied so
// later mutations to the source slice do not change gate behavior.
func NewGate(caps Caps) *Gate {
	g := &Gate{caps: Caps{
		NetworkOut: append([]NetRule(nil), caps.NetworkOut...),
		FSRead:     append([]string(nil), caps.FSRead...),
		FSWrite:    append([]string(nil), caps.FSWrite...),
		Exec:       append([]string(nil), caps.Exec...),
	}}
	return g
}

// CheckEgress authorizes one outbound network attempt. host is the
// resolved DNS name (lower-case ASCII); port is the TCP/UDP port; proto
// is one of http/https/grpc/quic (case-insensitive).
func (g *Gate) CheckEgress(host string, port int, proto string) error {
	resource := host + ":" + itoa(port) + "/" + strings.ToLower(proto)
	for _, r := range g.caps.NetworkOut {
		if !hostMatches(r.Host, host) {
			continue
		}
		if r.Port != 0 && r.Port != port {
			continue
		}
		if !strings.EqualFold(r.Proto, proto) {
			continue
		}
		return nil
	}
	return &DeniedError{Capability: "network_out", Resource: resource, Reason: "not in allowlist"}
}

// CheckFSRead authorizes one filesystem read. path must be absolute;
// the gate compares it against the FSRead allowlist using path-prefix
// matching with normalized separators.
func (g *Gate) CheckFSRead(path string) error {
	return g.checkFS("fs_read", g.caps.FSRead, path)
}

// CheckFSWrite authorizes one filesystem write. Same matching rules as
// CheckFSRead.
func (g *Gate) CheckFSWrite(path string) error {
	return g.checkFS("fs_write", g.caps.FSWrite, path)
}

// CheckExec authorizes one process execution. argv0 may be a full path
// or a bare program name; the gate matches on basename against the Exec
// allowlist. A sandbox that wants to permit absolute paths should list
// them as basenames in soyapack.yaml.
func (g *Gate) CheckExec(argv0 string) error {
	base := filepath.Base(argv0)
	for _, allowed := range g.caps.Exec {
		if allowed == base {
			return nil
		}
	}
	return &DeniedError{Capability: "exec", Resource: argv0, Reason: "not in allowlist"}
}

func (g *Gate) checkFS(cap string, allowed []string, path string) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return &DeniedError{Capability: cap, Resource: path, Reason: "path must be absolute"}
	}
	for _, root := range allowed {
		root = filepath.Clean(root)
		if clean == root {
			return nil
		}
		// ensure prefix match aligns to a path boundary (so /workdir
		// does not match /workdir-evil)
		if strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return nil
		}
	}
	return &DeniedError{Capability: cap, Resource: path, Reason: "not in allowlist"}
}

func hostMatches(rule, host string) bool {
	if rule == "*" {
		return true
	}
	return strings.EqualFold(rule, host)
}

// itoa avoids the strconv import for a hot, trivial conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
