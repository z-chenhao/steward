# 自动化与安静监控

按时间执行提醒或周期工作，监控跨阈值时才通知。

[需求主索引](../feature-map.md) · [技术方案](../designs/automation.md) · [测试方案](../test-plans/automation.md)

## 功能说明

用户可设定明确时区的 at/cron 计划，并修改或取消。

周期观察在首次进入阈值区间时通知，其余成功检查保持安静。

本文描述用户行为和范围；逐项完成状态以主索引为准。当前能力依据代码静态盘点，配置可用性与真实服务/模型质量需分别验证。

## 需求与验收

<a id="f-015"></a>

### F-015 · 一次性与周期自动化

已撤权计划保留审计记录。缺少冻结包时，受影响的周期计划保留原触发时刻；其他计划及 Run 继续工作。显式执行 `automation tick` 可查到受影响的计划引用。

**需求**：用户可设定明确时区的 at/cron 计划，并修改或取消。

**当前实现**：计划、触发记录、Task/Root admission、版本去重和恢复已接通。

**边界与剩余工作**：执行需要应用或前台 worker 在运行；不自动分离守护进程。

**验收目标**：验证时区、重复触发、修改/取消竞态、停机补偿及重启不重复工作。

F-015 的原子功能插件迁移已完成：现有 Main／CLI／Web／扫描入口使用同一功能包的托管版本，升级、回退、停用和历史执行保持原语义。Host 保留权威事实与控制机制；兼容实现仅服务具名历史消费者。F-016 不在该完成范围内。

<a id="f-016"></a>

### F-016 · 阈值状态与安静通知

**需求**：周期观察在首次进入阈值区间时通知，其余成功检查保持安静。

**当前实现**：Root observe 写入每次 fire 的观察值，保存阈值跨越状态并控制 Reporter 通知。

**边界与剩余工作**：本需求是监控机制，不内置价格或其他在线数据源；真实数据接入由 F-025 跟踪。

**验收目标**：验证跨入、持续在内、跨出再跨入、旧 fire 及缺失观察值的行为。

<a id="f-045"></a>

### F-045 · 主动规则的观察与启用

**依赖**：F-015/F-016 自动化与 F-042 事件监听。完成状态以主索引为准。

**需求**：用户可以先审阅助手依据环境变化本来会开展的工作，纠正规则，再显式启用规则范围内的自动工作。

**当前实现**：本机所有者可对已认证的确切 GitHub PR 和一项既有 Task 设置规则。规则按一种结构化事件类型筛选，CI 检查可选只匹配失败结论。观察模式保存每次命中或未命中的理由及当时的 Task、规则代次，自动模式只将新一代命中的观察交给 Main 判断是否在原 Task 范围内接续。

**边界与剩余工作**：规则只比较已授权轮询返回的结构化事件类型与可选 CI 结论。观察模式无需调用模型或付模型费用，也不创建 Main 或 Task。自动模式随后使用既有模型和审批管线。规则编辑入口位于已有任务的 Web 配置页；其他来源和语义判断尚待支持。

**验收目标**：同一候选事件在三种模式下产生对应结果；观察可审阅且不执行工作，只有明确启用的匹配规则可自动接单；重复事件、规则修改、关闭、撤权和重启不恢复过期授权。

