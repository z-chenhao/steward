# 产品测试总方案

[需求主索引](../feature-map.md) · [开发与交付](../features/engineering.md) · [产品架构](../designs/product-overview.md)

本页承载统一的软件测试层级、命令、覆盖、失败处理与评测边界。各功能测试方案补充需求级场景；执行报告仍单独保存。

## 当前执行规则

日常/扩展/发布的检查调度以下方 Testing 和 CI 入口为准。协议验收矩阵描述必须保护的行为，不表示每次文档或局部改动都重新执行完整发布检查。

普通 Go 套件包含真实隔离插件编译、独立版本与历史兼容场景，`make test-go` 将 WebPlugin／MemoryInspector、UI、Schedule／Automation、Apps／ConnectedApp、Memory／Recall／Context 等记忆上下文组、Plugin／ToolDiscovery／Skill／MCP 等插件工具组与其余完整 Go 套件分成七组，所有根用例恰好执行一次；CI 将同一套命令分在 `go` 和 `go-domains`，并核对分区互斥与完整覆盖。每个 package 仍采用原 15 分钟累计 alarm，与既有 race runner 的整组上限一致，不放宽用例、Provider／RPC 或执行租约期限；CI 外层作业总预算见工程门禁说明。package 按单任务调度，避免并行编译／启动突发挤占原生 Driver 原有两秒发现窗口；不改变 Driver 的期限或语义。

SQLite race 先串行运行原生与受管 Delegation 的五秒审批／Drain 契约，再将自动发现的其余测试按既有测量分区、每个进程最多 8 个根用例分批执行。SQLite 同时最多三个单核进程，保留编译余量；各批仍使用原有 race/count/累计 alarm，Memory 的十分钟上限也维持原值。验收必须核对全局发现清单，每个根用例恰好完成一次，后续批次缺失或重复同样失败；复用 evidence 目录前清除全部旧 SQLite 批日志。


## 详细测试入口与证明边界

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="contract-18-测试故障注入与-definition-of-done"></a>

### 18. 测试、故障注入与 Definition of Done

<a id="contract-181-必须自动化的测试层级"></a>

#### 18.1 必须自动化的测试层级

- 单元测试：所有状态机合法/非法迁移、Capability 派生、Context 裁剪、错误映射、冲突键与重放策略。
- 契约测试：Model Provider Bridge、OAuth UI 事件、LLM Adapter、Tool Provider、Sidecar RPC、ComputerProvider、ArtifactStore、EventBus 和渠道 Adapter。
- 集成测试：真实 SQLite + NATS；数据库可以临时目录运行，测试结束不依赖手工清理。
- 属性/竞态测试：重复事件、乱序完成、并发 append/cancel、Claim 接管、旧 epoch 写入、同 `call_key` 参数漂移。
- 端到端测试：Fake LLM 驱动完整 TUI → Main → Task → Root → Child → Main → Delivery 链路。
- 故障注入：在“事务提交前/后、Outbox 发布前/后、Tool 副作用前/后、结果持久化前/后、Sidecar Host Call 前/后”杀进程。

Go 测试必须运行 race detector；Sidecar 必须包含协议 fixture、资源上限、Promise 挂起、未 await、取消和非法 Host Call 测试。所有外部依赖都应有确定性 Fake，不能用线上账号作为 CI 前提。

<a id="contract-182-核心验收矩阵"></a>

#### 18.2 核心验收矩阵

