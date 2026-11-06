# agentsd — Headless Agent Runtime

**设计文档 / Draft 0.2**

## 1. 项目概述

`agentsd` 是一个面向 AI Agent 的无头（Headless）运行时与管理服务。

在现代智能体生态中（如 DeepSeek Harness 团队提出的“One Router, Many Workers”超级智能体理念），上层通常运行着一个 7×24 小时不间断工作、与人类用户自然对话的个人助理（Router）。人类不关心 Session 分裂与上下文工程妥协，希望如同与真人交流般随时发号施令。`agentsd` 的核心定位，就是作为**底层的 Subagent 运行时与执行引擎**，专职为上层助理提供可调度、可并发、模型可插拔的“苦力”执行能力。

核心设计理念：

- **Agent-native**：系统的主要调用者是外部 AI Agent（如个人助理），所有功能以机器与模型调用为第一设计目标。
- **Headless & Zero-baggage**：彻底剥离传统交互型工具（如 Codex）面向人类肉眼设计的复杂包袱（无 TUI 依赖、无审批弹窗、全自动化、纯 Event 流驱动）。
- **Model-pluggable**：模型适配层可插拔，兼容主流商业与开源模型，鼓励使用低成本高效能的模型（如高性价比的 GPT、DeepSeek）充当并发苦力工。
- **Multi-agent & Full-duplex**：原生支持多个 Agent 并发运行，支持父子级与同级 Subagent 之间的全双工、非阻塞协同通信。
- **Observable & Modular GUI**：以不可变事件（Event Log）为核心，CLI 提供标准查询与流式跟踪；同时支持独立的、可选的人类 GUI 监控面板作为外部客户端连入，实现可视化全局观察。
- **Persistent**：Agent 的运行状态和执行历史持久化，支持故障恢复。
- **Lightweight**：优先采用轻量级实现，以单机 Go Goroutine 为起点，控制资源消耗，并预留分布式演进能力。

项目自主实现 Agent Loop、工具系统和通信机制，不依赖第三方重型 Agent 框架，不引入 MCP、A2A。

---

## 2. 核心角色与交互拓扑

### 2.1 参与者角色

系统中存在四类交互实体：

| 实体 | 定位与职责 |
|---|---|
| **7×24 个人助理（调用方 Agent）** | 在 `agentsd` 外部常驻运行的超级路由，接收人类自然语言指令，拆解任务并通过 API/CLI 调度受管 Agent。 |
| **受管 Agent（Subagent 苦力）** | 由 `agentsd` 创建、托管在内部协程中的 Agent，拥有独立的推理循环、上下文、邮箱与工作空间，专职执行具体代码/工具任务。 |
| **人类开发者（CLI 操作者）** | 通过终端命令（如 `agents ps`、`agents send`）进行调试、手动派单或干预。 |
| **人类观察者（GUI 监控看板）** | 可选的独立 Web/桌面客户端，仅作为观察者订阅 Daemon 的事件流，提供全局可视化进展看板。 |

### 2.2 目标寻址规范

系统统一采用极简、无协议头负担的路径格式对 Agent 进行寻址（不引入 `agent://` 伪协议）：

- **同命名空间私信**：直接指定目标 `<session_id>`（如 UUID 或前缀短哈希 `4a8b2c91`）。调用方必须明确提供当前命名空间。
- **同命名空间广播**：指定 `"all"`，向当前命名空间内的所有活跃受管 Agent 广播。
- **跨命名空间寻址（可选扩展）**：采用 `[namespace]/<session_id>` 路径格式（如 `frontend/4a8b2c91`）。

无论是人类在终端使用 CLI（如 `agents send 4a8b2c91 "..."`），还是受管 Agent 在内部调用 `send("4a8b2c91", ...)`，格式完全统一直观，免除无谓的协议字符串解析开销。

### 2.3 Namespace & Session

- **Namespace（命名空间）**：**项目/工作区级的逻辑协作域**（长生命周期、相对稳定，如 `agentsd`、`frontend-app`）。
  - 磁盘组织：在存储层按 Namespace 划分目录 `sessions/<namespace>/`，避免随单次任务无限制膨胀。
  - 作用边界：同一 Namespace 下的 Agent 具备相互发现与协作可见性；总线内部广播消息 `send("all", ...)` 严格局限在当前 Namespace 内；跨 Namespace 需显式指定。