先配置并成功轮询确切 GitHub PR。用 `github binding` 查关联版本和当前模式，选定已有 Task 及其当前 epoch。首次关联版本为 0。下面的命令观察行内评论。参见 [GitHub 监听说明](integrations.md#f-042)。

```sh
steward -config /absolute/config.yaml github bind <<'JSON'
{"task_ref":"task:opaque-ref","task_epoch":1,"version":0,"mode":"observe","event_kind":"review_comment"}
JSON
steward -config /absolute/config.yaml github decisions
```

在 Web 打开已有任务的配置，找到「GitHub PR 规则」，选择已配置的 PR 和事件后点「开始观察」。来源需要先成功完成一次检查。用「刷新观察记录」查看本代最近五条事件及匹配依据；观察本身不会接续任务。「启用自动接续」仅对之后的新匹配有效。「停止规则」停止这个任务的接续，已有 PR 来源仍可轮询。每次提交同时校验当前工作台、任务 epoch 和规则版本，过期页面须刷新。

`event_kind` 可选 `review_comment`（行内评论）、`review`（已提交评审）或 `check_run`（当前 PR head 的已完成 CI 检查，含成功或失败）。每页最多 50 条，下一页用 `github decisions BEFORE_SEQUENCE` 传上一页最末的 `observation_sequence`。记录展示匹配依据、原事件标识和当时的 Task，不重复保存评论正文。历史记录仅供查阅，不能拿来接单。

只观察 CI 失败时，将绑定设置为 `"event_kind":"check_run","check_conclusion":"failure"`。省略 `check_conclusion` 仍匹配所有已完成检查。带筛选的决策显示当时的预期和实际结论，以及匹配或不匹配的理由。非规范结论显示为 `unknown`，无法触发失败规则。

调整筛选或切换到 `automatic` 时重新读取版本和 Task epoch，再以新值提交 `github bind`。绑定输出展示确切仓库/PR、原 Task 目标预览、规则代次和模式；`state` 另行反映绑定是否可用于新工作。未绑定默认为 `off`。用 `github unbind` 和当前 `version` 关闭，监听故障时也可关闭。已接纳 Root 仍由原 Task 生命周期管理。旧格式 `github bind` 继续显式授权同一 Task 处理所有新观察。

## 如何使用

用户可在对话中交代明确时间或周期的工作。CLI `steward automation list` 查看计划，`steward automation manage REQUEST_KEY` 从 stdin 接受 create/update/cancel JSON。

保持正常应用、Web 服务或 `steward automation work` 前台运行。`automation tick` 只保存到期事件；监控仍需要可用工具取得实际观察值。

## 详细操作参考

- [自动化操作详情](#automation-automation-operations)

## 操作说明与配置细节

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="automation-automation-operations"></a>

## Automation operations

Automation persists user intent separately from Task. The scheduler writes one
`automation_fire` and `ai.steward.automation_fired.v1` Outbox event atomically. It
never runs a model. Actor's ordinary `Next` recovery consumes authoritative fire
rows, writes its Inbox receipt, creates an ongoing Task if needed, freezes the
current model/capabilities in a new Root, and emits `run_ready` in one transaction.
Execution uses the existing RunClaim, Agent Loop, ToolInvocation, approval and
Main Reporter/Delivery path. Lost or repeated queue wakes cannot duplicate work.

Run the foreground scheduler/executor with an active, tested profile:

```sh
steward automation work
```

Keep that command running under the installation's process supervisor. It scans
at startup and at one-second intervals while idle. SIGINT/SIGTERM stops and joins
its work at existing recoverable boundaries; process exit does not cancel intent.
TUI/CLI ingress may run in another process against the same configured data root.
`chat work` also scans due Automation during its normal Actor drain.
`automation tick` only persists due events and exits; it does not execute them.
No scheduler process is implicitly detached by the application.

Create from a JSON object on stdin, with a stable request key for retries:

```sh
steward automation manage monthly-review < review.json
steward automation list
```

`review.json`:

```json
{"operation":"create","instruction":"Remind me to review my long-term goals.","triggerType":"cron","triggerSpec":"0 20 * * 0L","timezone":"Asia/Shanghai"}
```

`at` uses an RFC3339 timestamp with explicit offset. Every schedule stores an
explicit IANA timezone (`UTC` works; process-local `Local` is rejected). Cron has
five numeric fields: minute, hour, day of month, month, weekday (0=Sunday through
6=Saturday). Lists, ranges and positive steps work; `0L` through `6L` in the
weekday field select that month's last weekday. Restricted day-of-month and
weekday fields use ordinary cron OR semantics; wildcard steps retain wildcard
semantics (for example `0 8 */1 * 0` still means Sunday). See the
[crontab day-field rule](https://man7.org/linux/man-pages/man5/crontab.5.html). A new schedule fires strictly
after creation/edit time. Nonexistent DST wall minutes are skipped; both distinct
UTC occurrences of a repeated wall minute fire. Invalid calendars fail closed.

Missed occurrences coalesce to the original persisted due occurrence; cron's
next fire advances strictly past the recovery scan time. This deliberately avoids
replaying every missed day. Already accepted fires remain durable and execute
in order, with at most one live Root for the ongoing Task. A failed Root leaves
the Task active. A completed Root also leaves an Automation Task active.

Update supplies `operation: "update"`, `automationRef`, current `version` from
list, and the complete new instruction/schedule/timezone/optional threshold.
Cancel supplies only `operation: "cancel"`, `automationRef` and `version`.
Each schedule mutation is CAS; stale versions return `INVALID_ARGUMENT` without
writes. Scheduler bookkeeping and observations also advance the visible version;
read list again after a conflict. Edit increments an independent schedule version,
invalidating queued fires and suppressing stale results. Reporter and Main inbox
promotion recheck the persisted fire version, including results committed before
an edit and inbox entries queued before an edit. Current in-flight steps
keep their existing snapshots and approval boundaries. Cancel disables future
fires and uses the existing Task cancel/epoch mechanism for the current tree.
Task pause/cancel also disables its Automation. A stopped Task cannot be restarted
by a delayed fire or schedule edit; create a new Automation to establish new intent.
Memory Forget revokes prior intent as well as Task context. Creation freezes the
Agent's current privacy epoch in `intent_privacy_epoch` within the acceptance
transaction. Admission compares it with the current barrier before creating any
Task, including when the first fire was persisted before Forget. A mismatched
epoch or a linked Task predating the barrier commits the fire as `skipped` with
an Inbox receipt.
It never replaces the Task to regain its context. Fresh chat and newly created
Automations continue through the same Actor without manual cancellation of the
old schedule. A new Automation establishes fresh intent; editing the old schedule
does not restore revoked intent, even before its first Task. Receipt retries,
schedule edits and scheduler timestamps never renew the frozen privacy epoch.
Storage failures still abort admission and remain recoverable.

List uses the same privacy projection for the Main Tool and trusted CLI. A revoked
row retains only its Host-generated `automationRef`, current `version` and
`enabled` control state. `instruction`, `triggerType`, `triggerSpec` and `timezone`
are empty strings; `nextFire` and `below` are absent. This permits safe cancellation
without returning forgotten intent, schedule or threshold evidence. Fresh rows
retain their full view. Original fields remain in local audit. Edits and receipt
retries never restore visibility across the immutable intent epoch. Existing
Task search, Root admission and Reporter/follow-up privacy fences also protect
Automation-derived Task context; create/update/cancel receipts expose only the
Host reference and acceptance, not stored instructions.


Completed one-shot `at` intent retains `enabled=true, next_fire_at=NULL` for audit;
it never fires twice. Cancellation is explicit and disables it.

<a id="automation-threshold-monitoring"></a>

### Threshold monitoring

A create/update may include `below`, a nonnegative integer threshold (maximum
10^12, same unit as observations). The instruction specifies the real data source
and business units. This is not a price-fetching service: the Root uses its
existing scoped Tools to obtain evidence, then calls
`builtin__automation_observe({"value":1750})`. The Host binds the call to that
Root's persisted fire; the model cannot choose identities, dates or replay keys.
Only the linked Root may write an observation. Main and other Roots/Children
cannot update that state. Claims and schedule versions fence late writes.

The Host records one authoritative value per fire. Repeated sources returning
that value are idempotent; a different value under the same fire is
`NON_DETERMINISTIC_REPLAY`. A later observation can change the state from outside
to inside the threshold, permitting one completion notice. Further below-threshold
values remain quiet until an outside value resets it. Old/out-of-order fires do
not reset newer monitor state. No valid observation produces one `needs_input`
update rather than an invented measurement. Failures/unknown effects still use
the existing meaningful-notification path. Successful quiet observations create
no Main turn or DeliveryRequest. Workers never deliver directly to the user.

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/automation.md)；已有检查、未覆盖场景和验收方法见 [测试方案](../test-plans/automation.md)。

## Web 手动配置执行计划

Tasks → Create schedule 可以直接填写任务指令、是否启用、单次／每天／工作日／每周／每月／自定义 cron、日期时间和明确的 IANA 时区。已有安排通过 Edit schedule 修改；新建表单优先填入已知的浏览器 IANA 时区，未提供时沿用计划默认值；已保存计划不随浏览器位置变更。任务详情按保存的时区展示下一次触发及 UTC 偏移。高级阈值可以留空；新计划的阈值至少为 1，填 1 会在非负整数观测值降到 0 时通知。历史零阈值保留原判断，可按原回执重试或停用。保存仅配置后续触发，不立即创建 Task 或运行模型。

停用安排只停止后续触发，不等于取消已派发任务；需要停下具体任务时仍走原有任务控制。已派发工作保留冻结指令，旧版本阈值观察不再产生通知。不同页面同时编辑时拒绝覆盖并保留草稿；重复提交同一份请求返回原收据。单次本地时间处于夏令时空缺时，表单标明时间字段；回拨时显示两个 UTC 偏移，选择后仅触发一次。输错时区、cron 或填入过去时间均保留草稿并标出相应字段。已有单次安排的精确时刻和选定偏移在未修改日期／时间／时区时保留。

当前不提供结束重复日期、独立标题或每项计划的模型覆盖。Task 的原始目标和单次／持续完成规则仍不可变；没有计划的 Task 可创建一项独立安排，不会伪装成修改原 Task。

定时日历计算由 Steward 自有托管插件执行，插件版本包含固定时区规则；升级影响后续新建／修改，已有计划和执行保留原版本。停用当前插件不会改写旧计划；Guardian 停机与隐私撤回仍可阻止接纳，未修改配置的本地停用不依赖插件。完整迁移状态见 Feature Map。

在已有计划编辑页点 **Test saved schedule / 试运行已保存的计划**，将立即用当前已保存版本创建一个单次任务。该任务遵循自己的审批，在 Web 工作台里显示结果。阈值从计划当前状态复制，每次试运行有独立观察和回执；可在执行记录中重看。保存计划前的编辑不会用于试运行。


### 次日会议预备

在启用 `meeting-preparation` 1.1 的安装中，进入「设置 → 会议预备」，选择 Google 日历账号和 IANA 时区，保留「仅使用日历」，点击「授权只读预备」，即可看到明日第一场定时会议的时间和来源。尚未连接邮箱时，页面提供 Gmail 连接入口。连接成功后，在会议预备页选定邮箱并再次授权，才会为这场会议查找相关邮件。

日历窗口是所选时区的明日，最多 20 项定时事件，按开始时间排序。邮箱窗口是近七天最多 10 个会话，只读取主题及邮箱地址。匹配忽略大小写、空白及 Re、Fw、Fwd 前缀；命中时显示对应邮箱中的会话链接，没有匹配时保留会议并说明。点击「关闭会议预备」或断开已授权的来源即可停止。重新连接后须重新选择并授权。结果仅存于服务内存，重启会从现有授权重新预备。已有安装继续使用所有者选定的插件版本。1.0 按日历与邮件配对，切换至 1.1 需通过正常的插件验证和激活流程。

精确 PR 绑定的 `issue_comment` 匹配 Conversation 普通评论。观察模式可预览匹配，启用自动模式后只接纳该规则代次的新观察。
