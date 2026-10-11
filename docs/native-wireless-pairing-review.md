# 独立无线配对检查 · 2026-10-06

本轮完成源码检查；真实手机首次配对尚待验证。

当前断点：

- `pairing_qr.go` 的 `runPairCommand` 和 `runConnectCommand` 调用官方 adb 子进程。二维码发现由 Go 完成，配对与连接由官方工具执行。
- `native_settings.go` 在二维码成功和配对码成功后调用 `SetTransport("system", "")`，因此 UI 会恢复 server 路径。
- `adb_transport.go` 的独立 TCP 路径调用 `adblib.ConnectTCP(ctx, address, nil)`，由库读取系统 ADB 密钥。独立身份需要传入应用自行持久化的 RSA signer。
- 当前固定版本 `pgaskin/go-adb` 已实现 A_STLS / TLS 1.3 直连认证，`adb/adbtcpip/adbtcpip.go` 的首次配对仍为 TODO。

纯 Go 候选：`https://github.com/juanlianyangyang/adb-go`，本轮检查 HEAD `5acae68d6a534e44d5d0437397111bedbbc6077e`。它实现 TLS exporter、SPAKE2、PeerInfo 加密交换，可作为原生配对参考。源码当前会记录配对码；握手后的读写没有贯穿 context 的取消/超时；包头声明的长度直接用于分配；真实设备集成测试默认跳过。该库为 AGPL-3.0，实际引入需匹配本项目的许可选择。本轮保持应用依赖清单原状。

建议接入顺序：应用内生成并保存专用 RSA 身份 → 原生配对码协议 → Go TLS 直连 → shell 验证 → 存档读取 → 二维码复用相同配对逻辑。系统已有凭据作为用户显式选择保留。配对成功后直接使用连接服务的端口建立 Go transport；配对服务端口只用于首次配对。

验证时使用新的应用身份，完成手机配对、关闭并重启应用、重新发现设备、TLS 连接、拉取存档，覆盖错误配对码、取消和超时。
