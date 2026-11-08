# 终端、Web 与飞书

从多个渠道进入同一对话，并将回复发送到冻结目标。

[需求主索引](../feature-map.md) · [技术方案](../designs/channels.md) · [测试方案](../test-plans/channels.md)

## 功能说明

用户能完成模型设置、交代工作、查历史和状态。

用户能登录本地 Web，使用 Conversation、Tasks、Settings、附件和审批。

用户阅读或编辑草稿时仍能接收状态和回复更新，且草稿不会丢失。

已绑定个人身份能接收消息、附件和审批，并维持长期可用。

本文描述用户行为和范围；逐项完成状态以主索引为准。当前能力依据代码静态盘点，配置可用性与真实服务/模型质量需分别验证。

## 需求与验收

<a id="f-017"></a>

### F-017 · 终端交互

**需求**：用户能完成模型设置、交代工作、查历史和状态。

**当前实现**：标准启动/TUI/CLI、输入与已交付回复投影已接通。

**边界与剩余工作**：终端输出没有远端确认协议，不宣称屏幕显示 exactly-once。

**验收目标**：验证真实终端首条输入、后台工作时追加、退出和重启。

<a id="f-018"></a>

### F-018 · 基础 Web 工作台

**需求**：用户能登录本地 Web，使用 Conversation、Tasks、Settings、附件和审批，优先定位待回答、批准、审阅或修复的事项，并找到相应任务与下一步。

**当前实现**：独立 Conversation、Tasks 列表/详情、分层 Settings 和 Developer Trace 列表/详情已接通，Tasks 提供 Needs me 筛选，并在列表首部优先展示具名问题、确切成果审阅、冻结审批与当前可处理故障。Chat 的 Set up Steward 进入 Models，直接查看和管理当前与已保存的模型。模型配置按来源连接；首次连接自动选用，ChatGPT 目录模型无需额外生成测试，自定义端点仍须验证；通知设置独立，认证、附件、冻结审批、原生表单和语义 fallback 保留。

Settings 原生切页预加载并复用版本化界面／字标字体，避免左侧导航先显示系统字体再切换产生闪跳。导航展开／折叠按钮在脚本事件绑定完成后才可操作，慢加载时不会接收无效点击；脚本下载失败仍保留原生导航。语言下拉只有两项，不提供搜索，使用紧凑的原生组件交互；长列表按统一规范提供筛选。模型连接页合并账户信息与操作区，不显示图片开关、技术性选用说明或许可证页脚；许可证文件继续随构建保留。

Web 消息输入框支持 Enter 发送、Shift + Enter 换行；输入法选词时的 Enter 不发送。发送按钮使用 `#B84F2F` 橙色，明暗主题一致。输入框聚焦时只加强原有圆角边界，不在容器外再画一圈。模型未就绪或正在提交时禁止重复发送；无 JavaScript 时仍可点击按钮提交。

**边界**：待办来自 F-004 的当前记录、冻结审批和 Task 执行事实；已完成 Task 的当前成果审阅也会显示。复用现有导航，依据权威状态展示等待谁、需要什么，不从模型进度文本推测；普通健康状态保持安静，计数与明细一致。处理动作经现有 Main、审批或设置入口，不绕过任务控制。实时区域刷新见 F-019，开发区由 F-031 跟踪。

**验收目标**：真实浏览器验证设置、发消息、附件、审批和对话展示；各类事项可正确定位并处理，刷新后计数与明细收敛且无重复项，已处理或过期事项不继续请求行动；后台更新保持草稿、附件、焦点与阅读位置，断线与陈旧页面不误报完成。

<a id="f-019"></a>

### F-019 · Web 实时状态与回复更新

**需求**：模型还在生成时就能在 Web 看到回复；用户仍可阅读、输入或选文件。

**当前实现**：最新 Conversation 逐段显示 Main 的临时文本；正式回复提交后，原有消息组件显示完整结果和交互控件。点击发送后立即显示待发送气泡并清空输入区，保留当前页面和连接。已提交会话、审批、工作状态和任务摘要继续更新各自区域；读旧消息时保留阅读位置。

