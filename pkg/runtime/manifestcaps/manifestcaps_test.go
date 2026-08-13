package manifestcaps

import (
	"reflect"
	"testing"

	"github.com/soyaos/soyaos/pkg/runtime"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

func TestFromManifestFailClosed(t *testing.T) {
	for name, manifest := range map[string]*soyapack.Manifest{
		"nil manifest": nil,
		"no caps":      {},
	} {
		t.Run(name, func(t *testing.T) {
			if got := FromManifest(manifest); !reflect.DeepEqual(got, runtime.Caps{}) {
				t.Fatalf("FromManifest() = %+v, want deny-all zero Caps", got)
			}
		})
	}
}

func TestFromManifestSandboxCapabilities(t *testing.T) {
	manifest := &soyapack.Manifest{Sandbox: &soyapack.SandboxDecl{
		Capabilities: &soyapack.Capabilities{
			NetworkOut: []soyapack.EgressRule{{Host: "api.example.com", Port: 443, Proto: "https"}},
			FSRead:     []string{"/work/input"},
			FSWrite:    []string{"/work/output"},
			Exec:       []string{"ffmpeg"},
		},
	}}
	want := runtime.Caps{
		NetworkOut: []runtime.NetRule{{Host: "api.example.com", Port: 443, Proto: "https"}},
		FSRead:     []string{"/work/input"},
		FSWrite:    []string{"/work/output"},
		Exec:       []string{"ffmpeg"},
	}
	if got := FromManifest(manifest); !reflect.DeepEqual(got, want) {
		t.Fatalf("FromManifest() = %+v, want %+v", got, want)
	}
}

func TestFromManifestTopLevelFallbackAndSandboxPrecedence(t *testing.T) {
	manifest := &soyapack.Manifest{
		Capabilities: &soyapack.Capabilities{Exec: []string{"skill-tool"}},
	}
	if got := FromManifest(manifest); !reflect.DeepEqual(got.Exec, []string{"skill-tool"}) {
		t.Fatalf("top-level Exec = %v, want skill-tool", got.Exec)
	}

	manifest.Sandbox = &soyapack.SandboxDecl{
		Capabilities: &soyapack.Capabilities{Exec: []string{"agent-tool"}},
	}
	if got := FromManifest(manifest); !reflect.DeepEqual(got.Exec, []string{"agent-tool"}) {
		t.Fatalf("sandbox Exec = %v, want agent-tool", got.Exec)
	}
}

func TestFromCapabilitiesCopiesSlices(t *testing.T) {
	source := &soyapack.Capabilities{
		NetworkOut: []soyapack.EgressRule{{Host: "api.example.com", Port: 443, Proto: "https"}},
		FSRead:     []string{"/input"},
		FSWrite:    []string{"/output"},
		Exec:       []string{"tool"},
	}
	got := FromCapabilities(source)
	source.NetworkOut[0].Host = "mutated.example.com"
	source.FSRead[0] = "/mutated"
	source.FSWrite[0] = "/mutated"
	source.Exec[0] = "mutated"

	if got.NetworkOut[0].Host != "api.example.com" || got.FSRead[0] != "/input" ||
		got.FSWrite[0] != "/output" || got.Exec[0] != "tool" {
		t.Fatalf("converted Caps changed with source mutation: %+v", got)
	}
}