- **Session（会话）**：受管 Agent 执行的**不可变历史与状态持久化单元**（短生命周期）。
  - 绑定关系：当前架构下一个活跃的受管 Agent 对应一个持久化 Session。
  - **Session ID**：采用成熟标准库生成的 **UUIDv4**（如 `github.com/google/uuid`），全球唯一，彻底杜绝高并发或多次运行的命名冲突。

---

## 3. 系统架构

```text
  [ 7×24 个人助理 ]           [ 人类开发者 ]               [ 人类观察者 ]
  (外部 Agent，调接口)      (终端 CLI: agents ps)       (模块化 Web/GUI 界面)
           \                       |                       /
            \                      |                      /
             ▼                     ▼                     ▼
       ┌───────────────────────────────────────────────────┐
       │             agentsd (无头常驻后台守护服务)           │
       │                                                   │
       │  • 外部 IPC 接口：跨平台 UDS + WebSocket API (全双工)│
       │  • 进程内总线 (IrcBus)：非阻塞消息分发与邮箱管理       │
       │  • Agent Manager：调度受管 Agent (Goroutines)       │
       │  • 模型适配层：支持主流模型可插拔 (GPT/Claude/DeepSeek) │
       │  • 事件广播中心：向订阅的 CLI / GUI 实时推送事件流   │
       └─────────────────────────┬─────────────────────────┘
                                 │
                                 ▼
              [ 本地磁盘: sessions/<namespace>/<uuid>.jsonl ]
```

---

## 4. 通信机制设计

通信机制明确划分为两层：**外部 IPC（外部客户端与 Daemon）** 与 **内部通信（受管 Agent 之间）**。

### 4.1 外部 IPC（CLI / 外部助理 / GUI <-> agentsd）

* **传输层**：全平台统一采用 **Unix Domain Socket (UDS)**。
  * Windows 10 (Build 17063+) 原生支持 `AF_UNIX`，Go 语言自 1.12 起标准库 `net.Listen("unix", ...)` 原生跨平台兼容。
  * 弃用 Windows Named Pipe 设想，实现 100% 跨平台统一套接字抽象，免除端口冲突、防火墙弹窗与权限裸奔隐患。
  * 客户端 SDK 抽象 Endpoint 参数（默认为 `unix:///.../agentsd.sock`，允许配置 `ws://ip:port`，为未来本地调云端预留扩展能力）。
* **协议层**：采用 **WebSocket（运行于 UDS 之上）**。
  * 单连接全双工：一条连接同时承担指令请求、双向流式数据传输与实时插话，避免“一条读、一条写”的连接割裂。
  * 统一协议帧：借鉴轻量 JSON-RPC / 消息帧封装，CLI、外部助理与 GUI 仪表盘均通过同一套协议既接收事件推送，又实时反向注入控制指令。

### 4.2 内部通信（父子 Agent 直接通信、Subagent 兄弟通信）

所有受管 Agent 运行在同一个 Go 进程的不同 Goroutine 中，采用参考 IRC 风格的**进程内纯内存消息总线（IrcBus）**。

* **发后即忘（Fire-and-forget）**：`send(to, message)` 绝不阻塞等待对方模型推理，消息交由总线后立即返回投递回执（`outcome`）。
* **全双工事件驱动与三态消费**：
  1. **接收方处于 IDLE / WAITING 状态**：总线立即触发一轮新的 **Wake Turn（唤醒推理）**。
  2. **接收方处于 BUSY 状态（正在流式推理或执行工具）**：
     * **来自父级或外部调用方的消息** -> 视为 **`Steer`（实时操舵打断）**，在推理循环或工具等待间隙高优先级介入纠偏。
     * **来自同级 Peer 的消息** -> 视为 **`Aside`（旁白插话）**，暂存并延迟到当前步骤边界（Step Boundary，即当前工具执行完成）平滑并入上下文，不损坏原子工具执行。
  3. **接收方主动调用 `wait` 工具中**：总线直接 `resolve` 等待器，`wait` 工具立即返回新消息。
* **Agent 内置工具**：
  * `send(to, message)`：向指定 Agent ID、跨空间路径 `[namespace]/[id]` 或 `"all"` 广播消息。
  * `wait(timeout)`：主动让出资源，进入等待状态，直到有新消息或任务完成唤醒。

