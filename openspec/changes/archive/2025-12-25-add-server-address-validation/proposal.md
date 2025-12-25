# Change: Add Server Address Validation

## Why

fproxy 的多路径机制设计用于将同一个包通过多条路径（UDP/TCP）发送到**同一个服务端**，以实现冗余和可靠性。服务端使用 `(session_id, seq)` 对进行去重，确保重复的包只转发一次到目标服务器。

当前存在两个配置风险：
1. 如果用户错误地配置了指向**不同服务端 IP 地址**的多条路径，不同服务端之间无法共享去重状态，导致同一包被重复转发
2. 如果配置了域名，DNS 解析的不确定性（缓存、多 IP、解析失败）会导致行为不可预测

## What Changes

- **不再支持域名配置**：服务端地址必须使用 IP 地址格式
- **IP 地址一致性验证**：所有服务端端点必须指向同一个 IP 地址
- 如果配置了域名或不同的 IP 地址，拒绝启动并返回明确的错误信息

示例：
- 有效配置: `192.168.1.1:8001/udp,192.168.1.1:8002/tcp` （同一 IP，不同端口/协议）
- 无效配置: `192.168.1.1:8001/udp,192.168.1.2:8001/tcp` （不同 IP - **错误**）
- 无效配置: `myserver.com:8001/udp,myserver.com:8002/tcp` （域名 - **错误**）

## Impact

- **BREAKING**: 现有使用域名的配置将不再有效，需要改为使用 IP 地址
- Affected specs: `udp-relay` (Configuration requirement)
- Affected code: `config/config.go` (Validate function, ParseEndpoint function)
