// Package builtins is the aggregator that wires every Tool shipped in
// pkg/tooling/tools/... into a single tooling.Registry. Living in a
// sibling package (rather than inside pkg/tooling itself) prevents the
// import cycle the comment on tooling.RegisterAll warns about: leaf
// tool packages depend on pkg/tooling, so pkg/tooling cannot depend
// back on them.
//
// Stage 4 ships four builtins:
//
//   - parseinput  (APP-462)
//   - jsonapi     (APP-466)
//   - rssfetch    (APP-485, NewsBeam intake)
//   - originality (APP-504, EstateMuse multi-platform draft precheck)
//
// Callers (cmd/soyactl etc.) construct one Registry by calling
// Register(reg) at startup.
package builtins

import (
	"github.com/soyaos/soyaos/pkg/tooling"
	"github.com/soyaos/soyaos/pkg/tooling/tools/jsonapi"
	"github.com/soyaos/soyaos/pkg/tooling/tools/originality"
	"github.com/soyaos/soyaos/pkg/tooling/tools/parseinput"
	"github.com/soyaos/soyaos/pkg/tooling/tools/rssfetch"
)

// All returns every Tool shipped in pkg/tooling/tools/...
func All() []tooling.Tool {
	return []tooling.Tool{
		parseinput.Tool(),
		jsonapi.Builtin(),
		rssfetch.Builtin(),
		originality.Builtin(),
	}
}

// Register wires every builtin into r. Useful for callers that already
// hold a Registry and want the canonical set in one call.
func Register(r *tooling.Registry) {
	r.RegisterAll(All()...)
}

// NewRegistry returns a fresh Registry pre-populated with every builtin.
func NewRegistry() *tooling.Registry {
	r := tooling.NewRegistry()
	Register(r)
	return r
}