---

## 5. Event System 与不可变日志

### 5.1 事件分类

- **生命周期**：`agent.started`, `agent.waiting`, `agent.completed`, `agent.failed`
- **推理与执行**：`turn.started`, `turn.completed`, `llm.requested`, `llm.completed`, `tool.started`, `tool.completed`, `tool.failed`
- **通信与协同**：`message.received`, `message.sent`, `steer.injected`, `aside.queued`

### 5.2 存储与查询原则

- **单 Session 单 JSONL 文件**：
  存储路径固定为 `~/.agentsd/sessions/<namespace>/<uuid>.jsonl`。执行历史自包含且不可变，仅追加（Append-only）。
- **文件内容规范**：
  - **Line 1 (Session Header / Manifest)**：Session 创建时写入，包含静态元数据：
    ```json
    {"type":"session.header","id":"<uuid>","namespace":"agentsd","model":"gpt-4o-mini","task":"执行代码重构","workspace":"/path/to/cwd","started_at":"2026-11-06T03:00:00Z"}
    ```
  - **Line 2 ~ N-1 (Events)**：执行全生命周期不可变事件序列（`turn.started`, `tool.started`, `tool.completed` 等），纯追加写入。事件携带短 ID（如 Base62 生成）。
  - **Line N (Termination)**：终止事件（`agent.completed`, `agent.failed`, `agent.canceled`），包含耗时、最终报告或错误信息。
- **超大内容分离引用**：大型工具输出与文件使用已有的全局 Blob 存储（`~/.agentsd/artifacts/<sha256>.artifact`），事件中仅保存哈希/路径引用。
- **Crash 自愈与快速恢复（Recovery）**：
  - 服务重启时，如果内存中未跟踪某个 Session，但磁盘 `.jsonl` 最后一行既不是 `agent.completed` 也不是 `agent.failed`，系统自动向末尾追加 `agent.failed`（标记 daemon 异常退出）实现无损自愈。
  - 查询状态（如 `agents ps`）时无需解析全量事件，直接读取 Line 1 与文件末尾若干字节即可快速提取状态，无需维护额外的辅助索引文件。

---

## 6. CLI 设计

CLI 既是外部 Agent（个人助理）调度的主要抓手，也是人类工程师排查系统的入口：

**任务与生命周期**
```bash
agents spawn --model gpt-4o-mini --task "执行代码重构" -n <namespace>
agents ps -n <namespace>                    # 表格默认截取 UUID 前 8 位展示
agents ps -A/--all-namespaces
agents inspect -n <namespace> <id>             # 支持 UUID 前缀模糊匹配
agents kill -n <namespace> <id>                # 支持 UUID 前缀模糊匹配
agents compact -n <namespace> <id>             # 异步提交上下文压缩
agents compact -n <namespace> <id> --wait      # 等待压缩完成
agents pause <id>
agents resume <id>
```

**命名空间管理**
```bash
agents namespace create <namespace>
agents namespace delete [--force] <namespace>
agents namespace rename <from> <to>
agents namespace list
```

**消息与协同**
```bash
agents send <id> "中途纠偏：不要改动现有的导出函数"
agents inbox <id>
agents wait <id>
```

**观测与流式跟踪**
```bash
agents activity <id>          # 查看历史摘要
agents activity <id> --follow # 建立 WebSocket 实时跟随输出
agents log <session>
agents show <event-id>
```

所有命令支持 `--json` 输出，提供机器可读规范响应。

---

## 7. 模块化 GUI 监控面板（可选）

为满足人类用户对任务大局的直观把控，系统提供**解耦的模块化 GUI 界面**：

- **无头独立**：`agentsd` 本身纯无头运行，不绑定任何 GUI 框架依赖。
- **纯客户端连接**：GUI 作为普通的 WebSocket 客户端连接到 `agentsd` 的 UDS 或网络端口。
- **功能侧重**：只读为主，展示多 Agent 拓扑关系、活跃协程状态、实时 Token 流与工具执行树；按需提供一键中断/插话按钮。

---

## 8. 技术选型汇总

