# 后台任务与用户进度

持久保存工作目标，支持追加、暂停、继续、取消和阶段进度。

[需求主索引](../feature-map.md) · [技术方案](../designs/tasks.md) · [测试方案](../test-plans/tasks.md)

## 功能说明

用户能把耗时工作交给后台，之后通过对话追加、暂停、继续、取消或结束。

用户主动查看时能了解当前阶段、摘要与子任务完成情况。

本文描述用户行为和范围；逐项完成状态以主索引为准。当前能力依据代码静态盘点，配置可用性与真实服务/模型质量需分别验证。

## 需求与验收

<a id="f-004"></a>

### F-004 · Task 生命周期与继续工作

**需求**：用户能把耗时工作交给后台，通过对话及明确关联的渠道事件接续、暂停、继续、取消或结束；缺失信息或成果待审阅时，用户能定位问题或产物，回答或反馈后继续原任务。

**当前实现**：Task create/append/pause/cancel/complete、once/ongoing 和 Root 恢复已接通；已有 needs_input、冻结审批、Needs me 与关联交付记录。Main 可保存关联原 Task/源消息的信息问题或绑定确切 ArtifactRef 的成果审阅；所有者从 CLI、Web Task 详情或符合路由条件的个人飞书反馈卡回答，回答持久进入 Main 后由既有 Task 工具接续。

**边界与剩余工作**：Run 失败不新增 failed Task 状态；继续通常建立新 Root，不复活旧 Run。GitHub 精确 PR 可由本机显式关联到已有 Task，经 Main 接续；结构化人工参与和渠道交付按对应入口维护，具体要求如下：

- **渠道关联与接续**：F-042 的 `github bind` 将确切 PR 与当前 Task/epoch 关联，绑定后新观察交 Main 决定是否追加同一 Task；无新 Task、相似匹配或第三方动作授权。歧义、超范围和 unknown 交 Main 解释/询问；暂停、取消、人工 complete、Forget、解绑与监听停止不会被旧反馈恢复。once Root 自然完成仍允许已授权的同一关联接续；人工 complete 的当前代次需要用户显式 append 后才能重新关联。原话题交付由 F-020 维护。
- **信息回答与成果审阅**：CLI 与 Web 已接通问题/成果的具名待办、分页、提交去重和陈旧反馈拒绝；个人飞书复用同一记录和回应回执。成果审阅卡从可信工作台入口打开指定 Task 的确切成果，真实客户端与模型质量仍待验证。问题关联原 Task/消息，成果关联明确产物或修订，反馈经 Main 接续。信息回答、具体动作审批和成果审阅分别表达，不能相互替代；不强制所有任务等待人工验收，不从自由文本猜测阻塞，不引入通用人类工作流引擎。

**验收目标**：

- 状态转换、同 Task 单活动 Root、取消传播、旧 owner fencing 与结果回传正确。
- 飞书发起的工作在关联 PR 收到评审/CI 后接续原 Task 并回原渠道交付；重复和迟到事件不重复建任务或执行，越权、歧义、超范围及停止后的事件不绕过宿主检查。
- 用户可定位并回答当前问题、审阅对应成果；重启后待办仍在，多端/重复提交不重复接续；过期问题、旧成果反馈、暂停/取消与失效审批得到明确处理，审阅不转移为动作授权。

<a id="f-005"></a>

### F-005 · 执行中的有意义进度

**需求**：用户主动查看时能了解当前阶段、摘要与子任务完成情况。

**当前实现**：Root Worker 在阶段变化时随工具调用返回一句简短进度，Host 将完整响应的摘要与执行 checkpoint 原子提交。Web Tasks 与终端 `/status` 展示当前执行状态、最近阶段摘要及当前轮 Child 各状态数量和已结束/总数量。

**边界**：阶段摘要属于模型生成的展示文本，不是完成凭据。仅带有进度约定的新 Root 发布摘要；Child、最终业务结果、压缩摘要和未提交流式文本不发布。空白、包含控制字符、超过 512 UTF-8 字节或被截断的摘要保留上一条；模型未写摘要时仍显示真实执行状态。普通进度不发通知、不触发 Main 思考。

