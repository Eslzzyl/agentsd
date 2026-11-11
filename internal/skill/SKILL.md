---
name: agentsd
description: 使用 agentsd 启动、管理和观察后台 agent。
---

# agentsd

## 安装

```bash
go install -mod=readonly ./cmd/agents ./cmd/agentsd
```

## 启动 daemon

```bash
agents daemon start
```

## 创建任务

```bash
agents spawn -n <namespace> --model <model> --task "<task>"
```

## 查看状态

```bash
agents ps -n <namespace>
agents inspect -n <namespace> <id>
agents activity -n <namespace> <id> --follow
```

## 发送消息

```bash
agents send -n <namespace> [--mode steer|follow-up] <id> "<message>"
agents inbox -n <namespace> <id>
```
