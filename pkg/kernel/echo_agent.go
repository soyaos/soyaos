package kernel

import (
	"context"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/modelgw"
)

// EchoAgent is the reference Agent shipped in v0.1.0-alpha.0. It exists so
// the OpenAI-Compat smoke test works end-to-end without any external LLM
// credentials: messages sent to model id "soya:echo" come back prefixed
// with "echo: ".
//
// Real Agents (Compo, NewsBeam, EstateMuse, SilentCut) replace this with
// LLM-backed handlers via the same kernel.Register() API.
var EchoAgent = Agent{
	Slug:        "echo",
	Description: "Reference echo agent — replies with 'echo: <last user message>'",
	Handler: func(ctx context.Context, _ auth.Identity, req modelgw.Request, out chan<- modelgw.Chunk) error {
		return modelgw.Echo{}.GenerateStream(ctx, req, out)
	},
}
