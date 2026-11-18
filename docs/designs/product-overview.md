# Steward 产品架构与流程总览

[Feature Map：需求与完成状态](../feature-map.md) · [跨功能协议与约束](#contract-0-文档效力与实现规则) · [测试边界](../test-plans/product.md)

本页维护最新跨功能关系；每个功能的需求、用法、局部架构、测试方案和剩余缺口从 Feature Map 进入。

## 协议主题分布

跨功能数据、事件和配置约束见 [运行时契约](runtime-contracts.md)，工程与发布组织见 [工程设计](engineering.md)。局部工具、模型、Task、Child、Computer、Memory 协议在各功能设计中维护；统一验收矩阵见 [产品测试方案](../test-plans/product.md)。

## 当前架构

Steward 是本地 Go 模块化单体，使用独立 Node 模型 Bridge 和 QuickJS 运行时。Main 与 Worker 共用一套 Agent Loop；Root/Child 表示 Worker 在执行树中的位置。SQLite 保存权威领域状态，JetStream 提供事件唤醒。Web 资源嵌入应用，不需要独立前端服务。

```mermaid
flowchart TD
    U["用户：TUI / Web / 已绑定飞书"] --> I["输入认证 / 去重"]
    I --> A["Actor / Scheduler / Claim"]
    A --> L["共享 Agent Loop"]
    C["Conversation / 可配置 Memory 与 Recall / Skills"] --> L
    L <--> B["Node Provider Bridge → 选定模型"]
    L --> T["统一 ToolExecutor / Security / Approval"]
    T <--> J["配置型 JavaScript / QuickJS"]
    T --> X["默认文件与命令 / MCP / macOS Computer"]
    L --> S["SQLite：Task / Run / Generation / Invocation"]
    T --> F["Artifact 字节与授权引用"]
    L --> D["语义回复 / DeliveryRequest / Outbox"]
    D --> U
    Q["Automation fire"] --> A
    W["Worker 业务结果"] --> R["Reporter"]
    R --> A
```

权限、配置、数据、审批和恢复均由现有 Host 管理。可选能力是否注册取决于显式配置；有模块不代表当前安装已经就绪。

## 当前产品流程

```mermaid
flowchart TD
    START["启动 / 模型设置 / Probe / 显式选择"] --> INPUT["用户输入文字与附件"]
    INPUT --> MAIN["Main 理解并执行"]
    MAIN --> KIND{"即时工作或委派？"}
    KIND -->|"即时"| DIRECT["推理 / 控制 / Skill / 聚焦 MCP 操作"]
    DIRECT --> ANSWER["Main 保存最终回复"]
    KIND -->|"耗时 / 异步 / 需 Child"| TASK["显式创建 Task"]
    TASK --> ROOT["Root Worker"]
    ROOT --> TOOL["工具 / Code Mode / Computer"]
    TOOL --> ROOT
    ROOT --> CHILD["可选 Child Worker"]
    CHILD --> ROOT
    ROOT --> RESULT["保存结果与所选产物"]
    RESULT --> REPORT["Reporter → Main"]
    REPORT --> MAIN
    ANSWER --> DELIVERY["冻结交付目标 / Outbox"]
    DELIVERY --> USER["回复、附件或有意义通知"]
    USER --> INPUT
    AUTO["到期 Automation"] --> FIRE["持久 fire / Task / 新 Root"]
    FIRE --> ROOT
```

Main 创建 Task 是因果关联，不是 Main 直接创建 Child。Child 结果只返回直接 Parent；Root 的业务结果经 Reporter 回到 Main。普通进度是只读投影，不触发推理或通知。

## 最新待实现方向

保留上述架构，补齐用户流程中的断点：阶段进度生产、附件理解、富 UI 生成、自动经验、语义记忆和真实服务接入。逐项状态以 Feature Map 为准，不能由本页图推断完成。

Developer 包含 [Traces](observability.md) 与 [Memory](memory.md)。Traces 查询既有执行记录与供应商边界观测；Memory 提供所有者可见的记忆正文、来源和维护回执。两者只读，不接管执行、召回或维护生命周期。通用 Database 检查入口已移除。真实模型效果由[独立评测](../test-plans/product.md#testing-agent-evaluation)覆盖。

## 验证

各功能说明链接并列测试方案；统一命令和证据边界见 Testing。确定性协议/恢复测试不代表真实模型语义质量，macOS/在线账号需相应平台证据。

## 协议、实现与配置约束

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="contract-steward-技术方案基线已确认设计"></a>

### 跨功能协议的维护范围

下列协议原编号保留以便追溯，正文由所属功能技术方案共同维护；不存在另一份独立基线。当前需求状态由 Feature Map 维护，历史版本通过 Git 与 PR 追溯。

<a id="contract-0-文档效力与实现规则"></a>

### 0. 文档效力与实现规则

各功能技术方案与跨功能契约共同约束实现。Coding Agent 开工时遵循以下优先级：

1. 所属技术方案中的状态机、权限边界、持久化时序、模型可见 Tool Schema 与验收场景属于规范性要求。
2. TypeScript、Go 和表结构片段用于定义协议与最小字段，不要求逐字采用命名，但实现必须保持同等语义，并用测试证明兼容。
3. “一期不做”与“后置决策事项”不得被擅自扩展为首期功能；内部可以预留接口，但不得为假设中的未来需求建立第二套领域模型。
4. 若实现细节存在歧义，优先选择能够保持数据可恢复、权限不扩大、外部副作用不重复的最小方案，并更新所属设计的有效取舍与约束；不得以猜测修改模型可见协议。
5. 任何协议变更先更新所属技术方案及契约测试，再修改实现。数据库内部字段可以向后兼容地增加，模型可见 Tool Schema、事件版本和 Artifact 引用格式必须显式版本化。
6. 功能设计约束产品协议与软件安全边界。Golden Case 与真实模型效果评测由独立评测项目管理；产品仓库的测试分层、CI 与发布检查见 [产品测试方案](../test-plans/product.md)。

MVP 的启动门禁与首条纵向链路是：

`首次启动 → 模型连接/登录 → Model Probe → 激活 ModelProfile → TUI 输入 → Main Run → task_manage(create) → Root Worker Run → agent.run() → Child Run → Root 汇总 → Main 回复 → TUI 展示`

Code Mode、Durable Replay、Approval、Artifact 与 Outbox 必须沿这条链路逐步接入；Web、飞书、多 Agent、云端拆分不得阻塞首条链路。

<a id="contract-2-总体架构"></a>

### 2. 总体架构

```mermaid
flowchart TD
    C["Web / TUI / 飞书"] --> I["Interaction / InboundEvent"]
    I --> A["Agent Actor"]
    A --> R["Run"]
    R --> M["Main Agent Loop"]
    M --> K["Task"]
    K --> W["Root Worker Run"]
    M --> L["Model Provider Bridge"]
    W --> L
    M --> V["控制 / 记忆 / 证据 / 交付工具"]
    V --> M
    W --> T["Tooling / Security"]
    T --> X["Computer / MCP"]
    X --> P["结果与 Artifact 持久化"]
    P --> M
    M --> D["AssistantMessage / DeliveryRequest"]
    D --> O["Outbox / Delivery"]
    O --> C
```

主链路定义为：

`ChannelMessage → Interaction → InboundEvent → AgentActor → Run → MainAgentLoop → Context/Control Tools → AssistantMessage → DeliveryRequest → Outbox → Delivery → Channel`

聚焦 MCP 业务调用可直接经过 `MainAgentLoop → Tooling/Security → MCP → ResultPersistence → MainAgentLoop`；复杂工作委派为：

`MainAgentLoop → Task → WorkerRun → WorkerAgentLoop → Tooling/Security → Computer/MCP → ResultPersistence → ResultEvent → MainAgentLoop`

<a id="contract-21-agent-actor-与-main-agent-loop"></a>

#### 2.1 Agent Actor 与 Main Agent Loop

- `Agent Actor` 是每个 Agent 的运行协调器，负责接收事件、创建 Run、处理并发、顺序和生命周期。
- `Main Agent Loop` 是面向用户的主推理循环，负责理解对话、做决策、调用工具、解释结果和生成回复。
- 两者不能合并：Actor 管运行秩序，Main Agent Loop 管智能行为。
- 用户可见消息只能由 Main Agent Loop 产生。

<a id="contract-22-主循环与-worker-循环"></a>

#### 2.2 主循环与 Worker 循环

- 当前对话由 Main Agent Loop 负责。
- 实现层只有一套可复用 Agent Loop 内核。`Main` 与 `Worker` 是两种运行角色；`Root Worker` 与 `Child Worker` 又是 Worker Run 在 Run 树中的两种位置，不实现第三套 Child Agent Loop。
- 多步、耗时、批量、文件、命令和浏览器/桌面工作由 Main 在执行前显式创建或接续 Task，并启动 Root Worker Run；`agent.run()` 和 Runtime 不会将已有调用自动“升级”为 Task。
- Main Run 不创建 Child Run，也不获得原生文件/命令或 Computer 权限。Main 可直接推理，并通过 Code Mode 使用控制/记忆/证据/Skill 工具、调用已配置 MCP 完成聚焦查询和单步变更，交付成果；具体白名单见[统一工具方案](tools-approvals.md)。Root 从完整 Registry 独立派生执行能力，不能从已收窄的 Main 工具集继承。
- 这一限制是当前产品的默认 Capability Policy，不写死为 Agent Loop 内核限制；未来若需要允许 Main 直接创建 Child，只调整工具暴露与 Parent 校验，不重构 Run、ToolInvocation 或 Worker Loop。
- 同一 Task 同时最多只有一个活动 Root Worker Run；任务内部的并发统一由该 Run 的 Child Run 表达。不同 Task 的 Root Worker Run 可以并行。
- Root Worker Run 拥有独立上下文，可以与 Main Agent Loop 及其他 Task 的 Worker 并行运行。
- Worker 不直接向用户发消息。
- Worker 的完成、失败和异常都以事件返回 Main Agent Loop，由 Main Agent Loop 结合当前对话、人格和渠道决定如何告知用户。
- 即时 Run 通过当前对话自然呈现，不额外作为用户界面中的任务项目展示。

<a id="contract-15-明确不做的过度设计"></a>

### 15. 明确不做的过度设计

- 一期不实现多模型动态路由。
- 一期不实现 Memory/Conversation Provider 热切换与双写。
- 不把 Run、Task、Instance 等内部状态完整暴露给用户。
- 不做全局事件顺序。
- 不让 Worker 直接给用户发消息。
- 不让模型直接写数据库或绕过安全层产生副作用。
- 不把完整工具轨迹写入 Memory。
- 不因为参考 Coding Agent 而引入 Tree/Lane 等当前产品不需要的概念。
- `agent.*` 作为统一 Code Mode Host 能力按 Capability Policy 暴露；Main 默认不注入，允许委派的 Worker 只获得 `agent.run()`。Host 必须继续强制执行创建权限、深度、并发、安全上限和工具范围；不向模型暴露 Capability Snapshot 或预算监控接口。
- 一期不建立 Workflow 领域模型；Child Run 允许递归委派但必须受宿主深度、后代总数、调用次数、并发和高安全配额限制，也不提供任意文件系统、网络、动态 import 或不可重放的时间/随机能力。
- 不为 EventQueue 自研复杂基础设施。

<a id="contract-19-后置决策事项"></a>

### 19. 后置决策事项

以下选择不阻塞 Coding Agent 按 M0–M5 开工；实现必须放在接口后，不得把某个临时选择泄漏到领域协议：

1. Embedding 与 SQLite 向量实现采用：显式配置 OpenAI-compatible embedding endpoint/model/dimensions，SecretStore 引用凭据；SQLite 保存 dense vectors，私有 adapter 在用户/Agent/时间分区内执行 exact cosine，并与 FTS5/BM25 并行后 RRF。无新增 native extension，不声称 ANN 或次线性性能。未配置 embedding 时不提供召回能力，不将 FTS-only 视作完整向量交付。实现与离线重建限制见 `docs/designs/memory-context.md`；模型质量留给 release-time Live Shadow。
2. Web 框架、TUI 框架、飞书 SDK 与视觉实现；TUI 只需支持首次模型设置、首条纵向链路和审批。
3. Context Compaction 的最终阈值与 Token 分配；算法和配置点先实现，数值通过用户选择的模型压测冻结。
4. 各具体 Tool 的风险映射、范围授权文案与审批 UI；未知副作用 Tool 默认要求审批且 `replay_policy=never`。
5. Artifact 的最终保留周期、配额与清理批次；MVP 必须先有可配置默认值、引用计数/作用域检查和 dry-run 清理。
6. 公平调度权重、NATS Stream/Consumer/DLQ、租约时长、退避与 watchdog 的生产参数；先使用保守可配置默认值并记录指标。
   运维参数见[运维说明](../features/operations.md)：事件 Stream 默认 7 天/1 GiB/100 万条/单条 1 MiB，DLQ 仅保留无正文的诊断 receipt，5 次毒消息阈值，32 个未确认消费窗口。远程 NATS 必须 TLS + SecretStore token 引用，既有无界 Stream 通过显式、无立即丢弃的运维命令升级。三个数据库和 Artifact 使用独占冷备、认证加密清单及新目录原子还原；凭据独立保管，还原先禁用，确认源实例退役及外部结果后显式启用。正常关闭不改变 Task 状态；备份不承诺外部副作用 exactly-once。
7. 高安全上限的最终容量校准、成本估算与可复用脚本管理界面；模型仍不获得预算查询工具。
8. 金融交易与其他受监管能力的合规设计；未单独评审前不得启用真实交易 Tool。
9. 云端服务拆分、租户隔离、多 Agent 创建体验和迁移；一期保持模块化单体与单 Agent 产品面。

若后置选择影响模型可见协议、数据兼容性、安全边界或 Durable Replay，必须重新评审、更新所属设计、有效取舍和兼容方案；仅替换 Adapter 或调参不需要改变领域模型。
