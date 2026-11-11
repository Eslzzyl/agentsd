# 安装

本项目要求 Go 1.27.3。

```bash
go install -mod=readonly ./cmd/agents ./cmd/agentsd
```

安装的命令：

- `agents`：命令行工具
- `agentsd`：后台服务

默认安装到 `$(go env GOPATH)/bin`。指定安装目录：

```bash
GOBIN="$HOME/.local/bin" go install -mod=readonly ./cmd/agents ./cmd/agentsd
```
