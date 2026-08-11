# Changelog

All notable changes to **SoyaOS** are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Per **DD-003** (SoyaOS Design Decisions), versioning starts at `0.1.0`. The
public API is unstable until `1.0.0`; expect breaking changes in `0.x` minor
releases.

## [0.1.0-alpha.1](https://github.com/soyaos/soyaos/compare/v0.1.0-alpha.0...v0.1.0-alpha.1) (2026-08-11)


### Features

* **artifact:** CanonicalHash for snapshot identity (APP-464) ([9ac0a7c](https://github.com/soyaos/soyaos/commit/9ac0a7c742f4b6882cdd30ca342973497a96f262))
* **artifact:** HTML renderer with [@media](https://github.com/media) print injection (APP-462) ([f0c1762](https://github.com/soyaos/soyaos/commit/f0c1762889fb8477a8c8ffb97e6cea1884ccb2d0))
* **artifact:** long_image renderer for daily push channels (APP-493) ([ba7c302](https://github.com/soyaos/soyaos/commit/ba7c30249c505a0beac31764c364f61bfd294945))
* **artifact:** MP4 + StreamingRenderer + Range-aware HTTP delivery (APP-513) ([ad93ce8](https://github.com/soyaos/soyaos/commit/ad93ce80040732bc161b7bb1d66e6d3f6de37b57))
* **artifact:** MP4 streaming renderer wires to Remotion subprocess (APP-554) ([577b2d1](https://github.com/soyaos/soyaos/commit/577b2d1376474f44b5541e2e5c96d6cf135713fd))
* **artifact:** PDF renderer via chromedp headless (APP-463) ([2071ee7](https://github.com/soyaos/soyaos/commit/2071ee7b85311fad52e1ffc7f0460c249d65223d))
* **artifact:** xlsx renderer as first-class Artifact (APP-500) ([b4da939](https://github.com/soyaos/soyaos/commit/b4da939f78807383fce76d7654797208ea4cc2ca))
* auth/memory/scheduler persist through pkg/store (APP-461 [#2](https://github.com/soyaos/soyaos/issues/2)/2) ([3e476f6](https://github.com/soyaos/soyaos/commit/3e476f634de93f03cc763602b8527fa0763bd3e4))
* **auth:** row-scoped JWT token for shared per-row actions (APP-503) ([7c1799d](https://github.com/soyaos/soyaos/commit/7c1799d402adb68847236477c7624579b3732949))
* **cli,control:** agent deploy + POST /control/v0/packs + boot reload (APP-538) ([66c19e1](https://github.com/soyaos/soyaos/commit/66c19e111e6fece36b6b1952cb0f0aad51ea503f))
* **cli:** agent build produces canonical .spk archive (APP-536) ([2a93c43](https://github.com/soyaos/soyaos/commit/2a93c43b0225a79baf4458d0eca35ecad40e2ac2))
* **cli:** pack validate subcommand exposes pkg/soyapack.LoadFromFile (APP-535) ([2d3f5e3](https://github.com/soyaos/soyaos/commit/2d3f5e3b7ee19650689aaabb185f621e63f927d0))
* **cmd:** scaffold soyaos-planet / soyaos-moon / soyactl stub binaries (APP-471) ([f8b31fb](https://github.com/soyaos/soyaos/commit/f8b31fb411f6132c6e36eb056ac32af7bf8ef3c2))
* **connectors/dingtalk:** inbound + outbound channel (APP-495) ([3188a7b](https://github.com/soyaos/soyaos/commit/3188a7b8092068257a4ae823687668931d8759d3))
* **connectors/nas:** SMB/NFS/WebDAV/S3 outbound, WebDAV live (APP-511) ([0c19f6b](https://github.com/soyaos/soyaos/commit/0c19f6bdddd47587f5e107b56eda186b01b05df0))
* **connectors:** rich-link degradation + pkg/shortlink (APP-498) ([5a2e6bd](https://github.com/soyaos/soyaos/commit/5a2e6bd497e84ac60d103667a2f38929a7b39392))
* **docker:** root multi-stage Dockerfile + .dockerignore + image.yml hardening (APP-555) ([9bd0b56](https://github.com/soyaos/soyaos/commit/9bd0b56da74ee374c7150a228bff5bf13ec388d2))
* **factory:** NL → SoyaPack manifest translator v0 (APP-492) ([e99cefd](https://github.com/soyaos/soyaos/commit/e99cefd9228e8b51c7718906d9bc9772211188f1))
* **factory:** SandboxDecl.Capabilities triad for fail-closed gating (APP-476) ([0ef4ac0](https://github.com/soyaos/soyaos/commit/0ef4ac0985ba51ed47d959922f9a017e9cca8a77))
* **kernel,cmd:** register soya:llm Agent when SOYA_MODEL_API_KEY is set ([a11b59c](https://github.com/soyaos/soyaos/commit/a11b59c4561b47818e187208e814d4900709782c))
* **kernel,connectors/nas:** manifest.storage_nas wire to Agent handler (APP-554) ([a99dc9a](https://github.com/soyaos/soyaos/commit/a99dc9a556339add6ec13eafa2abec124400479b))
* **kernel,connectors:** RegisterFromPack publishes via manifest.channels (APP-552) ([775e550](https://github.com/soyaos/soyaos/commit/775e550ff7578b0423f42a56eef687a984e393f5))
* **kernel,scheduler:** RegisterFromPack consumes manifest.schedule + boot ticker (APP-552) ([bd9b62b](https://github.com/soyaos/soyaos/commit/bd9b62b372b23101ff42242207c3cf9e3029c9fe))
* **kernel:** RegisterFromPack — manifest -&gt; Agent + Handler with BYOK resolve (APP-541) ([66c3596](https://github.com/soyaos/soyaos/commit/66c3596a191f3c8f93c76e02667ba35e6c047cb5))
* **kernel:** RegisterFromPack consumes manifest.actions for per-row triggers (APP-553) ([9e50e54](https://github.com/soyaos/soyaos/commit/9e50e54f98392758885103a3aeb38b0bfc6a2b56))
* **kernel:** RegisterFromPack supports prompt.steps chain (APP-550) ([f6782b2](https://github.com/soyaos/soyaos/commit/f6782b237953278854c727d2df081b38f8d520fa))
* **llmcall:** OpenAI-Compat upstream provider + SOYA_MODEL_* env tuple ([8e91a5c](https://github.com/soyaos/soyaos/commit/8e91a5c2e02e1038dac0d0dbd4152ed3d2b7f73f))
* **memory:** KV.List + Episodic Append/Recent + TTL (APP-505) ([368c064](https://github.com/soyaos/soyaos/commit/368c06484d3695f0c322ad11c5e65cc0705090a4))
* **mesh:** Transport interface + QUIC stub + path selector strategy (APP-509) ([0af28ee](https://github.com/soyaos/soyaos/commit/0af28eebb2d79dad6ea81faacb24b70a6cb3457b))
* **openaicompat:** GET /v1/agents superset endpoint for in-binary Studio ([d6cafca](https://github.com/soyaos/soyaos/commit/d6cafcad1ce05848e1d6e6f42dc582617fc9a2a7))
* **openaicompat:** per-row Action trigger endpoint (APP-502) ([9f2f74b](https://github.com/soyaos/soyaos/commit/9f2f74be6150406f03954c454eff271c97236329))
* **orbit:** add Node.HostsAgent symmetric to HostsComet (APP-473) ([869296e](https://github.com/soyaos/soyaos/commit/869296edae1dcf5fb50ea831665ed7ec75f86f4f))
* **runtime/process:** Remotion CLI wrapper for SilentCut (APP-554) ([f2f303d](https://github.com/soyaos/soyaos/commit/f2f303d795b34e6f55d71fd8550c69fed5aed26e))
* **runtime/providers:** process / container / microvm provider scaffolds (APP-507) ([bd756ff](https://github.com/soyaos/soyaos/commit/bd756ffec44b98393c87bcc8d77d1c896627f16d))
* **runtime:** Capability gate enforces fail-closed allowlist (APP-479) ([5521dfa](https://github.com/soyaos/soyaos/commit/5521dfa9aa39643ab8e8b2b55985ead9718a7e66))
* **runtime:** declare CometProvider contract stub (APP-475) ([59b8262](https://github.com/soyaos/soyaos/commit/59b826284c04695a2a17044503462d33ed8c740c))
* **runtime:** typed DeniedError + ErrDeniedByCapability sentinel (APP-477) ([982c84b](https://github.com/soyaos/soyaos/commit/982c84ba30417ef0690e6d5964e47978127ba24c))
* **runtime:** video-base Comet image Dockerfile + manifest (APP-508) ([d5e4e2d](https://github.com/soyaos/soyaos/commit/d5e4e2d1889fe8972be3a581b978c1f075b2d459))
* **s2-a1:** align Solo to cli.v0 spec — ports 7474/7475, plain healthz, CLI verbs ([ece55b1](https://github.com/soyaos/soyaos/commit/ece55b18df7e69e9356f5a9b499361bbcd28ba69))
* **scheduler:** persistence + missed-fire policies (APP-494) ([d3465dd](https://github.com/soyaos/soyaos/commit/d3465ddc1efdea30fdf90cf54f04d44d618d2921))
* **scope:** per-100ms usage aggregator + /control/v0/usage endpoint (APP-514) ([e023025](https://github.com/soyaos/soyaos/commit/e0230252551abfe507bff2d1bfd2600c9bd99b6a))
* **soyapack,llmcall:** UpstreamDecl + ResolveConfig for per-Agent BYOK (APP-543) ([c586ca5](https://github.com/soyaos/soyaos/commit/c586ca56331a50e2098313ff8ba5888bebaba56a))
* **soyapack,llmcall:** UpstreamDecl + ResolveConfig for per-Agent BYOK (APP-543) ([8e9f1ce](https://github.com/soyaos/soyaos/commit/8e9f1cebd40af21daa2d6301faf1571b951356f1))
* **soyapack:** canonical Manifest + YAML loader + validator (APP-460) ([adbbb97](https://github.com/soyaos/soyaos/commit/adbbb9776e890466aef7b45c47f84155430f4768))
* **soyapack:** prompt.steps + channels manifest segments (APP-550, APP-552) ([b84dcfd](https://github.com/soyaos/soyaos/commit/b84dcfdad7bed2aa68e8d154e2fe9e2114ace70b))
* **state:** Agent/User/Row-scoped state store with MVCC (APP-501) ([aa0460f](https://github.com/soyaos/soyaos/commit/aa0460f27e9c8b6505c4571de4f35f8cbff80778))
* **store:** pkg/store Bolt-backed persistence layer (APP-461 [#1](https://github.com/soyaos/soyaos/issues/1)/N) ([4c62653](https://github.com/soyaos/soyaos/commit/4c62653250a866a8cc6a124fa3cbfaab37f13f8f))
* **studio:** embed soyaos/studio SPA via //go:embed + SPA fallback ([1a19ce0](https://github.com/soyaos/soyaos/commit/1a19ce0de17a3027f9392b0308b9d7810f43216f))
* **tooling:** multi-platform draft templates + originality precheck (APP-504) ([3190346](https://github.com/soyaos/soyaos/commit/3190346c89e0804dbbf0771f0342c731539b1a84))
* **tooling:** tool.parse_input multi-modal input normalizer (APP-466) ([22aa3e8](https://github.com/soyaos/soyaos/commit/22aa3e8d8a6957e54951eb6948524489ad04b5bb))
* **tooling:** tool.rss_fetch + tool.json_api builtin tools (APP-497) ([1405e1a](https://github.com/soyaos/soyaos/commit/1405e1a3e8a727604c682273238883adbda863f1))


### Bug Fixes

* **auth:** SeedDevKey scope narrows to invoke+list (APP-478) ([67be5dc](https://github.com/soyaos/soyaos/commit/67be5dc266c8da5cb231f0d470a9f97a6a154aa3))
* **auth:** 在生产网关装配行级令牌签名器 ([6944d35](https://github.com/soyaos/soyaos/commit/6944d35b02fcdbdf353a5038695da3b57a6b29c9))
* **ci:** 为受限 Linux Chrome 测试显式关闭沙箱 ([686a931](https://github.com/soyaos/soyaos/commit/686a93164441d0bdd689d86194be532ebb8da62c))
* **cli:** accept flags in any position for `agent run` / `agent list` ([cb9b930](https://github.com/soyaos/soyaos/commit/cb9b9308914261eed258cf2918bf45951dfb0bc2))
* **llmcall,gateway:** no total timeout + finish_reason pass-through + structured stream errors ([2e4bcc9](https://github.com/soyaos/soyaos/commit/2e4bcc9dc6c0882bf88fb092db4db2b509fc9884))
* **openaicompat:** SSE chunk delta must omit role on non-first frames ([543df61](https://github.com/soyaos/soyaos/commit/543df61ba19a935b7f91adbea8865d88d3c3841f))
* **release:** 串联可验证的发布产物流水线 ([52fbadd](https://github.com/soyaos/soyaos/commit/52fbaddceb55f8c8e01a8ab07e7485b65f0fc78b))
* **release:** 落地主干提交与可验证发布流水线 ([e65be49](https://github.com/soyaos/soyaos/commit/e65be4977cb903ef2ee9d0f0e0a7d73e8b28f8de))
* **state:** 原子化 BoltStore 并发写入 ([ec936cd](https://github.com/soyaos/soyaos/commit/ec936cdcd420664cac1c791d00d55bbe160204c5))


### Code Refactoring

* **llmcall:** rename pkg/modelgw → pkg/llmcall + term-lock cleanup (APP-469) ([0a46a0e](https://github.com/soyaos/soyaos/commit/0a46a0ec7bd7207d1e4fd7db85f2ae1a3ea48fe1))
* **runtime:** switch Profile to isolation axis (APP-474) ([6e25382](https://github.com/soyaos/soyaos/commit/6e253820a892930d4c6c7dff0e1eb003842d43d7))


### Documentation

* **governance:** 明确主干开发与 PR 交付策略 ([5226666](https://github.com/soyaos/soyaos/commit/52266662ada2e067a601588156d07c83c4a05cf8))
* **readme-zh:** sync data-plane segregation + Planet/Moon/Comet term lock (APP-483) ([7a2116d](https://github.com/soyaos/soyaos/commit/7a2116db04d0a18632b6934f5d0f8fe60b0f8190))
* **readme:** add Concepts section mapping to pkg/* (APP-481) ([ee9c848](https://github.com/soyaos/soyaos/commit/ee9c848761ed2ad0aa9f234f083a5b814564e8e8))
* **readme:** add OpenAI-Compat clients section (APP-482) ([dcf737b](https://github.com/soyaos/soyaos/commit/dcf737b6d5a6539686397ff873c7920d649cfe63))
* **readme:** link DD-008 row to example-essay-tutor reference repo (APP-465) ([0e1b739](https://github.com/soyaos/soyaos/commit/0e1b739837d634177522b1cdb905f94953c6dbf9))
* **readme:** 补齐四旗舰参考仓链接 ([6408652](https://github.com/soyaos/soyaos/commit/6408652190653afb1a51bbf457869a082c00174e))
* record pkg/soyapack + pkg/factory refactor in CHANGELOG (APP-460) ([6665dae](https://github.com/soyaos/soyaos/commit/6665daecf96740a45236db92915799a774687ba7))


### Continuous Integration

* **image:** ghcr.io/soyaos multi-arch image push pipeline (APP-519) ([0799bb5](https://github.com/soyaos/soyaos/commit/0799bb58f50166fd8dfc2ba6195fa676bb81ad28))
* **image:** ghcr.io/soyaos/soyaos multi-arch push hardening (APP-559) ([0bdf5cd](https://github.com/soyaos/soyaos/commit/0bdf5cd66ddaec39c21c74e67784fa84461e6c64))
* **matrix-build:** 6 editions × 5 platforms, solo-active subset (APP-522) ([c828526](https://github.com/soyaos/soyaos/commit/c82852616e5fd238b78770c661f53e9d595d2b65))
* **release-please:** conventional-commits → changelog + auto tag (APP-521) ([4029d82](https://github.com/soyaos/soyaos/commit/4029d8246681a1d1a5f5dd498c36429f3b110039))
* **release-please:** real bootstrap-sha + config polish (APP-556) ([c403b00](https://github.com/soyaos/soyaos/commit/c403b00df91b4498f32c5bffe406f8ff19974505))
* **release:** Sigstore cosign workflow polish + verify hint in release notes (APP-557) ([30ada05](https://github.com/soyaos/soyaos/commit/30ada050b887e4f2ec02c01da769ebfef5f98b77))
* **release:** Sigstore keyless cosign sign-blob pipeline (APP-517) ([d418c06](https://github.com/soyaos/soyaos/commit/d418c06602f33eac2df74379b10efa57d49b5781))
* **sbom:** SPDX + CycloneDX SBOM workflow polish (APP-558) ([8799447](https://github.com/soyaos/soyaos/commit/8799447bded47fa8d006d7e04e17a3bf5a11340d))
* **sbom:** syft-generated SPDX + CycloneDX SBOM on release (APP-518) ([64db93b](https://github.com/soyaos/soyaos/commit/64db93bf1b8a903f232433ea2c8ac2ec87e3c4d8))

## [Unreleased]

### Added
- `pkg/soyapack` — canonical SoyaPack v0 manifest types, YAML loader
  (strict at the top level with `x-` extension passthrough) and `Validate()`
  that enforces the contract from [`soyaos/specs`](https://github.com/soyaos/specs).
  Three KIND fixtures live at `examples/manifests/{agent,skill,memory}.yaml`.
  Adds dependency `gopkg.in/yaml.v3`.
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
- `pkg/factory` no longer carries the `Manifest` type — `pkg/soyapack`
  owns it. Factory becomes a thin `Translator` interface that produces
  `*soyapack.Manifest`. The alpha `Stub` returns `ErrNotImplemented` until
  the NewsBeam Agent Factory (APP-492) lands.

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
