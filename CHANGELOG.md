# Changelog

All notable changes to **SoyaOS** are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Per **DD-003** (SoyaOS Design Decisions), versioning starts at `0.1.0`. The
public API is unstable until `1.0.0`; expect breaking changes in `0.x` minor
releases.

## [Unreleased]

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