**验收目标**：真实长任务产生阶段摘要，UI 能读到更新；普通进度不触发 Main 推理或通知。

## 如何使用

在 Conversation 交代工作，随后说“补充这个要求”“暂停这件事”“继续之前的工作”或“取消”。Main 使用 Task 工具查找和控制工作。

Web 的 Tasks 和终端 `/status` 可查看总体状态与 Child 聚合。新建 Root 会在工作阶段变化时更新简短摘要。页面区分工作中、等待审批、等待子任务、暂停和本轮结束；持续工作的一轮结束不会被展示为整个 Task 已完成。`Last update` 保留最近摘要，不代表它仍是当前阶段。

Task 列表默认 Open（active/paused），还可查看 Needs me、Finished（completed/cancelled）或 All tasks，按标题/目标搜索并翻页，不限于最近 20 项。Needs me 是当前执行需要处理的展示筛选，不是第五种 Task 状态。

点击任务进入独立详情，查看完整目标、当前阶段、Child 聚合、待处理审批、最近 10 条关联 Main 交付和可访问附件；指令/里程碑按游标读取更早历史。Open original request 返回最初的对话消息；Continue in conversation 带可见任务上下文进入 Main，用户可移除。页面没有绕过 Main 的暂停/取消/重试按钮，也不把 Worker 原始结果当交付。

### 回答已保存的问题与审阅成果

Main 可通过 `task_manage` 的 `request_input` 记录具体问题，通过 `request_review` 记录至多 16 个确切 ArtifactRef 的成果审阅。它们是独立信息记录，不改变 Task 状态，不自动暂停执行，也不代表所有任务必须等待验收。Worker 仍只向 Main 汇报；用户可在对话中要求 Ghost 记录需要确认的问题。

