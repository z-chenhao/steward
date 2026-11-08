# 后台任务与用户进度：测试方案

[功能与验收目标](../features/tasks.md) · [技术方案](../designs/tasks.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 范围与证据

F-004/F-042 的精确 PR 关联接续使用 [github_followup_test.go](../../internal/agent/sqlite/github_followup_test.go) 的真实 HTTP/SQLite/Actor/Tool 链路；覆盖自然完成、人工结束、重复与不同追加、旧 owner/晚模型与工具、停止/Forget 和 unknown 效果。原飞书 Task 的 GitHub 接续经真实 HTTP peer 验证原话题交付；平台登录、真实飞书渲染和真实模型范围判断不由该确定性回归证明，参见 [集成测试方案](integrations.md)。

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-004](../features/tasks.md#f-004) | 验证状态转换、同 Task 单活动 Root、取消传播、旧 owner fencing 与结果回传。 | 保护已接通的限定范围；Live/配置另行验证 |
| [F-004](../features/tasks.md#f-004) | 同一工作从飞书关联到 PR，再由评审/CI 接续；验证关联持久、同 Task 单活动 Root、重复/迟到输入、歧义与超范围、暂停/取消/Forget 后到达的事件。系统走实际接纳和 Reporter/Main 链路；受控 peer 证明机制，真实跨渠道兼容另行 Live。 | 未实现；目标入口回归及相应 Live/效果评测待补 |
| [F-004](../features/tasks.md#f-004) | 缺信息、动作审批、成果审阅分别通过目标 UI/渠道进入 Main；验证精确关联、可访问成果、重启保留、双端/重复提交、陈旧修订、暂停/取消及失效审批。真实 SQLite、既有 Web/渠道系统入口与浏览器验证配合；CLI 具名问题/成果入口已接通；已有审批不算全部覆盖。 | CLI 的 Actor/SQLite 与跨进程系统入口覆盖；Web 入口由 channels attention 场景覆盖；个人飞书问题和成果审阅卡有签名回调、HTTP peer 与 Web 权限回归，Live/模型质量待补 |
| [F-005](../features/tasks.md#f-005) | 真实长任务产生阶段摘要，UI 能读到更新；普通进度不触发 Main 推理或通知。 | 通过实际 Root 循环、真实浏览器和 checkpoint 前后杀进程证明闭环；模型质量单独评测 |

## 层级与关键路径

覆盖暂停/追加竞态、取消整棵树、once/ongoing、旧 epoch 结果拒绝和新 Root 接续。F-005 必须由真实生产路径产生摘要，不能在测试中直接调用 Progress 后当作闭环证据。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 已有检查与缺失覆盖

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_tasks.py](../../tests/system/verify_tasks.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [tests/system/verify_task_control.py](../../tests/system/verify_task_control.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |

`verify_tasks.py` 按持久化的上下文准备记录区分普通生成与摘要生成：两者必须使用同一 Run 的冻结模型；普通请求的工具名称及 schema 对照 Capability Snapshot，并保持逐轮一致；只有标记为 `summary_request`、具有摘要计划且上下文与准备记录一致的请求才能不声明工具。`test_model_task_control.py` 的负例继续拒绝摘要获得工具、未标记的无工具请求、模型/schema 漂移和准备上下文漂移。

### F-004 信息反馈验收路径

- `internal/task/task_test.go`：新问题/审阅分支的严格 union、必填/越臂/重复/null 字段、成果数量与冻结旧 schema 拒绝；旧请求省略新字段时保留规范化身份。
- `internal/agent/sqlite/task_feedback_test.go`：真实 Main Tool/Actor 保存问题，所有者并发八次回应只生成一条 Main input，Main 接续原 Task；不同重复冲突、源消息关联、原子失败回滚与重开读取。独立检查 supersede、到期、epoch、pause/cancel/Forget、当前反馈换目标/因果、已接纳反馈遇到后续停止、新普通输入继续与新审阅取代旧修订。成果引用沿真实 ArtifactStore 保留、作为回应附件进入 Main，始终不产生动作审批。
- 同文件的读取 fixture 验证 52 个当前问题跨稳定游标完整读取，按 Task 筛选与读取前后事件数不变；历史和隐私撤销不占待办页；元数据 read fixture 覆盖缺失/删除/其他所有者/到期成果被过滤，受活动 Task 保护的原 TTL 到期成果仍可用，不读取 blob。它不替代实际写入口验证。
- `verify_tasks.py` 的 feedback 场景通过现有脚本模型、实际 CLI、每次独立进程和 SQLite readback：记录问题 → 列表 → 回答/重试 → Main append → 原 Task 的新 Root；暂停后拒绝旧回答；真实上传成果经 Main 记录精确修订审阅、回应与接续不创建动作批准；回应提交前后 SIGKILL 后只接续一次。软件协议证据不证明真实模型能稳定识别何时提问或准确理解成果意见。
- Web 的具名待办与跨 CLI 回应复用见 [渠道测试](channels.md)；个人飞书问题和成果审阅卡的签名提交、同 Task 接续及跨端重试见[渠道测试](channels.md#feishu-task-question-tests)。真实客户端兼容与模型语义质量仍为缺口。

### F-005 验收路径

- `internal/agent/sqlite/progress_test.go`：通过实际 Root 工具轮产生多阶段摘要，重复文本幂等，执行段的进度标记和历史独立进度段均可接纳；最终结果/Main/Child/未启用进度的旧冻结上下文不泄漏；空白、UTF-8 字节上限与截断响应保留上一条。注入 Task 写入失败验证 checkpoint 回滚，换 owner 恢复后复用已保存 generation，旧 claim 拒绝写入。
- 现有 Child 审批和费用授权组件检查验证 Activity 不把等待授权错误显示为工作完成；既有 Task control 检查继续覆盖 pause/cancel 的 epoch fencing。
- `verify_tasks.py`：实际 CLI/Actor 在进度事务提交前后 SIGKILL，恢复后保留摘要，完成的模型调用不重发；只生成原有最终 Root update 和 Main follow-up。现有 PTY `/status` 仍从同一 Activity 投影读取。
- `verify_channels.py` 的真实 Chromium live 场景：Root 先写一条摘要并等待冻结审批，进度页可见；拒绝后进入下一阶段并结束 ongoing 的本轮，摘要和阶段自动更新；随后取消，页面不增加任务控制按钮，Conversation 草稿及附件保留。
- 上述使用显式脚本模型检查软件协议。真实模型是否在适当阶段稳定生成有用摘要属于外部 Agent eval，确定性回归不能证明该质量验收通过。

## 详细测试入口与证明边界

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="task-control-model-counterexample-and-implementation-evidence"></a>

### Counterexample and implementation evidence

`test_one_shot_completion_uses_root_outcome_after_child_drains` and
`TestTaskControlModelCompletionUsesRootOutcome` pair eight traces: both Root
outcomes, both Child outcomes, and both ending orders. For example,
`claim root0 → admit child owner1 → claim child → completed root0 → failed child`
must complete the Task and publish one completed Root update. The Go regression
uses the existing synthetic Child admission fixture, the real Agent Loop for
success, `Runs.Fail` for failure, and SQLite readback. Admission itself is not
proven by this fixture. A failed Root stays active even if its Child succeeds.
The unsafe-effect slot still belongs only to Root 0: an unknown effect in the
current epoch prevents completion; an old epoch's unknown does not prevent a
fresh Root from completing.

The unsafe model finds `claim root0 → commit pause → accept old-owner result`.
The Task epoch changes while the old owner still has a live lease. Checking only
ownership would accept that result. The normal model rejects it.
`TestTaskControlModelCounterexampleRejectsOldOwner` repeats this trace through
real model-result persistence, `Progress` and `ToolStore.Accept`, for pause and
cancel. It proves no stale progress, invocation or Child survives rejection.
The earlier `TestTaskControlPauseFencesLateWritesAndContinueNewRoot` also checks
late generation/usage rollback, reply, terminal and trace writes after stop.

For mutation evidence, use a disposable Go overlay removing only the Task
predicate from `fence` and run the new regression. It must fail with
`model counterexample admitted stale write`; rerun without the overlay and keep
both logs. Do not commit the overlay or a weakened production predicate.
Process-level evidence remains the maintained `task-control`, `children`,
`tools` and `code-recovery` gates; repetitions are explicitly selected through
the current runner rather than assumed from the historical schedule.

<a id="tasks-migration-and-verification"></a>

### Migration and verification

State schema v9 extends v8 with immutable completion policy, invocation stop
intent and legal stop/epoch transitions, without rewriting existing Runs,
snapshots or Conversation. Frozen capabilities retain their exact schemas.
Forward tests cover empty and every v1–v8 database, including v8 rollback;
existing identity/config/event checks remain in place. Future schemas fail fast.

`make test`, `make test-race`, `make lint`, and the system runner run the deterministic
checks. `tests/system/verify_tasks.py` owns isolated launch, doctor, CLI/PTY drive,
SQLite evidence and cleanup. It kills actual processes before/after Task,
result, Reporter and follow-up commits, then reclaims work and checks receipts,
model reuse, one completion per Root and delivery dedup. `evidence/tasks.json`
contains before/after state and terminal output. `evidence/local-provider.json`
also drives Task creation, independent Root execution and runtime-framed Main
follow-up through the actual Node/Pi Bridge and owned HTTP peer. `verify_task_control.py` adds live
PTY cancellation and three real SIGKILL boundaries around stop acceptance and
settlement. Repeat count and output directories are explicitly selected through the system runner. Go contracts cover transition rejection,
duplicate and concurrent append/pause/cancel, stale results/claims, synthetic
descendant propagation, retained results, one-shot/ongoing policy and shutdown.
Scripted fragments receive no semantic score. Live-model quality,
online accounts and full product Golden Case gates remain unexecuted.

Shutdown joins the currently owned work at a stable boundary. SIGKILL recovery
waits for the five-second lease to expire. Re-run `chat work` or restart the TUI;
never edit the database to revive terminal Runs. No background child is required
to survive the command or test harness.

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

## Tasks 工作台回归

`internal/agent/sqlite/workspace_test.go` 用真实 Main/Root/Child 与 SQLite 证明详情、原始用户消息定位和非空 Main 交付，读取前后领域计数不变；65 项任务在同 created_at 下完整分页，游标不得跨搜索/视图使用，其他 Agent 的 Task 不可读。`internal/task/sqlite/reader_test.go` 验证只读池不新建或改写数据库。既有 Activity 测试继续保护终端/API 摘要契约。

既有 channels 浏览器测试打开任务详情，核对目标、审批冻结 hash、带任务上下文回 Main、移动端，以及生产 Pi peer 场景完成后的 Main 交付；不以原始 Worker result 代替用户回复。列表筛选不是新的 Task 状态；所有控制仍经 Main 和既有生命周期回归。

计划视图由 `task_planning_view_test.go` 验证真实保存配置、首次触发前安排、已结束／无后续触发、最多50项、指令最多20条、搜索／视图分离、所有者与撤销上下文、不派发／不写库。Web 验证安排读取失败不隐藏普通任务、字段转义与原审批卡片；手动编辑的协议与浏览器验证归属 [Automation 测试](automation.md)。

`TestMainQueuesSecondRequestUntilFirstTaskHandoffReply` 在真实 SQLite/Actor 中阻塞第一轮模型生成，再接纳第二条请求；验证首条创建一个 Task、交接回复先提交，第二条才进入模型，且第二条回复不等待 Worker 完成。并发 ingress 回归逐条核对全部回复；旧 checkpoint 继续覆盖原 Steering 与恢复语义。

`TestSerialMainRetainsBothRequestsAfterReopeningBeforeFirstReply` 在首条输入 checkpoint 后接纳第二条，关闭并重开真实状态库，验证两条请求各回复一次且先后关系不变。Main 系统 gate 对 16 条并发输入逐条核对回复内容与顺序，并保留旧冻结 checkpoint 的慢工具响应和 SIGKILL 恢复场景。

执行列表回归覆盖同一持续任务的两轮真实 Root、Task 仍 active 而两轮 execution 已 completed、旧轮指令与结果隔离、有界游标及跨筛选拒绝、标题／完整目标搜索、读取不改变任务或执行。title 合约验证独立存储、缺省旧调用、空白／null／超长／非 create 字段拒绝。Web 检查任务配置与执行列表分离、列表不重复描述或结果、行属性列、详情安全转义、明暗与窄屏截图、表格本地滚动，以及既有人工反馈、审批与冻结执行语义。

二级导航回归检查 Tasks 与 Settings／Developer 的共享侧栏几何、分类切换和详情选中态、内容区无重复分类 Tab、320／390／761／1024／1440px 明暗主题与中英文布局、折叠偏好、手机抽屉 Escape 与焦点恢复，以及无脚本入口可达。

执行记录默认列出全部，搜索旁共享 Filters 菜单选择执行状态及独立 Needs me 条件；组合条件、搜索、分页游标与实时刷新保持一致，旧 view=attention 深链接仍可用。无脚本使用原生筛选表单。

`channels` 的 attention 场景用真实待处理／失败执行验证桌面及 390px、320px 的明暗列表：读屏标记保留，页面不横向溢出，宽表格仍可在自身区域通过键盘滚动。不能只用无待处理记录的列表验证宽表格裁剪。

`TestRootFailureNoticeSurvivesUnrelatedMainReplyAndRecovery` 用 Main 故意答复另一话题复现漏报：真实 Root 失败后仍有一条 Host 失败交付、正确任务标题与 failed 执行记录；Task 继续 active，重复 Recover 不增加通知或交付。`tasks` 系统门禁同时核对实际 CLI 失败场景的安全通知。

列表回归通过共享 `assertDataList` 检查 Refresh 仅位于全局顶栏右侧、Filters 在搜索下方左侧且展开收起不移位；覆盖桌面／手机和明暗主题，保留原生筛选与分页。

`TestExecutionRecordsKeepHistoricalRoundsSeparateFromTaskConfiguration` 核对最近运行的 owner／task 范围、历史轮次不导入当前进展、读取不写状态。`TestExecutionDetailPrioritizesDeliveryAndRefreshesRunFacts` 覆盖结果优先、说明折叠及转义、六种执行状态空态、Main 交付和实时区域。native-trace 浏览器场景保留执行结果页桌面／手机、明／暗截图，检查窄屏结果先于次级信息、Trace 对应 Root、配置仍走原入口。

`channels_task_executions.mjs` 在两个真实 Root 与 Main 交付上验证整行半屏非模态检查、前后执行、返回／前进、Escape／关闭与焦点恢复、调宽／展开、背景列表与详情独立更新及查询保留、原生全页／无脚本。保留 1400／1920／390px 明暗截图并检查溢出和窄屏结果优先。所属 native-trace 场景由既有 channels 门禁执行。

执行抽屉初始化依赖稳定的列表容器，不依赖已有行；native-trace 启动时先在真实空列表接纳一次用户输入，等待实时出现执行并点击验证共享抽屉及最终 Main 交付，避免空态后的新记录退回全页。

执行详情阅读回归使用实际 HTTP 模型 peer 产出的合成中文标题、长说明及含表格的 Main 报告；首轮 Root 受控延迟用于截取运行中桌面／手机状态。native-trace 核对说明默认折叠及轮询后仍展开、单次历史去重、18px 标题与首屏高度、桌面／手机明暗布局，继续验证独立刷新、前后记录、原生全页及无脚本。模板回归覆盖交付后隐藏旧进展与单次历史。

执行详情的 `executionDetails` 浏览器回归先要求非空正文和 Markdown 表格，再检查桌面、移动端和明暗主题下的实际 UI 字体、14px 字号及表格与正文一致性，并保留对应截图；不能让零匹配的循环跳过检查。
