# Contributing to SoyaOS

Thanks for your interest. Two ground rules before we get into the details:

1. **DCO** — every commit needs `Signed-off-by:`. No exceptions.
2. **Be specific** — small, focused changes review faster than large grab-bags.

## Developer Certificate of Origin (DCO)

SoyaOS does **not** use a CLA. We use the [Developer Certificate of Origin](https://developercertificate.org/) instead. By signing off on a commit, you certify that you wrote the contribution and have the right to submit it under the project's MIT license.

Every commit must include a `Signed-off-by` trailer matching the commit author:

```
Signed-off-by: Your Name <you@example.com>
```

Easiest way is to commit with `-s`:

```bash
git commit -s -m "feat(orbit): add bootstrap token rotation"
```

PRs with unsigned-off commits will be blocked by the DCO check.

## Branching

- `main` is the default branch. Everything is rebased / merged onto `main`.
- Feature branches: `feat/<short-slug>`, fix: `fix/<short-slug>`, docs: `docs/<short-slug>`.

## Commit messages

We follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short summary>

<optional body>

Signed-off-by: ...
```

Common types: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `perf`, `build`, `ci`.

Common scopes: `kernel`, `orbit`, `mesh`, `dispatcher`, `memory`, `tooling`, `runtime`, `auth`, `scope`, `modelgw`, `scheduler`, `connectors`, `artifact`, `openaicompat`, `factory`, `sdk`, `cmd`, `deploy`, `docs`, `ci`.

## Local development

```bash
# build, vet, test
make all

# or invoke them directly
make build           # builds ./bin/soyaos
make vet
make test
make fmt
```

You'll need:
- Go 1.23+
- `gofmt` (ships with Go)
- For local CI matching: `golangci-lint` (optional, used in `make lint`)

## Adding a new package

The 13 core modules each live under `pkg/<module>/`. Public interfaces sit at the package root; Solo-mode implementations sit in `pkg/<module>/inproc/` (or as the default impl when only one exists). Keep `internal/` for things genuinely not for external consumers.

## Tests

- Place `*_test.go` next to the code under test.
- Tests must pass under `go test ./...` from a clean checkout.
- Don't add a dependency just for a test — prefer stdlib + `testing`.

## Reporting security issues

Do **not** open public GitHub issues for security vulnerabilities. See [SECURITY.md](SECURITY.md).

## Code of Conduct

By participating, you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).