Web 从 Tasks 首部的待办进入原 Task 详情回答/审阅，相关文件和源消息均有明确链接；过期或另一端处理后的本地草稿保留为不可提交参考。Web、CLI 与个人飞书反馈卡使用同一回答记录，首次接纳的渠道决定该条输入的回复路由。飞书卡的设置和可用范围见[渠道说明](channels.md#feishu-task-questions)。

所有者运行 `steward task feedbacks` 查看当前可处理待办，可加 `TASK_REF` 筛选。每页至多 50 条；响应的 `next` 非空时用 `steward task feedbacks "" BEFORE` 继续全局读取，或用 `steward task feedbacks TASK_REF BEFORE` 读取该任务下一页。已回答、替代、过期、停止、旧 epoch 或 Forget 前的问题不会进入列表。记录包含具体问题、原 Task、可用时的源消息 ID、成果引用与到期时间；用既有 `artifact describe/open` 检查确切成果。

```sh
printf '%s' '使用上一完整季度的数据。' | steward task respond feedback:OPAQUE_REF
steward chat work  # 未运行前台工作服务时，显式处理已接纳的回答
```

回答文本上限 16 KiB。重复提交同一记录与完全相同文本返回原接纳回执，不同的第二次回答得到冲突。回答与 Main 输入原子保存，回应本身不创建 Worker，也不批准外部动作。Main 若据此追加工作，保留 `feedbackRef` 因果；宿主重检确切 Task、epoch 和隐私边界。新的普通用户输入仍由 Main 理解其意图，宿主不推断自然语言授权。

每个 Task 只保留一个当前待办，新问题/审阅替代旧待办；默认七天到期。暂停、取消、Forget、新一轮工作或更新的成果审阅会使旧反馈失效；即使旧回答已入队，后续处理也不能借其恢复旧工作。用户需要通过新的普通对话明确继续。待办属于非阻塞信息，不替代审批，也不将“认可成果”转换成具体动作授权。

## 详细操作参考

- [任务操作详情](#tasks-task-delegation-and-root-recovery)
- [任务停止与准入模型](../designs/tasks.md)

## 操作说明与配置细节

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="tasks-task-delegation-and-root-recovery"></a>

## Task delegation and Root recovery

The M2 slices add Main-only Task discovery, create/append/pause/cancel/complete,
independent Root execution and durable Main follow-ups. Use a selected, probed
model through the existing TUI or `chat send` / `chat work` commands. There is
one shared Agent Loop and one ToolInvocation pipeline.

A self-contained delegation brief belongs in `task_manage(create).task`. Main
must include necessary background, constraints, completion criteria and expected
outputs. For continuing work, Main can explicitly create with
`completionPolicy: "ongoing"`; omission or `"once"` creates a one-shot Task.
The policy is fixed at creation. For example:

```json
{"operation":"create","task":"Keep the wedding checklist current as guest counts and dates change; preserve confirmed facts and return the next dependencies to resolve.","completionPolicy":"ongoing"}
```

The Host generates an opaque TaskRef and a title; the receipt means
accepted, not finished. Short synchronous questions remain in Main.

| Canonical tool | Input | Output | Visibility / permission | Recovery |
| --- | --- | --- | --- | --- |
| `builtin__task_search` v1 | Optional `query`, `scope`, `status[]`, `limit` | `TaskSummary[]` | Main, `read_only` | `safe`; bounded 64 KiB result |
| `builtin__task_manage` v1 | Discriminated `create/append/pause/cancel/complete` union | `{taskRef,status,accepted:true}` | Main, `task_control` | `safe`; transactional receipt dedup |

The exact schemas and strict validators live in `internal/task/task.go`.
Unknown fields, nulls, duplicate fields, wrong union arms, invalid limits and
unknown statuses return `INVALID_ARGUMENT`. Missing or inaccessible Task or
Artifact references return `NOT_FOUND`. Responses expose no Run/invocation IDs,
credentials, SQL, budgets or internal traces. Infrastructure write failures stop
at a recoverable boundary instead of becoming a misleading Tool success.

[Artifact validation](artifacts-media.md), [external effects and approvals](tools-approvals.md),
and [delivery](../designs/channels.md) use the shared Tool pipeline. Workers can use
[Direct Child delegation](../designs/delegation.md); [Code Mode](../designs/codemode.md) supplies
JavaScript and fixed-source Durable Replay with frozen snapshots.

<a id="tasks-controlled-stop-and-continuation"></a>

### Controlled stop and continuation

| Current Task | pause | cancel | append | complete |
| --- | --- | --- | --- | --- |
| active | paused, epoch +1 | cancelled, epoch +1 | Stable Steering if a Root is active; otherwise new Root, epoch +1 | completed if current epoch has no live Run or unresolved Tool; otherwise INVALID_ARGUMENT |
| paused | Same receipt state, no epoch change | cancelled, epoch +1 | active, new Root, epoch +1 | INVALID_ARGUMENT |
| completed | INVALID_ARGUMENT | INVALID_ARGUMENT | active, new Root, epoch +1 | INVALID_ARGUMENT |
| cancelled | INVALID_ARGUMENT | Same receipt state, no epoch change | active, new Root, epoch +1 | INVALID_ARGUMENT |

Stop acceptance atomically persists Task status/epoch, Run and invocation desired
state, receipt and Outbox. This immediately closes business-write and new-work
admission. The owner observes intent every 100 ms, cancels its safe provider/Tool
context, joins the current step, then settles the old Run as `cancelled` with
`task_paused` or `task_cancelled`. Already committed exchanges, Tool results,
instructions and progress remain available to the new Root as framed Task data.
An in-flight response arriving after acceptance is rejected, including its usage
write. Stop/progress events do not wake Main with a Worker business result.

Cancellation covers all current Task tree members and their invocations. The
scanner settles leaves before parents; live owners retain only lease/stop
authority, while expired or unclaimed work settles without a business claim.
Every claim-bound business write checks both ownership and the Task epoch.
An append racing stop waits outside the database transaction for the old tree
boundary, then atomically saves instruction/reactivation/new Root. The database
retains its one nonterminal Root constraint. A call interrupted during admission
remains recoverable under its frozen ToolInvocation and creates no partial append.
The private [bounded state model](../designs/tasks.md) maps these transactions,
records safety and recovery-suffix checks, and retains a fencing counterexample
with a real SQLite regression.

Only the same terminal Root's result is reported after its last descendant ends.
`completion_policy` defaults to `once`; explicit create `completionPolicy: "ongoing"`
keeps the Task active when one Root ends. `append` retains that policy. An idle
ongoing Task does not start another Root on its own: Main must append work, or
an existing [Automation](automation.md) must fire. Ordinary Root success is still reported once
to Main. Pause/cancel, unknown-effect handling and epoch fencing keep their
existing behavior.

Main can explicitly end idle active work with
`{"operation":"complete","taskRef":"<existing TaskRef>"}`. The Host rejects any
nonterminal Root/Child or queued/running/waiting/unknown ToolInvocation in the
current Task epoch with non-retryable `INVALID_ARGUMENT`. Complete does not cancel,
drain or wait. Paused work must be reactivated through append before completion;
completed/cancelled Tasks reject a fresh complete request. A repeated accepted
source or invocation returns its original receipt.

Completion stores status, timestamp and receipt atomically under Main ownership
and Task version/epoch fencing. It changes neither epoch nor completion policy,
and emits no Worker TaskUpdate or Main follow-up. Append can later reactivate
that completed Task with a new Root and epoch. See [ADR 006](../designs/tasks.md#decision-006-006--explicit-task-completion-policy).

No storage migration is needed: schema v9 already stores the immutable policy
and migrates prior Tasks to `once`. New Main Runs freeze the extended schema.
A pre-extension Main capability cannot run with the changed Tool Spec: registry
selection fails `MODEL_INCOMPATIBLE`. Finish that Run with its original binary
before upgrading; do not rewrite snapshots. Omitted and explicit once normalize
to the prior request shape, retaining receipt hashes. Completed Tool results
remain durable. Automation continues using its existing Host policy.
The original M2 tests retain stored topology and conservative unsafe-stop
fixtures. The M3 Tool harness verifies approvals and external reconciliation;
the [Child harness](../designs/delegation.md) exercises actual admission, recursive execution
and cancellation. No external exactly-once guarantee is made.

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/tasks.md)；已有检查、未覆盖场景和验收方法见 [测试方案](../test-plans/tasks.md)。

## 计划与配置视图

Tasks 首先显示已保存的安排（含首次触发前尚未生成 Task 的项目），再显示已派发任务。安排区提供创建／编辑，未结束与全部视图展示安排；搜索匹配真实指令。Task 详情依次展示原始目标、单次／持续完成规则、真实关联计划、已接收补充指令和当前执行情况；成果位于其后。没有持久化的完整未来步骤计划时，不把 Worker 历史冒充未来步骤。

原始目标和完成规则仍是不可变事实；没有自动安排的 Task 可以创建独立安排，已关联安排可手动编辑。安排配置的写入是独立的所有者 Automation 入口，不改变 Main-only Task 控制和冻结审批。

Tasks 分为「执行记录」和「任务配置」。前者每行是一轮真实 Root 执行，可按 Open／Needs me／Finished／All 筛选，并搜索标题或完整目标；后者独立展示任务原始配置和已保存的调度，未派发配置不出现在执行记录中。列表仅显示标题、类型、状态、创建与更新时间；描述和结果进入详情查看。旧轮次不会被新轮次覆盖，持续任务的完成规则与某轮执行完成保持独立。

新建任务支持独立短标题 title，完整要求仍保存在 task 简报；旧记录不重写，长标题在列表省略、详情完整显示。待审批、问题和结果审阅仍使用原 Task 详情；Chat 综合待办保留独立收件页。

Tasks 的执行记录与任务配置通过共享二级侧栏切换，执行详情／待办与配置详情／调度编辑分别继承所属分类；列表区域仅保留状态筛选、搜索和记录。

执行记录默认列出全部，搜索旁共享 Filters 菜单选择执行状态及独立 Needs me 条件；组合条件、搜索、分页游标与实时刷新保持一致，旧 view=attention 深链接仍可用。无脚本使用原生筛选表单。

Root 执行失败时，Task 保持可续作；Host 同时持久保存带任务标题的安全失败提醒，沿原请求的交付来源发送，不依赖 Main 是否正确复述失败。Main 的新失败 Task update 携带任务标题以区分并行工作。恢复重复扫描不会重复提醒，不将 Child 的单独失败当作整个 Task 失败。

执行记录和任务配置的 Refresh 共用顶栏右侧入口；执行记录 Filters 固定在搜索下方左侧，配置创建操作右对齐。任务配置不增加没有查询能力的筛选入口。