**边界**：回复草稿可能因工具调用、失败、取消或新输入取代而撤下，不进历史、不投递。无关工具参数、Worker 输出和上下文压缩内容不进入 Web 流；Intelligent UI 仅发送受控的临时展示预览，正式消息继续使用已绑定的冻结产物。断线后重新读取当前预览与已提交状态；隐藏页面暂停。会话或绑定失效时保留用户草稿，支持另页登录与显式重试。任务阶段摘要由 [F-005](tasks.md#f-005) 的 Root 进度链路生产。

**验收目标**：模型停在中间时，Web 已能显示先前片段；正式消息只出现一次。断流没有半条正式消息；焦点、选区、草稿及附件不被后台更新覆盖。

<a id="f-020"></a>

### F-020 · 可维护的飞书交互与通知

**需求**：已绑定个人身份能长期接收消息、附件和审批；用户在原工作话题收到适合该渠道的结果，详细成果与简短通知相互关联，减少重复打扰。

**当前实现**：签名回调、p2p 身份绑定、收发和本地回执已实现。自建应用可通过 SecretStore 中的 app secret 自动换取 tenant access token，按有效期提前刷新，并发请求共用刷新结果；CLI 可检查连接与凭据失效状态，也可显式选择外部维护 access token。新接收的原消息来源随路由冻结，同步回复和自动偏好下的后台 Task 结果引用该原消息；不同来源的 Task 结果经 Main 分组交付。个人飞书消息引出的具名 Task 问题和成果审阅可通过原消息的结构化卡提交。配置可信工作台地址后，IM 提供简短预览与完整回复/文件入口，完整语义和产物保存在原工作台，不额外上传重复详情。

**边界与剩余工作**：当前支持配置型自建单租户应用与已绑定个人 p2p 渠道，不包含群聊或应用商店多租户授权。原消息关联限定于新接收且已绑定的个人消息；旧接受路由不回填，未找到明确来源的任务保留既有通知偏好，不猜测关联。F-042/F-004 的显式 GitHub PR 接续复用同一 Task 原消息交付；其他来源的跨平台成果组织、多渠道事件归并或远端权威回执查询待补。无可信身份/目的地关联不跨渠道发送，目的地失效不得擅自扩大受众；沿用显式通知偏好、冻结路由与审批。未知投递仅查询已存本地回执，不通过重发消除未知。真实飞书展示、应用权限、工作台可达性与模型语义简洁度仍需 Live/质量验证。

**验收目标**：真实绑定、token 生命周期、通知偏好、撤销、重启与不确定投递正确；结果发送到原话题并可追溯对应成果，多渠道重试/重启不重复产生通知意图，目标平台已可见的详情不机械重复到 IM；目的地失效不扩大受众，未知发送结果不盲目重发。

## 如何使用

标准 `steward` 使用终端；配置 Web 本地监听与运行时后执行 `steward web serve`，使用其打印的本地地址。默认本机直接进入；`token_ref` 是可选登录保护。

飞书需配置应用凭据、回调入口，并通过可信 CLI `steward channel manage` 绑定个人身份；群聊不在当前范围。Settings → Notifications 可选择未来通知渠道，保存后显示实际选择；原目的地不可用时显式提示，不回退成 Automatic。已提交的消息、审批、任务阶段与状态按区域更新。

<a id="feishu-task-questions"></a>

### 在飞书回答问题与审阅成果（F-004/F-020）

在现有自建应用订阅新版 `card.action.trigger`，使用与消息回调相同的 `/feishu/callback` 及签名配置。Main 从已绑定的个人消息保存具体问题后，在 Automatic 或显式选择同一飞书绑定时，Steward 引用原消息发送一张问题卡。填写答案并点击 **Send answer**。保存提示表示答案已进入 Main，后台 Task 接续及外部动作仍由原有规则决定。 [飞书卡片回传交互](https://open.feishu.cn/document/feishu-cards/card-callback-communication)

相同答案可在飞书、CLI 和 Web 重试，首次保存决定同步回复渠道。转发副本、过期问题、暂停/取消/人工结束、Forget 或绑定撤销后的卡会拒绝提交。其他通知偏好和不明来源的问题留在 CLI/Web。配置可信的 `feishu.workspace_url` 后，符合相同路由条件的成果审阅也会收到卡片。通过 **Open result in Steward** 登录工作台，打开确切的 Task 审阅和对应文件，再提交 **Send review feedback**。工作台地址缺失时可在 CLI/Web 审阅。卡片只显示审阅说明与工作台入口，文件和详情由认证页面提供。真实飞书账户上的展示和权限需要单独验证。

### Web 待办与回答（F-018/F-004）

Tasks 顶部的 Needs your attention 优先呈现已保存的问题、成果审阅、具体动作审批和需要讨论下一步的失败/未知结果。每条问题或成果可进入对应 Task；成果链接指向确切文件，关联消息可回看原对话。任务状态不决定审阅是否完成：已 completed 的 Task 仍可保留当前审阅。

Task 详情中的 Answer question / Review result 表单将回答作为已认证 Web 用户输入交给 Main。回答不直接重启 Task，成果审阅不批准外部动作；冻结审批仍使用原参数和哈希。相同回答跨 Web 页面或 CLI 重试返回原回执，首次接纳的渠道和回复目标不因重试而改变。暂停、取消、Forget、新修订或过期后的旧回答被明确拒绝。

Chat 右上角提供统一待办入口；问题、审阅或故障在标注的新标签页中打开，原对话的草稿和附件保留。只有动作审批时继续保留 Chat 内的原审批定位。当前页数量由同一呈现投影计算，存在后续页面时用 `+` 和更多入口说明范围，不把第一页说成全部待办。

表单不随普通状态更新重建。另一端回答或任务失效使当前表单消失时，非空本地输入保留为明确未提交、只读且可复制的草稿；它不计入可操作事项。已知过长/无效输入提示先修改，断线或确认丢失保留草稿并读取当前状态，不自动重发。原生表单保留无 JavaScript 的提交与错误恢复路线。

### 飞书凭据与连接检查（F-020）

自建应用可选择自动维护 tenant access token：

```yaml
feishu:
  app_id: your-app-id
  tenant_key: your-tenant-key
  verification_token_ref: env:STEWARD_FEISHU_VERIFICATION_TOKEN
  encrypt_key_ref: env:STEWARD_FEISHU_ENCRYPT_KEY
  app_secret_ref: env:STEWARD_FEISHU_APP_SECRET
```

这里只填写 SecretStore 引用，实际密钥不进入 YAML。`app_secret_ref` 与 `access_token_ref` 必须且只能配置一个。原有外部 token 方式仍可用；新配置在重启后生效，不改变已绑定的身份或通知目标。缓存只存在于进程内，不向领域数据库保存 access token。

```sh
steward -config config.yaml channel status
steward -config config.yaml channel check
```

`status` 只显示配置模式，不发网络请求；新 CLI 进程显示 `unchecked` 是正常情况。`check` 按需取得 token 并读取机器人的自身信息，不发送消息、不修改绑定或处理未知投递。无需先设置活动模型。结果以 `state` 为准：`ready`、`credentials_unavailable`、`credentials_rejected`、`unavailable` 或 `not_configured`；失败时附安全的 `action`，不回显密钥或供应商原始响应。`ready` 证明检查当时机器人信息接口鉴权成功，不保证全部业务 API 权限。

自动模式在到期前刷新；显式 token 拒绝会使对应缓存失效，下一次由原 Delivery 队列调度的安全尝试使用新 token。临时取得凭据失败允许既有有限退避；应用凭据被明确拒绝会终止该次投递，并保存 `credentials_rejected`。已经发出的消息若超时、断连或缺少有效回执，仍是 `unknown`，重启和凭据刷新都不会授权重发。修正凭据后先 `channel check`，再通过正常对话继续工作；不把旧未知消息当作未发送。

### 飞书原消息回复与成果链接（F-020）

新飞书输入的同步答复引用已验证的原消息；后台 Task 完成后仍先经 Main 形成语义交付。在通知偏好为 Automatic 时，Task 结果回到发起该任务的原绑定和消息；显式选中的其他渠道优先，不同时再向 IM 复制一份。审批通知仍保留完整冻结参数，不能用摘要替代授权。

可选配置：

```yaml
feishu:
  # 与上述必需应用/SecretStore 配置合并。
  workspace_url: http://127.0.0.1:8787
```

`workspace_url` 是操作员确认的工作台 origin，只接受 HTTPS 或 literal loopback HTTP，不含账号、密码、路径、查询参数或 fragment。它不部署工作台、不改变监听地址或权限，不携带登录 token。loopback 链接只适合同一电脑，飞书手机或其他设备不能据此访问；远程地址需操作员已有的 HTTPS 部署与访问控制。点击后仍经过现有 Web 身份检查。

配置后普通结果显示最多 1200 个字符的确定性预览和 **Open full response and files**，链接直接定位保存的完整 Conversation 消息与附件。它不是另一次模型摘要，不删除完整语义。已有可访问文件保留在工作台，不机械上传到 IM；未配置地址时保持完整卡片和原附件投递。链接、卡片和原消息目标在第一次传输准备时持久冻结，改配置或重启不会重定向同一交付。原消息不可回复或发送结果未知时，不降级到别的收件人或顶层新消息。

### Web 操作路线

| 分区 | 入口与主要操作 | 下一步 |
| --- | --- | --- |
| Conversation | `/` 输入消息、添加/移除附件、确认具体审批；历史使用 Earlier messages | 后台工作在 Tasks 跟踪；缺少可用模型时进入设置 |
| Tasks | `/tasks` 按 Open / Needs me / Finished / All tasks 筛选、搜索与翻页 | 点击任务查看目标、当前阶段、审批、Main 交付、附件与历史；返回原始请求或带任务上下文继续对话 |
| Settings → Models | `/settings/models` 分组管理账户与 API 连接 | 选择账户提供方并授权，或填写 API 连接并自动校验；在 Chat 输入区选择模型后开始对话 |
| Settings → Notifications | `/settings/notifications` 显示已保存的通知目的地 | 选择后自动保存未来通知偏好；不迁移已冻结交付 |
| Developer → Traces | `/developer/observability` 筛选执行列表 | 打开独立 Trace 详情，选择树/时间线节点，检查 Context、Requests 或相邻轮次 Compare |

Task 详情只展示该任务已由 Main 交付的内容，不将 Worker 结果冒充最终回复。Continue in conversation 会带上可见且可移除的任务上下文，再交给 Main 理解要求；页面不直接调用 Task 控制。审批只在每个页面显示一次主要操作，完整参数和原冻结哈希保留；不要求 Web 用户复制 CLI 命令。

登录后回到原本的本地子页面。设置失败在对应页显示安全错误并保留可恢复字段；增强后的发送失败保留草稿和附件，不自动重发。没有 JavaScript 仍能通过原生链接和表单完成主要路线。`/settings`、`/progress` 和旧开发者深链接跳转到新页面。

## 详细操作参考

- [渠道接入详情](#channels-web-feishu-and-safe-delivery)
- [安装与部署详情](installation.md)
- [交付与回执协议](../designs/channels.md)

## 操作说明与配置细节

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="channels-web-feishu-and-safe-delivery"></a>

## Web, Feishu and safe delivery

Authenticated interaction and rendering use the existing
`InboundEvent -> Actor -> Main/Worker -> ToolInvocation -> Conversation ->
DeliveryRequest/Outbox -> Adapter` path. There is one Conversation per Agent.
No channel creates its own reasoning loop, Task state machine or approval store.

<a id="channels-launch-and-identity-enrollment"></a>

### Launch and identity enrollment

Configure the real Provider Bridge as in `config.example.yaml`. Without an
active, tested profile the Web setup page remains available and ingress stays
`setup_required`. Neither startup nor a missing credential selects a Fake.

```yaml
web:
  listen: 127.0.0.1:8787
  # Optional: token_ref: env:STEWARD_WEB_TOKEN
feishu:
  app_id: your-app-id
  tenant_key: your-tenant-key
  verification_token_ref: env:STEWARD_FEISHU_VERIFICATION_TOKEN
  encrypt_key_ref: env:STEWARD_FEISHU_ENCRYPT_KEY
  access_token_ref: env:STEWARD_FEISHU_ACCESS_TOKEN
```

Web opens directly for a loopback client when `token_ref` is omitted; no workspace token or session is required. An explicit `token_ref` opts into the existing token/session login, with tokens of at least 24 bytes. Credential values are SecretStore references, never literal tokens. Set credentials through the installation's credential store
or its explicit environment-reference backend. Feishu's configured access token
must remain valid; rotation/refresh belongs to the credential owner. A missing
or expired credential never falls back to another account or recipient.

The installation owner enrolls the exact Feishu tenant/app/open ID and p2p chat
through the OS-authenticated CLI. A callback cannot enroll itself. Group chats
and bot senders are rejected to avoid broadening access to the personal history.

```sh
printf '%s' '{"op":"bind","channel":"feishu","subject":"your-tenant-key:your-app-id:your-open-id","destination":"your-chat-id","enabled":true,"version":0}' | bin/steward -config /absolute/config.yaml channel manage
bin/steward -config /absolute/config.yaml web serve
```

`web serve` prints its loopback URL, enrolls the local browser identity once,
and runs in the foreground until SIGINT/SIGTERM. It owns HTTP ingress, scans the
same Actor/Automation claims and independently drains the same Delivery Outbox,
so one busy Worker cannot delay another Run’s approval notice. It joins its
work on shutdown. An explicitly revoked Web binding stays revoked on restart.
`channel manage` also accepts `list`, `preference`, `prefer` and versioned `bind`
updates. Set `enabled:false` with the current binding version to revoke access.
There is no destination migration: enroll a new destination and select it for
future work. Frozen deliveries keep their original destination. A changed Feishu app/tenant
cannot acquire an old binding’s pending delivery.

When `token_ref` is configured, Web uses a process-local, one-hour HttpOnly/SameSite=Strict session after token
login, or a Bearer token for API access. It rejects DNS rebinding, cross-site
POSTs and invalid identities, provides no CORS access, and serves a restrictive
CSP. Auth URLs and device-code events stay ephemeral and are visible only in the
authenticated settings surface. Account login, reconnect and logout use the existing Manager/Bridge
lifecycle. The settings picker retains connected account metadata across restart.
Account model additions use its authenticated visible-model catalog. Credentials never enter Conversation, projections or evidence.

<a id="channels-user-surfaces-and-callback-contract"></a>

### User surfaces and callback contract

- `/`: shared semantic conversation, attachments and pending frozen approvals.
- `/settings/models`, `/settings/models/new`, `/settings/models/{profile}`: current
  model, source choice and focused configuration/verification/explicit activation.
  `/settings/notifications` owns future notification preference.
- `/tasks`, `/tasks/{taskRef}`: bounded, searchable Task workspace and read-only
  details including Main delivery and approvals. Pause/continue/cancel stays in
  conversation; no raw Tool, model, Run ID or Child trace in everyday pages.
- `GET /api/conversation`, `/api/progress`, `/api/status`, `/api/auth`: authenticated
  projections. `POST /api/message` accepts only `source_id` and semantic `content`.
  `POST /upload` stores a local Artifact; `/artifact?ref=...` resolves only through
  the authenticated owner scope and downloads as an attachment, never active HTML.
- `POST /feishu/callback`: Feishu v2 `im.message.receive_v1`, signed exact bytes,
  five-minute timestamp freshness, configured token/app/tenant and bound owner/chat.
  AES-256-CBC encrypted envelopes are supported. URL verification may omit signature
  headers and must authenticate with the configured verification token. It creates
  no input, Run or binding. Publish only this callback through a TLS reverse proxy;
  keep the Web control surface on loopback.

Feishu text and file/image messages normalize to the same semantic ContentParts
as Web/TUI. Downloaded resources are tied to the verified source message. Feishu
approval responses use `/approve APPROVAL_ID FROZEN_HASH` or `/deny ...`; Web uses
controlled confirmation forms. Both use the same frozen-hash Security transition.
Duplicate decisions are idempotent; conflicting, invalidated or expired approvals
fail closed. Steering invalidates old approval arguments before dispatch.

Normal synchronous replies return to the latest input's bound channel. TUI input
remains TUI even if earlier Steering came from Feishu. A Main reply without user
input, Worker approval, completion, failure or needs-input notice uses the future
notification preference. `auto` prefers the first enabled Feishu binding, then
falls back to TUI. Multiple enrolled destinations can be selected explicitly in
settings. Ordinary progress never creates a notification. All routes freeze with
the semantic intent, before the first DeliveryRequest; preference changes cannot
redirect accepted messages. Revocation rejects pending deliveries to that binding.

<a id="delivery-commands-and-operations"></a>

### Commands and operations

```sh
bin/steward delivery list  # retained requests, including unknown/failed
bin/steward delivery work  # one eligible transport/reconciliation scan
bin/steward chat work      # drain Agent work, then delivery
bin/steward tui            # live delivery polling and /history
```

Delivery work runs even with `setup_required`; it does not activate an implicit
provider. TUI polls persisted transport work every 250 ms, including after its
Agent drain becomes idle. Semantic `chat history` remains independent of send
status. It can fill Conversation intents, but does not claim channel acceptance.

`config.example.yaml` documents `delivery.lease_seconds` (1–300),
`retry_milliseconds` (1–60000), and `max_attempts` (1–20). Defaults are 5 seconds,
250 ms, and five attempts. Known transient failures use bounded exponential
backoff. Failed requests remain inspectable; there is no automatic resend or
delete command. Unknown rows retain their uncertainty and reconcile by query.
Future remote adapters must supply their own truthful idempotency/query contract.

State migration v12 preserves v11 requests, keys, status, attempts and history;
it renames the old local channel `local_cli` to `tui` and creates outbox rows only
for pending requests. Prior sent messages remain visible as retained history,
without being resent. Empty/prior schemas and failed-DDL rollback are tested.
The terminal projection is part of state schema v12. Keep `state.db`,
`conversation.db`, and their SQLite WAL files together during operational backup.
New database files use mode 0600 in the private data root.

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/channels.md)；已有检查、未覆盖场景和验收方法见 [测试方案](../test-plans/channels.md)。

## Web 实时更新（F-019）

打开最新 Conversation 即可看到 Ghost 的回复草稿，完成后自动显示正式回复。Tasks 继续显示已提交进度，无需额外配置。阅读上方内容时，“New messages”只提示有新内容，点击后再定位到最新消息，草稿和附件仍保留。输入消息和选择文件不会停止后台更新；批准/拒绝具体动作也不会整页跳转。

短暂断网时页面提示重连，恢复后读取当前已提交状态。会话失效时，在提示中的 Sign in 打开另一页完成登录，再回到原页点击 Retry updates；原页保留的草稿和文件无需重填。Settings 只实时更新全局状态和登录过程，不在后台覆盖配置表单或更换活动模型。禁用 JavaScript 时仍提供原有语义 HTML 与表单提交。

### Steward Web 视觉重构

正式 Web 采用Steward 黑白界面：对话、任务、审批、成果、模型/账户、通知、能力与 Developer 继续使用既有实现；Chat 右上角按需读取真实 Worker 执行及 Computer 只读画面。没有引入多会话或独立文件库。品牌与兼容范围见[品牌功能](branding.md)。

Settings → Notifications 的自动渠道选项使用 Auto／自动，与能力配置一致；选中态表示当前选择，不附加默认或推荐标记。

Web 默认英文；Settings → Preferences → Language 确认简体中文后自动保存。设置组左侧为语言名称及问号，右侧为当前值与应用内列表；无脚本时保留原生选择器和保存按钮。刷新、跨页和实时界面片段使用已选语言，原始对话和交付内容不变。见[品牌与界面语言](branding.md)。


### Steward 工作区精修

一级导航上方保留 Chat、Tasks、Developer，Settings 固定左下；Settings 与 Developer 的内部分类在相邻二级栏展开，Task 当前筛选留在内容区。Chat 顶部居中两行显示 Agent 名称 Ghost 与动态头像，下方持续对话；空会话欢迎标题与输入框作为整体位于主内容区中部，文案在中线之上、输入框靠近中线，短窗口和长草稿允许正常滚动；右上角显示 Computer 和当前 Worker，点击分别打开互斥详情面板。Worker 包括正在执行和已经结束的记录，按实际开始时间（未开始则取创建时间）倒排，状态保持后端原值，不把 Worker 结果标为用户交付。Task 恢复独立列表与详情，不额外查询 Conversation。成果从任务详情及交付消息进入。

首页待审批提示与实际审批卡片使用同一份权威读取，覆盖已内嵌消息及独立待审批卡片，解决后同步移除。打开具体审批保留草稿；向上阅读可主动回到最新消息。当前提示仅覆盖已有审批，不宣称 F-004 的待回答、审阅闭环已实现。

Chat 右上角 Computer 可展开只读桌面画面与已提交的操作进度；打开且页面可见时约每两秒读取一次完成的画面，关闭/后台停止。查看不会启动或恢复 VM，不提供鼠标键盘接管；离线、暂停、画面不可用及中断都有明确状态。机制与兼容性边界见[Computer 设计](../designs/computer-use.md)。

Chat 的 Computer、Worker 与待确认入口依次在右上角上下排列；窄屏使用具名图标按钮，仍采用互斥详情面板。

四个一级入口共用 224px 主侧栏，Settings／Developer 的二级栏同为 224px；桌面收起主栏仅变为 64px 图标栏，不隐藏二级分类；手机继续使用整组抽屉。Settings 齿轮与任务配置滑杆采用不同图形。

任务与执行详情根据导航之后的实际内容宽度切换双列／上下布局。窗口小于 1280px 时，Computer／Worker 使用浮层，避免挤压对话与输入框。设置标签条自动露出当前栏目；能力配置示例放在编辑框之前，空白未配置时展开。键盘打开／关闭导航和切换执行树节点保留正确焦点。


### 设置页的共同交互

设置页保留一级导航，在相邻二级分类与居中单列中呈现偏好、通知、模型和能力。五个固定分类直接展示，不设导航搜索；支持下拉与键盘选择；输入、开关、按钮与分组行保持统一。界面外观可在偏好设置中选择浅色、深色或跟随系统，也可开关头像动效。一级侧栏底部只保留 Settings，公共规则见 [design.md](../../design.md)。退出模型账户前显示确认框，取消不改变连接；模型切换仍保留显式授权。详细说明和连接诊断按需展开，移动端和无 JavaScript 的原生表单可继续使用。

### Chat 呈现与设置保存

就绪的空白 Chat 将欢迎标题与输入框整体放在视口中部偏下，随高度伸缩；已有对话继续使用底部输入框。Tasks 的执行记录与任务配置采用和 Developer 相同的全宽紧凑列表、搜索、操作栏、状态样式及底部分页，当前页计数只统计实际显示的行。详情、编辑和审批保留原有页面。

用户消息显示右侧橙色气泡，Ghost 显示左侧主题灰色气泡；处理请求时只显示三点动效，状态名称保留为辅助标签。Web 安全渲染 Markdown、认证 artifact 下载链接与 Mermaid 图；源语义和无脚本回退保留。桌面折叠导航保留 64px 图标栏，Computer/Worker 固定在视口。标签页 Ghost 图标随页面深浅主题切换。

能力配置修改后自动校验并保存，失败保留草稿和重试；界面使用具名字段和 MCP 服务／工具行，不暴露配置文档、示例或宿主配置文件路径。无 JavaScript 时保留原生字段表单。默认安装启动已启用自动 Memory 与本地检索；显式运维配置仍保留其选择，自动加密密钥只由 SecretStore 管理。

### 消息与列表时间

聊天消息在气泡外下方提供时间，桌面悬停／键盘聚焦显示，触屏常驻；悬停、聚焦或点按可查看完整日期与时分秒。当天显示时间，昨天／明天与近六天采用自然日期／星期，更早使用 yyyy-MM-dd；跨天分隔按当前浏览器的当地日历日计算，时间只显示到秒，不附加时区名、偏移或毫秒。Conversation 的 CreatedAt 表示消息接纳／提交到语义历史的时间，不是对端已读或外部投递回执。

Tasks（含下一次计划发生时间）、Worker、Traces、Memory 及其详情使用同一浏览器时区，列表保留日期与时分秒，不显示时区文字。Traces 图表与时间筛选一致，筛选提交绝对时刻；原始 JSON、持久化时间与计划规则的执行时区保持原义。展示偏好保存在当前浏览器的时区 cookie，无脚本使用已有偏好；首次无法获知浏览器时区时显示宿主本地时间，脚本启动后转换。

### 即时发送与展示顺序

点击发送后，文字和附件立即进入带状态的用户气泡，输入区可继续编辑下一条消息；当前上传仍单飞。权威消息到达后按本次客户端来源键替换待发送气泡，响应丢失不会重复显示。未确认发送可复用原请求及附件重试；明确的格式／大小拒绝可恢复为可编辑草稿，不覆盖后来输入。接纳消息按已配置的就绪状态保存，账号凭据刷新留在模型调用边界。

Intelligent UI 按工具调用顺序各占一个气泡，随后单独显示 Ghost 最终文字；历史已保存的展示采用相同布局，不改变原会话或交付回执。用户气泡与发送按钮统一使用 #B84F2F 配白色，正文对比度约 5:1；Ghost 处理状态只显示动画。

Web 的按钮、输入框、搜索选择器、菜单、卡片、复选框和开关统一使用共享组件与皮肤，覆盖设置、任务、开发者页面及会话富组件。字体、字号和页面布局保持；语言/能力/通知自动保存、任务显式保存以及原生无脚本表单保持各自语义。


Web 页面遵循[功能优先与按需帮助](../../design.md#功能优先与按需帮助)：Chat 空状态、设置页、任务与 Developer 清理无行动价值的介绍，必要指引使用共享问号。真实错误、未保存、操作可用性、结果与授权确认保持清楚；帮助行为不提交表单或改变执行语义。

Settings 单值配置按左侧标签／右侧控件同行排列，同级命令统一宽度并右对齐；Apps 目录与详情保留原布局。Chat 的组合输入框仅保留外层焦点边界。输入区模型选择器展示已连接账户的可用目录与已验证直连模型；切换保留草稿、附件及现有工作的冻结配置，失败可重试。ChatGPT 登录、重连与退出统一在 Models，无逐模型添加步骤。

通知目的地选择后自动保存，携带版本并更新确认版本；冲突／写入结果未知不自动重发，刷新读取实际状态。无脚本保留原生提交。Settings 五个分类及详情共用内容宽度、标题起点与留白，配置行的单值选择器跨页等宽，Models 按 Models 归组两类模型连接，App service 使用键值行和右对齐同宽操作；Apps 目录在公共容器内保留双列／单列布局。相关回归由 settings_test、channels_preferences、page-transitions 和 apps_connections_browser 覆盖。

Models 为 Settings 的独立分类，包含「Sign in with an account」和「Connect with an API key」。账户目录自动进入聊天模型选择器；API 连接使用现有私有文件 SecretStore，凭据与首个模型元数据一次事务保存，失败清理新文件。连接后自动验证；首次无当前模型自动选用，后续连接不覆盖选择。连接操作复用共享菜单和原生表单，断开采用模型及凭据双版本确认并撤销后续使用，保留历史冻结配置。Apps 工具栏 App settings 进入服务配置；原 Models/Resources 服务入口移除，旧 setup 与 services/apps 深链接保留重定向。浏览器回归覆盖实际 HTTP provider 的 API key、聊天模型切换、断开及明暗／窄屏布局。

浏览器请求标识在 HTTPS 使用原生 UUID，在 HTTP 来源使用 Web Crypto 加密随机源生成同格式的 UUIDv4；聊天发送、Intelligent UI 交互／导出和图表渲染不依赖 HTTPS 专属的 `randomUUID` 接口。重试继续复用原请求标识，不改动 Host 授权或监听策略。

Developer Traces 的状态前置，Trace 识别列合并原 Name／Input；固定倒序不展示排序操作图标。费用口径集中在标题帮助，首轮仅工具调用时 TTFT 保持未知。工具内模型调用按归属与用途展示，工具 Preview 展示业务内容，原始回执仍可读取。Session／Run／Trace／Task 和来源 ID 使用共享完整值复制，包含局域网 HTTP 与手动回退。