| 主题 | 必须证明 |
|---|---|
| 首次启动 | 无 Active Profile 时只能进入 Setup；本地 URL 与 ChatGPT 登录都能创建并激活通过 Probe 的 Profile |
| 模型快照 | Profile 切换不改变活动 Run；Root/Child 树固定使用同一 ModelSnapshot；Durable Replay 拒绝模型漂移 |
| 登录刷新 | OAuth state/PKCE、防重复回调、并发单飞刷新、过期重登和 Logout 均不泄露或残留 Secret |
| Provider 隔离 | Provider Bridge 无 Tool/Computer/数据库权限；Code Mode Sidecar 无模型凭据或任意网络 |
| 单 Main | 并发消息只注入一个活动 Main Run，用户 Steering 优先于 Worker Follow-up |
| Task 生命周期 | create/append/pause/cancel/complete 与四状态一致；失败 Run 不产生 `failed` Task |
| Root/Child | Main 不可直接创建 Child；`agent.run()` 永不创建 Task；Child 只回直接 Parent |
| 权限 | Child 不获得用户交付、Task 管理、凭据管理或 Parent 没有的能力 |
| 并发 | 100/1000 项在高上限内透明排队，用户无并发询问风暴 |
| Durable | 崩溃后固定源码重放，已完成调用零重复，未完成调用重新附着或继续 |
| 副作用 | `safe/query_before_retry/never` 三种策略分别通过故障注入；不确定结果进入 `unknown` |
| 取消与暂停 | 取消递归传播；暂停受控落盘；迟到事件因 epoch/fencing 被拒绝 |
| Approval | 冻结参数跨重启不变；批准仅执行原调用；拒绝不会调用 Provider |
| Artifact | 大结果不进入消息历史；引用越权、过期、篡改和路径穿越均被拒绝 |
| Delivery | AssistantMessage 与 DeliveryRequest 原子建立，渠道重试不重复发送 |
| 安全 | API Key、access/refresh token 不出现在 SQLite、YAML、Artifact、Trace、错误或日志；模型无法读取 CredentialRef 或伪造内部 ID 扩权 |

<a id="contract-183-完成定义"></a>

#### 18.3 完成定义

一个里程碑只有同时满足以下条件才算完成：

1. 代码、migration、配置样例、Fake、测试和最小运维说明均已提交。
2. 新增模型可见 Tool 有稳定 Schema、权限矩阵、错误码、重放策略和契约测试。
3. 新增状态有合法迁移表、并发/幂等策略、恢复扫描和故障注入测试。
4. `go test -race ./...`、Sidecar 测试、静态检查与端到端测试全部通过。
5. 至少完成一次“运行中 kill -9 → 重启 → 自动恢复”的演示，并保存可审计结果。
6. 文档与实现没有新的未标注差异；必要偏差更新到所属技术方案的有效取舍与约束中，并更新当前架构/流程后才合并。

<a id="contract-184-软件回归与-agent-评测边界"></a>

#### 18.4 软件回归与 Agent 评测边界

产品仓库维护确定性的单元、集成、端到端和故障恢复测试；CI 是运行这些检查的自动化机制。日常 PR 执行核心软件回归，完整软件回归和 race 检查独立定时或手动执行，发布前按风险补齐相关验证。安全、审批、取消、Durable Replay、工作区保护和数据完整性要求不因调度拆分而降低。

完整 Golden Case 题库、真实模型运行器、评分规则和结果归独立评测项目管理，不再作为每次代码提交的 CI 门禁，也不在产品仓库维护逐段条款哈希审计。评测必须通过普通产品接口，记录产品版本、模型和配置。固定回复或预写 Tool 计划只能证明软件机制，不能证明模型效果。真实账号和 macOS Computer 兼容性仍需显式 Live 验证。

