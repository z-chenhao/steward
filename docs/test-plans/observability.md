# Developer Observability 执行检查器：测试方案

[功能与验收目标](../features/observability.md) · [技术方案](../designs/observability.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 范围与证据

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-031](../features/observability.md#f-031) | 真实工作能从入口定位到 Main、Task、Root 和 Child；Root 不被误标为 Main 的 Child。 | 已有真实 Actor/SQLite 与浏览器入口；逐轮内容不是本行完成范围 |
| [F-034](../features/observability.md#f-034) | UI 与被冻结的 Host 请求逐项一致，工具调用/结果对应正确，来源和省略可追溯，长文本可完整查看。 | 已有冻结记录/API 比对、真实压缩恢复与浏览器入口；无发送边界观测或模型质量结论 |
| [F-035](../features/observability.md#f-035) | HTTP peer 接收的请求与观测记录对应，差异明确标记为脱敏或媒体引用；请求头凭据不可见。 | 已有目标 HTTP/API 与浏览器入口，限制和执行结果分开记录 |
| [F-036](../features/observability.md#f-036) | 受控中断恢复后，UI 不把复用结果显示成新外部调用；未知结果、缺失 usage 和未 dispatch 等待如实展示。 | 已有目标 HTTP/API 与浏览器入口，限制和执行结果分开记录 |

## 层级与关键路径

F-031 复用真实 Actor/store 和现有 Web。测试用显式 scripted provider 决定调用，ToolInvocation、Task 和递归 Child 由真实执行链产生；页面读取真实保存的数据，不为 Inspector 新建执行引擎。F-034 沿用同一真实执行与冻结存储；F-035/F-036 通过生产 Pi/HTTP peer、既有故障恢复入口及新的 Trace 查询覆盖发送边界与时间线。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 首版具体场景

下表维护整组功能的验收用例；F-031 的已有入口见下节，F-034 的具体证据也列在下节；F-035/F-036 的生产 HTTP peer、恢复、只读与浏览器证据也列在下节。

| 场景 | 关联需求 | 输入 / 操作 | 预期断言 | 层级 |
| --- | --- | --- | --- | --- |
| O-01 执行导航 | F-031 | 创建普通 Main 与包含 Root/递归 Child 的 Task，按时间和状态查找 | 列表可定位记录，关系与实际数据库一致；Task 创建关联与 Parent/Child 关系区分 | SQLite 集成 + 浏览器 |
| O-02 逐轮消息 | F-034 | 驱动两次以上模型请求，包含普通文本、工具调用和工具结果 | 消息顺序、角色、完整正文、Tool Schema、参数及结果与冻结记录一致；不存在的角色不伪造展示 | Bridge/store 集成 + 浏览器 |
| O-03 来源与压缩 | F-034 | 加入 Skill、召回、追加指令并触发上下文压缩 | 可追溯来源和省略记录；当前没有细粒度来源的数据明确显示未知，不以猜测补齐 | SQLite 集成 + 浏览器 |
| O-04 相邻轮次 Diff | F-034 | 在追加和工具续轮前后查看 Diff | 明确标识新增、移除与变化内容，原始快照可单独查看，查询不改写历史 | 组件 + 浏览器 |
| O-05 最终供应商请求 | F-035 | 受控 HTTP peer 接收经真实 Bridge/SDK 转换的请求 | system 合并、工具命名空间等转换能在最终请求视图核对；脱敏和媒体替换项有明确标记 | 真实 Bridge + HTTP peer |
| O-06 图片与凭据 | F-035 | 使用合成图片与仅用于测试的凭据发送一次请求 | 观测无 Authorization/Token/图片 Base64；媒体引用、尺寸和哈希可核对，不能伪称脱敏记录为逐字原始包 | 发送边界集成 |
| O-07 等待与恢复 | F-036 | 先等待审批或 Child，再在持久化边界中断/重启 | 等待不显示为已 dispatch；恢复复用的结果与新发送尝试区分，效果次数和回执不变 | 故障/恢复系统测试 |
| O-08 失败与不确定 | F-036 | 制造模型失败、工具失败、未知外部效果和缺失 usage | 如实显示失败/unknown/缺失值；不补零、不显示虚构成功，不由浏览操作触发重试 | 协议/SQLite 集成 + 浏览器 |
| O-09 有界查询 | F-031 / F-034 | 加入较多记录和长消息，分页、切换、展开完整内容 | 列表有界、详情可继续读取；截断有提示，完整正文有可达入口；不一次加载全部数据库 | 查询集成 + 浏览器 |
| O-10 只读与访问边界 | 全部 | 未认证访问、重复刷新、含 HTML 的消息与敏感上下文查看 | 未授权拒绝；消息安全转义；受控访问；查询前后领域记录、审批和外部效果不变 | 认证/查询集成 + 浏览器 |

真实线上模型不是这些机制检查的必需条件；产品实例中的敏感上下文不能复制进测试数据。未来若增加外部导出、重放按钮或评测入口，需另行定义其访问和副作用边界。

## 已有检查与缺失覆盖

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [internal/agent/sqlite/inspection_test.go](../../internal/agent/sqlite/inspection_test.go) | 真实 Main/Task/Root/递归 Child、原始提交因果、选择与分页、相同时间游标、角色/状态/UTC 边界、Agent 隔离、只读并发、schema 29→30 回滚与冻结数据保持 |
| [internal/observability/sqlite/inspection_test.go](../../internal/observability/sqlite/inspection_test.go) | 数据库层拒绝检查器写入，缺失状态文件不能由读操作创建；读取失败不成为 Runtime 启动依赖 |
| [internal/interaction/web/inspection_test.go](../../internal/interaction/web/inspection_test.go) | 认证、无执行 runtime 依赖、参数校验、标题转义、规范页面/旧链接跳转、无 POST 动作、脱敏失败 |
| [verify_channels.py](../../tests/system/verify_channels.py) 的 inspection_relationships / [channels_browser.mjs](../../tests/system/channels_browser.mjs) | CLI 设置 → 实际 Web Main 创建 Task 与递归 Child → API 与数据库关系比对 → 浏览器导航/角色状态时间筛选/桌面移动/无 JS → 逐轮内容与实际冻结 JSON 比对、Diff 和长 Unicode 正文续读 → 原始领域计数和冻结哈希不变 → SIGKILL 重启不重跑；随既有 channels gate 运行，不新增脚本或 gate |
| [context_inspection_test.go](../../internal/agent/sqlite/context_inspection_test.go) / [context_test.go](../../internal/agent/sqlite/context_test.go) | 真实 Main/Root/递归 Child、语义摘要恢复各轮的完整 messages/tools/response 与冻结记录逐项一致；原请求/效果与归档隐私不变 |
| [observability/sqlite/context_test.go](../../internal/observability/sqlite/context_test.go) | 长中文/emoji/NUL、8192 字节续读、40 项分页、来源隔离、重复键 Diff、增加/移除/改变/移动、未知响应与压缩/召回原记录、独立并发读取；`TestFrozenContextUsesExplicitReusedRecallReference` 验证续轮按确切引用读取首次召回，旧记录缺失继续未知 |
| [web/context_test.go](../../internal/interaction/web/context_test.go) | 同一授权边界、参数/偏移校验、完整模板含 step 0 的前后链接、安全转义、无 POST、no-store 与 CSP |
| [traces_test.go](../../internal/agent/sqlite/traces_test.go) | 真实 Main/Root/递归 Child 的统一查询、输入输出、角色与 Agent/trace 隔离、缺失网络证据不猜测、同时间 100 节点分页及跨 trace 游标拒绝；元数据搜索与 Session/角色/状态/时间交集、字面特殊字符及搜索分页；完整查询计数/时间桶总和与分页隔离 |
| [channels_traces.mjs](../../tests/system/channels_traces.mjs)（沿用 native-trace 浏览器场景） | 搜索/角色/时间组合、预设时间、列显隐持久化、半屏非模态预览、关闭/history/前后 Trace/展开/全页、共享紧凑尺寸、内外分栏的键盘/指针调宽、节点/JSON 分支折叠、Formatted/JSON 原文保持、类型语义色、无 JS 搜索；桌面/手机、明暗主题截图和日期面板边界 |
| [web/traces_test.go](../../internal/interaction/web/traces_test.go) | 树/时间线与详情模板、授权/绑定、参数验证、转义、真实 parent 分支、页外 parent 与几何上限；搜索条件往返详情 URL、清除条件/时间预设重置游标；真实父子连接线/分支标记及归档大整数/小数的展示精度 |
| [bridge/observations_test.go](../../internal/llm/bridge/observations_test.go) / [images_test.go](../../internal/llm/bridge/images_test.go) | 可选诊断不改变 generation.event 序号；跨 registration 丢弃、上限、凭据、异常 sink；实际 Pi 收到图片而观测仅含安全引用/hash |
| [Node capture tests](../../provider-bridge/test/observability.test.mjs) | Node 侧 opt-in、凭据/像素替换、尺寸/hash、超限标记与 callback 故障隔离 |
| [verify_channels.py](../../tests/system/verify_channels.py) 的 native_trace_inspection | 生产 Bridge + 独立 HTTP peer；实际重试、最终 JSON 比对、usage/cost 缺失、完成后重启不重发；桌面/移动/无 JS、详情滚动/history、只读账本；计数前等待 Task 的后续 Main、全部投递及终态后的 agent_end，前后计数使用一致读事务；随现有 channels gate 运行，截图为受控 peer 下的真实产品页面 |

- **F-031 证明范围**：O-01 导航、O-09 的关系元数据分页、O-10 的认证/只读/转义已有入口。缺少来源记录时明确未知；标题裁切、跨页状态变化和树片段有说明。截图不证明模型质量。
- **F-034 证明范围**：O-02/03/04/09/10 由实际冻结记录比对、语义压缩/恢复检查、SQL 边界测试和真实浏览器共同覆盖。完整内容是保存的 Host 请求和已接纳结果；旧来源缺失明确未知，压缩归档仅显示引用，不授予新 Artifact 访问。
- **F-035 证明范围**：生产 Go/Node/Pi 请求与独立 HTTP peer 收到的 JSON 比对；503 重试与 attempt 对应。实际图片发送测试核对 Artifact/预览 hash 证据且无 Base64；Node 与 Host 验证凭据移除、限额与诊断故障隔离。Capture 本身 best-effort，不能用空记录证明没有网络调用。
- **F-036 证明范围**：统一树/时间线、当前状态、唯一 generation 的已有 token/cost；同一接纳结果在 checkpoint 前 SIGKILL 后只调用一次 provider，并显示 reuse 事件。旧细粒度时间缺失保持未知，tool unknown 不触发重试；等待和未知效果仍由既有 tools/children/recovery gates 保护。

## 执行与完成判定

`TestFrozenContextMarksSealedMemoryWithoutChangingStoredJSON` 验证独立与附加的加密记忆均有文本标记，普通工具消息不受影响，JSON 和冻结请求保持原样。`TestLayeredMemoryInjectedSnapshotFrozenSealedAndLoadedWithoutSearch` 通过真实 generation 检查 Preview 标记、实际发送前解密、无明文泄漏及冻结复用；`TestRecallEmptyEvidenceOmittedFromAgentMessagesAndFrozen` 验证空召回／空注入不产生消息，后续新增记忆或检索不可用仍复用空快照。新增空证据回归包含在 `make test-prompts` 与必需 CI 的 `make test-memory` 中。

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

HTTP 诊断写入以真实 SQLite 锁验证：短业务写锁释放后保存记录，测试为 CI 调度留出余量。持续锁定场景通过实际邮箱验证生产写入时限和调用方的非阻塞行为。`go test -race ./internal/observability/sqlite -run TestRecorder` 覆盖两条边界。原 native Pi/HTTP 回归保留完整 attempts/headers 数量断言，失败输出已接收请求数、诊断阶段和 generation 状态计数。

生产 Bridge + HTTP peer 的 channels 回归核对 Generation Details 的时间点顺序，覆盖 503 后重试、200、首段数据、首个工具调用和终止事件。Web 回归验证收到 200 后仍能显示空闲超时。旧数据没有流诊断时只展示已采集的阶段。

`TestTraceRunningListShowsLatestMetricsWithoutPartialBadges` 与 `TestTraceCostRoundsDisplayAndKeepsExactSourceInTooltip` 覆盖进行中缺失值、最新用量、两位小数和精确提示；`channels_traces.mjs` 验证 ID 首列、默认可见、v3/v4 偏好按字段迁移，以及既有桌面／手机和明暗截图。TTFT 首轮只有工具调用也有值；真实 SQLite 回归核对文本／工具输出先到者、含重试及缺失证据，不改用后续轮次。

## 页面重构验收

真实 channels 浏览器 action 从列表进入独立 Trace 详情，验证树/时间线对齐导航、输入输出／详情分工、原始 metadata、完整 Context/JSON/来源与相邻 Compare 前后内容，返回列表和旧 deep link 保留参数；长 UTF-8 正文、桌面/移动端、无 JavaScript 与原 API 的读取边界继续覆盖。`TestTraceDetailDoesNotDependOnGlobalList` 明确让全局列表失败而详情成功，防止拆页后仍耦合列表查询。沿用同一 SQLite/HTTP peer 证据，不引入第二套执行或评测系统。

列表共享规范见 [design.md](../../design.md#数据列表页契约)。`npm test --prefix web` 覆盖共享调宽与 JSON 无损回退；现有 native trace 浏览器场景调用公共列表几何断言，并进入 channels-web CI。

费用覆盖分为两层：`llm/pricing` 与 `agent/sqlite` 的 Go 回归验证精确供应商／chat 模型匹配、长上下文与缓存价格、手动覆盖、缺失用量、迁移和不可变接纳；channels-web 的 account 场景使用受控 OAuth／Responses peer 生成真实接纳用量，验证无需价格配置的估算、来源、重启不重计及审批账本独立。浏览器以已计价金额核对列名和显隐菜单、单行密度、金额和估算标记不侵入相邻列，保留桌面／移动端及明暗截图；未知价格仍由 native trace 场景覆盖。

Requests 阅读器的 Go 回归验证完整小请求组装，以及超限、分页长度变化、来源不可用、游标不前进和读取失败时保留原生分页；Node 回归验证转义文本、Unicode 重复键和无损数值，不能为了可读性覆盖原始请求证据。

Trace／Memory 侧栏关闭的浏览器回归必须确认已返回原列表 URL，再开始下一次整页导航。原生 dialog 关闭后会先离开可访问角色树，History 遍历随后才完成；仅等待对话框消失不能证明返回完成，也不应以重试或固定 sleep 掩盖导航竞争。


节点详情回归验证：所选冻结模型与单次 usage/cost，缺失与零值区分，实际零费用优先于估算；25 条长 UTF-8 消息及工具调用／图片／加密标记完整保留；常规内部节点默认隐藏、深链接及失败节点不消失；输出速率只使用同一 attempt 的首个有效输出到终止事件，缺少 usage 或终止时间保持未知。真实 HTTP peer 浏览器流程核对两个主视图不重复输入输出、内部记录切换、模型指标、原始 JSON、上下文与请求导航、桌面／移动及明暗主题。


时间分布与详情时间线的行为回归：真实 Trace 分桶计数等于同一条件的表格计数；桶的起点包含、终点排他，首尾裁切保留纳秒边界，空窗不包含边界事件。可读间隔保持最多 48 桶。浏览器断言非零柱条实际有高度，hover／焦点 Tooltip、点击与反向刷选更新相同 URL 范围，其他筛选保留，Back 后交互仍有效；触屏点击／刷选先固定预览、Explore 后才筛选。时间线默认可见、统一轴、缩放／范围拖选／键盘平移／Fit／选节点保留窗口，不篡改服务端记录。截图覆盖桌面宽度 1400／1920、390 手机及明暗主题，保留无脚本柱条链接和节点导航。

直方图复核回归额外验证：首尾裁切桶按真实时间比例绘制并导航，微秒边界不被 Date.parse 的毫秒精度吞掉；原生 SVG 与浏览器指针使用相同位置。触屏 tap／brush 释放后，所选当地时段和计数在 Explore 旁持续可见，即使提示浮层被滚动关闭。

显示精度由 `TestWebMetricPrecisionAndUnits` 与 `web/trace-time.test.mjs` 覆盖零值、亚毫秒、一秒边界、长耗时及非零小额费用。浏览器 Trace 详情验证不重复 Trace metrics，原始证据与查询精度不受显示舍入影响。

## 插件展示策略回归

`internal/rsi/trace_test.go` 使用独立编译的真实包和实际 SQLite／Actor 记录，验证旧引用进入新展示策略、源字段不被改写、只读请求不写调用／状态／审计、停用不回退且原始诊断仍可读。`internal/plugin/traceinspector/plugin_test.go` 覆盖缺页父节点、环和未知 TTFT；`internal/observability/sqlite/plugins_test.go` 拒绝伪造／丢失来源、越界布局和未知 Tab。既有 Web Trace 及浏览器门禁仍覆盖原生页、部分更新、明暗主题和窄屏；这些回归不代替尚未完成的完整检查器迁移。

真实包回归还对照原始冻结上下文 Diff，验证内容指纹、重复身份／完整分页、普通业务 JSON 不递归猜解、v1／v1.1 升级回退与保留。隐私竞态在真实隔离进程准备读取输入时推进 epoch，必须拒绝派发并无展示结果。`channels` 的原生 HTTP Trace 场景要求默认图包含真实 Trace 包，浏览前后对照调用／状态／审计计数，隔离展示版本后页面明确不可用而原始诊断保持可读。

解释器游标覆盖升级／回退／停用／退役后的继续读取、未激活目标拒绝及隔离撤销；Web 的 Before／After、Context 与 Diff 链接验证版本传播。连接池回归在占满源快照连接时要求复查继续且禁止写入。


### 工作台插件迁移的原始行为证据

`web/trace-original-behavior.test.mjs` 校验迁移前封存源文件的哈希，并执行当前原生入口比对原始 246 项案例；后续替换实现时仅调整入口适配，不重生成期望值。`web/trace-worker.test.mjs` 对同一独立证据验证包内候选 Worker 的树搜索／祖先／循环／折叠、旧列偏好及错误优先级、时间窗／微秒／分桶边界，并覆盖非法及超限输入。两者由现有 `web npm test` 自动收集。VM 回归不证明进程封存、默认业务派发或浏览器隔离。

`TestTraceWorkbenchBrowserReadOnlyControls` 使用实际认证路由、非空只读事实和共享控件，覆盖桌面／手机及明暗主题下的列切换、树折叠／搜索恢复与无脚本阅读；截图和几何记录写入外部 evidence。该直接嵌入消费者测试不能替代托管包的真实 App 换版、冻结引用、回退／停用和隐私验收。


`TestTraceWorkbenchActualSealedProcessResourceAndReadOnlyLedger` 编译、封存并运行实际 1.1.0 插件，核对声明的资源路径及返回摘要／字节、只读账本不变、多余正文输入／未知字段拒绝，以及停用后显式旧图仍可读取。既有 Trace 升级回归用封存原 1.0.1 handler／manifest 构建旧包；新服务的 Owner 用例只用于接受新图，精确回退沿用旧政策，不能为迁移静默覆盖旧政策。


资源接纳回归拒绝缺少声明、错名称／摘要、变化字节、空／非法 UTF-8／超限源码。实际封存进程测试还经真实 Inspector 验证相同资源，停用后首次显式图读取不依赖重新选版。`TestTraceProgramRequiresExactFrozenReferenceAndOpaqueWorker` 覆盖认证、重复／非法／附加参数、错图、无服务及错误信息不外泄，并检查隔离响应头／源码转义／顶层纯文本；它是 HTTP 边界证据，实际浏览器的 Worker 能力与异常终止仍须另行验收。


`TestTraceProgramActualOpaqueWorkerDeniesEffectsAndTerminatesFailures` 通过受保护的实际资源路由及 `web/src/trace-browser.ts` 传输执行候选 Worker，并注入恶意程序验证 opaque origin、无 DOM／IndexedDB／网络能力、错误序号／无限循环／超大回复关闭和 `pagehide` 销毁后调用拒绝。已纳入 `make test-browser`。候选注入仅存在于测试 reader，封存字节验证由真实 Inspector／包专项独立证明；此测试不声称业务页面已经使用托管程序，也不证明实际 BFCache 准入。


页面选版专项验证先选择图再读取列表／节点事实、原查询传递、页面资源标记和链接冻结版本；选择失败不得读取事实。真实封存进程回归覆盖默认新图工作台选择、1.0.1 历史图旧解释、新图升级后旧图读取、停用后的当前选择拒绝及显式旧图仍可读取。修改共用读取选择时，运行三个 owning 包的 `TestTrace` race 回归；当前 HTTP／VM／隔离专项仍不能替代后续实际业务页面迁移验收。


`TestTraceWorkbenchBrowserManagedTreeAndColumnPolicies` 使用真实页面、共享控件、受保护资源路由及实际包内 Worker，读取事实由具名测试 reader 提供，覆盖四种视口／主题的列切换、树折叠、搜索恢复及无脚本阅读，并确认页面建立冻结程序 frame；真实包封存／Inspector 图选择由独立专项证明，尚不能代替整个实际 App 生命周期。历史入口也执行同一浏览器场景。`trace-tree.test.mjs` 对生产 DOM 适配器仅替换传输，控制异序回复／销毁后回复，断言旧结果不覆盖新状态，整份非法投影不产生半应用或 native 回退。另直接执行归档原始列算法，验证逐键存储读取失败的五种优先级情形，原 246 项基线不重生成。


列控制新增 60 组独立原始事件回归：直接执行归档 TypeScript 的实际 checkbox 监听器／render／localStorage 保存，对比同包 `column_controls` 的可见性、列映射和保存目的地／时机；不以新算法生成期望。`trace-columns.test.mjs` 对生产 DOM 适配器只替换传输，验证实际勾选前不保存、接纳后保存、不能隐藏必保留列或写其他存储键、初始计划不能保存以及销毁后回复不改 DOM。托管／历史浏览器专项均在勾选保存后直接刷新，随后立即断言原偏好及受影响列恢复，不增加重新选版或修复动作。


窗口政策通过直接执行归档原按钮／键盘／滚轮处理器的 318 组输入对比，不复制新实现作为期望；原 246 项基线继续保留。托管与历史真实浏览器均覆盖四种视口／主题下的缩放、键盘平移、连续两次缩放不丢动作、Fit、拖选取消及拖选后直接 Fit，且确认树和时间轴共用一个程序 frame。截图按 managed／legacy 分开保留在现有 App browser evidence 目录。原系统 `traceTimeline` 检查改为等待实际已接纳窗口后再断言相同条件，不增加选版或修复动作；完整系统 gate 尚须随整项 Trace 迁移验收执行。


刻度与泳道投影通过 36 组实际归档 SVG render 执行对比，覆盖宽度阈值、裁剪、进行中／缺失结束、零时长、窗口外事件及首输出边界；合并的 `timeline` 响应同时断言窗口与投影，不重生成原始期望。投影接纳器回归拒绝缺行、非法／无穷／越界坐标、过长标签、错误 full 与按钮类型。

`TestTraceTimelineMalformedProjectionKeepsOriginalSSR` 通过真实页面和受保护 Worker 路由注入有效封装但非法泳道的程序，确认关闭 frame、没有提交窗口、原 SSR 轨道／事实／链接仍在、增强按钮关闭，没有旧算法回退。该专项与四种视口／主题的托管／历史浏览器控制回归共同进入必需 browser gate；实际封存包与历史图由独立 Inspector／进程回归验证。整项迁移仍需完整系统与全量验收。


节点时间提示同时执行原归档 `describe` 函数与包内投影比较；原中文六项标签在确认全局字典完整哈希仍等于初始 accepted-parent 后单独封存并记录 provenance，不依赖候选输出、不重生成原 246 项基线。覆盖英文／中文及进行中／未知结束／零时长／反向时差／首输出为零；Host 接纳回归另拒绝非文本或超长提示行。真实浏览器在桌面／手机、明暗主题逐字断言悬停内容，并使用视口截图保留实际浮层；全页截图会触发共享 tooltip 的尺寸变化隐藏，不能作为浮层视觉证据。原共享组件、字体、位置和无脚本事实阅读保持原契约。


拖选政策通过直接执行归档原 `pointermove`／`pointerup` 与原 `apply` 的 117 组情形对比：正反向、域外裁剪、4／5／6 像素阈值以及真实量级微秒时间戳；同时检查识别、捕获释放、预览和最终窗口。投影接纳拒绝非有限／越界几何及非法提示。实际浏览器矩阵等待已接纳的 brush 后再断言原泳道绘制，不加入修复动作。

`TestTraceBrushActualDelayedRepliesCoalesceAndCancel` 在测试驱动层暂存真实 Worker 的实际 MessagePort 回复，不修改生产传输或程序。确认一条回复在途时新位置只更新待处理项；释放后只绘制最新实际回复；Escape 后再释放已完成回复不复活范围，窗口保持原值；真实 `pagehide` 清理动作后释放回复也不恢复 frame／提示／范围。该事件用于销毁回归，不证明实际 BFCache 准入。专项已进入必需 browser gate，证据路径沿用 App browser artifacts。


### 服务端分桶迁移

原 `traces.go` 与 interval 回归源在替换前校验为 accepted-parent 相同字节并封存。独立 Go harness 直接执行原窗口块和 interval 函数，形成 220 组静态输入／配方期望，覆盖各可读桶宽阈值、最小窗口、显式边界与空结果。仓库只保留被使用的事实／期望，原带 bin 数组的完整输出作为外部 evidence；不从新实现生成期望。Producer、Host 接纳和具名历史解释器均对比原配方；Host 负例拒绝丢弃事实、改写显式边界、非法步长／对齐和过量桶。

`TestTraceActivityActualDispatchUpgradeRollbackAndColdReferences` 运行真实封存 1.0.1／1.1 和独立修改最小窗口的 1.2 进程，经实际 Inspector 证明原记录／total／桶计数不变、独立窗口改变、旧图读取、精确回退、停用不 fallback、退役包引用与三个图分别首次冷读取；不增加选版或修复动作，纯读取不得修改控制状态或调用／状态／审计账本。Guardian quarantine 后旧政策不可读，独立原始 Observation 仍可检查事实。HTTP 回归确认请求的 `reader_revision` 原样进入实际列表消费方。运行 owning producer／store／RSI／Web 的相关 race 回归；完整默认分发、Owner 用例实际数量、浏览器直方图系统门禁及全量检查仍需在整项验收完成。


### 浏览器直方图展示迁移

`web/trace-worker.test.mjs` 执行独立封存的原始直方图刻度与最大值代码，对照 35 个宽度／跨度案例，覆盖 500px 密度阈值、恰好一天格式切换和同毫秒时间边界。`web/trace-activity-view.test.mjs` 验证真实适配器的旧 resize 回复、整体验收后更新、非法投影保留原内容、销毁及冻结程序失效后拒绝晚到回复；不允许原生 fallback。

既有 `TestTraceWorkbenchBrowserReadOnlyControls` 与 `TestTraceWorkbenchBrowserManagedTreeAndColumnPolicies` 使用代表性非空直方图，在桌面／手机和明暗主题检查最大计数、实际宽度对应的刻度数，以及列控件／直方图仅有一个冻结程序。真实重新加载后列偏好立即保留，详情树和时间轴的原行为继续回归。该专项不代替直方图拖选／键盘迁移、独立整个 UI 包换版及最终全量／系统门禁。


### 浏览器直方图交互迁移

`web/trace-worker.test.mjs` 实际执行封存原始 TypeScript 的鼠标／触屏／键盘处理器，比对 162 步状态、几何、计数、提示和导航次数，覆盖正反范围、空桶、越界坐标、Shift 选区及键盘边界。`web/trace-activity.test.mjs` 执行当前真实适配器与同一 Worker，验证移动合并、冻结原桶导航、保留筛选／清除 cursor、连续按键不丢步、异常投影整体验收及取消／销毁后拒绝晚到导航；旧数学算法仅由具名历史入口参与原对照。

两项工作台浏览器回归额外执行 hover／焦点提示、键盘选区及取消、空桶 Enter 不导航、真实 Enter 导航／Back、鼠标反向拖选与触屏预览／Explore／反向拖选，覆盖四种视口／主题组合。`TestTraceActivityActualDelayedRepliesCoalesceAndCancel` 在真实受保护路由、iframe／Worker 和 MessagePort 中暂存实际已完成回复，验证移动合并，以及 Escape 和 pagehide 后释放回复不能导航或恢复程序；此检查加入必需浏览器门禁，不把合成 pagehide 当作 BFCache 接纳证明。系统交互助手等待实际接纳的键盘状态和触屏最终边界，不插入额外选版或状态修复。


### 工作台模式／复制／前后控制迁移

`web/trace-worker.test.mjs` 执行封存原始 `setMode`、复制启用／反馈／复位及前后导航代码，对照当前 owning 配方，覆盖两种模式、输入／输出缺失和列表边界。`web/trace-workbench-controls.test.mjs` 执行真实适配器与同包 Worker，验证原复制字节不进入程序、模式及相邻索引绑定、销毁后初始化／复制回复失效、计时器清理、非法配方整体验收和无原生 fallback。

两项工作台浏览器回归验证侧栏详情的单条前后按钮、两种模式状态、节点切换后模式保留、原正文复制及按原时长复位，并验证合成 pagehide／pageshow 后同一 DOM 不保留旧复制反馈，覆盖桌面／手机与明暗主题；此检查不证明真实 BFCache 接纳。浏览器仅以隔离测试写入端接收剪贴板字节，不写本机剪贴板；实际控件、共享复制函数、节点受保护读取和隔离程序均参与。此专项不替代完整页面资源、独立版本生命周期及最终全量／系统门禁。


### 请求阅读配方迁移

`web/trace-request-recipe.test.mjs` 执行封存原始阅读器与当前真实绑定器／Worker，逐一比较完整 DOM 和原始 source，覆盖两种语言、不同 provider 字段／attempt 形态、工具、遗漏、null／原始值、大正文、重复键、不精确数字及不完整 JSON。请求形状断言保证正文不进入 Worker；独立改变 owning 配方的字段优先级实际改变阅读结果，原始源不变。销毁后的晚到配方及非法字段／文案／范围不能改写证据或进入 native fallback。

工作台两项浏览器回归以非空系统／消息／工具／参数事实进入真实 Provider request 页面，验证 owning 阅读段落、原文 JSON／Formatted 切换不改字节，以及真实 Back 后原时间轴继续可用。返回时等待原正文节点实际移除和新控件接纳，不额外重载、重选或修复状态。阅读绑定器的其余政策、页面资源及整个版本／隐私／系统门禁仍待完成，VM／页面专项不代表整项迁移完成。


### 页面链接／筛选栏控制迁移

`web/trace-worker.test.mjs` 实际执行封存原始宽窄切换函数、初始开关及带内人工变化，对照 current owning `page_controls`；同时执行原链接类别选择，验证列表／详情入口与行增强默认值。`web/trace-page-controls.test.mjs` 执行当前真实驱动及 Worker，覆盖原同源链接、修饰／交互／选中文字／下载或外站边界、浮层由自己的 owning 作用域处理、移动合并、更新的原生开关、同 DOM 恢复宽屏偏好、独立配方关闭增强、失效／非法回复及无 native fallback。

既有两项工作台浏览器回归实际点击标题所在单元格的空白区打开详情，并跨 760px 改变 viewport；窄屏执行合成 pagehide／pageshow 后直接扩大 viewport，立即检查原宽屏开关恢复，不额外选版、重读或修复偏好。列表／详情／请求页、模式、复制、节点切换、Back 后交互及程序数量继续在四种视口／主题验证。合成页面事件不证明真实 BFCache 接纳；原版本／隐私／完整系统与最终全量门禁仍待整个 feature 收尾。


### 直方图封存样式迁移

`TestTraceStylesheetAcceptsOnlyExactSealedBytes` 校验资源名／摘要／缺失／篡改／UTF-8／长度边界；`TestTraceStylesOriginalIndependentLayoutAndPassiveBoundary` 校验原样式、独立图高及网络／私有选择器／隐藏内容／过小字高等拒绝。`TestTraceStylesRoutePinsFrozenResourceAndRejectsUnknownStyle` 校验显式唯一引用、原字节／no-store 及非法样式拒绝。标准构建必须复制两份资源，独立 Owner 静态期望同时校验其返回描述和字节。

原 `TestTraceActivityActualDispatchUpgradeRollbackAndColdReferences` 的独立 1.2 实际编译改变 CSS 资源和生产者，校验正常／独立样式、旧图、精确回退、停用／退役后的首次独立冷读、quarantine 和只读账本；不能只改 manifest 或伪造浏览器返回。原 1.0.1 继续原生具名样式，不要求新增服务。真实浏览器继续验证非空柱条、交互、无脚本事实以及四种视口／主题；同时与样式迁移前保留的独立截图比较，避免仅比较两份同时改变的实现。共享全局 CSS 发生拆分，需要完整常规检查及 Memory／Trace 的实际浏览器验证；整个 feature 的系统／版本验收仍另行完成。


### 跨 reader 的整页资源边界

`TestTracePageBrowserKeepsOneFrozenAssetVersion` 通过真实认证 HTTP 页面／样式路由和 owning Worker，验证 88px／96px 两个 reader 的整页导航、实际计算图高和样式引用、同版本浮层保留 document、浮层转到不同版本时重建 document，以及无 Worker 历史 reader 的同版本增强。夹具以原有链接建模已发布的另一 reader 引用，不修改原始业务数据；它不替代实际 App 中整个包的升级验收。

同一测试截获一次实际同版本 fetch 响应，注入另一 reader 标记，断言旧 document 没有采用异图 DOM 并转为完整受保护导航；不是重新选择、修复服务端状态或手动 reload。该真实浏览器回归加入必需门禁。原树／列／时间轴／直方图／阅读和 Back 行为继续用既有工作台回归验证。


### 托管 Trace 模板资源

- `TestTraceViewOwnedTemplatesMatchAcceptedOriginalReadModels` 对照迁移前已验收源码的两份独立模板快照，检查列表／模型输入输出／metadata／wire 的中英文完整 DOM 和时间筛选；快照出处及 SHA 见 `trace-workbench-original-v1/managed-template-provenance-v1.json`。
- `TestTraceManagedPageActuallyUsesOwningTemplateAndFailsClosed` 通过正式页面渲染消费独立改写的模板标题；缺模板、缺冻结版本拒绝服务，不能恢复原生。`TestTraceViewRejectsEffectsForeignOwnersAndUnboundedSyntax` 和 `TestTraceViewCannotReplaceOrOmitSavedRawEvidence` 覆盖效果／外部目的地、未知控制器、跨页面字段、模板覆盖／递归、错误冻结标记、伪造行、原始证据替换／删除及无界格式化。
- 原 Memory 模板行为与拒绝规则必须继续通过，证明共用 AST 检查没有放宽原消费者。资源读取覆盖封存名称／摘要／字节／UTF-8／限额；实际独立编译 1.2 同时修改分桶、CSS 与 HTML 标题，校验 1.1／1.2、精确回退、停用后旧引用、首次冷读取与 Guardian 拒绝，不能用当前版本重选修复。
- 现有真实 Trace 浏览器门禁继续经过正式托管模板路径，覆盖桌面／移动和明暗主题、无脚本阅读、树／列／时间轴／直方图／复制／模式及跨 reader 整页边界。页面夹具不替代完整真实 App 生命周期与整项系统验收；剩余模板辅助业务、样式与阅读／导航政策不得计为完成。


### Trace 有限展示规则迁移

迁移前通过只读 Go overlay 执行原生辅助函数并保存独立结果及原源码 SHA；`template-helpers-provenance-v1.json` 记录出处，`template-helpers-v1.json` 包含 147 组原生行为，供本项与后续几何／节点语义迁移复用。本项消费全部 147 组已执行原始结果：66 组标签／标题／父引用、72 组节点类别／图标，以及 9 组 SSR 几何，另检查列／状态选项与完整 DOM；仍不代表其余 Trace 政策或整项已迁移。

- `TestTraceOwnedLabelsMatchExecutedOriginalNativeCases` 覆盖已迁入的 66 组规则，包括未知值、大小写、空白与原始标题字节，并校验原源码／结果指纹。
- `TestTraceOwnedOptionsAndMainTitlesKeepOriginalFullDOM` 对照冻结原模板，检查全部状态选择与混合 Main／Root／Child 标题、列名称／顺序；原中英文完整页面对照仍必需通过。
- `TestTraceOwnedLabelsCanEvolveWithoutNativePolicyAndKeepMarkupBoundary` 证明实际渲染可改变角色标签，且十三个已迁移政策不再绑定或允许原生调用。真实 1.2 编译同时改变 HTML 标题、角色标签、SSR 几何、CSS 与分桶；完整模板及旧引用／回退／停用／首次冷读／Guardian 拒绝继续断言。
- 现有跨 reader 浏览器门禁同时检查版本化标题、表格角色标签与实际 CSS 高度。所有 Trace 控件／无脚本／移动明暗主题回归沿用正式托管路径；这些夹具不替代整个真实 App 生命周期和最终系统验收。

- `TestTraceOwnedNodeKindsAndIconsMatchExecutedOriginalCases` 对照原始 72 组类别及旧图标，覆盖 Provider HTTP 的名称优先级、span wait 和未知值。
- `TestTraceOwnedSSRGeometryMatchesExecutedOriginalCases` 从真实渲染的非空 SVG 读取坐标与尺寸，对照全部 9 组执行前基线；包括全零、稀疏及重复最大值，禁止改用候选计算期望。
- `TestTraceOwnedGeometryCanEvolveAndScalarFailuresCloseBeforeRendering` 证明模板能独立改变几何且保留原始桶；零除、整数溢出、非有限浮点在输出前拒绝，并验证 Trace 局部赋值没有放宽 Memory 语法。跨 reader 实际浏览器还检查独立版本 90／100 的 SSR 条高、y 与 CSS 高度，不能仅用类名或文案作为几何证据。

- `TestTracePrivateTemplateStructureCanEvolveWithoutHostRegistry` 验证内部片段改名、新增和删除无需更改 Host，同时拒绝未知入口、未定义引用、共享名称覆盖和递归；Memory 精确模板契约保持原限制。独立 1.2 的实际编译包与跨 reader 浏览器同时使用已改名的角色片段，不能只用修改文案证明独立实现迭代。


### Trace 详情分组与证据显隐

`TestTraceOwnedDetailPolicyMatchesOriginalCapabilityAndStepMatrix` 将两种语言、nil／空／单项／全部／未知证据能力、nil／负数／0／1 step 和五种详情视图组合为 320 个场景，对照冻结的迁移前完整模板与历史输入，检查完整 DOM。未知／大小写任务动作的标签另与原固定结果对照。托管加载必须不再填充原生主 Tab，候选不能调用三个已移除展示回调或访问历史 `Tabs`。

正式跨 reader 浏览器同时检查独立版本的详情标签；独立编译包仍覆盖全文资源、旧图、回退、停用／首次冷读及 Guardian。主分组重排须能由模板独立完成，且不改变原始能力事实或后台读取协议。没有新增原生兜底。

### 时间线样式迁移回归

- `TestTraceStylesOriginalIndependentLayoutAndPassiveBoundary` 覆盖原样式与独立时间线宽度变化，拒绝网络值、私有身份选择器、隐藏证据和未知容器。
- `TestTraceActivityActualDispatchUpgradeRollbackAndColdReferences` 覆盖真实独立二进制、时间线样式摘要、回退、停用及冷引用。
- `TestTracePageBrowserKeepsOneFrozenAssetVersion` 检查真实托管 48% 和历史 44% 时间线宽度以及跨版本导航。既有工作台浏览器门禁继续验证非空桌面／手机、明暗主题、拖选与原始控件；截图对照迁移前固定源码，Memory 共用 pane 不得变化。

请求证据与指标区的 32 条样式归同一 Trace 资源；真实换版／回退／冷引用与浏览器计算间距验证复用既有门禁，六条 Memory 共用规则保留共享层。

树节点／状态／连线的 65 条样式也归 owning 资源。既有版本浏览器验证独立 guide 缩进，样式边界拒绝非空装饰内容／属性读取和未知 important；原树搜索、折叠、滚动与历史消费者回归保持原流程。

列表、消息与详情剩余 61 条专属样式已归同一封存资源，混合选择器按实际 Memory 消费者拆开，保留共享检查器／可信 Context reader 原语。原全局专属规则已删除；保持原共享控件和冻结读取通道，不增加服务或生命周期。

请求阅读器的分组顺序、展开、文本拼接、单输入包装和标签格式由原 owning 配方提供。`web/trace-request-recipe.test.mjs` 对照迁移前真实源码验证全部提供方形状／双语言 DOM、原 JSON、独立配方变化、无正文出站及坏配方／晚回复拒绝；真实独立包与冻结浏览器继续覆盖换版／回退／停用和原引用。

导航的抽屉选择、筛选／滚动／焦点／Tab 保留已接入同包纯元数据投影。`web/trace-navigation.test.mjs` 覆盖原转换行为、独立变化、非法输出、页面移除与撤销；版本浏览器验证独立版本实际清空同 Trace 筛选且不刷新文档，原冻结资产与跨版本整页导航约束保持。

### 服务端显隐与运行中宽度迁移

- `TestViewPolicyMatches384ExecutedNativeLayouts` 以迁移前原函数执行结果验证显隐、循环／缺失父链、选中、层级与运行状态几何；固定时钟和来源哈希见 fixture provenance。
- `TestTraceWorkbenchManagedMissingLayoutRejectsNativeFallback` 拒绝当前包缺失展示投影。
- `TestTracePluginProjectsOwnerEvidenceWithoutWritingLedger` 和 `TestTraceActivityActualDispatchUpgradeRollbackAndColdReferences` 验证当前／严格原始1.0.1 contract 的真实分发、回退、停用、冷引用与旧事实，升级后的 Owner 预期按真实版本区分。
- 后续验收须覆盖独立显隐／几何变化、原控件浏览器、隐私／撤权及源固定的全量检查；当前专项通过不能替代整项完成。

同一真实分发专项还以明确 running tool 的独立 Owner 样本验证运行中比例，并从实际 Context 回执核对 Primary／点宽在1.1／1.2及回退／停用／冷读取下的值；不能只检查资源摘要或在 Host 重算后比较。原失败样本保留为证据，pending tool 不被假定为 running。

### 指标展示解释回归

- `TestMetricsPolicyMatches384ExecutedNativeCases` 核对迁移前实际 loadTrace／速率结果、48组未知首输出和336组未知速率；额外验证选中记录不在 Items 页内仍可解释自身事实。
- `TestTraceMetricProjectionBindsRecordedEvidenceAndKnownUsage` 拒绝虚构事件时间和未知用量下的已知速率。
- 真实独立1.2口径与原1.1分别使用首文本／首有意义输出，既有实际分发专项校验各自固定输入输出并覆盖升级／回退／停用／冷引用；原始来源与账本须保持不变。

### 缺失 Catalog 的首次读取

`TestTraceManagedHistoryWithoutCatalogCannotRestoreNativeDiffOrWorkbench` 先证明 never-managed 的真实 Diff 可读，再保留激活历史并验证 Catalog 缺失不回退。`TestTraceColdMissingCatalogNeverRestoresNativeProjection` 经实际隔离插件和 Host 激活后新开只读 Inspector；首次 Workbench／分桶／布局读取均须不可用，停用后再次新开也不回退，原始轮次仍可独立读取。`TestTracePartialActivationSchemaIsNotNeverManaged` 拒绝部分迁移表伪装成原生状态。这些回归同时保留在 ordinary Go 检查与必需 capabilities race；不增加额外 select、配置或 reload 作为恢复前提。

真实 Trace 浏览器回归按列表筛选／列偏好、时间窗手势、列表截图／几何、详情／抽屉交互、模型 I/O／配置与截图、请求／上下文、任务执行导航七个场景分别运行，保留每个浏览器进程原45秒期限与全部原检查；场景共享同一真实后端 fixture 和原事实。冷页面早期链接／整行点击须等待原冻结程序的决策；无损浏览前后事实计数保持一致。移动端角色筛选发生真实导航后，按折叠状态显式重新打开 Filters，不依赖异步初始化竞态。
