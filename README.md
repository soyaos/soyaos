# SoyaOS

[简体中文](README.zh-CN.md) | **English**

> **An Agent Operating System** — one binary, six editions, three node roles (Planet / Moon / Comet), unifying compute and capabilities across the public internet, your intranet, and ephemeral sandboxes.

SoyaOS is named after the humble soybean (黄豆) — one bean, many forms: edamame, tofu, soy milk, yuba. SoyaOS is the same idea for agents: one kernel, many shapes.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-alpha-orange.svg)](CHANGELOG.md)
[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8.svg)](go.mod)

## Status

**Pre-release: v0.1.0-alpha.0.** Day-1 scaffolding only — APIs are unstable and will change before v0.1.0. Not yet ready for production use.

The v0.1.0 milestone is locked to four flagship user stories (DD-008 ~ DD-011):

| # | Agent | Persona | Aha Moment |
|---|---|---|---|
| DD-008 | **Compo** | Parents tutoring kids | One sample essay + title → printable PDF writing guide |
| DD-009 | **NewsBeam** | AI knowledge workers | One sentence → daily 9am AI news long-image to DingTalk |
| DD-010 | **EstateMuse** | Real-estate creators | One sentence → 500 topic ideas in Excel, per-row "generate post/video" actions |
| DD-011 | **SilentCut** | Solo video creators | NL → Remotion script → Comet renders → MP4 lands on your NAS |

When all four stories run end-to-end across the six editions, v0.1.0 ships.

## Architecture at a glance

Three node roles forming the SoyaOS network:

- **Planet** — long-lived, public-internet control plane (auth, discovery, scheduling, capability tokens). Never reaches into your network.
- **Moon** — long-lived, lives inside your intranet / on your device. Reverse-dials Planet; holds your data, credentials, and persistent state.
- **Comet** — ephemeral, task-scoped sandbox (microVM / container / process). Used and discarded.

**Control plane through Planet; data plane prefers Moon ↔ Comet direct.** Large payloads (videos, files) never have to touch the Planet.

**All-in-One Mode** (Solo Edition): all three roles run inside a single Go process — one binary, zero dependencies, `./soyaos start` and you have a SoyaOS.

### Six Editions

| # | Edition | CLI | For |
|---|---|---|---|
| 01 | SoyaOS Solo | `solo` | One developer, one laptop |
| 02 | SoyaOS Cluster | `cluster` | A team with one VPS + intranet devices |
| 03 | SoyaOS Cloud | `cloud` | Hosted, register → API key → go |
| 04 | SoyaOS Hybrid | `hybrid` | Hosted control plane, your-own Moon |
| 05 | SoyaOS Enterprise Cloud | `ent-cloud` | Multi-tenant SaaS with SSO, SLAs, compliance |
| 06 | SoyaOS Enterprise Private | `ent-private` | On-prem / air-gapped, customer-managed |

## Repository layout

```
soyaos/                        # this repo (core monorepo)
├── cmd/                       # binary entry points
│   └── soyaos/                # main multi-role binary
├── pkg/                       # public Go packages — the 13 modules
│   ├── kernel/                # SoyaKernel (LLM kernel, routing, context)
│   ├── orbit/                 # node registry, health, bootstrap tokens
│   ├── mesh/                  # SoyaMesh — overlay network (in-process in Solo)
│   ├── dispatcher/            # task scheduling, DAG, affinity
│   ├── memory/                # SoyaMemory — working/episodic/semantic/procedural
│   ├── tooling/               # MCP / A2A tools, registry, permissions
│   ├── runtime/               # Comet sandbox runtime
│   ├── auth/                  # SoyaAuth — zero-trust, capability tokens
│   ├── scope/                 # SoyaScope — observability, replay
│   ├── modelgw/               # Model Gateway (BYOK / platform / private vLLM)
│   ├── scheduler/             # cron + one-shot scheduler (DD-007)
│   ├── connectors/            # Channel Connectors — DingTalk/Feishu/WeChat/... (DD-006)
│   ├── artifact/              # Artifact abstraction — HTML/PDF/long_image/MD/XLSX/MP4
│   ├── openaicompat/          # OpenAI-Compat Gateway — /v1/* (DD-005)
│   ├── factory/               # Agent Factory — NL → manifest
│   ├── sdk/                   # Go SDK for agent authors
│   └── version/               # build/version info
├── internal/                  # not for external import
├── api/                       # protobuf-generated stubs (mirrors soyaos/protos)
├── plugin/                    # closed-source enterprise plugin interfaces
├── deploy/                    # Helm / Terraform / offline tarball
├── web/                       # frontend dist (embedded via //go:embed)
├── docs/                      # design docs, mirrors soyaos/docs site
├── examples/                  # reference agents
│   └── echo-agent/            # 30-second OpenAI-Compat smoke test
└── scripts/                   # build, test, release helpers
```

Single `go.mod` at the root — no multi-module workspace. The binary is one file, embedding frontend assets via `//go:embed`.

## Quickstart

> Pre-release: `./soyaos start` boots Planet+Moon+Comet **in-process** and exposes the OpenAI-Compat data plane on `127.0.0.1:7474` and the control RPC on `127.0.0.1:7475`. Localhost-by-default; no external dependencies.

```bash
# Build
make build

# Run Solo edition (all-in-one binary)
./bin/soyaos start

# Smoke-test the OpenAI-Compat endpoint
curl http://127.0.0.1:7474/v1/models \
  -H "Authorization: Bearer sk-soya-dev-local"

# Talk to the echo agent via the CLI
./bin/soyaos agent run echo "hello"
```

See [`examples/echo-agent/`](examples/echo-agent/) for the first runnable agent.

## Contributing

We welcome contributions. SoyaOS uses [DCO](https://developercertificate.org/) — every commit needs `Signed-off-by:`. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Design docs

The full SoyaOS design lives in a separate WebMind knowledge base (17 HTML documents covering architecture, editions, design decisions, flagship stories, and more). The implementation in this repo is reviewed against those docs at every stage milestone.

## License

[MIT](LICENSE) — Copyright (c) 2026 SoyaOS Contributors.
