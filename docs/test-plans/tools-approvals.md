# 统一工具执行与审批：测试方案

[功能与验收目标](../features/tools-approvals.md) · [技术方案](../designs/tools-approvals.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 范围与证据

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-007](../features/tools-approvals.md#f-007) | 验证拒绝/过期/失效审批不 dispatch、取消 fencing、崩溃窗口及 query-only 恢复。 | 保护已接通的限定范围；Live/配置另行验证 |

## 层级与关键路径

覆盖批准前/后及 dispatch 前/后中断、Steering 失效审批、可信 query、过期/冲突决定和 unknown 保留。断言外部效果次数与内部回执身份。

`TestMainToolsAllowConfiguredBusinessAndExcludeWorkerExecution` 验证 Main 获得 Skill 与 Host 配置的读/写 MCP，未知 builtin、非业务权限 MCP、原生执行工具及内部桥接仍被排除。`TestMainAndWorkerUseOnlyCodeModeWithinTheirFrozenRole` 通过真实 SQLite/Actor/QuickJS 验证模型仅有 Code Mode 入口、Main 脚本创建 Task、Worker 脚本调用自身工具，以及顶层绕过在参数校验前返回 NOT_FOUND。`TestMainPendingApprovalAcceptsSteeringWithoutDispatch` 验证新 Main 的未执行审批遇到纠正后失效且零派发。LegacyMain 回归保留旧冻结能力的费用等待及 pending 结果恢复，不热更新权限。

CLI capabilities 的 `main-business` 场景通过真实 stdio MCP 验证 Main 查询与 Skill 读取不创建 Worker、拒绝写入零派发、批准后仅执行一次；持有慢 MCP 时在释放屏障前先回复新输入，之后交付原结果，调用 attempt 仍为一。该场景记录观察延迟，不将宿主同步窗口视为端到端响应 SLA。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 已有检查与缺失覆盖

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_tools.py](../../tests/system/verify_tools.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [tests/system/verify_delivery.py](../../tests/system/verify_delivery.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |

现有入口仍需在后续相关实现变更时按风险运行；需求范围变化时同步扩展场景。

## 详细测试入口与证明边界

工具命名回归：`TestNamespacePreservesSingleUnderscoresAndSourceBoundaries` 验证 builtin 的工具名前缀和 MCP server 的精确匹配；`TestMCPWireNameKeepsServerAndToolUnderscores` 通过真实 MCP stdio 发现 `mcp__browser_use__click_button`，并拒绝含结构分界符的 server/tool 名。`TestLegacyBuiltinSnapshotIsRejectedWithoutAliasesOrMutation` 验证旧名不成为别名、冻结快照不被改写且失败为 `MODEL_INCOMPATIBLE`。Child 与 Code Mode 的 SQLite/CLI 回归继续验证新名下的权限隔离、终态和恢复。

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="tools-deterministic-evidence"></a>

### Deterministic evidence

The system runner selects `tests/system/verify_tools.py` for Tool regression. Each run launches isolated
real CLI processes with explicit `-test-bridge approval`/`approval-never` fixtures,
proves setup readiness, drives decisions and steering, kills Go at four execution
boundaries for both unsafe strategies, injects an oversized external receipt after
a Worker effect commit/SIGKILL and later reconciles the original call, reads back
invocation/approval/Outbox and a separate Fake external ledger, then joins children
and removes temporary data.
Reports are `evidence/tools-{1..5}.json`. The fixture tool is unavailable without
explicit test injection. The Fake ledger is not a domain database and has no
access to Run/Task state.

Focused Go tests cover expiry/denial, wrong principal/hash, duplicate decisions,
argument drift guards, unsupported schema rejection, resource limits/conflicts,
independent out-of-order completion, unknown reconciliation and migration
rollback. `go test -race ./...` covers their concurrent boundaries. These are
SC-06/07 and GC-11/12/13 approval/recovery **fragments**; full real purchase,
calendar and booking quality requires separate Live evidence. [Artifact handling](../features/artifacts-media.md)
and [channel delivery](../features/channels.md) have their own maintained harnesses; excess
inline output still fails `RESOURCE_LIMIT`. These Tool fragments alone do not
establish those capabilities or full Golden Case scores.

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

## 渐进工具发现与 500 工具目录

`TestMCPConfigurationHasOneInvocationPath` 验证 MCP 无模式配置，并拒绝过时的 exposure 与单工具覆盖字段。`TestMCPDiscovers500ToolsWithoutChangingExecutionIdentity` 使用真实 stdio peer 验证 500 工具发现、展示变更不重绑执行资源及旧直接声明快照恢复。

`TestLegacyMainLoadsOneOf500ToolsAndWorkerUsesCodeModeWithoutLoading` 通过实际 Actor/SQLite/QuickJS 验证旧冻结面的 Main 搜索加载与 Worker 脚本调用。`TestLoadedToolSurvivesApprovalWaitAndPresentationChange` 验证旧冻结面等待、重建适配器和配置变化后仍只执行原批准动作一次。`TestLoadedDeclarationsRemainSeparateFromAuthorityAndWorkingState` 验证历史加载状态不随语义压缩丢失，外部结果不能激活工具；隐藏、内部及范围外工具不能加载。`TestMainToolSearchRespondsWithFullToolCapacity` 保护历史发现入口在满载时的新输入响应。新 Run 的统一入口由下述 Code Mode-only 场景验证。

CLI capabilities 的 `tool-loading` 场景在真实 stdio MCP 的 500 工具目录中验证无原生 MCP 声明、默认 8 条描述匹配、直接绕过拒绝，以及 Worker 通过 Code Mode 调用全部允许工具。该场景用 16K 测试窗口容纳显式历史工具面的固定控制声明及多轮结果；全量 500 个 Schema 不进入模型请求。实际模型的检索与任务分流质量仍需独立评测。

记忆维护综合场景使用 16K 模型窗口容纳新增控制/发现 Schema 和受保护记忆结果；24 条长历史合计超过该窗口，继续断言真实摘要执行、私有历史 Artifact 和原始消息保留。独立的小窗口拒绝/压缩 Go 回归保持原参数。

### 高频直接工具与冻结兼容

`TestProductionToolSurfaceKeepsOnlyFrequentControlsDirect` 检查实际生产 registry：Code Mode、Tool search、UI、Skill 直接声明；Main 另有 Task 管理和查询，Worker 不能越权。伪造 loaded checkpoint 不得扩大声明，缺少 JavaScript 运行时仍只保留高频控制工具而不开放原生执行。其余 Code Mode-only 组件场景继续验证显式冻结面与恢复兼容。

显式 Code Mode-only 冻结面的 Main/Worker 组件场景验证模型只声明 Code Mode、Main 脚本能创建 Task、Worker 脚本只能看到自身冻结权限，以及伪造顶层普通工具调用在 schema 校验前被拒绝。真实 500 工具 stdio 场景检查无原生 MCP 声明、脚本搜索默认 8 条、服务描述可作为独立搜索入口、全部 500 个允许工具可通过 Code Mode 发现且直接绕过失败；Main 与 Worker 的实际请求均不含 MCP Server 目录或其描述。`TestAgentStartsWithOneFrozenSystemMessage` 与完整消息字节快照同时覆盖 Main、Root、Child 的无目录系统上下文。原生模型系统回归检查实际 HTTP tools 数组包含 Code Mode 与角色允许的固定高频工具，并从数据库独立核对被隐藏的角色权限仍存在。旧冻结面和加载 checkpoint 的回归用于证明恢复兼容，不代表新增暴露模式。

PR 的 `Ubuntu checks` 显式运行 capabilities 的 `tool-loading` 与 `main-business` 两个场景，使用真实 MCP/QuickJS/SQLite 验证目录规模、具体动作审批与慢工具挂起期间的 Main 响应，并保留失败证据。完整回归仍覆盖相同场景；不需要真实模型账号。

Computer 动作参数的原 hash／解析选项、批准前固化与不重算、失权及存储故障、旧审批迁移由[Computer 回归](computer-use.md)覆盖；普通原生审批与其他工具的协议继续运行原门禁。Web 使用真实有值投影检查手机触控、明暗与无脚本，不改决定／执行流程。

`TestToolUnknownReconciliationUsesOnlyOriginalQueryableResource` 经真实审批与 unknown 回执结束 Run，再改变无关能力，分别验证无 Query／原 Query 确认成功／原 Query 仍未知／目标资源漂移／目标移除。无关能力不得阻断扫描；原目标指纹改变不得调用 Query，原参数和幂等键保持冻结，不重发或复活 Run。该入口随 required `mcp-plugins` race 执行，实际默认二十二包审批／unknown 冷恢复另经 MCP 专项系统链验证。

`TestToolUnknownUnfrozenOperationCannotAcquireNewQueryAuthority` 验证能力快照以外的未知动作在工具仍不可用、后注册 Query 或后注册无 Query 定义时都保持未知；不调用新定义、不重发、不复活 Run。原 `TestTaskControlCancelTreeAndInvocationOutcomes` 的 once／ongoing 取消树与继续语义同时保留，并进入 required MCP 恢复 race。

### 测试商户的单次付款

参考 Cue 的预算与回执流程。所有者先读取已报价订单，批准固定商户、订单、币种与按最小单位计的上限。MCP 插件门禁覆盖批准前未付款、批准后精确成交、截断付款应答后冷恢复只查原单。使用独立商户接收日志核对每单只有一次 `pay_order`，以账本核对查询所得回执。单元回归覆盖超额、错币种、错商户以及并发重复付款。

### 具名 Gmail 发信审批

参考 [Linger 邮件确认卡](https://github.com/simplexai-labs/linger/blob/77abdcb37f779f9c9d2de89fa245efa88d3f6584/apps/web/src/features/rooms/cards/decision-connector-write-view.tsx#L25-L182)。所有者在原审批中核对已授权账户、显式发件地址、全部收件目标（含密送）、附件、主题和正文，然后决定是否放行。跨账户混淆、未展开就漏看密送，以及把批准误作送达，都会影响这个决定。

| 检查 | 可观察结果 |
| --- | --- |
| `TestConnectedAppsPluginActualApprovalAndUnknownNeverRepeats` | 经真实插件冻结具名账户并建立 SQLite 审批；切换当前方法包，旧审批的账户、配方、参数和 hash 保持原值。批准前零次外部写入，应答遗失后冷恢复仍仅一次发送尝试。外部端点使用受控 HTTP peer。 |
| `TestMailApprovalRendersFrozenEnvelopeAndCompleteValues`、`TestMailApprovalKeepsOriginalViewForUnrecognizedOrAmbiguousCalls` | 本地 HTML 转义正文、保留完整附件引用和未知参数；已有草稿、重名键及无布局的历史审批使用原参数。 |
| `STEWARD_TEST_APP_BROWSER=1 go test ./internal/interaction/web -run TestApprovalParametersBrowser -count=1` | Chromium 在桌面与手机、明暗主题测量有值控件；核对 To/Cc/Bcc、实际表单 hash、键盘展开长正文、原生无脚本页面。设置 `STEWARD_APP_BROWSER_EVIDENCE` 可保存截图。 |
