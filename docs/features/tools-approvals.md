# 统一工具执行与审批

统一校验、执行和恢复工具调用，冻结具体外部动作的审批。

[需求主索引](../feature-map.md) · [技术方案](../designs/tools-approvals.md) · [测试方案](../test-plans/tools-approvals.md)

## 功能说明

用户能确认具体外部动作；重复请求或重启不能盲目重复副作用。

本文描述用户行为和范围；逐项完成状态以主索引为准。当前能力依据代码静态盘点，配置可用性与真实服务/模型质量需分别验证。

## 需求与验收

<a id="f-007"></a>

### F-007 · 工具、审批与未知副作用处理

**需求**：用户能确认具体外部动作；重复请求或重启不能盲目重复副作用。

**当前实现**：新生产 Run 的模型工具入口统一为 `builtin__codemode_execute`。Main 与 Worker 在脚本中使用各自冻结的工具目录；具体调用继续共用 ToolExecutor、精确审批和 unknown 恢复。Main 可执行受限控制、Skill 读取及已配置 MCP；文件、命令、Computer 和 Child 仍按 Worker 角色授权。

**边界与剩余工作**：审批只约束具体冻结动作；无法可信核实的 unknown 必须保留，不承诺外部 exactly-once。

**验收目标**：验证拒绝/过期/失效审批不 dispatch、取消 fencing、崩溃窗口及 query-only 恢复。

## 如何使用

