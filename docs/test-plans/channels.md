# 终端、Web 与飞书：测试方案

[功能与验收目标](../features/channels.md) · [技术方案](../designs/channels.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

通知选择器检查 Auto／自动标签、`auto:` 值及明暗主题桌面／手机显示；沿用 `settings_test.go` 的版本冲突、保存和失效渠道场景，以及 `channels_preferences.mjs` 的通知自动保存场景。

## 范围与证据

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-017](../features/channels.md#f-017) | 验证真实终端首条输入、后台工作时追加、退出和重启。 | 保护已接通的限定范围；Live/配置另行验证 |
| [F-018](../features/channels.md#f-018) | 真实浏览器验证设置、发消息、附件、审批和已有对话展示。 | 保护已接通的限定范围；Live/配置另行验证 |
| [F-018](../features/channels.md#f-018) | 真实浏览器组合待回答/审批/审阅/故障与普通工作，验证顺序、计数到明细、权限、重复/已解决/过期事项；后台更新、断线与旧页面下保护草稿/附件/焦点/位置。沿用现有渠道浏览器入口；F-004 未完成前仅能验证已有事项，不能据渲染器宣称闭环完成。 | 未实现；目标入口回归及相应 Live/效果评测待补 |
| [F-019](../features/channels.md#f-019) | 长任务与输入草稿并行时，回复/进度更新可见，焦点、草稿及附件不被覆盖。 | 必须通过目标入口补齐闭环；不能只直接调用底层方法 |
| [F-020](../features/channels.md#f-020) | 验证真实绑定、token 生命周期、通知偏好、撤销、重启与不确定投递；未知结果不盲目重发。 | 必须通过目标入口补齐闭环；不能只直接调用底层方法 |
| [F-020](../features/channels.md#f-020) | 签名回调→Actor→原消息同步/Task 异步交付→受控飞书 HTTP peer；验证精确原来源、单 Main 分组、可信全文/文件链接、通知偏好和完整语义，覆盖重复/重启/撤权/发送未知。 | 个人 p2p 入口已有回归；显式 GitHub PR 接续由 F-042/F-004 驱动，并经真实 Feishu HTTP peer 验证原话题，真实飞书展示、链接可达性与语义质量待 Live/效果验收 |

### F-018/F-004 Web 待办覆盖

`verify_channels.py --attention-only` 使用真实 Main Task 工具保存问题/审阅与失败执行，真实上传文件，再由认证 Web 与 Chromium 完成优先定位、已完成 Task 审阅、具体文件链接、回答/审阅及 Main 接续。`channels_browser.mjs` 的 attention 场景检查当前页数量与行数、长中文拒绝、native 输入/焦点/选区、Chat 草稿与附件、新标签页、中文窄宽、键盘提交、确认丢失后只读草稿、另一端使本地表单失效、断线陈旧页面被拒绝且保留意见。持久化 readback 证明三条反馈只产生三次接续、始终保留 Web 来源/路由、无审批替代，随后 CLI 同答重试仍返回原 receipt。

`task_feedback_web_test.go` 以真实 SQLite/Actor 验证 CLI/Web 首次顺序、callback aliases、不同回答冲突、真实 Main 交付与绑定撤销先于去重；`attention_test.go` 验证转义、确切 Task/消息/文件定位和认证入口；51 个 Main 当前审批的有界投影保留50条并将更多入口指向Chat中第51条真实审批，而非无关Task列表。原冻结审批与对话 live 浏览器场景继续覆盖 approval-only 原定位。确定性模型证明协议和交互，真实模型何时提问及意见理解质量仍需外部评测。

## 层级与关键路径

实际浏览器/PTY/签名回调分别驱动；输入草稿、附件与焦点必须在后台更新时保留。飞书失效 token、撤销绑定和发送结果未知单独验证；本地回执不冒充远端核实。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 已有检查与缺失覆盖

`verify_channels.py` 的 `live_updates` 通过真实浏览器验证长回复滚动、草稿与附件、审批和 Task 更新、断线及会话轮换。该界面夹具明确声明 32768 的模型窗口，以保留滚动标记及原始工具结果；脚本摘要器只保存调用回执，不承担语义文本复述。上下文压缩的冻结与恢复由记忆/Task 场景单独验证，界面断言不通过放宽文字或超时来绕过。

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_channels.py](../../tests/system/verify_channels.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [tests/system/verify_tui.py](../../tests/system/verify_tui.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [tests/system/verify_delivery.py](../../tests/system/verify_delivery.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |

- **F-019**：通过具名投影与真实浏览器覆盖；不以软件回归证明真实模型质量或外部账户可用性。
- **F-020 剩余覆盖**：显式 GitHub PR 以外的跨平台事件接续与成果组织；真实账户的原消息回复/卡片展示、工作台链接可达性和客户端登录。loopback 只能证明同机访问。不确定投递继续只查询本地回执，可信远端核实能力仍待定义。

原消息与成果投影回归入口：

- [reply_origin_test.go](../../internal/agent/sqlite/reply_origin_test.go)：真实 SQLite 与 Actor 验证 create 来源、同步/Task 结果、显式偏好、旧路由不回填及失败迁移回滚；CLI/Web 反馈回复保留实际作答渠道，接续 Task 的异步结果仍回原飞书话题，同答 CLI 重试复用原 receipt 且不增加通知。既有 Channel 回归继续覆盖 CAS、撤权、route/projection 冻结和回调去重。
- [projection_test.go](../../internal/interaction/feishu/projection_test.go)：真实 HTTP reply wire、原来源转义、无自选接收者、目标失效无 fallback、可信 origin 校验、摘要边界和审批全文。
- `verify_channels.py` 的 `feishu_topic_delivery`：签名 callback→Main→Task→冻结 Delivery→HTTP peer，检查原消息回传、Conversation 分页锚点、完整 Artifact、单一简短通知、重启/重复回调、显式 TUI 偏好与 unknown 不重发。`feishu_auth_lifecycle` 和现有 multipart/租约场景同时覆盖 source reply；无 workspace 配置保留原全文/附件。

聚焦验证可复用已有函数：`PYTHONPATH=tests/system python3 -c 'from pathlib import Path; from verify_channels import feishu_topic_delivery; feishu_topic_delivery(Path("bin/steward").resolve(), {"checks": [], "scenarios": {}})'`。跨模块变更仍运行 `make test lint` 和相关 channels 系统门禁。合成模型证明机械链路，不证明摘要的真实模型质量或远端平台可达性。

<a id="feishu-task-question-tests"></a>

### 个人飞书 Task 问题和成果审阅卡（F-004/F-020）

`verify_channels.py --feishu-questions-only` 从签名原消息经过真实 Main、持久问题、投递 outbox 和受控 HTTP peer 生成卡片。使用实际卡片回执提交新版签名表单，验证 durable Main 输入、同 Task 一次接续、同步回复引用卡片与异步结果引用原 Task 消息。覆盖 CLI/Web/飞书同答重试、不同答案冲突、错误身份/会话、转发/换卡、审批组件、过期因果、暂停、撤权重绑、显式 TUI 偏好与本地来源。发送已到 peer 但丢失确认时，拒绝该卡回应，重启保持查询语义。

[Feishu 任务问题边界测试](../../internal/agent/sqlite/feishu_task_question_test.go) 阻塞真实本地 HTTP token 交换，验证凭据返回前撤权和 Forget 后卡片无法发送。已有回执上的 CLI 优先回复保留原路由，Forget 后拒绝飞书重放并保留既有投递回执。真实飞书客户端的表单呈现、权限与回调仍需单独验证。

`verify_channels.py --feishu-reviews-only` 从原个人消息产生确切成果审阅卡，核对冻结工作台路径和锚点、匿名拒绝、认证页面只在审阅中展示该修订，以及真实文件内容。覆盖无工作台时留在本地、卡片无文件和原始 ArtifactRef、错卡/替换修订、三端回执和接续。SQLite race 覆盖 token 取得期间文件消失、工作台地址变化、已回执后的文件失效/重绑/Forget，以及 039 问题投影和回执升级至 040。

## 详细测试入口与证明边界

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="channels-verification-and-evidence"></a>

### Verification and evidence

```sh
make test
make test-race
make lint
make test-full
python3 tests/system/verify_channels.py --binary bin/steward --evidence evidence/channels.json
python3 tests/system/verify_channels.py --binary bin/steward --evidence evidence/channel-leases.json --delivery-lease-only
python3 tests/system/verify_channels.py --binary bin/steward --evidence evidence/components.json --ui-components-only
```

`verify_channels.py` owns Launch (private root, local platform Fake and real Web
process), Doctor (auth and real browser setup), Drive (HTTP/UI/CLI boundaries),
Evidence (SQLite/Conversation/API/platform ledgers and browser screenshots), and
Cleanup (joined children, stopped Fake server and removed disposable state).
CI selection follows the current engineering design; repetitions require an explicit runner option.
The pinned Browser dependency is the repository's existing `playwright-core`.
Screenshots and DOM evidence are under `evidence/channels-ui/`.

The harness covers concurrent callback/source retries, identity/auth/recipient
rejection, input commit and delivery SIGKILL windows, approval Steering plus
restart and callback races, inbound/outbound attachment bytes, unknown-send
non-retry, and GC-08/09/12/14 channel/state fragments. It also covers
slow sends/uploads, cumulative multipart duration beyond the default lease,
competing live/recovery consumers, lost ownership cancellation, late settlement
and renewed ingress after restart. The existing platform Fake owns deterministic
response delays and an independent send-attempt ledger. Unit/contract tests cover
migration rollback, CAS/revocation, route/projection freezing, encryption,
credential redirect refusal, session expiry, XSS and unknown/invalid-component
fallback. The channel harness also drives Web account login through the real Manager/Bridge
against the existing local OAuth Fake, actual Probe/Select, hidden-model rejection,
restart and logout, then scans persisted files for credential values. Existing
suites retain replay, cancellation, account lifecycle and
Artifact access coverage.

Scripted-policy transport fragments are not full product scores. Complete 20-case
semantic grading and live Feishu/ChatGPT authorization remain release evidence;
this slice does not claim M7 or the complete first release is finished. No live
external account or write is required by CI.

Feishu transport shapes follow the official [message API](https://open.feishu.cn/document/server-docs/im-v1/message/create),
[resource API](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/im-v1),
and [callback encryption guide](https://open.feishu.cn/document/server-docs/event-subscription-guide/event-subscription-configure-/encrypt-key-encryption-configuration-case).

<a id="delivery-inspectable-verification"></a>

### Inspectable verification

The system runner selects `tests/system/verify_delivery.py` for delivery regression. It reuses
`MainHarness` and `Terminal` for Launch, Doctor, Drive, Evidence and Cleanup.
Each run captures 20 actual SIGKILL/restart cases: Main and Root final replies at
Conversation append, delivery commit, checkpoint, send and acknowledgement
boundaries. Root acceptance is delivered before faulting its final Main follow-up.
Database snapshots prove one semantic message/receipt, correct destination,
retired outbox, reused generations, and read-only duplicate scans. PTY transcripts
prove final replies and frozen-hash approvals reach the real terminal surface.

The explicit `-test-delivery` Fake requires `-test-bridge` and supports
`retry-once`, `unavailable`, `ambiguous`, and `unknown`. It wraps the same durable
terminal acceptance boundary. Recovery evidence covers bounded retries, query-only
unknown, accepted-send reconciliation, and duplicate approval callbacks without
unnecessary Main inference. Go tests additionally cover concurrent consumers,
claim fencing, identity drift, atomic event failures, legal/illegal transitions,
and populated v11 migration. These are M3 conformance fragments, not full product
Golden Case scores or a claim that M5 Durable Replay is complete.

<a id="web-components-build-and-verify"></a>

### Build and verify

```sh
npm ci --prefix web --ignore-scripts
npm run build --prefix web
make build
npm run check --prefix web
npm run lint --prefix web
python3 tests/system/verify_channels.py --binary bin/steward --evidence evidence/components.json --ui-components-only
```

Commit component source, lockfile and generated assets together. `make test`
rejects stale embedded assets; CI installs the same pinned dependencies. Go-only
builds consume those checked-in assets and need no frontend server or network.
The maintained channel browser action proves actual React composition, native
table cells and fallback, frozen approval fields, artwork loading, responsive
layout, CSP-compatible execution and local-only frontend requests. Its complete
action also drives model setup, file upload and actual message submission.
It deliberately holds the local JavaScript bundle while entering a draft and
selecting an attachment through the usable server form, then releases it and
checks text, file bytes, focus, selection and source identity before submitting.
The composer retains the native form and input nodes, applies shared control
styles and adds keyboard submission. The existing image scenario also holds the bundle while opening
a native picker, completes selection after enhancement, then verifies scoped
pixels at the actual Pi HTTP peer; text-only and recovery checks still apply.

消息键盘回归使用同一浏览器驱动：Enter 经实际消息入口发送，Shift + Enter 产生换行且不提交；组合输入与兼容性键码 229 不提交，按键重复、按钮禁用和请求进行中不重复提交。检查明暗主题发送按钮背景均为 `#D97757`，失败仍保留草稿与附件；原有按钮与无 JavaScript 提交场景继续覆盖。合成组合事件验证处理逻辑，物理输入法候选确认仍需人工验证。

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

## 工程基础重构回归

F-018/F-039：`foundation_test.go` 实际 HTTP listener 在 Main provider failure 后仍服务并执行下一请求；`web/page_test.go` 证明 Settings/Tasks 不依赖历史查询；`conversation/sqlite/store_test.go` 验证 205 条记录无重叠/遗漏分页和作用域查询。`tests/system/verify_channels.py` 通过实际 Web 证明缺失依赖不隐藏控制页、修复后恢复 ingress、GET 不修改已稳定业务状态。

## F-019 实时更新验收

- `internal/interaction/web/reply_stream_test.go`：真实 HTTP 流检查未认证拒绝、Worker 和过期事件隔离、增量仅发送后缀、绑定撤销关闭已有连接。`internal/agent/sqlite/foundation_test.go` 验证工具开始前撤下预览、取消时在供应商清理完成前撤下预览，并验证正式通知晚于消息持久化。
- `tests/system/verify_channels.py` 的 streaming 场景：真实 Node/Pi HTTP 流等待 Chromium 观察首段和第二段后才继续；后打开的页面接收当前文本，同一页面断开重连后逐字核对草稿。延迟正式投影时，尾段仍进入纯文本草稿；完成后展示一个富结果，供应商断流只留下 Host 失败通知。检查无整页跳转，输入、焦点、选区与附件保留。
- `internal/interaction/web/updates_test.go`：条件 ETag、消息/审批/状态变化、历史游标、HTML 转义、无输入/内部字段、失败无部分响应；条件请求也必须重新认证和检查绑定；Settings/Tasks 不查询 Conversation。
- `tests/system/verify_channels.py` 调用维护中的 `channels_browser.mjs` live 场景：在真实 Actor/SQLite/HTTP/浏览器链路中，输入草稿和选择文件后收到新回复，保留原输入节点、request source ID、光标/选区、焦点和阅读锚点；断网后重连；Task/Worker 产生冻结审批与 busy 状态，拒绝动作不重载、不执行副作用；另一页显示任务取消；会话轮换后保留草稿并显式恢复。
- 原有 setup/components/approval/移动端/无 JavaScript 场景继续验证渐进增强、受控投影和原表单兼容；整个渠道回归保留签名/身份/去重、未知副作用和重启场景。

不增加 golden case、模型效果分数或独立 CI 编排。完整检查使用既有 `make test lint` 与 channels/core runner；执行结果保留在既有 evidence 报告中。

## Web 信息架构与流程回归（F-018/F-019）

沿用 `verify_channels.py` 和真实浏览器 action，不新增脚本或 CI gate：

- 空白 Chat：setup 场景验证大桌面、常规桌面、iPad 横竖屏与窄屏短窗口中标题与输入区作为整体位于可视中部、标题在中线之上、输入区位于标题下方 24–28px、互不遮挡且无页面横向溢出；空间不足或草稿增长时仍可正常滚动到输入区，同时保留增强前的草稿、附件、焦点与选区。
- Tasks 共享列表：taskNavigation 场景在两类列表的明暗／桌面／窄屏检查同一标题、搜索、导航接缝及紧凑行高；搜索／筛选与原生详情路径保持，当前页计数排除被去重的计划任务。

- 原生设置：模型来源 → 保存 → 选用时自动校验并明确授权 Select → Conversation；候选 probe 与活动状态分开；ChatGPT 登录、可见目录、重启和退出仍经真实 Manager/Bridge（外部 OAuth 为受控 peer）。通知保存后停留原页，重载仍选实际目的地。
- 首次设置浏览器路径：在 Preferences 从中文切回英文，经 Chat 返回对话，确认英文保留且未就绪时无法发送，再通过 Set up Steward 连接、验证并显式选用模型。标题、提示密度、设置控件尺寸与登录步骤数量随界面改动在桌面和窄屏验收。
- 原生输入：延迟脚本时编辑、选文件与打开 picker，增强后保持同一节点/文件；拒绝或丢失确认不自动重发并保留草稿和附件；任务上下文可移除而不丢输入。
- Tasks：列表筛选、分页、详情目标/阶段、对应的冻结审批、已由 Main 交付的结果，以及带任务上下文回到 Main；不能用 Worker 结果替代 Main 交付。详情和 Conversation 保留同一审批 hash。
- Developer：列表打开独立 Trace 详情，树与时间线对齐；节点类型决定可用 Tab；完整 Context/JSON/来源/长正文和 Diff 深链接可达，原生链接、移动端与无 JavaScript 路径继续可用。
- 读取边界：真实 SQLite 验证跨 20 条限制、同时间稳定游标、scope 拒绝、归属校验与只读池；Web 审批过期投影不写库/事件，原运行时恢复仍落库。Trace 详情不能依赖全局列表成功。
- 模块测试：notification 持久化选择、候选 proof version、登录本地返回、Host 审批单卡片/用户文本不改写、任务消息通过既有 ingress；`make test lint` 检查所有包及嵌入资源一致。

系统断言采用受控模型/HTTP 响应，证明产品链路与回归，不证明真实模型质量或真实账户可用性。执行结果与截图留在对应运行的 evidence，不把测试计划写成执行报告。

首次设置界面通过现有浏览器和 Web 组件场景检查以下路径。

- 在没有模型且没有工作待办时，配置提示有可达入口，待办计数不包含配置项。已有工作待办在不同就绪状态下仍可见，系统操作按当前语言翻译，任务标题保留原文。
- 检查 Preferences 与 Models 各有一个可见的页面标识，帮助可通过键盘访问。Chat 未就绪时保留一个清楚的设置入口。桌面与窄屏的视觉验收核对标题、输入区尺寸和提示密度。
- 从 Chat 进入 Models，直接看到模型状态和连接入口。连接、验证和选择模型期间均保持 Models 标签选中。选择模型连接方式后在当前页显示对应配置。点击一次 ChatGPT 登录操作即发起授权，浏览器未打开时仍能手动继续。


## F-020 自建应用凭据与失效诊断

- `internal/interaction/feishu/auth_test.go`：真实loopback HTTP验证并发单飞、提前到期刷新、旧响应不能失效新token、取消等待者、临时刷新失败与恢复、过期token不回退、关闭中迟到结果、异常TTL/缺失字段、密钥不回显、重定向不泄密、仅只读探测可重查。
- `internal/config/channels_test.go` 与既有CLI语法回归：凭据模式二选一、SecretStore引用、受限端点与 `channel status/check`。
- 既有 `verify_channels.py` 的 `feishu_auth_lifecycle`：无需浏览器，以真实CLI/Actor/Delivery和受控飞书HTTP peer验证配置检查、token拒绝后的持久安全重试、过期主动刷新、无效凭据的安全诊断与持久失败码、撤销绑定、凭据不落盘，以及已发生副作用但回执丢失后重启仍不重发。该场景接入现有channels门禁，不新增脚本或CI阶段。
- 真实飞书自建应用、实际账户权限和公网回调配置需独立Live验证；软件peer不证明真实平台授权、模型效果或永久 exactly-once。

Steward 重构检查补充：Chat 的 Computer/Worker 只读详情与真实执行状态一致；明暗切换、头像暂停、移动抽屉与键盘焦点恢复；发送、附件、审批冻结哈希、旧消息定位和 live 更新沿用本页原有场景。

界面语言的默认、持久化、认证/CSRF、实时片段及冻结审批内容回归由 `internal/interaction/web/locale_test.go` 覆盖；中英文与移动浏览器检查见[品牌测试方案](branding.md)。


工作区精修：updates_test 验证待审批计数包含内嵌卡片且不重复、解决后清空；computer_test 验证 status/frame 的认证、绑定撤销、身份裁剪与 no-store。渠道浏览器继续覆盖真实草稿、附件、审批、背景更新和最新消息入口；Task 读取维持独立于 Conversation，Worker 采用有界 Inspector 结果。执行报告保存在 evidence，不据软件 peer 宣称真实 Guest 画面兼容性。

Chat / Worker：`workers_test.go` 验证认证与绑定撤销、只读字段和面板仅属于 Chat；`observability/sqlite/inspection_test.go` 验证真实只读数据库中的运行时间排序、50 条上限、运行中查询和 Agent 隔离。`channels_browser.mjs` 的 live 场景通过真实 Web 验证 Computer/Worker 互斥、焦点返回和草稿/附件节点保留。

本机免 token：`server_test.go` 通过真实 loopback HTTP 验证无 SecretStore 时直接进入页面及只读 API、同源设置表单、绑定撤销；拒绝远端 peer、跨站／同站不同 origin、伪造 Host 与转发身份。显式 token 模式的原有认证、CSRF、session 过期回归继续执行；配置检查覆盖可省略 token 与非法引用。

待确认入口沿用 live 浏览器场景：真实审批到来时，入口按 Computer → Worker → 待确认排列，点击聚焦原审批；冻结 hash、未发草稿和附件仍保留，解决后入口消失。顶栏预留三行，计数变化不改变对话偏移。

布局与焦点回归沿用同一个 `channels_browser.mjs`：inspection 在 851／900px 断言详情完整堆叠，Enter 选择 Generation 后焦点仍位于对应树行；live 在 820／900px 验证任务正文与进度值没有压缩为零宽；共用移动检查通过键盘打开／关闭导航、320px 当前设置标签露出及 1001px Computer modal。原有草稿、附件、审批、滚动、会话恢复与无 JavaScript 检查继续执行。人工补充检查明暗主题、模型／通知／六类能力详情、长内容和宽屏恢复；模拟视口不等同于物理触摸或屏幕阅读器验证。


### 设置页视觉与控件回归

channels gate 的 `default_capability_settings` 使用真实 HTTP、受控 SecretStore 和 Chromium 验证五个设置分类直接可见且无导航搜索、两项语言下拉无搜索、紧凑尺寸、原生方向键／Enter、点击外部与 Escape 关闭、外观保存与刷新、默认 Memory 单入口、自动／自定义字段切换、390px 导航及无 JS 语言保存。输出偏好、下拉、通知、模型输入、能力和明暗／移动端截图。`account_presentation` 经原有真实 Manager/Bridge 和自有 OAuth fixture 验证账户退出确认：取消和 Escape 零 POST、取消焦点归还、确认一次 POST；原有凭据扫描与重启校验保留。


两级导航回归复用 `default_capability_settings`：从 Chat 点击左下 Settings，断言一级入口次序／位置不变、二级栏位于一级与内容之间；切换 Dev → Database 复用同一二级栏，Tasks 不创建空分类；显式折叠后可原页展开。390px 抽屉同时保留两级选中态且不越界，Tab 在整个抽屉内循环，Escape 归还焦点；保留无 JS 表单验证。完整 channels gate 继续覆盖 Developer、数据库导航、320px 布局与聊天面板。


侧栏清理与个人偏好：默认设置场景验证一级栏没有宣传语或重复偏好按钮；在 Preferences 开关头像动效，验证返回 Chat、刷新后的持久化和系统 reduced-motion 优先。原头像完整帧与问候检查继续在维护中的浏览器驱动运行。公共设计规范入口为 [design.md](../../design.md)。


`make test-browser` 的 Apps 真实服务场景同时调用 `channels_capabilities.mjs` 的 `settingsLayout`，覆盖五个 Settings 分类及 Memory 详情：2048、1400、1024、761、760、390、320px，中英文、明暗主题，逐页比较内容左右边界、标题起点和 Language／Appearance／通知／能力模式的选择器宽度、高度与尾部对齐，并检查溢出。另检查折叠导航、无脚本原生选择器及语言提交入口；输出几何数据及桌面／移动截图。使用临时数据根目录，不接触实际账户配置；沿用 required browser CI，无需新增重复 job。

### 设计规范的自动回归

`channels_capabilities.mjs` 的同一默认设置场景在 320、390、760、761、1024、1400px 运行导航检查：一级／二级的空间关系、正文避让、底部 Settings、两级选中态、侧栏仅有折叠控制、Escape 焦点和 inert 恢复。输出 `navigation-viewports.json`；失败消息包含当次宽度和几何信息，不以完整 HTML 或像素快照锁死实现。系统 reduced-motion 从开启到关闭反复切换后，用户保存的头像暂停选择仍需保留。

[PR CI](../../.github/workflows/ci.yml) → `make test-e2e` → [core](../../tests/system/runner.py) → `channels` → `default_capability_settings` 是现有执行链；原有 `make test lint` 检查构建资源一致性。无需另建相同 job。公共规则与人工视觉评审边界见 [design.md](../../design.md#自动回归与评审分工)。

Settings 切页回归复用 `page_transitions`：真实点击 Preferences／Notifications，逐帧检查字体就绪与字标宽度稳定，并读取 Resource Timing 确认关键字体来自缓存。连接页检查账户信息不重复、图片开关／技术性选用说明／许可证页脚不存在；桌面、320px、明暗主题检查按钮无重叠。`settings_account` 验证首个账户模型添加后 ready，OAuth peer 的生成请求计数仍为零，重新登录后直接选用而无需测试生成。

Chat 输入框焦点回归在真实浏览器中检查鼠标点击和 Tab 导航：容器不出现向外扩张的 outline，焦点仍通过既有边界的颜色／内侧描边呈现，明暗主题和窄屏一致。

组件场景通过真实交付投影验证 Markdown 加粗、危险协议／HTML 拒绝、认证 artifact 字节与下载头、Mermaid 图像实际可解码、明暗 favicon 与固定 Computer/Worker 几何。page-transitions 检查 64px 折叠图标栏和脚本前后布局；主动阻塞主脚本时，导航按钮须保持禁用，释放后桌面／手机都必须首次点击即可切换并恢复；capability-setup 检查自动保存、失败草稿、六类字段与 MCP 权限、无配置文档暴露、原生无脚本提交及明暗／窄屏几何。

`send-feedback` 使用实际 HTTP/Actor，分别阻塞发送、丢失已提交回执和拒绝请求，检查立即出泡、输入区释放、附件／后续草稿保留、重试复用来源键和权威消息去重；明暗白字对比度与移动布局一并测量。rich-replies 检查原生 UI 在前、最终文字独立气泡，以及原表单身份、重试、重启和无脚本路线。`TestInboundAdmissionDoesNotWaitForCredentialIO` 检查接纳不读取模型凭据，执行就绪仍须认证；`TestMainUIWidgetUsesCodeModeAndDurableReply` 检查分组不修改冻结投影。

全局组件回归使用可见 Base UI 控件选择，直接读取原生字段验证提交值。覆盖 Settings 语言搜索/键盘/取消/单次保存、主题立即应用、Task 依赖字段与显式保存、Dev 筛选和富组件弹窗内的选择器/菜单；无脚本与初始化失败继续操作原生表单。保持原有字体、导航和布局几何检查，不依赖组件库生成的内部 ID。

富组件交互 gate 用实际 Main 交付的 14 项表单选项验证原生 Combobox 搜索、空结果和弹窗内选择；短列表与语言选择保持原生 Select，无额外搜索区域。

Settings 行布局回归检查通知标签与控件同行、尾部命令右对齐及同级宽度一致，覆盖桌面／窄屏与深浅主题。输入框鼠标和键盘聚焦不得产生内外双框。Apps 目录与详情保持原布局。账户场景验证首次 Web 登录即就绪、目录读取不创建配置、隐藏模型与陈旧配置版本拒绝、切换保留草稿附件、无脚本回退和退出权限。

Apps 协议回归覆盖无托管 OAuth 且未配置的具体提示、自定义 OAuth 的正常连接、元数据缺省及受限 API Key 无配置读取权限时保留链接流程；失败不创建本地连接、不泄露凭据。

通知目的地选择后自动保存，携带版本并更新确认版本；冲突／写入结果未知不自动重发，刷新读取实际状态。无脚本保留原生提交。Preferences 同组短值选择器等宽，Models 按 Models 归组两类模型连接，App service 使用键值行和右对齐同宽操作；Apps 目录布局不变。相关回归由 settings_test、channels_preferences 和 page-transitions 覆盖。

Models 为 Settings 的独立分类，包含「Sign in with an account」和「Connect with an API key」。账户目录自动进入聊天模型选择器；API 连接使用现有私有文件 SecretStore，凭据与首个模型元数据一次事务保存，失败清理新文件。连接后自动验证；首次无当前模型自动选用，后续连接不覆盖选择。连接操作复用共享菜单和原生表单，断开采用模型及凭据双版本确认并撤销后续使用，保留历史冻结配置。Apps 工具栏 App settings 进入服务配置；原 Models/Resources 服务入口移除，旧 setup 与 services/apps 深链接保留重定向。浏览器回归覆盖实际 HTTP provider 的 API key、聊天模型切换、断开及明暗／窄屏布局。

确定性浏览器 fixture 不继承宿主的 Composio 部署凭据，避免未配置目录场景访问真实服务；显式协议 peer 测试自行配置。原生弹窗关闭后的焦点恢复在 close 事件完成后断言，不能用立即采样代替状态等待。


消息及列表时区回归：`web/time_test.go` 验证真实页面／片段使用浏览器时区、跨天、明确偏移与无脚本筛选同一时刻、无效 cookie 回退，以及请求间隔离；`web/local-time.test.mjs` 覆盖当地日期、非整小时偏移、夏令时重复时间、午夜和隐藏时区／毫秒文案。`channels_browser.mjs` 的 live 场景在 Asia/Shanghai 中检查消息时间、hover／键盘无布局位移、手机常驻及明暗截图；native-trace 场景验证列表的当地时分秒、无时区文案、当地筛选提交的精确时刻，以及点击普通单元格打开详情侧栏。原有 live 测试继续检查草稿、附件与富组件实例不被时间增强替换。


Trace 阅读与筛选回归：`traces_test.go` 的分页请求读取测试验证完整 JSON 组装及安全原始回退；`agent/sqlite/traces_test.go` 使用真实数据库验证 TTFT 含重试、缺失证据与 HTTP 阶段区别。`web/trace-request.test.mjs` 验证转义、多行、重复键与精度回退；日期 UT 验证当地跨天、星期和旧日期 yyyy-MM-dd。native-trace 浏览器场景在同一抽屉中切换 Context 消息与 JSON 格式、核对结构化 Requests 与原始 JSON；Memory 与 Traces 场景直接选择筛选并核对 URL／结果，无需 Apply filters。共享列表及设置导航场景核对一级／二级宽度一致，保留明暗、宽窄屏截图及无脚本路线。

HTTP 浏览器兼容：`web/random-id.test.mjs` 覆盖原生 UUID、仅有 `getRandomValues` 时的 UUIDv4 格式／版本／variant 及加密随机源失败；沿用 required frontend `npm test`。HTTP 局域网部署验证实际非安全来源下的聊天发送标识轮换、模型选择、五个 Settings 页面及同源表单；不向真实模型发送测试消息。

Trace 列表回归验证状态前置、Name／Input 合并、无伪排序箭头、费用无逐行 Estimated，以及 v2/v3 列偏好迁移。native-trace 浏览器验证动态抽屉完整 Run ID 键盘复制、Clipboard API 缺失／拒绝、原生复制失败后的完整值选中回退与焦点恢复。`web/traces_test.go` 验证 ID 转义与独立导航；`tool_preview_test.go` 验证工具传输展开边界、精度与原始证据保持；`observability/sqlite/tool_generation_test.go` 验证工具内模型节点归属、用途与跨 Run 隔离。
