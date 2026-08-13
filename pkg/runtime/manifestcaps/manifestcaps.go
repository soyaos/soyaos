// Package manifestcaps adapts SoyaPack capability declarations to the
// runtime Gate model without making the core runtime package depend on the
// manifest schema.
package manifestcaps

import (
	"github.com/soyaos/soyaos/pkg/runtime"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

// FromManifest returns the effective runtime capabilities for a Pack.
// Sandbox capabilities take precedence for Agent manifests; top-level
// capabilities are the Skill surface. A nil manifest or absent declaration
// returns zero Caps, which is deliberately deny-all.
func FromManifest(manifest *soyapack.Manifest) runtime.Caps {
	if manifest == nil {
		return runtime.Caps{}
	}
	if manifest.Sandbox != nil && manifest.Sandbox.Capabilities != nil {
		return FromCapabilities(manifest.Sandbox.Capabilities)
	}
	return FromCapabilities(manifest.Capabilities)
}

// FromCapabilities copies the four runtime-enforced axes from a SoyaPack
// declaration. Higher-level LLM, MCP, memory, NAS and resource constraints
// are enforced by their respective subsystems, not runtime.Gate.
func FromCapabilities(capabilities *soyapack.Capabilities) runtime.Caps {
	if capabilities == nil {
		return runtime.Caps{}
	}
	network := make([]runtime.NetRule, 0, len(capabilities.NetworkOut))
	for _, rule := range capabilities.NetworkOut {
		network = append(network, runtime.NetRule{
			Host:  rule.Host,
			Port:  rule.Port,
			Proto: rule.Proto,
		})
	}
	return runtime.Caps{
		NetworkOut: network,
		FSRead:     append([]string(nil), capabilities.FSRead...),
		FSWrite:    append([]string(nil), capabilities.FSWrite...),
		Exec:       append([]string(nil), capabilities.Exec...),
	}
}