MCP 只配置 `tools` 权限允许列表，不再配置 `exposure` 或 `tool_exposure`。所有工具经 Code Mode 调用；system 不预载 MCP Server 目录，使用 `searchTools` 按工具或服务的名称、用途检索，再用 `describeTool` 读取所需参数，默认最多 8 个搜索结果，不会把目录 Schema 加入下一轮模型声明。删除旧配置中的模式字段后再加载；校验会拒绝过时字段，避免静默改变工具可见性。配置示例见[集成方案](../designs/integrations.md#mcp-工具暴露)。

工具名使用 `builtin__task_search`、`builtin__artifact_manage` 这类完整 snake_case 名称；MCP 使用 `mcp__browser_use__click_button`，保留 server 与工具名内部的单下划线。旧名称不作别名，升级前需完成旧 Main 和未决外部效果核实；具体兼容边界见[命名规范与升级说明](../designs/tools-approvals.md#contract-61-工具命名规范)。

在终端或 Web 查看待审批动作及最终参数，明确批准或拒绝。CLI 使用 `steward approval list` 和 `steward approval approve APPROVAL_ID FROZEN_ARGUMENTS_HASH`。

`steward approval reconcile` 核实未知结果。修改要求后未执行的旧审批会失效；批准 Task 不代表授权全部子动作。

## 详细操作参考

- [工具与审批操作](#tools-tool-execution-and-approval)
- [交付与回执协议](../designs/channels.md)

## 操作说明与配置细节

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="tools-tool-execution-and-approval"></a>

## Tool execution and approval

Every Direct Tool enters `tool.Executor`: persisted provider response → frozen
admission → input validation → security/approval → bounded dispatch → normalized
result commit. MCP stdio, progressive Skills and the controlled coding fixture
use this path; see [capability operations](../designs/integrations.md). [Code Mode](../designs/codemode.md),
[Child Runs](../designs/delegation.md) and the configured [Computer adapter](../designs/computer-use.md) use
the same pipeline. No implicit Fake is enabled.

`approval list` shows the local user's approval requests, complete final arguments,
frozen hash, status and expiry. To decide the displayed action:

```sh
steward approval list
steward approval approve APPROVAL_ID FROZEN_ARGUMENTS_HASH
# Or: steward approval deny APPROVAL_ID FROZEN_ARGUMENTS_HASH
steward chat work
steward approval reconcile
```

These are local user control commands. The principal comes from the private data
root's Agent identity; neither a model nor tool arguments can select it.
[Web/Feishu channels](channels.md) authenticate the principal before calling the
same decision boundary. IDs, hashes and provider keys do not enter model-visible tool schemas.
Approval is bound to one invocation, principal and request hash. A matching
callback can repeat after completion; a conflicting decision fails. Expiry,
denial and steering fail closed. New Main input invalidates every unexecuted
approval in that Main Run; Task append does the same for its active Root. This
includes approval granted before dispatch. A changed action requires a new
invocation and approval. Already dispatched effects cannot be revoked by steering.

| Invocation transition | Meaning |
| --- | --- |
| queued → running | Frozen arguments, policy, key and accepted Outbox event already committed; dispatch checks approval and resources atomically |
| queued/running → waiting(approval/resource/retry_backoff) | Persisted wake condition; executor and Run workers are released |
| waiting → running | Approval decision/expiry, available capacity, or due transient retry |
| running → succeeded/failed | Independent normalized result committed before end event; final batch delivery retains original call order |
| running → unknown | No trustworthy outcome for an unsafe dispatched call |
| unknown → succeeded/failed | Trusted provider query supplies authoritative outcome; evidence recorded with result |
| queued/waiting/running → cancelled/unknown | Controlled Task stop joins or fences owners; unexecuted calls cancel, unsafe dispatched calls stay unknown |

`unknown` is never dispatched again, even when a query later says absent.
`approval reconcile` queries unknown calls by their original key and may settle
only authoritative success/failure. It does not revive Runs, fabricate a new
approval, or send another action. The scheduler also runs this query-only scan. It checks only the queried tool against its original frozen resource. Calls without a query or original capability remain unknown; a later tool registration cannot acquire query authority over them. Unrelated plugin changes do not block this scan. The CLI prints each unknown or
reconciled action with its observed status and safe result/receipt.
A Root with any unresolved effect in its current tree reports `unknown` and keeps
its Task active. A previously delivered unknown message is a historical observation; reconciliation
does not rewrite Conversation history or claim a new user delivery.
An oversized, inconsistent or nonterminal recovery query result is inconclusive.
The executor persists `unknown` before delivering `UNKNOWN_SIDE_EFFECT` to the
Worker, so the call remains available for later authoritative reconciliation.

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/tools-approvals.md)；已有检查、未覆盖场景和验收方法见 [测试方案](../test-plans/tools-approvals.md)。

高频 Tool search、Intelligent UI、Skill list/view 与 Main 的 Task 协调直接提供给模型；其余授权工具通过 Tool search 发现后经 Code Mode 调用，审批与恢复仍走同一工具管线。旧冻结执行不切换工具面。

Computer 实验参数方法的完整选择在原审批前固化；所有者控制投影可包含 action_parameters，原 arguments／hash、审批身份与未知效果不重放保持原协议。Web 和通知展示相关解析选项及 Playwright 超时，显式输入仍由 Host 复核，详见[Computer 设计](../designs/computer-use.md#审批前固化动作参数私有实验接入)。


Computer 实验导出审批同时展示已固化的名称／媒体类型，沿原 hash、决定 API 和共享控件；原文件授权与具体参数校验仍由 Host 执行。

### 本机测试商户

所有者可用 `scripts/sandbox_merchant.py` 试验一笔虚拟订单。建立私有目录，将脚本复制进去，用系统 Python 和专用账本创建报价：

```sh
python3 sandbox_merchant.py --ledger orders.db quote test-001 CNY 1299
```

在现有 MCP 配置中添加 `test_merchant`，工作目录指向该私有目录，命令使用受沙箱支持的系统 Python 绝对路径。参数依次为脚本绝对路径、`--ledger`、账本绝对路径、`serve`。将 `get_order` 配置为 `read_only`，将 `pay_order` 配置为 `external_effect`。使用已选定的 MCP 托管插件，先查询 `test-001`，再请求为该订单支付不超过 1500 分人民币。

审批绑定商户 `steward-test-merchant`、订单号、币种和上限（以最小货币单位计）。商户按预先录入的价格结算；超出上限或币种不同会拒绝。查询结果列出实付金额、上限及唯一回执。丢失付款应答时只用 `get_order` 查询原单；商户回执与 Host 的未知执行历史可分别核对。此试验使用本机 SQLite 记录虚拟订单，无银行卡或真实支付机构接入。
