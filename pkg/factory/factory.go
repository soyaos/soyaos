// Package factory is the Agent Factory — natural language → SoyaPack
// manifest translation (DD-009, NewsBeam).
//
// v0.1.0-alpha.0 exposes only the translator shell. The real implementation
// lands with the NewsBeam milestone (Linear epic APP-455 / DD-009). The
// canonical Manifest type lives in pkg/soyapack and is not duplicated here;
// the Factory consumes it as a value type via its public API.
//
// Architectural intent (locked by APP-460):
//
//   - pkg/soyapack — manifest *schema* (Skill / Agent / Memory) + loader +
//     validator. Owned by spec.
//   - pkg/factory  — *behavior*: turn a natural-language sentence into a
//     soyapack.Manifest{Kind: Agent, ...} value. Owned by Agent Factory.
//
// Splitting them means a manifest written by hand and a manifest produced
// by the Factory pass through the same validator.
package factory

import (
	"context"
	"errors"

	"github.com/soyaos/soyaos/pkg/soyapack"
)

// Translator converts a natural-language description of an Agent into a
// SoyaPack v0 Agent manifest. Implementations:
//
//   - LLM-backed (production) — uses a configured Provider to extract intent.
//   - Template-only (test) — returns a fixed manifest, used by unit tests.
//
// All implementations must produce manifests that pass soyapack.Validate.
type Translator interface {
	Translate(ctx context.Context, nl string, locale string) (*soyapack.Manifest, error)
}

// ErrNotImplemented signals that the alpha shell has no real LLM-backed
// translator wired yet. NewsBeam (APP-492) replaces this stub.
var ErrNotImplemented = errors.New("factory: NL→manifest translator not implemented in v0.1.0-alpha.0 (see APP-492)")

// Stub is a placeholder Translator. It satisfies the interface so callers
// can hold a Translator without depending on the future LLM-backed impl.
type Stub struct{}

// Translate always returns ErrNotImplemented.
func (Stub) Translate(_ context.Context, _ string, _ string) (*soyapack.Manifest, error) {
	return nil, ErrNotImplemented
}
