# SoyaOS

**简体中文** | [English](README.md)

> **Agent 操作系统** —— 一份二进制、六种发行版、三种节点角色（Planet / Moon / Comet），把公网、内网、临时沙箱里的算力与能力一次串起来。

SoyaOS 取名自一颗黄豆——一颗豆子能长成毛豆、豆腐、豆浆、腐竹。SoyaOS 想为 Agent 做同一件事：**同一颗内核，长出无数形态**。

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-alpha-orange.svg)](CHANGELOG.md)
[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8.svg)](go.mod)

## 状态

**预发布：v0.1.0-alpha.0。** 仅 Day-1 工程骨架；API 在 v0.1.0 之前**不稳定**，会变动。尚未达到生产可用。

v0.1.0 锁定四个旗舰用户故事（DD-008 ~ DD-011）：

| # | Agent | 画像 | Aha Moment |
|---|---|---|---|
| DD-008 | **Compo**（[示例仓](https://github.com/soyaos/example-essay-tutor)） | 辅导孩子的家长 | 一篇范文 + 标题 → 一份可打印的写作指引 PDF |
| DD-009 | **NewsBeam** | AI 知识工作者 | 一句话造 Agent → 每天 9:00 钉钉收到 AI 资讯长图 |
| DD-010 | **EstateMuse** | 房产自媒体作者 | 一句话 → 500 条选题 Excel + 每行"生成图文/短视频" |
| DD-011 | **SilentCut** | 独立短视频创作者 | 自然语言 → Remotion 脚本 → Comet 渲染 → MP4 落 NAS |

四个故事都能跨六种发行版端到端跑通的那一天，就是 v0.1.0 发布日。

## 架构一览

SoyaOS 网络由三种节点角色组成：

- **Planet** —— 长期在线、跑在公网的控制面（鉴权、目录、调度、能力令牌）。永远**不主动**连进你的内网。
- **Moon** —— 长期驻留在你的内网或自己的设备上。**反向出连** Planet；持有你的数据、凭据与状态。
- **Comet** —— 任务级临时沙箱（microVM / 容器 / 进程）。用完即焚。

**控制面经 Planet，数据面优先 Moon ↔ Comet 直连。** 大文件（视频、产物）从不必经 Planet。

**All-in-One Mode**（单机版）：三种角色跑在**同一个 Go 进程**里——一份二进制、零依赖，`./soyaos start` 就有一颗 SoyaOS。

### 六种发行版

| # | 发行版 | CLI | 适合 |
|---|---|---|---|
| 01 | SoyaOS 单机版 | `solo` | 一个人、一台机器 |
| 02 | SoyaOS 集群版 | `cluster` | 有 1 台 VPS + 若干内网设备 |
| 03 | SoyaOS 云版 | `cloud` | 不想运维，注册 → API Key → 用 |
| 04 | SoyaOS 混合版 | `hybrid` | 厂商管控制面，数据从不离开自己 |
| 05 | SoyaOS 企业云版 | `ent-cloud` | 多租户 SaaS + SSO + SLA + 合规 |
| 06 | SoyaOS 企业私有版 | `ent-private` | 客户自部，含离线 / air-gapped |

## 概念

文档、CLI、代码里反复出现的五个概念，每个概念对应一个 Go 包。

| 概念 | 是什么 | 代码 |
|---|---|---|
| **SoyaKernel** | 单二进制内核，承载所有其它概念。 | `pkg/kernel` |
| **SoyaPack** | 便携、声明式的 Agent 格式（manifest + prompts + tools + sandbox）。 | `specs/specs/soyapack/v0/` + `pkg/soyapack` |
| **SoyaForge** | Agent 工厂：自然语言意图 → SoyaPack manifest。 | `pkg/factory` |
| **SoyaScope** | 仅追加的事件日志，承载审计、调度、按秒计费。 | `pkg/scope` |
| **SoyaAuth** | API Key 颁发、scope token、面向分享产物的 row-scoped JWT。 | `pkg/auth` |

> Aha：把 `base_url`、`api_key`、`model` 粘进 Cherry Studio，就能像调用 OpenAI 模型一样和自己的 Agent 对话。

## 目录结构

参见 [README.md](README.md#repository-layout)。单一 `go.mod` 位于根目录；前端 dist 通过 `//go:embed` 内嵌进二进制，"一份文件全搞定"。

## 快速开始

> 预发布：`./soyaos start` 会在**同一进程**里拉起 Planet+Moon+Comet，并把 OpenAI-Compat 数据面挂在 `127.0.0.1:7474`、控制 RPC 挂在 `127.0.0.1:7475`。默认仅监听 localhost，不依赖任何外部组件。

```bash
make build
./bin/soyaos start            # 数据面 127.0.0.1:7474 / 控制面 127.0.0.1:7475
# 在另一终端冒烟测试 OpenAI-Compat 接口
curl http://127.0.0.1:7474/v1/models \
  -H "Authorization: Bearer sk-soya-dev-local"

# 或直接走 CLI
./bin/soyaos agent run echo "你好"
```

参见 [`examples/echo-agent/`](examples/echo-agent/) 获取第一个可运行的 Agent。

### 从任意 OpenAI 兼容客户端接入

SoyaOS 完整复用 OpenAI 的 `/v1/chat/completions` 协议。只要把同样的三个值 —— `base_url`、`api_key`、`model` —— 粘进任意客户端，你的 Agent 就会作为一个虚拟模型出现在模型列表里。

| 客户端 | `base_url` | `api_key` | `model` |
|---|---|---|---|
| Cherry Studio | `http://localhost:8080/v1` | `sk-soya-…` | `soya:compo` |
| Cursor | `http://localhost:8080/v1` | `sk-soya-…` | `soya:compo` |
| Continue (VS Code / JetBrains) | `http://localhost:8080/v1` | `sk-soya-…` | `soya:compo` |
| Zed | `http://localhost:8080/v1` | `sk-soya-…` | `soya:compo` |

> `model` 取值即 Agent 在 SoyaPack manifest 中声明的 `virtual_model_id`（例如 `soya:compo`、`soya:news-beam`）。

## 贡献

欢迎贡献。SoyaOS 使用 [DCO](https://developercertificate.org/) —— 每个 commit 必须带 `Signed-off-by:`。详见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 设计文档

完整 SoyaOS 设计存放在独立的 WebMind 知识库（17 份 HTML 文档：架构、发行版、设计决策、旗舰故事…）。本仓库每个 Stage 收尾都会与那 17 份设计文档做对齐审查。

## 协议

[MIT](LICENSE) —— Copyright (c) 2026 SoyaOS Contributors.
