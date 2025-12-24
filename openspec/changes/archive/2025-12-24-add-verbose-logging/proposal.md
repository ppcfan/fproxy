# Change: Add verbose logging flag

## Why
用户配置好客户端和服务端后转发不通，需要更详细的日志来诊断问题。当前日志级别固定为 Info，无法看到 Debug 级别的数据包转发详情。

## What Changes
- 添加 `-verbose` 命令行参数
- 添加 YAML 配置文件中的 `verbose` 选项
- 当 verbose 启用时，日志级别降为 Debug，显示每个数据包的转发详情

## Impact
- Affected specs: `udp-relay` (Configuration, Logging 相关 requirements)
- Affected code: `config/config.go`, `main.go`
