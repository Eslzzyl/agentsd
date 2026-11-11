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

## Skill 安装

默认安装到 `~/.agents/skills/agentsd/SKILL.md`：

```bash
agents skill install
```

指定 skills 根目录：

```bash
agents skill install --path "$HOME/.agents/skills"
```

目标文件已存在时，使用 `--force` 更新：

```bash
agents skill install --force
```
