# Changelog

All notable changes to **SoyaOS** are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Per **DD-003** (SoyaOS Design Decisions), versioning starts at `0.1.0`. The
public API is unstable until `1.0.0`; expect breaking changes in `0.x` minor
releases.

## [Unreleased]

### Added
- `pkg/control` — Solo control-plane JSON-over-HTTP RPC at `127.0.0.1:7475`,
  loopback-only. Exposes `GET /control/v0/healthz`,
  `GET /control/v0/agents`, `POST /control/v0/agents/{slug}/invoke`.
- `soyaos --spec-version` — prints `cli.v0` (matches `soyaos/specs`).
- `soyaos agent create <name>` — scaffolds a SoyaPack v0 Agent directory
  with `soyapack.yaml` + `prompts/` + `templates/` + `examples/` + `README.md`.
- `soyaos agent run <slug> "..."` — invokes an Agent through the running
  gateway and prints the reply.
- `soyaos start --rpc` flag for the control RPC address.
- `soyaos start --data-dir` flag with `$XDG_DATA_HOME/soyaos` default.
- Studio placeholder at `GET /` of the data plane (real Studio later).
- Recognition of `SOYA_MODEL_API_KEY` env var (BYOK key; stashed for the
  upcoming Stage 2 LLM providers — not yet used by Echo agent).

### Changed
- **Breaking** for anyone scripting against the alpha: default OpenAI-Compat
  listen address moves from `:6473` to `127.0.0.1:7474` (localhost-by-default
  for Solo). Locked by `soyaos/specs/specs/cli/v0.md`.
- `/healthz` defaults to plain-text `ok` (per spec). JSON envelope still
  available via `?format=json`.
- `soyaos agent list` now talks to the running control RPC (was: ran an
  in-process kernel inside the CLI process).

## [0.1.0-alpha.0] — 2026-05-18

First Day-1 scaffolding cut. Not functional yet — this commit only stakes out
the repository layout, governance, and Go module identity so that subsequent
stage work has somewhere to land.

### Added

- Core monorepo skeleton at `github.com/soyaos/soyaos`.
- MIT license (DD-001), DCO contributing flow (DD-001).
- Six-edition CLI vocabulary (`solo` / `cluster` / `cloud` / `hybrid` /
  `ent-cloud` / `ent-private`) reflected in docs.
- Top-level directory layout for the 13 core modules under `pkg/`.
- `cmd/soyaos` single multi-role binary stub.
- `examples/echo-agent` as the first OpenAI-Compat smoke test.
- Bilingual README (English + zh-CN), CONTRIBUTING, CODE_OF_CONDUCT,
  SECURITY, and this CHANGELOG.
- `.github/` org health files: workflows, issue templates, PR template,
  CODEOWNERS, dependabot.

[Unreleased]: https://github.com/soyaos/soyaos/compare/v0.1.0-alpha.0...HEAD
[0.1.0-alpha.0]: https://github.com/soyaos/soyaos/releases/tag/v0.1.0-alpha.0
