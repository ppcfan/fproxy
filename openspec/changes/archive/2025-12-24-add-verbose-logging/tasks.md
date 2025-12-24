## 1. Implementation
- [x] 1.1 在 `config/config.go` 的 `CLIFlags` 结构体中添加 `Verbose` 字段
- [x] 1.2 在 `ParseCLIFlags` 中添加 `-verbose` flag 定义
- [x] 1.3 在 `Config` 结构体中添加 `Verbose` 字段
- [x] 1.4 在 `Load` 函数中处理 verbose 配置的合并逻辑
- [x] 1.5 修改 `main.go`，根据 `cfg.Verbose` 设置日志级别为 Debug 或 Info
- [x] 1.6 运行测试确保无回归
