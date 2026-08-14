<p align="center">
  <img src="assets/logo.png" alt="SoyaOS" width="120" height="120" />
</p>

# SoyaOS

[简体中文](README.zh-CN.md) | **English**

> **An Agent Operating System** — one binary, six editions, three node roles (Planet / Moon / Comet), unifying compute and capabilities across the public internet, your intranet, and ephemeral sandboxes.

SoyaOS is named after the humble soybean (黄豆) — one bean, many forms: edamame, tofu, soy milk, yuba. SoyaOS is the same idea for agents: one kernel, many shapes.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-alpha-orange.svg)](CHANGELOG.md)
[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8.svg)](go.mod)

## Status

> [!WARNING]
> **Active development; not formally released.** APIs and features are
> unstable and may introduce breaking changes at any time. Do not use this
> alpha as a production dependency.

The v0.1.0 milestone is locked to four flagship user stories (DD-008 ~ DD-011):

| # | Agent | Persona | Aha Moment |
|---|---|---|---|
| DD-008 | **Compo** ([reference repo](https://github.com/soyaos/example-essay-tutor)) | Parents tutoring kids | One sample essay + title → printable PDF writing guide |
| DD-009 | **NewsBeam** ([reference repo](https://github.com/soyaos/example-news-beam)) | AI knowledge workers | One sentence → daily 9am AI news long-image to DingTalk |
| DD-010 | **EstateMuse** ([reference repo](https://github.com/soyaos/example-estate-muse)) | Real-estate creators | One sentence → 500 topic ideas in Excel, per-row "generate post/video" actions |
| DD-011 | **SilentCut** ([reference repo](https://github.com/soyaos/example-silent-cut)) | Solo video creators | NL → Remotion script → Comet renders → MP4 lands on your NAS |

When all four stories run end-to-end across the six editions, v0.1.0 ships.

## Architecture at a glance

Three node roles forming the SoyaOS network:

- **Planet** — long-lived, public-internet control plane (auth, discovery, scheduling, capability tokens). Never reaches into your network.
- **Moon** — long-lived, lives inside your intranet / on your device. Reverse-dials Planet; holds your data, credentials, and persistent state.
- **Comet** — ephemeral, task-scoped sandbox (microVM / container / process). Used and discarded.

**Control plane through Planet; data plane prefers Moon ↔ Comet direct.** Large
payloads normally bypass Planet. When direct LAN and WireGuard both fail, the
optional Planet relay forwards end-to-end encrypted QUIC datagrams; it never
terminates Moon ↔ Comet mTLS or sees artifact plaintext. See the
[relay privacy commitment](docs/security/relay-privacy.md).

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

## Concepts

Five concepts you will see again and again across the docs, the CLI, and the code. Each is one Go package away.

| Concept | What it is | Code |
|---|---|---|
| **SoyaKernel** | The single-binary core that hosts every other concept. | `pkg/kernel` |
| **SoyaPack** | The portable, declarative Agent format (manifest + prompts + tools + sandbox). | `specs/specs/soyapack/v0/` + `pkg/soyapack` |
| **SoyaForge** | The Agent Factory that turns natural-language intent into a SoyaPack manifest. | `pkg/factory` |
| **SoyaScope** | The append-only event log behind audit, scheduling, and per-second billing. | `pkg/scope` |
| **SoyaAuth** | API key issuance, scope tokens, and row-scoped JWT for shared artifacts. | `pkg/auth` |

> Aha: paste `base_url`, `api_key`, and `model` into Cherry Studio and chat with your Agent as if it were just another OpenAI model.

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
│   ├── llmcall/               # LLM call layer behind the OpenAI-Compat Gateway (BYOK / platform / private vLLM)
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

### Check NAS write compatibility

The alpha CLI can write a random probe through four real NAS protocols: SMB
2/3, NFSv3, WebDAV, and S3-compatible object storage. Credentials are accepted
only through named environment variables; they are never accepted as raw CLI
arguments or included in the JSON result.

```bash
export NAS_USER='temporary-test-user'
export NAS_PASSWORD='temporary-test-password'

# SMB 2/3. For NFS use: --protocol nfs --host 192.0.2.10 --share /volume1/test
./bin/soyaos channel bind nas \
  --protocol smb \
  --host 192.0.2.10 \
  --share soyaos-test \
  --username-env NAS_USER \
  --password-env NAS_PASSWORD
```

Success produces one machine-readable JSON line containing the protocol,
generated remote path, byte count, and elapsed time. The command writes only a
new probe file below `soyaos-check/`; run it against an isolated test share,
never production data. NFS currently uses NFSv3 `AUTH_SYS` with the CLI
process's UID/GID. WebDAV `--host` and S3 `--host` must be explicit `http://` or
`https://` URLs; for S3, `--share` is the bucket name. Run
`./bin/soyaos channel bind nas -h` for all flags.

### Use from any OpenAI-Compatible client

SoyaOS speaks the OpenAI `/v1/chat/completions` API verbatim. Paste the same three values — `base_url`, `api_key`, `model` — into any client and your Agent shows up as a virtual model.

| Client | `base_url` | `api_key` | `model` |
|---|---|---|---|
| Cherry Studio | `http://localhost:8080/v1` | `sk-soya-…` | `soya:compo` |
| Cursor | `http://localhost:8080/v1` | `sk-soya-…` | `soya:compo` |
| Continue (VS Code / JetBrains) | `http://localhost:8080/v1` | `sk-soya-…` | `soya:compo` |
| Zed | `http://localhost:8080/v1` | `sk-soya-…` | `soya:compo` |

> The `model` value is the Agent's `virtual_model_id` declared in its SoyaPack manifest (e.g. `soya:compo`, `soya:news-beam`).

## Contributing

We welcome contributions. SoyaOS uses [DCO](https://developercertificate.org/) — every commit needs `Signed-off-by:`. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Design docs

The full SoyaOS design lives in a separate WebMind knowledge base (17 HTML documents covering architecture, editions, design decisions, flagship stories, and more). The implementation in this repo is reviewed against those docs at every stage milestone.

## License

[MIT](LICENSE) — Copyright (c) 2026 SoyaOS Contributors.
