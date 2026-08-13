# SoyaOS relay 隐私承诺

> [!WARNING]
> SoyaOS 尚在开发中，未正式发布。协议、接口和部署方式尚不稳定，随时可能
> 发生 breaking change；请勿把当前 alpha 当作生产级隐私或合规认证。

状态：APP-510 alpha 架构承诺
生效日期：2026-08-13

当前 alpha 节点：DigitalOcean SFO3，`137.184.35.52:7443/udp`。节点只开放
密文 relay 端口；健康接口绑定服务器回环地址。

## 结论

SoyaOS relay **不终止 Moon ↔ Comet 的 QUIC/TLS/mTLS 会话，不保存或解密
MP4 等应用层内容**。双方在 relay 之外完成证书验证；relay 收到的应用载荷
已经是 QUIC 密文。

实现边界：

1. relay 只解析固定长度的路由信封：协议版本、Moon/Comet 侧标记、过期时间、
   随机会话 ID 和 HMAC。
2. 信封后的 QUIC 数据报原样转发，服务端没有 TLS 私钥，也不调用 QUIC 解析器。
3. 路由 token 默认 5 分钟过期；每侧绑定第一个出现的 UDP 地址，避免随后持有
   token 的地址抢占已有会话。
4. 健康接口只输出活动会话数、转发/丢弃数据报数和转发字节数。
5. 服务不记录 token、IP 地址或 payload；基础设施层的系统和网络日志仍须按本
   文档的最小化原则配置。

## relay 仍然能够看到什么

“看不到明文”不等于“看不到任何信息”。运行 relay 的基础设施仍可观察：

- 两端公网 IP、UDP 端口；
- 连接时间、持续时间、数据报数量和字节数；
- 短期会话 ID、token 过期时间以及 Moon/Comet 侧标记；
- 丢包、限速和健康统计。

这些元数据不得用于内容分析或用户画像。生产监控只保留聚合计数，不应落盘
逐会话 token 或完整网络地址。

## 为什么不能写成“relay 终止 mTLS”

一旦 relay 终止端到端 TLS，它在技术上就可能读取应用明文。因此 APP-510
原描述中的“relay 节点用 mTLS 终止”被明确修正为：

> Moon ↔ Comet mTLS 只在两端终止；relay 位于 QUIC 之下，转发密文 UDP 数据报。

## 可验证证据

- `pkg/mesh/relay` 不依赖 TLS 或 QUIC 库，只处理路由信封和 UDP 转发。
- `pkg/mesh/quic` 把 `relay.PacketConn` 放在 quic-go 之下，由 quic-go 在两端完成
  TLS 1.3 和双向证书验证。
- `TestTransport_RelayCarriesOnlyQUICCiphertext` 在 relay socket 记录数据报，传输
  已知 MP4 明文标记，并断言 relay 观察到的任何数据报都不包含该标记。
- SilentCut E2E 以 SHA-256 校验 relay 前后的真实 MP4 字节一致。

## 决策签署

- 架构选择与安全边界：项目负责人 Jie Ke 于 2026-08-13 在
  [APP-510](https://linear.app/appforges/issue/APP-510/silentcut-planet-quic-relay-集群选型与部署)
  批准方案 A（自建、不终止 QUIC 的 relay）。
- 实现验证：以 APP-510 中附带的测试、部署地址和验收记录为准；上线前不得删减
  上述 mTLS、token 过期、限速和无 payload 日志要求。