| 组件 | 最终选型 | 决策依据 |
|---|---|---|
| **开发语言** | **Go** | 原生高并发协程（Goroutine）、极低资源开销、跨平台单二进制交付。 |
| **外部 IPC** | **Unix Domain Socket (UDS)** | 跨平台统一（包括 Win10+）、原生文件权限安全控制、免端口冲突与防火墙弹窗。 |
| **IPC 协议** | **WebSocket over UDS** | 单一连接实现真正的全双工双向交互，兼顾短命令请求与实时事件流，免去多连接维护。 |
| **内部通信** | **IrcBus (内存 Channel/Map)** | 进程内纯内存调度，发后即忘，配合 Steer/Aside 实现全双工非阻塞。 |
| **Agent 并发** | **Goroutine Runtime** | 单机单进程内并发，轻量秒级创建，支持全局资源限流。 |
| **模型接入** | **可插拔统一 Provider** | 优先适配低成本模型（如 GPT、DeepSeek）充当执行苦力，解耦具体模型供应商。 |
| **唯一标识** | **UUIDv4 (`github.com/google/uuid`)** | 成熟事实标准，解析校验完备，彻底避免跨任务/跨时间的 ID 碰撞。 |
| **持久化** | **JSONL (Append-only per session in namespace) + Artifact Blob** | 单 Session 单文件，天然抗故障中断，支持高效流式追加与无损自愈。 |
| **观测层** | **CLI + 可选独立 GUI** | 彻底分离无头核心与可视化看板。 |

---

## 9. 演进路线与实施计划

开发过程坚持**“核心引擎优先、外围包装后置”**的原则：

### 阶段一：Agent Runtime 核心闭环（MVP）
聚焦单 Agent 推理执行，无需考虑 Daemon 或网络：
1. **上下文组装**：定义系统提示词拼装与消息历史管理。
2. **上下文压缩**：压缩始终启用；阈值由模型 `ContextWindow` 推导；使用独立摘要请求、多次压缩、阈值前摘要预生成和 `context.compacted` 事件。
3. **模型统一抽象**：对接 OpenAI 格式接口（打通高性价比模型作为苦力工）。
4. **工具执行系统**：实现本地 Shell 执行和基础文件读写工具。
5. **Agent Loop 核心**：实现 `LLM -> Tool -> LLM` 自主循环。
6. **工具系统基础能力**：建立 `ToolSpec`、`ToolPolicy`、`ToolResult`、`SessionContext`、`ToolFactory`、hidden/启用列表、生命周期事件、参数校验、timeout、取消、进度更新与批量调用调度。
7. **文件工具基础能力与第一批工具**：建立 Workspace、路径解析、输出限制、artifact、文件快照，并接入 `read`、`glob`、`grep`、`find`、`write`、`edit`、`shell`。
8. **事件持久化**：每一步操作追加写入本地 `events.jsonl`。

### 阶段二：轻量 Daemon 宿主、UDS IPC 与 Session 持久化
在核心 Runtime 稳定后，将其托管至后台服务并实现完备的持久化：
1. 实现基础常驻守护进程 `agentsd`。
2. 实现全平台统一的 Unix Domain Socket 监听与基础 WebSocket 握手。
3. **Session 持久化与存储引擎**：
   - 引入成熟的 `github.com/google/uuid`。
   - 建立 `internal/session` 包，管理 `~/.agentsd/sessions/<namespace>/<uuid>.jsonl`。
   - 实现单文件 Header 写入、Append-only 事件追加、文件末尾状态快速提取与 Crash 自愈。
4. **Daemon 状态机改造**：将 `daemon.Manager` 从纯内存映射切换为基于 `session.Store` 的状态驱动。
5. 实现 CLI 的 `spawn`、`ps`（支持 `-n` / `-A` 与 UUID 短前缀显示）、`inspect`（前缀匹配）、`kill`。
6. 实现启动文件锁与单实例保护。

### 阶段三：全双工内部通信总线（IrcBus）
实现多 Agent 间的全双工与非阻塞干预：
1. 实现进程内单例 `IrcBus` 与 Agent 邮箱。
2. 实现 `Steer`（实时操舵打断）与 `Aside`（步骤间平滑并入）。
3. 向受管 Agent 注入 `send` 与 `wait` 工具。
4. CLI 对接 `agents send` 实现外部中途插话。

### 阶段四：流式观测与模块化 GUI
1. 实现 WebSocket 实时事件广播，完成 `agents activity <id> --follow`。
2. 搭建独立的、可选的轻量 Web 监控看板。
3. 完善 Session 分支历史与故障恢复机制。