当前执行入口和覆盖范围见 [测试治理](#testing-test-levels) 与 [CI](../designs/engineering.md#ci-continuous-integration-and-delivery)。

<a id="verification-product-verification"></a>

## Product verification

Use the [testing guide](#testing-test-levels) for test levels, ownership and the current
coverage map. [CI](../designs/engineering.md#ci-continuous-integration-and-delivery) describes which checks run on PRs, weekly runs and
releases. Actual commands live in Makefile and the system runner.

```sh
make test lint
make test-e2e E2E_EVIDENCE=evidence/my-change
python3 tests/system/runner.py --list --suite full
python3 tests/system/runner.py --gate tools --gate children --repeat 5 --evidence evidence/focused-change
make test-full E2E_EVIDENCE=evidence/full-change
make test-race
```

Keep failed outputs until diagnosis is complete, use a fresh directory for a
rerun and preserve the command's exit status. A PID, open port, empty test run
or valid report shape alone does not prove a user flow. Inspect state and outputs
through the maintained tests; do not point them at user data.

Fake peers verify software contracts. Real model behavior, actual account login
and native macOS Computer compatibility require explicit live checks. The full
Golden benchmark and scoring run outside this repository.

<a id="testing-testing-and-engineering-boundaries"></a>

## Testing and engineering boundaries

Tests protect product behavior. Scripts provide a small set of entry points;
they are not a separate product roadmap. CI schedules the checks. Delivery builds
and publishes artifacts under explicit release authorization.

<a id="testing-test-levels"></a>

### Test levels

| Layer | What it proves | Ownership and execution |
| --- | --- | --- |
| Unit | Local rules, parsing, validation, state transitions | Go/Node tests beside code; fast focused runs during editing |
| Component/integration | Real SQLite, adapter contracts, Sidecar RPC, concurrency | Existing Go/Node package tests; PR checks and relevant race checks |
| System/E2E | Actual UI/CLI → Main/Task/Worker/Tool → durable result, failure and recovery | `tests/system/`; core PR checks, extended weekly/manual checks |
| Test-support regression | Process cleanup, setup integrity and small shared test helpers | `tests/harness/`; PR checks; no historical CI topology or audit-ledger tests |
| Agent evaluation | A real model completes an actual user goal, with quality, cost and reliability measured | Independent evaluation repository and explicit low-frequency runs |

A Fake or deterministic HTTP peer is appropriate for checking a protocol or
recovery contract. A hard-coded answer or scripted Tool plan cannot prove Agent
quality. Existing Go test packages mix local and integration coverage; that mix
is stated rather than hidden behind a `test-unit` alias.

<a id="testing-commands"></a>

### Commands

- `make test`: Go, Node, real-browser and retained infrastructure regressions.
- `make lint`: documentation links, shell syntax, Go checks and TypeScript.
- `make test-e2e`: core deterministic user flows, once each.
- `make test-full`: all retained product system flows, once each.
- `make test-race`: the complete race-instrumented Go inventory with bounded scheduling.
- `python3 tests/system/runner.py --gate tools --repeat 5 --evidence evidence/tool-stability`: explicit repeated/focused regression when warranted.

Use a built `bin/steward` and a fresh evidence directory for system runs. Every gate
must complete, produce a fresh report and drain owned processes. Cancellation,
launcher failure, missing reports or source/binary drift reject success. Local
loopback peers, disposable databases and pinned runtimes make the checks account-free.
Test output stays in ignored `evidence/`; credentials and personal data stay out.

The core account gate runs the account harness against private credential files
in a disposable data root. It checks permissions, cross-process refresh and cleanup
without a desktop credential service. Commands and proof limits are
in the [model connection test plan](model-connections.md#私有文件账户回归).

<a id="testing-system-coverage"></a>

### System coverage

| Core gate | Product contract |
| --- | --- |
| `startup` | Source/native installation, real terminal setup, Tool use, broker and crash/restart |
| `main` | Single Main, steering, Tool continuation and durable response |
| `tasks` | Main-to-Task/Worker handoff, pause/append/cancel and recovery |
| `children` | Recursive delegation, frozen capabilities, parent-only results and approvals |
| `tools` | Frozen approved actions, invocation persistence and unknown side effects |
| `code-recovery` | Actual Go/QuickJS interruption, 37 completed/8 waiting calls, no repeated completed work |
| `intelligent-ui` | Main-only UI tool, frozen Skill, native and sandboxed widgets, explicit follow-up and restart |
| `ui-interactions` | Shared native controls, validated forms, durable state and tool actions, display modes, exports and restart. Its full Chromium interaction and export sequence has 90 seconds. The shorter restart check has 45 seconds. |
| `local-provider` | Discover/probe/select, transport and image projection with an owned HTTP peer |
| `account` | Deterministic auth/callback/refresh/logout contracts and secret handling |
| `channels` | Real Web/browser and channel delivery behavior |

Extended gates cover artifacts, memory, recall, automation, scheduling, spend,
capacity, operations, Computer, capabilities and additional terminal behavior.
They protect software mechanics and never assign semantic scores. Full inventory
and exact commands come from `tests/system/runner.py --list --suite full`.

<a id="critical-user-journeys"></a>

### 关键用户旅程

用同一个已构建的 `bin/steward` 和本次独立证据目录运行受影响的旅程。`--list --journey first-reply` 可查看旅程实际选择的门禁。`summary.json` 记录运行结果、源码和二进制身份，各门禁保留自己的报告与日志。

```sh
python3 tests/system/runner.py --binary bin/steward --journey first-reply \
  --evidence evidence/first-reply-my-change
```

| `--journey` | 从真实入口验证的行为 | 门禁和可核对结果 |
| --- | --- | --- |
| `first-reply` | 安装发行包、首次配置和对话、进程崩溃后继续。缺失随包发行的必需插件时，首次 Artifact 写入被拒绝 | `startup` 的真实 CLI、选中的组合、Artifact 和恢复后的最终回复 |
| `delegated-work` | 通过 Main 启动 Task、委派 Child、交付成果 | `main`、`tasks`、`children`、`artifacts` 的交接、子任务状态和可读产物 |
| `approved-effects` | 工具获批后执行，未知结果在恢复时保持原身份和尝试次数 | `tools` 的 `unknown_without_evidence_retained`，以及 `codemode`、`code-recovery` 的执行与恢复回执 |
| `coding-handoff` | 启用工作区、隔离候选、核对审阅报告与已观察的 PR 提交 | `capabilities` 的 `task-code-environments`、`task-code-recovery`、`task-code-exact-report` |
| `model-recovery` | 换版、回退模型插件后直接在 Chat 发送，原模型收到请求且回复送达 | `model-menu-plugins` 的 `recovery_send`、首个交互截图和请求观察 |
| `forget` | 冻结请求后执行 Forget，原进程中断，再发送新消息。新请求不含撤销内容，原回执保留 | `memory-maintenance` 的 `forget-fences-frozen-recovery` 和 `recall` 的真实检索结果 |

功能维护时重新走 affected gate，并把发现归为文档过时、验证驱动缺口或产品回归。行为预期的变化需要由所有者确认，修复后的驱动应重新执行。

<a id="testing-agent-evaluation"></a>

### Agent evaluation

The full Golden benchmark, real-model drivers, rubrics, Judge integration and
large run records belong in a separate evaluation repository. Version those
assets there. Bind a run to the product commit or binary digest, model/version,
configuration and evaluation-set version. Keep raw traces and results as run
artifacts. Do not copy them into this product checkout on every code submission.

When comparing workflow or harness variants, give both the same task, model,
budget and starting state. Score observable results using shared held-out
criteria and hide variant labels during review. Report completion, interventions,
time and observed usage independently.

This repository retains only synthetic software-test fixtures needed by actual
regressions. Tool schemas, CLI/API behavior and ordinary user controls are the
integration surface for external evaluators. Do not give an evaluator a separate
Agent Loop or privileged production bypass.

The former Golden/audit implementation is recoverable from Git at
`006ac326e7db279f916f066334bfddd26ac33735`. A verified export was also retained
outside the checkout during this migration. That export preserves the original
assets; it is not a newly deployed or independently validated evaluation service.

The product repo makes no Agent-quality or whole-benchmark pass claim. Quality
thresholds and benchmark governance are owned by the evaluation project. Software
safety requirements still apply to every product change and release.

<a id="testing-keep-the-boundaries-small"></a>

### Keep the boundaries small

Operational commands belong in `scripts/`, software tests in `tests/`, product
logic in its owning module. Add a helper only for a real repeated operation;
extend an existing test for a product regression. Do not recreate the retired
Multica task hierarchy, per-clause source-hash ledgers, CI sharding/join framework
or a parallel benchmark platform inside the product repository.

冷启动 Host 就绪与运行中工具期限分别验收。`ChannelHarness.wait_runtime_ready` 对实际标准组合的独立初次验证使用有限60秒等待，仅轮询原 `/api/status`；不补 select／update／reload，不改变单例沙箱、工具十秒预算或 Run 租约。原推进／效果边界仍使用自己的等待限制；未就绪不能接纳输入的负例继续保留。

服务重启插件迁移：`TestServiceWatchdogActualProducerUpgradeRollbackDeactivationAndCold` 使用真实隔离插件与 App 选择路径验证独立配方、精确回退和停用后的首次冷读；`internal/operations/service` 的 `TestRestartRecipe*` 验证真实关闭／重开后的首个 Inspect、冻结部署次数／退避、旧会话围栏及不可用时无原生重启。最终交付仍须统一 test/lint 与适用系统门禁，不以编译或 manifest 数量代替实际调度证据。
