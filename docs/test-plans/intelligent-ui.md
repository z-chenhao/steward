# 受控富 UI 交付：测试方案

[功能与验收目标](../features/intelligent-ui.md) · [技术方案](../designs/intelligent-ui.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 范围与证据

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-021](../features/intelligent-ui.md#f-021) | Main 读取 Skill 后经 Code Mode/UI 工具/Delivery 生成表格、进度与表单；用户消息不变成组件，单元格文字转义。 | `verify_channels.py --rich-replies-only` 不注入投影或历史行 |
| F-021 | 相同表单并发重试与重启只接纳一次，修改已接受回答、跨消息表单与额外字段被拒绝；回答不创建审批。 | 同一系统场景读回真实输入、回执和冻结投影 |
| F-021 | 无 JavaScript 原生提交、多行回答跨提交方式重试去重；Unicode 字节上限、失败发送保留答案、失去回执后恢复已保存状态、其他表单草稿与普通输入框不丢失。 | Chromium `rich-replies` 阶段，同时保留桌面/移动端截图 |
| F-021 | 工具拒绝越界或伪造字段，未知投影与渲染失败保留文字替代；授权、CSRF 与字段集合验证保持原边界。 | UI、HTTP 与真实 SQLite 组件回归 |

## 私有插件迁移验证

`go test -race ./internal/plugin/intelligentui/... ./internal/rsi -run '^Test(DocumentMatchesCapturedNativeBehavior|UIDocumentPluginIsolatedMatchesOriginalNativeOracle)$' -count=1` 使用迁移前真实原生实现捕获的样本，比较准入、规范化文档与文字替代；隔离进程必须通过独立 Host policy 的18个原生文档样本、1个定义／资源用例和1个非执行预览用例，并拒绝缺失、错配与超限输入。定义预期取自原生工具声明，资源摘要由测试直接读取封存前的独立文件计算，不能由候选生成期望值。不能用候选实现重新生成期望值，也不能把 sandbox skip 当作通过。

运行库归属调整执行 `npm run check --prefix web` 与 `node --check internal/plugin/intelligentui/view/widget-runtime.js`；构建检查须确认现有嵌入资产完全一致，旧源文件不存在，不能用过渡转发文件保留两份当前实现。随包资源接入后另行验证封存身份、真实历史读取与换版，不能把字节相同的目录调整作为完整迁移证据。

`TestUIResourceReadRequiresDeclaredRoleAndOriginalBytes` 覆盖声明角色、服务／封存摘要、重复角色和实际文件篡改；`TestWidgetManagedMissingResourcesNeverUsesNative` 覆盖托管资源缺失不得加载原生字节。原 Web bootstrap／CSP 和静态 UI 规范化回归继续执行。这些局部检查尚不能替代真实 Main 发布、原图资源选择及 HTTP 换版回退场景。

`TestUIManagedCorrectionRecoversAfterReceiptCommitFailure` 从正常 Main 工具调用接入封存的 UI 包，注入真实结果提交失败，恢复后立即断言原 staging、替换关系、托管 Run 边界及规范化／定义回执数量；不额外选择版本或重写产物。该用例必须实际通过，不能把安装、候选验证或 sandbox skip 当作恢复证据。

`TestUIManagedWidgetUsesCodeModeAndDurableReply` 复用既有 Code Mode 真侧车与正常 Main／Delivery 流程，验证非空预览、正式回复、冻结投影和气泡顺序；必须读回嵌套调用对应的两份托管回执，并确认 settled drain 不重复。原生 `TestMainUIWidgetUsesCodeModeAndDurableReply` 继续覆盖具名兼容路径。

`TestUIStoredManagedPreservesAcceptedReadableAndRejectsMissingReceipt` 验证托管文字取自已接纳回执，即使不同于当前原生组合结果；历史 Run 保留原生文字，托管回执缺失、无效或文档错配必须拒绝。真实 Main／Code Mode 场景同时覆盖原图实例选择的 SQL 读取，不以该解析用例替代完整交付。

托管 Main／Code Mode 回归还须读回 capability 快照：UI 的 resource hash 等于已安装 UI 包摘要，权限仍为 `ui_present`、重放仍为 `safe`。独立改变定义、旧快照续跑、撤下后新快照缺席及跨特性投影重试仍须在完整生命周期场景验证。

`TestUIManagedDeactivationKeepsFrozenRunAndRemovesNewCapability` 在接纳旧 Run 后停用插件，紧接着恢复原 Run；验证原能力字节、实际交付和原图两份回执。再正常创建新 Run，核对模型声明与数据库快照均无 UI 工具，且新冻结 Skill 索引没有 UI 条目、托管迁移边界仍在，不使用重新选择或补写快照修复夹具。

`TestUIManagedIndependentUpgradeRollbackRetainsOldDeclarationsAndReadable` 独立编译并封存 1.8 测试包，实际改变工具说明、文字替代实现、逐组件摘要、预览解析、方法正文与 frontmatter 摘要；Owner policy 的预期由原生基线加明确新语义构造。旧 Run 在升级后续跑保留旧包，新 Run 使用新包；回退后立即续跑已接纳的新 Run 仍用新包，再创建的 Run 使用旧包。每次从真实 capability、插件回执、Skill list／view 工具回执、冻结索引包身份与 Conversation 交付核对版本，不重写历史行。该场景只覆盖声明和文字策略换版，不能替代 renderer 字节与浏览器生命周期验证。

Renderer 生命周期须独立编译不同握手版本、封存实际不同的 JS／CSS，并通过真实已发布组件的鉴权 HTTP 引用校验：升级不改旧引用字节，新组件读取新资源，回退、SIGKILL 重启与停用保留两版原引用。字节验证不能替代浏览器执行；另须在实际 iframe 中断言新版运行库的行为与样式，并保留独立沙箱、表单／动作和历史快照边界。

`TestUIManagedSkillReadsOriginalPackageWithoutNativeReader` 在正常 Main 工具流程中把原生 reader 替换为禁止调用的哨兵，逐字节验证入口正文及架构模板来自原包；未声明文件返回 `NOT_FOUND`，原生 reader 调用数必须为零。完整 Skill 文档测试使用明确足够的模型窗口，不把未实现摘要的脚本 Provider 当作压缩质量证明。后续仍须验证独立正文／摘要换版、旧 Run、回退、停用、未知版本缺失资源与物理文件篡改。

`TestWidgetLegacyResourcesRemainOriginalNativeGeneration` 用迁移前已验收默认30发行独立捕获的摘要，锁定历史原生 JS／CSS；当前包资源更新不应改变该快照。现有 widget bootstrap／CSP 与托管缺失不回退用例继续覆盖服务分支，旧原生引用的真实升级场景仍须纳入最终兼容验收。

`TestUIStoredManagedRejectsHostIdentityFields` 对审批、消息、表单、接纳状态、原件和既有组件引用逐项验证：即使回执与产物字节一致，也不能接受候选制造的 Host 字段。真实托管 Main／Code Mode、Skill、状态／动作和恢复回归同时通过，才能证明严格信封未损失现有交付。

`TestDocumentMatchesCapturedNativeBehavior` 与真实隔离服务还比较迁移前捕获的每个组件文字，期望不由候选重新计算。`TestUIStoredNewProjectionRequiresAcceptedComponentText` 验证 Host 使用已接纳的不同摘要，1.5／未来／未知版本缺少逐组件结果不得恢复原生策略；原 1.3 回执只走具名兼容分支。独立1.6测试包修改真正的摘要实现，须从真实 `WebProjection` 核对旧、新与回退执行的原组件文字，同时保留原表单与引用身份。

插件预览解析复用迁移前的前缀、Unicode、转义、CSS 完整性与禁用动作测试期望；隔离服务的预览预期来自原生实现，包含必须被移除的脚本。`TestUIManagedPreviewCannotCarryHostAuthorityOrExecutableFields` 另验证 Host 独立拒绝身份／动作数据、移除脚本字段及未发布图片引用。主循环、Code Mode staging 与已接受 staging 恢复须经原 Run 的真实预览服务，不可将隔离解析测试当作端到端流式性能证据。

`TestUIPreviewQueueCoalescesWithoutBlockingModelStream` 阻塞第一个真实投影回调，验证生产侧仍能提交，同一调用仅执行最新待处理前缀，另一调用不丢失且保持首次出现顺序。`TestUIPreviewQueueStopCancelsAndJoinsWithoutLatePublish` 验证生成阶段切换取消并等待投影退出，待处理前缀与迟到提交不能发布。不能用这些受控调度测试替代实际隔离服务、Browser 流式 DOM 身份和重启场景。

`TestUIPreviewDocumentUsesOriginalManagedStyleAndNeverNativeFallback` 从真实 Web handler 的原会话 Run 读取样式，检查托管字节实际进入文档；缺失资源返回失败，不加载原生快照。真实流式浏览器仍须检查明暗主题的计算背景色、Inter 字体、移动端宽度及增量 DOM 身份；模块载入成功或 HTTP 200 不足以证明样式正确。

独立1.8测试包还修改真正的预览解析实现；Owner policy 的预期是迁移前预览加明确的新标题规则。正常 Main staging 预览事件须实际观察到对应标题，不能只检查包版本或 manifest。升级后原 Run、回退前接纳的新 Run 立即恢复、回退后新 Run 均验证原预览实现，同时保留声明、文字、投影和 Skill 的既有断言。该换版用例不替代完整浏览器场景；流式场景另由当前默认发行实际执行。

HTML 边界的独立换版用例还在组件摘要中加入脚本字样，直接检查原 `WebProjection` 对应的 `ui.HTML`：旧／新／回退必须使用原摘要，同时脚本始终转义。`TestNativeEscapesCells` 与表单接纳／恢复回归继续证明固定转义和身份机制；这些检查不等于原生 React renderer 已插件化。

这些检查尚不证明完整 UI 迁移。后续必须实际经 Main／Code Mode／展示／Delivery 调用托管实现，验证旧数据库中的组件、表单、CAS 状态、动作回执和冻结请求；覆盖独立 renderer／Skill 换版、回退、停用、重启与 Forget／授权边界，保留非空桌面／移动端及明暗主题画面。当前默认原生实现被替代时须同步清理，历史保留代码须列明消费者和删除条件。

## 层级与关键路径

必须通过正常回复生成端到端组件，不能直接往数据库注入 UISpec 冒充生成链。普通文字请求经显式脚本 Provider 的真实工具续轮提交展示产物，再从 Web 交互和 SQLite 读回证明接纳与恢复。脚本 Provider 仅证明软件协议，不证明真实模型会选择合适组件或理解表格内容。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 已有检查与缺失覆盖

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_channels.py](../../tests/system/verify_channels.py) | `--rich-replies-only` 验证生成至提交闭环；`--ui-components-only` 同时覆盖既有渲染降级、普通草稿和本场景；`channels` core gate 包含本场景 |
| [tests/system/channels_browser.mjs](../../tests/system/channels_browser.mjs) | 实际浏览器执行填表、Unicode 越界、失败/不确定提交、同消息另一表单草稿、重载、原生无脚本提交和移动端布局 |
| [internal/delivery/ui/compose_test.go](../../internal/delivery/ui/compose_test.go) | 生成映射、恶意组件、用户文本保持原样、稳定且独立的表单身份、字段顺序及恢复 |
| [internal/interaction/web/ui_forms_test.go](../../internal/interaction/web/ui_forms_test.go) | 现有认证、CSRF、撤销绑定、原生 POST、重复身份和额外动作字段 |
| [internal/agent/sqlite/channels_test.go](../../internal/agent/sqlite/channels_test.go) | 真实接纳记录的来源读取与绑定隔离，既有并发输入和审批去重 |

既有 `native_components` 的直接投影夹具只证明渲染与降级，不作为生成端完成的依据。真实账户、模型组件选择和语义质量仍需独立评测；不以合成数据或脚本 Provider 宣称通过。

相关验证入口：

```sh
go test ./internal/delivery/ui ./internal/interaction/web ./internal/tui
go test -race ./internal/agent/sqlite -run 'TestChannelSourceContent|TestChannelConcurrentCallback'
python3 tests/system/verify_channels.py --binary bin/steward --rich-replies-only --evidence evidence/my-rich-reply/report.json
make test lint
make test-e2e E2E_EVIDENCE=evidence/my-core-run
```

## 生成式 Widget 与单条 Prompt

`verify_intelligent_ui.py` 使用实际 Go 模型 HTTP peer、Actor、QuickJS、内建 Skill、持久 UI 工具和真实 Chromium。检查上游原生表格、generateSandboxedUi 六字段与完成态 activity、计算器、SVG 模拟、主题、展开、移动端、无脚本替代和异常降级；父 DOM、存储与 fetch 被阻止，CSS 闭合标签不能在初始化前捕获禁用构造器，独立浏览器导航仅得纯文本；Widget 提问必须通过 Host 按钮发送，后续 Widget 消息不覆盖宿主草稿或重置不确定发送的来源键，重启不重复生成。该场景不注入投影或 Conversation 行。

`TestMainUIWidgetUsesCodeModeAndDurableReply` 验证 Main 权限、Worker 排除、真实嵌套调用与持久绑定。`TestAgentStartsWithOneFrozenSystemMessage` 验证 Agent 内已经是一条 system，分段元数据不进入模型；拆分消息明确拒绝。原生 catalog Schema 和候选时间选择校验继续由 Go 测试覆盖。

```sh
python3 tests/system/verify_intelligent_ui.py --binary bin/steward --evidence evidence/my-intelligent-ui/report.json
```

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

## OpenIntelligentUI 原生基础与协议扩展

`verify_channels.py --rich-replies-only` 由 Main 的真实工具调用产生五类上游原生组件，验证柱状图/饼图的真实 SVG、表格字面量转义、来源、深浅主题与移动端宽度。候选时间测试覆盖提交前网络失败、提交后丢失回执、会话草稿不被覆盖、无脚本拒绝、重启恢复、六路并发去重、伪造身份/字段与更改已接受选择的拒绝。

`verify_intelligent_ui.py` 验证生成式活动与 Websandbox 接口，真实修改计算器输入和模拟滑块，检查共享 SVG 样式与主题、展开/收起、异常文字降级、宿主确认的后续提问和重启不再生成。`native_test.go` 验证过期协议拒绝、原生参数/图表数据校验与候选选择的事实语义；历史数据仍能读取且不执行旧 HTML。

原生组件的桌面、明暗主题与窄屏检查同时验证 Steward 字体/标题尺寸、去除演示装饰和重复 Text version 入口。生成式场景验证内嵌字体实际可用、正常画面不重复 fallback、失败仍显示替代；宿主按钮组在桌面与移动端有实际几何间距，沙箱按钮组不相接，展开和 Escape 操作可用。


## 状态、数据与操作闭环

- `TestUIStateCASFreezeAndActionUsesOrdinaryExecutor`、`TestUIStateConcurrentWritersAndInvalidPayloads` 验证授权引用、旧版本/并发写拒绝、状态有界和普通输入的不可变 UI 上下文快照；快照明确标识已保存状态不是当前活动页或当前话题。
- `TestUIActionCannotApproveAnExternalEffect` 验证动作按钮仍需既有高风险审批，未审批时提供商未被调用；动作结果只能取实际匹配的 ToolInvocation。
- `TestTypedFormValidationAndRestore` 验证日期、有限数字、范围、候选、多选去重/规范顺序、布尔确认与回答恢复。
- `web/ui-interactions.test.mjs` 验证 CSV 公式处理，以及同一页面并发排空保存、写入期间继续编辑和上下文合并。
- `verify_ui_interactions.py` 使用真实 Main、Code Mode、UI 工具、工具搜索与 Chromium，覆盖折线/散点、表格排序筛选、图表范围、typed form、原生无脚本多选提交、丢失动作回执时强制同源重试、轮询确认成功后才注入迟到 POST 错误、宿主确认的沙箱动作回传、真实 CSV/SVG/PNG 下载和浏览器错误检查。展开/弹窗/悬浮保持同一 iframe；刷新和 kill/restart 不重新生成 UI，恢复输入和已接纳表单。保留明暗/移动端截图，不注入产品表或投影。

```sh
python3 tests/system/verify_ui_interactions.py --binary bin/steward --evidence evidence/my-ui-interactions/report.json
```

真实模型是否选择合适组件、准确使用工具请求并生成优质交互仍属质量评测；脚本 Provider 只证明实际软件边界与恢复行为。

组件呈现回归同时覆盖原生表单在 Host 弹窗顶层中的选择器与菜单操作，确认浮层可点、Escape 取消和原状态保留；必填选择器为空时校验聚焦可见控件并标明无效，选择后恢复；继续验证表单提交后不可编辑、无脚本回答和现有持久回放。明暗/窄屏截图核对组件内部留白、边框、数字列对齐、图表提示及焦点，不将截图生成本身作为视觉验收。

## 流式预览

- `internal/delivery/ui/preview_test.go`：JSON 任意字节截断、完整行/点边界、Unicode/转义、未完成 HTML 字符串、重复键、资源上限；预览不含 JS、动作或正式产物引用。
- `internal/llm/bridge/ui_preview_test.go`：真实 HTTP/原生 Bridge 在工具调用结束前交付 UI 参数，完整工具结果不变；无关工具参数不进入预览，重复/漂移事件仍校验。
- `TestMainDirectUIPreviewIsEphemeralAndWithdrawnOnFailure`、`TestMainUIWidgetUsesCodeModeAndDurableReply`、`TestMainUIPreviewKeepsDistinctInvocationsForRepeatedProviderCallID`：实际 Main 直接与真实 Code Mode 两条路径在回复前发出预览；失败撤下，最终只绑定一次；模型跨轮复用 call ID 不覆盖独立预览，预览不进 Trace。
- `TestUIStagingRecoversAfterResultCommitFailure`：暂存与工具结果提交间失败，原产物幂等恢复；新 claim 可读取已成功暂存预览，旧 claim 被拒绝。
- `internal/agent/ui_preview_test.go`：取消不等待工具/供应商清理即撤下预览，迟到观察不发布；每轮提交释放观察上下文与取消监听。
- `internal/interaction/web/ui_preview_test.go`：匿名键、Worker/过期会话隔离、提交/丢弃/移除、迟到事件和重连快照。SSE 的身份与绑定撤权仍由 `reply_stream_test.go` 共用检查。
- `verify_intelligent_ui.py`：真实 HTTP 模型输出停在未闭合 JSON/HTML，再由 Chromium 验证首行与静态 HTML 已显示；Code Mode 实际完成嵌套 UI 暂存而最后文字尚未产生时同样可见。检查 inert、scriptless sandbox、生成脚本未执行、刷新重连、正式交付后不重复预览且脚本才启用；保留两条链路明暗主题与桌面/手机截图，随后 kill/restart 核对产物未再生成。

这些回归通过现有 PR CI 的 Go 软件分片、`startup-ui` 中的 `intelligent-ui` / `ui-interactions` 系统门禁，以及 `capabilities-race` 中的 Agent、SQLite、Web、Bridge 定向并发检查执行。`tests/harness/test_ci.py` 验证这些入口及浏览器依赖仍在必需分片中；保留原 `Ubuntu checks` 汇总，不新增执行状态机或独立测试平台。真实模型的参数输出顺序、组件选择和视觉质量仍需另行评测。

verify_ui_interactions 的超宽沙箱夹具在桌面及 390px 视口验证：内容宽于组件、ArrowRight 实际滚动、最右侧标记可达、滚动条隐藏、外层页面无横向溢出；同时保留导出、模式切换及持久状态回归。

空脚本列表回归：`TestSandboxEmptyExpressionsNormalizeWithoutCoercingCode` 覆盖空字符串、数组及非空字符串/null/对象/布尔拒绝；`TestUIEmptyExpressionsPersistCanonicalArtifactAndRejectNonemptyString` 验证真实执行器保存规范产物而不改写请求，并继续拒绝非空字符串。`verify_intelligent_ui.py` 的 direct 与 Code Mode 流式场景均包含 `jsExpressions: ""` 静态周计划，浏览器验证同页提交、刷新及 kill/restart 后内容仍在、完成态表达式为数组，并保留桌面/手机明暗截图。以上复用现有必需 PR CI 的 Go 分片、startup-ui 与 capabilities-race 门禁。

## 主动展示、typed 工具与本地可视化

`TestPresentationToolDescribesAndValidatesActualComponentProps` 覆盖完整执行 Schema 和 Code Mode 类型声明中的逐组件参数、缺失字段、错误类型和跨组件字段；`TestMainDirectUIAcceptsCatalogSizedVisualWithoutCodeMode` 通过实际 Main/Store 证明大于普通工具 16 KiB 上限但在 UI 64 KiB 内的完整图解可直接交付；其他普通工具上限不变。空表达式兼容继续使用既有规范化回归。`verify_intelligent_ui.py` 检查真实 HTTP 请求中已包含精确的紧凑参数签名。模型声明与完整执行校验分离；普通 8K Main 并发输入继续由 core main 门禁覆盖，冻结声明与旧快照省略字段的兼容由 Agent/Registry 回归覆盖。

内建 Skill 回归同时检查未配置本地目录时的读取声明不误报为空，且内建方法确实可读取；继续校验四份新增配方经过真实 UI catalog。相同系统门禁经 Main 与普通执行器交付架构图、多方案费用比较、Three.js SVG 几何、带来源署名的离线地理底图，浏览器实际选择节点、修改用量和角度，核对计算/图形变化、刷新恢复、明暗/桌面/手机截图、无外部资源请求与无脚本预览。几何动画不产生重复状态写入，控件和结果变化仍可保存。原生长分类/大数值场景归 ui-interactions 浏览器回归。

主动选择和视觉质量仍须真实模型同题评测；固定 peer、可运行模板和浏览器通过不证明模型主动性已达到特定在线产品，也不证明在线地图或真实照片来源已经接入。

三维配方检查固定轴标签、三角度真实几何变化、旋转顺序切换和刷新后的角度/顺序恢复；类别标签检查实际测量宽度及左边界，组件详情截图临时隐藏固定宿主工具栏，整页截图仍保留真实宿主布局。

## 同轮修正与共轴比较

`TestUICorrectionSelectsOnePendingTargetAndPreservesHistory` 经真实 Main/执行器并发修正同一目标，检查只成功一个修正、不同区块保留、原证据未改和后续输入不能改已交付内容。补充失败提交恢复覆盖同一替换回执；有效版本的恢复预览与回复绑定使用同一派生关系。

`TestSharedAxisLineSeriesAndFallback` 和前端 schema 回归覆盖原单序列兼容、错位数值/重复序列名/双表示拒绝和带单位的文字替代；ui-interactions 浏览器门禁检查共用 Y 轴、两条折线、提示、CSV、明暗/窄屏截图。intelligent-ui 门禁使用实际沙箱验证 SVGRenderer 默认主题、显式背景保留、resize 和中心投影不修改输入向量，并继续检查三角度、顺序和状态恢复。真实模型同题结果单独记录，不以配方或工具调用数替代质量结论。

### 图文叙事与生成连续性

- `TestPublicUISectionsPreserveOrderWithoutPublishingToolNarration` 经真实 Main／Executor 验证正文与视觉顺序、非 Web 替代、临时工具文字不发布、组件身份和冻结投影匹配；旧无 text 回归继续通过。
- `TestPreviewWaitsForCompleteCSSAndPreservesPublicText` 验证不完整样式不绘制。`ui_preview_test.go` 验证授权／撤权／终止、清理、nonce CSP 与无原始源码的 SSE。
- `verify_intelligent_ui.py`／`channels_browser.mjs` 验证同一 iframe 和已有节点在第二次实际参数增量后保留、正式实例未就绪时预览持续可见、正文与视觉交替、生成脚本关闭，覆盖明暗／桌面／手机与重启。真实模型同题另记录首次有效画面、重载与位移，不能用受控 peer 推断 Agent 的表达质量。

### 阅读与关系布局

- `web/markdown.test.mjs` 用同题中复现的中文强调、代码与转义、半个强调、危险链接／HTML 验证解析边界；只有临时显示使用格式补全。
- `channels_browser.mjs` 的 streaming 场景覆盖真实模型 HTTP 增量、CJK 强调、已显示段落的 DOM 身份、断线恢复与持久原文；direct preview 场景验证 iframe 异步增高时的跟随以及上滚后的保留。
- 架构配方沿 intelligent-ui 浏览器门禁验证有向分支、无节点重叠、标签大小、键盘选择与刷新恢复，覆盖 1400／390 和明暗主题。真实模型是否使用 Dagre 另从实际工具产物记录，不能由配方回归推断。
- `TestUIStagedRecoveryKeepsAcceptedOrderDespiteReverseCompletion` 与 `TestUIPreviewRestoredAndNewSectionsKeepPrivateAcceptedOrder` 覆盖逆序完成、释放／重新取得 claim 后的接纳顺序及私有排序键不外泄。`TestUIPreviewUsesCatalogCharacterLimitsForPublicText` 与前端 wire 回归覆盖中文／补充平面字符到达和超出字符上限，防止以字节或 UTF-16 长度过早拒绝合法正文。
- ui-interactions 浏览器回归验证尺寸通知后仍可操作同一 iframe；intelligent-ui 的失败夹具抛出同消息的真实 Error，验证异常对象仍触发原失败替代。失败证据保留有界浏览器 console，避免仅从控件消失推断原因。

以上单元与真实 SQLite 回归由现有 CI 的 go／frontend 分片执行；图文连续性、阅读位置和配方渲染由 startup-ui／channels-web 系统分片执行，UI 竞态继续归 capabilities-race。复用现有必需 `Ubuntu checks` 聚合门禁，不增加重复工作流。工具／渠道门禁保留普通 8K 模型的批准后续轮与多轮回执，防止展示声明挤占必需输入预算；不通过扩大模型窗口替代兼容性回归。

数值横轴回归由 TestSharedAxisNumericIntervalsValidateAndKeepFallbackCoordinates、前端 schema 和 ui-interactions 共同覆盖。真实浏览器以 0／1000／10000 为输入，验证中间点横向距离是总距离的十分之一，并核对 CSV 的 X 列；桌面／手机和明暗主题保持该比例。错误长度、重复／递减、错误类型及超范围坐标被拒绝，未提供 xValues 的旧分类轴保持原行为。

架构图响应式回归继续在真实 iframe 中检查桌面／手机、明暗主题的实际标签尺寸、节点包含与不重叠；尺寸重排在 ResizeObserver 回调内完成，不以增加固定等待或降低 11px 可读性断言解决旧布局缩放。

### 插件表单的冻结绑定（迁移中）

`TestFormMatchesCapturedNativeBehavior` 对照独立捕获的31项原生答案／恢复样本；`TestUIWebFormBindingPreservesDuplicatePositionsAndFrozenRun` 通过真实 Main 和托管执行器生成两个 widget 中三张内容相同的表单，验证独立身份、精确位置、原 Run 图、未知表单及非 assistant 拒绝。托管分支在实际 owner deactivation 后立即读取原绑定，不额外选择或修复夹具。此回归与相关恢复测试执行 race；App 包编译验证不等于 App HTTP 行为验收。

`TestUIManagedFormAdmissionRollbackRetryAndOfflineRestore` 经真实 Main 产生表单，再用托管服务提交；故障注入证明答案与入站同事务回滚，并发提交只保存一条原消息，CRLF/LF 重试复用 receipt。停用后立即恢复原答案，执行设施缺失仍可恢复／同答重试，变化答案不能静默回落；伪造已接纳内容和无效 UTF-8 被拒绝。`TestUIManagedFormRevocationBeforePolicyAndAdmission` 在真实 Forget／Guardian 停止后立即验证原提交被拒绝、没有新入站或答案，不修复原选择／epoch。两者执行 race。

`TestUIDocumentPluginIsolatedMatchesOriginalNativeOracle` 在实际隔离进程执行44项 Owner 样本：原18项文档、定义、预览、23项预先捕获的表单答案及原 Plan plain case；激活后另以真实纯 RPC 对照四组规范化 DOM，避免浏览器与 Go 的实体拼写差异被误判为渲染差异。原31项纯策略基线另覆盖恢复解释；当前 RPC 不提供 restore 操作。schema71 与共享 system fixture 版本保持一致；schema70 历史升级边界保留，现有 catalog、普通 harness 版本一致性和原 operations／inspection 升级回归保持可达。

`TestUIManagedIndependentUpgradeRollbackRetainsOldDeclarationsAndReadable` 独立编译1.10候选，实际改变答案首段，再经 Owner 验证并激活。回退后的旧／新／保留／新建 Run 表单各使用原冻结包，重复提交返回原 receipt；停用后立即恢复全部原答案，不运行候选。实际默认三十一包的现有 intelligent-ui 与 ui-interactions 系统场景验证两次有脚本／无脚本提交、刷新、状态／动作及 SIGKILL 后恢复；独立持久读回核对答案内容、channel source 和原 Run 表单服务边界。

历史 native30/schema69 归档以只读原件和副本升级检查：从正常 history.Get 取得含 sequence 的消息，再读取投影／表单／引用。不得把 outbox intent 当作已交付 Message 或改写原投影哈希来通过验收。schema70 后原记录逐项保持，旧表单恢复不创建当前托管答案行。此 Store 检查不替代旧归档 HTTP／浏览器升级。

尚待证明：整个 UI 发行验收与剩余 renderer 的真实托管迁移。完成之前不标记该功能迁移完成；保留具名历史原生边界，旧归档读取证明见下文。

历史归档升级还通过当前1.9 Host 的实际 HTTP：11个原引用返回冻结 native CSS／runtime，旧表单及状态在原 `ui-interactions-restart` 浏览器流程和 SIGKILL 后恢复。保持原模型端点和原30包选择，端点已离线时 readiness 为 degraded；此检查专门验证无需推理的历史读取，不声称模型服务恢复。所有原不可变记录及状态值保持，未运行 setup／重选或业务行修复。当前新包的就绪与完整交互另由正常默认31场景验证。

维护中的 `verify_ui_interactions.py` 已直接检查两个真实 HTTP 表单提交的 `ui_form_answer`：原 Run 的表单服务边界、冻结图、文本与原 Web 入站身份一致；SIGKILL 后精确结果不变。该断言随现有 UI 系统门禁进入必需 CI，不依赖本地一次性验证脚本。

上述表单接纳／撤权及重复位置绑定回归使用 `TestUI...` 名称进入现有 capabilities-race 的 `.*UI.*` 选择器；`test_ci.py` 同时检查实际函数存在且被该必需选择器匹配，防止仅本地定向执行 race 而 CI 漏跑。

Renderer 表单前置拆分由 `web/open-intelligent-ui.test.mjs` 验证：即使存在展示身份，没有 Host 回调也不能提交；禁用／已提交状态拒绝调用；目录只传字段值，预约映射与取消信号保持，未知选择被拒绝，目录本身不触发 fetch。真实 typed/native 提交、预约丢失确认与重试继续使用现有 ui-interactions／rich-replies 浏览器门禁，不能由回调单测替代。

状态／动作 HTTP 收口继续运行已有 `ui-interactions.test.mjs` 的并发保存回归，验证同一引用只有一个在途写入、写入期间的新编辑不丢失、明确 model_context 合并。实际 ui-interactions 浏览器场景验证筛选／选择联动、动作前 flush、原 source 幂等、表单草稿、冻结上下文与 SIGKILL 恢复；不以文件位置或静态类型代替这些行为证明。

私有 Plan producer 的 `TestUIPlanViewMatchesOriginalReactDOM` 使用新实现产生前由真实 Chrome 捕获的原组件 DOM 与 CSS。经 HTML parser 规范化实体拼写后比较结构，验证 complete 状态折叠，并拒绝缺失字段、未知身份字段和非字符串元素；不由候选生成期待值。该 race 检查只证明纯 producer，不证明 Host 分发、iframe 样式或独立版本；这些证明必须在实际调用接通后进入现有 UI 系统门禁。

Plan 实验图进一步验证真实 Main／隔离进程的原包调用与冻结回执：`TestUIPlanViewSnapshotSurvivesReceiptFailureWithoutCandidateReplay` 在工具回执故障后禁用候选执行设施，直接恢复原 staged 视图并拒绝未发布／错误 ordinal 读取；真实 Forget 后视图不可通过新增表绕过原撤权。`TestUIPlanViewSnapshotRollbackDoesNotReserveEffect` 令快照插入失败，证明视图与 artifact 同时回滚、不残留 pending 效果，再从原输入恢复一次。缺失、重复、其他组件位置及候选身份字段由 `TestUIPlanViewSnapshotRejectsMissingDuplicateAndOtherComponentSlots` 拒绝。这些 SQLite root 进入既有 UI race 选择器，CI harness 核对实际函数和可达性。

原默认包的 managed correction 恢复回归继续执行，以证明实验服务不会改变普通声明／握手。schema71 同步维护迁移 catalog、共享 system fixture 版本及原 operations／inspection 升级回归；该后端回归只证明调用与快照生命周期，默认分发另由真实三十一包场景验证。

只读 Plan HTTP 壳由 `TestUIPlanViewBrowserIsolationAndDetails` 进入既有 `make test-browser`／browser-infra 门禁，CI harness 核对真实入口。该测试启动真实受保护 Web handler 与 Chrome，领域读取仅为 fixture；验证未登录拒绝、opaque parent／storage 隔离、候选脚本不执行、details 点击展开／键盘折叠、真实高度通知，以及1400／390和明暗主题。此范围不冒充完整 App／投影／preview 迁移。

`TestUIPlanViewUsesNonceOnlyScriptAndReadOnlySanitization` 检查大小写 style 闭合注入和 HTML 活跃属性不会产生候选脚本；`TestUIPlanViewRouteRetainsAuthenticationBindingAndNoStore` 经实际 router 验证认证、渠道撤销与不可缓存；缺少快照／样式不能退回原生 renderer。原 UI Styles 资源角色／字节及预览样式测试继续执行。Web 包测试同时保护 SQLite 不导入 Web 适配器的层次，固定 Web 身份值保持原 browser/local。

实际 `FrozenPlanView` 控制器由浏览器 fixture 编译运行，验证父页伪造高度无效、主题变化传递及404后10秒无通知时显示原文字且没有旧 PlanCard。领域视图仍为 fixture，不替代完整 App 场景。恢复测试进一步直接读取真实 WebProjection／WebBubbles，核对原 `view_ref` 和气泡一致性；缺失托管回执拒绝绑定，只有原图未声明 Plan 服务的历史记录允许无引用。

Plan 窗口断点回归使用保留的原 CSS 在父页计算期待值，再与实际 iframe 的 computed min-height 比较；父页为1400／761／760／390px，聊天栏最大672px，明暗主题均检查，并保留截图。0、负数或超出4096的候选断点拒绝接纳；原有重复／错位负例使用合法断点，避免被新增字段校验提前遮蔽。此测试证明窗口语义，不替代完整 App 浏览器验收。

`TestUIPlanViewPreviewUsesOriginalPureServiceAndPrivacyFence` 调用真实冻结 Run／隔离进程，验证未完成前缀生成 Plan、私有 recipe 不进入 SSE、无持久视图／effect 行及真实 Forget 后拒绝继续读取。`TestUIPlanViewPreviewRemainsPrivateAndExpiresDuringResourceRead` 经实际路由检查认证／渠道撤销／no-store、原 Run 样式、缺失样式不回退、过期 revision 与资源 I/O 中 discard。既有 Chrome 门禁还运行实际 `createUIPreview` 分支，核对 Plan 使用新路由且 body inert；领域 recipe 是 fixture，不能当作完整 App 或默认包迁移证明。

现有 `intelligent-ui` 系统入口先运行 `verify_ui_plan_plugins.run`，CI harness 核对调用可达。它通过 CLI 安装、Owner case 验证与激活实验包，不直接改写产品表；使用标准 provider 兼容性探测，只定制业务流式响应。前缀切在组件 props 内，避免仅观察到 fallback 标题而误称已显示 Plan。真实浏览器验证预览 inert、正式 details 可展开、原 ordinal1 引用、桌面／手机与明暗 computed 排版；SIGKILL 后立即读取原引用和快照，禁止追加选择／修复数据。原 DOM／CSS 作为隔离的样式对照，沿用实际 Host 基础排版；必须拒绝原1.5行高二进制。记录前缀到浏览器显示的单次上界，包含浏览器启动，不作性能基准承诺。该场景与原 Code Mode／流式 UI／交互／重启场景一并运行，Plan 升级／回退／停用及默认启动另有下述验收，全量发行检查仍待完成。

`TestUIPlanViewIndependentVersionsRetainPendingPreviewAndFrozenSnapshots` 使用真实1.11／1.12隔离包、独立原 DOM case 与实际模板差异，验证待执行 Run 跨升级／回退继续使用冻结版本，预览和快照一致；停用并将执行设施设为不可用后，各快照／原图仍可读且原结果逐字节不变。该 root 由既有 UI race 选择器和 CI 函数可达性检查维护。

`verify_ui_plan_plugins` 在同一真实 App 中补充1.12升级、旧引用立即读取、新版本流式预览及正式模板、回退后两版本原引用、停用后两版本历史展示及原快照不变。浏览器按原引用选定目标 iframe，并核对 owning template 的版本标记，防止另一个历史视图掩盖版本替换。所有子进程沿用 fixture scope。首版与升级版各自记录前缀观察上界，不能用后一次请求的时间戳计算前一次延迟。该场景继续处于原 Intelligent UI 完整门禁，默认包激活／清理和全量发行检查尚待完成。

默认1.11分发检查必须从未初始化的临时根启动，直接断言标准图拥有 Plan 服务、初始 Owner 为249 case，然后执行完整 App 的原引用、流式 preview、1.12独立模板、回退、停用和 SIGKILL 检查；不能先调用实验激活 helper 再声称默认接线。构建新的完整三十一包，不重封旧包。历史 native Plan 实现仅位于命名的 legacy 路径，原基线与旧 Run 边界仍须验证；其余 renderer 迁移及全量发行检查保留。

直接前缀的 ephemeral／失败撤回测试必须模拟持续中的模型流：peer 发出 prefix 后等待真实 Sink 的非空组件事件，再结束或返回错误。等待仅限测试 peer 且有界；不能假定异步队列同步投影，也不能为测试改成阻塞模型回调。仍要求预览早于 ui_widget 接纳，失败无发布、无 committed、无持久 preview trace。

Cards 原子视图的默认迁移验收：`TestUICardsViewMatchesOriginalBrowserDOM` 使用 producer 编写前的真实 App／Chrome DOM 作为独立预期；`TestUICardsViewRejectsUnsafeAndMalformedProps` 检查危险链接、未知属性、路径穿越及文本转义。`guest-browser/cards-view.test.mjs` 已接入现有 browser test／lint，加载实际 owning runtime 到 opaque iframe，验证原第二项恢复、翻页模型上下文、Host 回传选择、flush capture、旁侧 iframe 伪造拒绝和过期 restore 不覆盖本地修改。该单项只证明 owning runtime；真实 App 派发、图片／链接和版本生命周期由下列独立专项覆盖。

`TestUIDocumentPluginIsolatedMatchesOriginalNativeOracle` 已直接构建真实默认包并运行45项独立 Owner case，覆盖 Cards 原 DOM／CSS／runtime／冻结描述、ordinal0／1 和错误 descriptor／预算拒绝；原仅暴露 Cards handler 的私有 overlay 测试已合并删除。`TestUICardsPureServiceRejectsInvalidSlotsWithoutPartialOutput` 继续覆盖重复／越界位置及后项无效不得泄漏前项。默认初始 Owner 的独立 Cards 原样例由 `builtin/ui-cards-original.json` 冻结，不从候选响应生成。

Cards publication 回归使用实际完整默认 UI 包的私有服务与独立原 DOM Owner case，验证快照和原组件原子接纳、工具回执失败后不执行候选的恢复、快照写入失败整事务回滚及恢复不重复、原 ordinal1 投影／WebBubbles、发布前不可读、非原 ordinal 不可读及真实 Forget 后拒绝。补充缺失／重复／其它组件位置、代码预算与 NUL 拒绝；旧 Run 的 NULL 边界和已管理结果缺失错误分别断言。新增 schema72 同时更新共享系统 fixture 版本，迁移目录一致性检查继续保留。

`TestUICardsViewSeparatesAcceptedRuntimeFromMarkupAndNetwork` 检查固定脚本／已接纳脚本的 nonce、混合大小写 script／style 结束标记、HTML 注入清理与直接导航纯文字；路由回归保留匿名401、撤权403、no-store及缺少接纳字节时拒绝。`TestUICardsViewBrowserIsolationAndOriginalStyles` 进入现有 make test-browser，使用受保护真实 HTTP、实际 owning runtime 及 fixture domain／parent state，验证 opaque origin、父页／存储／网络拒绝、原第二项恢复与翻页通知；在1400／761／760／390和明暗主题下对照原 DOM 的字体、字号、行高、最小高度、颜色、背景、边框和 padding，包括选中标题及禁用按钮。先对齐两侧状态、移走指针并等待有限 CSS 动画完成，避免 hover／过渡中值假差异。此处不是完整 App／CAS 或当前默认包证明；真实 Host 控制器、图片／链接、生命周期及预览由下列专项覆盖。

`TestUICardsControllerBrowserProjectionCASGroupAndRecovery` 由原 make test-browser 与 CI 可达性检查维护。它构建实际 enhanceProjections／React Cards controller，走标准受保护状态 HTTP／CAS 与实际 owning runtime；领域视图与状态存储是严格的双引用 fixture。检查真实 Table 过滤、原引用刷新 DOM 一致、fullscreen／modal／PiP、响应式重排仍对齐原当前项、未知调用／伪造 parent／越界项／foreign ref 不扩权、并发保存409可见、冲突后刷新立即恢复原版本／状态／选择、缺失视图只保留文字。覆盖手机末项因浏览器截断滚动距离而误回第一页的实际失败；不借额外 select／update／reload 修复原恢复动作。此处仍不能代替完整 App／SQLite／默认包、图片／链接或独立版本验证。

`TestUICardsImageGrantUsesPublishedOriginalItemAndPrivacyFence` 使用真实 managed Cards 包与原子发布，拒绝未发布图片、其它 ordinal／非规范 ordinal、未声明 item 和 Forget 后原 grant；回执失败恢复不需要候选执行设施。HTTP 回归拒绝 Artifact 参数、重复／畸形 query、非规范 item 和非 raster 媒体，校验关闭流、不泄漏私有错误及 no-store。`TestUICardsImagesBrowserOriginalBlobAndBoundRequests` 进入 make test-browser／CI 可达性检查：实际 React、受保护 HTTP、owning runtime 将真实 PNG Blob 解码到 opaque frame；iframe 无 Artifact identity 或认证网络，重复／foreign ref／越界请求不扩权，拒读后新加载不复活图片。其 domain image grant 是 fixture，不替代完整 App／真实 Artifact、打开期间撤权与历史版本兼容验证。

图片回归同时发送伪造像素文本 model_context 和带 pixels 的 state：保存结果必须只有合法 index，描述必须是 Go producer 冻结的原文本，不得保留任一私有标记。实际 runtime 不再上报 model_context；独立原 DOM／图片 DOM 对照同时检查默认文本描述，快照校验拒绝缺失／错数量／越界长度／NUL 描述。`TestUICardsContextFormatReturnsOnlyFrozenDescriptions` 检查 context 格式不夹带 HTML／CSS／runtime，缺失数据503，不使用 iframe 描述兜底；既有发表／回执故障恢复继续逐字节保留包含描述的原快照。

固定 Playwright1.64.1 的 waitForFunction 不等待 async predicate；Cards 驱动的异步状态条件统一用 `guest-browser/ui-state.mjs` 在 Node 侧有界轮询真实 HTTP JSON，predicate 必须同步返回 boolean。CI 拒绝在 Cards 状态／图片及预览驱动使用 async waitForFunction，避免 Promise 真值导致尚未持久化就提前通过；轮询只读，不为恢复做任何选择或写入修复。

Cards 链接服务端回归：`TestUICardsLinkGrantUsesPublishedOriginalItemAndPrivacyFence` 覆盖发布前拒绝、真实 receipt 故障恢复后保留原始地址／图、错误组件与条目拒绝及 Forget 撤销。`TestUICardsLinkRouteOriginalDestinationAndGuards` 覆盖认证、严格参数、禁止调用方目标 URL、HTTPS／用户信息限制、原地址跳转和无私有错误泄露。`TestUICardsControllerBrowserProjectionCASGroupAndRecovery` 还通过真实浏览器鼠标／Enter 打开原始地址，确认普通消息、第二个伪造端口、脚本 `.click()` 与合成键盘事件不产生额外弹窗；实际使用 React／HTTP，domain grant 仍是 fixture，完整 App 链接生命周期由下列系统专项覆盖。

`TestUICardsPreviewUsesOriginalPureServiceWithoutDeliveryGrants` 通过实际隔离插件验证未闭合 JSON 前缀、原 ordinal1、只读 HTML／CSS、无 runtime／图片 grant／model context、无持久 widget／snapshot／effect、事件私有字节不外发与 Forget 拒绝。`TestUICardsPreviewPrivateBytesCountTowardSessionBound` 验证私有视图计入会话限额，超限更新保留此前版本。`TestUICardsPreviewRemainsPrivateAndExpiresDuringResourceRead` 验证认证／渠道撤权、无脚本与目标 URL、原 Run 样式、stale revision、缺资源不兜底和资源 I/O 期间过期拒绝。`TestUICardsPreviewBrowserReadOnlyAndRevision` 进入现有 `make test-browser`，实际 `createUIPreview`／React／HTTP 验证原文本、单一固定脚本、opaque origin、inert、无图片／链接 grant、revision 替换、stale read 拒绝及 discard 卸载；覆盖四组桌面／移动明暗截图和 computed 字体。本浏览器单项的 accepted domain 仍为 fixture；真实 App 流式证据由下列系统专项提供。

真实 App 专项由 `verify_ui_cards_plugins.py`／`ui_cards_plugins_browser.mjs` 执行，并纳入既有 `intelligent-ui` 系统门禁：使用生产 bridge、全新默认31包直接激活的 UI1.13及其独立 Owner 验证、真实 Artifact CLI PNG、流式 JSON 前缀、原 ordinal1 正式引用、真实链接跳转、Cards CAS 与实际 Table 过滤、SIGKILL 冷启动及原视图快照字节不变。同一专项还构建 owning 标题模板不同的1.14候选，经 Owner 验证／激活后核对真实新草稿与新交付；升级保留旧引用，回退与停用冷启动后两版原引用／原模板／图片像素／CAS版本／Table 过滤保持，SQLite 快照逐字节不变。历史检查只滚动目标消息和改变显示视口，不选择／写入状态修复；每次视口变化后重新滚动原目标，再等字体与目标消息动画，避免把视口外图片或其他动画当作版本失败。图片回归检查真实 load／error 与 naturalWidth，而非无界二次 decode；插件观察器已预取的 Blob 在授权到达后 eager 加载，旧消息也须恢复自然像素。在途 Run／Child 另见下述 race 回归；旧 native 归档在当前 Host 上另有下述专项，默认切换后的真实旧载体复验亦已完成；真实 App 文件打开后撤权见下述专项；该专项证明当前本地默认分发，不代表已合入 main 或部署到用户环境。

`TestUICardsViewIndependentVersionsRetainPendingPreviewAndFrozenSnapshots` 构建真正模板不同的隔离包，验证升级前待执行 Run 的旧预览／快照、回退前待执行 Run 的新预览／快照、新 Run 跟随当前选择，以及停用且移除候选执行设施后原快照仍可读、无纯 producer 效果。`TestUICardsChildAdmissionAfterUpgradeKeepsParentGraphAndService` 在真实 Root generation 已冻结而 Child 尚未接纳时升级，随后实际接纳并执行 Child／grandchild，核对原父图／服务清单、解析原包与纯服务旧模板；无 Main-only widget／snapshot，并确认当前选择仍为新版。两项纳入现有 UI race 选择器与 CI 可达性检查；这不宣称 Worker 可以发布 UI。

`TestUICardsActualOpenRevocationFence` 由 Cards 系统专项在 Host 关闭后传入真实 config／原 UI ref／真实 Artifact，使用独立复制根分别调用真实 Delete 和已接纳 Forget。现有 Artifact driver 真实 Open 后注入控制动作，确认返回流为空、已打开流关闭且未向调用方读取；重复图片读取拒绝，Forget 后视图／原链接拒绝。删除仍保留 Owner 墓碑元数据读取及独立链接，原字节 Open 被拒绝。control-only fixture 显式装配真实 Memory writer；不改 SQL 隐私字段。首次读旧引用和控制前后均要求当前图保持停用，无 select／setup 修复。系统门禁显式传入 fixture 并以 `go test -race -v` 执行两个子场景，普通无 fixture 的 Go 测试会跳过；CI 检查该真实调用可达。该范围不承诺收回已经发送给浏览器的像素，也未单独注入自然到期时钟。

历史 Cards 专项：`verify_ui_cards_legacy.py --old-binary <retained carrier> --binary <candidate> --evidence <fresh directory>` 固定真实 UI1.11 载体哈希，真实发布后 SIGKILL，直接替换 Host 完成 schema71→72。第一个依赖 UI 读取核对原 DOM／非空状态／版本／Table／真实 PNG／链接属性；全部原 widget／state／Run服务行不变，没有额外模型请求或托管快照，四类托管读取拒绝旧引用。限定桌面明暗；运行需要显式提供本地保留载体，不能把当前编译包伪装成旧版本。此二进制专项不冒充普通 CI 自动拥有该归档。普通 CI 的必跑 `TestUICardsControllerBrowserProjectionCASGroupAndRecovery` 同时校验实际 React 历史无 view_ref 分支和原 CAS 版本保持；Store 现有 missing managed receipt 回归继续拒绝静默 native 兜底。默认切换及旧 renderer 移动后重跑实际归档专项。

旧 CardCarousel 移入 `legacy/card-carousel-v1.tsx` 后，重跑实际保留1.11载体专项与完整 Cards Go 浏览器根；归档 DOM／状态版本逐字节对照继续保持，原名在通用 extensions／catalog 的引用应消失，只允许显式 LegacyCardCarousel 导入。当前默认1.13已接通并重跑真实归档；全量门禁另行执行。

默认1.13验收：`verify_ui_cards_plugins.py` 从全新默认31包直接读取活动图，要求 UI1.13声明 Cards 且初始 Owner 为250项，不再安装基线实验包。随后真实1.14 owning 模板、回退／停用、旧引用及 Open 后撤权照常验证。`verify_ui_plan_plugins.py` 同样以默认1.13及独立1.14验证原 Plan；`verify_ui_interactions.py` 的 Cards 操作进入真实隔离 frame，原排序／过滤／表单／动作／导出／模式／SIGKILL 检查不削减。上述专项与最终默认归档及全量 `make test lint` 均已通过；这些结果仅证明 Cards 的功能／生命周期与清理；新发现导航隐私缺口后不再判为完整迁移，其他 UI renderer 仍须逐项迁移。

Cards 键盘退出回归：`TestUICardsControllerBrowserProjectionCASGroupAndRecovery` 在三个展开模式分别把焦点放入 iframe 的 Beta 按钮，要求真实 Esc 返回 inline，当前项保持，再重开模式验证关闭按钮。合成 Esc、普通 escape 消息与伪造私有端口不得关闭；把 iframe 的 KeyboardEvent.key getter 改为 FORGED 后，真实 Esc 仍须有效。`ui_cards_plugins_browser.mjs` 在默认1.13和真正换版1.14的正式交付中也执行三个模式的真实 Esc。固定 gesture driver 进入 Node 语法检查；预览仍不装载交互手势端口。最终验收必须冻结修复后的源码／二进制；中断或失败的尝试不能计为通过。历史 schema fixture 回退须移除 Cards／Plan 快照表，再运行真实前向迁移；生产 migration 不用 IF NOT EXISTS 掩盖错误 schema。

### Table producer 的独立原基线

`TestUITableProducerOriginalMarkupAndFrozenSortDescriptions` 读取 producer 编写前的 `original-table.json` 与来源摘要，核对静态 DOM 和全部有限 sort／direction 描述。原组件由 JavaScript 判断数值列，因此静态比较只去除独立原 DOM 的 data-numeric 属性，并要求确切移除数量；候选的唯一传输属性与 sort-icon inert 模板另行核对后由固定驱动消费；完整属性由实际 Worker／固定驱动浏览器比较，不能借静态检查放宽最终 DOM。静态检查另解码唯一的私有文本传输属性并与原 props 完整比较，固定驱动消费后须移除它。`TestUITablePureProducerRejectsInvalidSlotsWithoutPartialOutput` 覆盖错误／重复／越界 ordinal、后项错误不得泄漏前项、错误输入 descriptor／预算、非法列／行、额外属性及脚本文本转义。

`guest-browser/table-view.test.mjs` 纳入现有 npm test／lint，使用真实 opaque iframe、固定驱动和 owning Worker，对照原 en-US／zh-CN／sv-SE 的初始、升降序、查询、重复标签选择、重置完整 DOM，包括 data-numeric／ARIA 和原行顺序；原始 textContent 另逐字比较 CRLF／NUL，覆盖 FEFF 前缀十六进制数值列，不通过重新解析 HTML 掩盖文本变化；检查未恢复／旁侧 frame 不可修改、过期 restore 不覆盖本地修改、空结果及导出仅携带原行 ordinal。局部 producer 验收可通过 `STEWARD_TABLE_PROTOTYPE_RESULT` 接收 Go 测试实际生成的视图；该输出是测试证据，不是生产 fallback。普通 npm Worker 检查使用独立原 DOM，不能将其冒充生产服务派发或 Go／JS 完整集成。

当前 Table 尚未进入默认服务或 Host controller；发布／快照／恢复已有未激活接线，尚无真实服务集成验收；该 prototype 不证明独立版本生命周期、授权下载、主题／字体 computed 一致或真实 managed App。受保护 Worker HTTP 有下列独立检查，但领域 grant 仍为 fixture。后续必须在当前必需 UI 系统／race／浏览器门禁中覆盖上述链路，并与真实原默认载体的非空状态／原引用比较，才可清理原默认实现并标记完成。

Table Host 接线回归：`TestUITableViewAcceptanceRejectsMissingDuplicateAndForeignSlots` 检查缺少／重复／非 Table ordinal、非法排序配对／描述、资源预算与可执行字节；`TestUITableViewBindingKeepsHistoricalNULLAndRejectsCorruptManagedReceipt` 分开断言历史 SQL NULL 与损坏 managed receipt，并保持原 ordinal1。`TestUITableHistoricalRunNeverInventsManagedReceipt` 使用真实当前 UI1.13发布，Table 保持无 managed 快照且私有读取拒绝，原 Cards 仍能按原引用读取；这仅证明历史边界不被新机制误接管，不是 managed Table 派发证明。schema73 必须同时更新 catalog／共享系统 fixture／历史 schema 清理；真实迁移目录和旧工作升级／回滚仍留在普通必跑 Go 检查。服务未注册阶段不声称已经验证 Table 原子提交、故障恢复或版本生命周期。

### 可执行视图的导航隐私回归缺口

已有 fetch／parent／storage 拒绝检查不足以证明无网络隔离。必须通过独立临时服务观察请求，验证候选不能通过 location／导航 URL 发出原组件数据；合成测试不得使用真实私有文本。当前 Chrome 的 DOM 脚本复现已显示该路径能发出请求，Cards 完整隔离验收重新打开。Table 已有实际 owning Worker、固定 DOM driver 和受保护 HTTP 的回归；无 DOM／parent、导航拒绝、fetch／存储拒绝与目标零请求不等于默认服务和真实 App 派发已完成。还须覆盖超时终止、恶意／超额投影、未请求的导出、所有原 DOM／字符串／locale／CAS／旧引用和版本语义，并让关键场景进入现有必需 CI；不得把 monkey patch 或导航发生后的卸载当成事前权限边界。

Table 受保护 Worker 浏览器：`TestUITableWorkerBrowserIsolationAndBoundedProjection` 纳入原 make test-browser 和 CI 可达性检查。实际 HTTP／CSP 与 owning Worker 处理原表格，临时服务观察导航／fetch／XHR／脚本导入／WebSocket、恶意 markup／CSS 的出站请求，必须为零；正向原排序继续有效，只有一个 Host DOM script，候选标记及 script 结束字样不得进入 DOM realm。非法行投影、未请求导出、死循环必须产生失败并实际触发 Worker close，Host realm 继续响应。此测试只用合成文本；真实业务 grant／原子发布和 React controller 另验，不借 fixture 宣称已迁移。

`TestUITableViewUsesOnlyHostScriptAndInertMarkup` 检查 nonce、Worker 数据序列化、混合大小写结束标记及标签／URL／事件清理；`TestUITableViewRouteRetainsAuthChannelAndExactQuery` 检查401／403／no-store和重复／未知 query 拒绝；`TestUITableContextDoesNotCarryWorkerOrMarkupAndMissingViewRejects` 检查冻结描述不夹带源码及缺少接纳 Worker 时拒绝。原 private `runtime` 字段在首次激活前已改为明确的 `worker`，旧 DOM 执行源码删除，不保留不受支持的双重默认解释。

Table 私有导出后端的 `TestUITableExportKeepsOriginalOrdinalValuesAndRejectsForeignRows` 验证原组件／行位置与空导出；`TestUITableExportRouteRetainsAuthorizationAndOriginalDataBoundary` 使用领域 fixture 验证匿名401、渠道撤权403、遗忘404、禁止候选字节／URL、唯一行号及 no-store。它们不证明真实 App 的 Forget／版本生命周期或已生成 CSV 内容。Worker 浏览器回归将恢复回复延迟，在恢复期间连续更新共享查询，并直接检查第一次恢复通知后的原 DOM；不通过额外选择／重载修复状态。

`TestUITableControllerBrowserProjectionCASGroupAndExport` 进入 `make test-browser`，使用真实投影／React 控制器、受保护 HTTP／CAS、固定 DOM driver 与 owning Worker，领域接纳事实与状态存储为 fixture。验证原排序、与原生 Table 共享筛选、冻结描述而非浏览器文字入模型、独立原 CSV 字节、重载后的第一次 DOM／CAS、不追加选择或修复、三种展示模式下可信 Escape、未请求导出与重新授权失败、无效冻结描述／状态读取失败后的语义替代。桌面／手机明暗主题使用非空原数据比较字体、间距、边框、对齐、颜色与控件计算样式；先等待有限动画完成并移开指针，保存截图。此范围不替代完整 App／SQLite 的版本、撤权和恢复 gate。

`TestUITablePreviewRemainsPrivateReadOnlyAndExpiresDuringResourceRead` 覆盖私有视图不广播、正文 inert、只读参数、原 Run 样式、匿名／渠道撤权、重复与额外参数拒绝、缺失资源拒绝，以及资源读取期间丢弃后不得复活。`TestUITablePreviewPrivateBytesCountTowardFrameBudget` 保证未广播的 Worker 仍计入帧总额。必跑 `TestUITablePreviewBrowserReadOnlyAndRevision` 使用实际 createUIPreview／React／受保护 HTTP／固定只读 driver／owning Worker，检查独立原数据与数值列、四种主题／宽度计算样式、状态／导出命令和合成点击拒绝、只有高度桥、无已发布授权请求、新 revision 替换与旧 URL／丢弃后拒绝；领域产物与样式授权仍为 fixture，不替代真实 App 的 Run claim／隐私／冻结版本证明。

Table 的默认 UI1.15 提供第七个纯服务，Owner 用例从迁移前 React／Chrome fixture 的 DOM、原列／行和独立冻结的已审查 Worker／CSS 构造，保留明确的传输与 SVG 适配，不取候选返回作为预期。`TestUITableManagedSnapshotSurvivesReceiptFailureAndForgetRevokesExport`、`TestUITableManagedSnapshotRollbackDoesNotReserveEffects`、`TestUITableManagedPreviewUsesOriginalPureServiceWithoutDeliveryGrants` 使用真实插件／Store 的原子提交、收据失败恢复与真实隐私控制；`TestUITableViewIndependentVersionsRetainPendingPreviewAndFrozenSnapshots`、`TestUITableChildAdmissionAfterUpgradeKeepsParentGraphAndService` 改变 owning caption 显示事实，保留真实 Run／Child／grandchild 的原图。

`verify_ui_table_plugins.py` 已接入 Intelligent UI 必跑系统流程，覆盖默认三十一包 Owner、真实生产 bridge 的流式 ordinal1 预览、原数据 CSV／筛选状态、SIGKILL（刻意延迟共享位置状态读取后仍检查第一次可操作状态）、独立1.16 owning 显示事实、升级／回退／停用后的双引用和快照字节；停用后在独立数据副本运行 `TestUITableActualForgetRevokesOriginalViewAndExport`，通过真实所有者 ManageMemory／Forget 控制后立即检查原视图与导出拒绝，不补做 setup／select 或状态修复。新增流程须实际通过才可计为完整 App 证据；合成历史服务图不冒充真实旧二进制兼容证明。现有 Plan／Cards 独立换版 fixture 同步使用默认1.15／候选1.16，保留各自真实模板差异和 Owner 用例。

`TestUITableProducerRetainsValidLiteralCellBudget` 用仍被原 Native／工具输入接受的30×2000字符单元格验证纯视图边界；`TestUITablePreviewRetainsValidLiteralCellExpansion` 验证合法原文本经实体转义和原文编码后，私有帧不会因表示扩张丢掉预览。该回归复现了原512 KiB帧合计限制；当前2 MiB合计上限仍统计所有未广播资源，超额保留上一版本，原输入／每视图／Worker边界保持不变。

Plan 的完整 App 鼠标展开检查等待相邻托管 Table 完成原状态恢复、字体加载与 Host 高度同步，再发送原物理点击；这些等待只读，不更新选择或版本。事件诊断保留 pointer／click／toggle，失败不得以额外点击／选择／重载修复。综合交互检查将 opaque 本地 Blob Worker 与真正外部来源区分；临时 HTTP 目标的零请求隔离回归继续独立执行。全量 Go 检查按普通／WebPlugin／TestUI／调度四组执行，TestUI 与调度组使用所有包范围且从普通组排除；CI 回归检查每个根测试恰好执行一次，保留各组原15分钟包级时限。

### Cards Worker 接线的验收范围

Cards／Table 的失败 Worker 检查须在等待关闭前验证 Host 主线程仍可响应，并保留网络零请求与拒绝越权投影的断言。Chromium 对死循环脚本的强制终止本身有 [2 秒延迟](https://raw.githubusercontent.com/chromium/chromium/main/third_party/blink/renderer/core/workers/worker_thread.cc)，关闭事件的测试观察窗口为 5 秒，包含引擎终止与 IPC 余量；这不改变生产 Host 的初始化／命令 watchdog，也不允许 Worker 无限存活。仍必须观察真实关闭事件，不能以页面关闭或测试主动终止 Worker 代替。

对照冻结原 Runtime 验证边界翻页、重复可达滚动位置、选中、状态采集及预取去重；实际 HTTP／CSP 浏览器验证候选未进入 DOM、导航／网络零请求、非法／未请求回复及死循环终止。正式接线还须验证原链接真实手势、图片 Delete／Forget、恢复后首次交互及 group 原状态，不用再次选择修复 fixture。以真实旧 UI 1.13 载体发布的快照验证原摘要、DOM／非空状态／图片／链接，旧记录不改写；独立换版、回退、停用与在途执行保持原图。草稿／夹具检查不能替代实际 App 和旧载体验收；当前仍待完成。

Table 的真实 App、实际旧 UI 1.11／1.13 载体、独立换版／回退／停用、撤权与清理已完成验收，固定源码全量 `make test lint` 已通过。Cards Worker 的当前真实 App 生命周期已通过；历史 Runtime 快照浏览器兼容已通过，本轮安全隔离／生命周期的固定源码全量门禁已通过，换行／NUL 原文与选中匹配已修复，UI1.19／独立1.20的真实生命周期与本轮固定源码全量验收已通过，不能复用前一轮源码门禁替代。

实际旧 Runtime 兼容由 [verify_ui_cards_runtime_legacy.py](../../tests/system/verify_ui_cards_runtime_legacy.py) 验证：使用脚本固定摘要的真实 UI 1.13 载体建立 Cards 翻页／选中、图片／链接及原生 Table 非空状态；新 Host 的首个依赖读取直接比较原 DOM／CAS 与快照，不重新 setup、选择或修复状态，也不产生模型请求。仅排除不可比较的临时 Blob URL，保留真实解码／授权链接检查。图片必须先获得实际 Blob，不能将空 img 的 complete 状态当成解码证据。

Cards 原文修复已验收：合法 CRLF／NUL 文本使用 owning 原文编码和固定还原器保留。回归直接比较原 props 与实际 textContent／选中状态，不能将期待值与实际 HTML 都再次解析后比较而掩盖归一化差异；修复应保留 owning producer 的展示决定和旧快照，不能通过删除换行或改写原记录规避。此项已完成验收，整个 Intelligent UI 因其他 renderer 仍保持部分迁移状态。

Cards 原文修复的直接回归纳入既有必跑 `TestUICardsViewBrowserIsolationAndOriginalStyles`：经真实 HTTP／CSP／净化／固定驱动／Worker 比较含 CRLF、NUL、中文、emoji 和标签外观的原文，验证初始 group 选中、切换再还原，以及 inert 预览原文与唯一脚本。producer 单测另核对四种展示字段的编码，不能将 DOM 再次解析后的相同值当原文证据。UI 1.19／独立1.20的冻结与 Child 继承沿原版本测试执行；原生 DOM 金样仅忽略会在交互前移除的传输属性，原文还原另行断言。该修复已通过真实 App 与固定源码全量验收；保留原始证据，不替代后续 renderer 的独立验证。

图表迁移先保留真实原生 App／冷恢复基线，再将既有 query／局部 zoom／范围／选择与 CSV 投影移入 owning Worker。`guest-browser/charts-policy.test.mjs` 随普通 guest-browser 测试执行，覆盖筛选后范围、局部 zoom 不持久化、原数字横轴／多序列／单位 CSV、可见类别方向和原始文本。正式图表渲染和原 Run 冻结已接通，默认 UI1.21 已注册服务和独立 Owner，实际 App 的换版／回退／停用／冷恢复／Forget及历史载体专项已通过，固定源码全量验收及启动器修正后的专项复验已通过，图表原子迁移已验收；不得只凭 Worker 单测替代真实派发、旧引用和独立生命周期验收。

`TestUIChartSnapshotReceiptRecoveryRetainsOriginalGraphAndForgetFence` 通过真实插件进程、Main／Generation／ToolInvocation 与 SQLite 接纳，先制造收据失败，再在生产者不可用时恢复；首次原引用读取必须保持原图及 Worker 字节，未交付时拒绝，Forget 后拒绝，纯准备不登记未知效果。`TestUIChartSnapshotFailureRollsBackWidgetWithoutEffect` 验证快照故障不留下部分产物。schema79 同步共享系统期望与迁移目录回归；这些 Host 测试不替代待完成的浏览器渲染／生命周期验收。

图表桥接的 `TestUIChartWorkerBridgeBrowser` 已加入必跑 `test-browser` 和 CI 入口断言；真实 HTTP 验证匿名读取拒绝、opaque Worker、原始换行／NUL／中文事实、候选网络请求零到达以及回包条目必须与请求位置一致。桥接提供有界投影命令，正式 Recharts 和原值 CSV 下载授权已接通；只读预览及默认生命周期专项已通过，固定源码全量验收及启动器修正后的专项复验已通过；图表原子迁移已验收。

`TestUIChartControllerBrowserProjectionCASAndRange` 纳入必跑浏览器／CI 入口，使用实际 esbuild／React 投影、HTTP、Worker 和 CAS，比较托管／历史 SVG 几何与计算字体，保存范围后第一次重载必须保留原状态／context／版本并关闭局部 zoom。四组桌面／手机明暗截图在 reduce-motion 与真实宽度稳定后捕获，避免动画中间态；托管读取缺失时只保留文字、不渲染历史图表。本用例的接纳领域是 fixture，真实 App／独立版本仍另行验证。

图表 CSV 回归核对独立 owning 列头／单位／列次序、原数字横轴／数值／原文和行次序；空行集保留列，重复／越界行和无原来源的列拒绝。真实插件／SQLite 收据恢复后首次导出保留原图和原值，真实 Forget 后拒绝。实际 React／HTTP 浏览器验证范围内 CSV 的原字节，重复行请求拒绝以及撤回导出授权后零下载；不以 Worker 自报的单元格作为 oracle。

图表预览回归验证原图纯准备、私有字节预算、零持久快照／效果及真实 Forget；`TestUIChartPreviewBrowserReadOnlyAndRevision` 纳入必跑浏览器和 CI 入口，实际 createUIPreview／React／HTTP／opaque Worker 验证 inert、伪造命令无状态／动作／下载请求、原图像和计算字体、四种明暗尺寸，以及 revision 替换／丢弃后旧 GET 拒绝且不重建。领域接纳仍为 fixture，不替代待完成的真实 App 生命周期。

`TestUIChartViewIndependentVersionsRetainPendingPreviewAndFrozenSnapshots` 构建真实 UI1.22，改变 owning 标题和五项柱图方向规则，并实际执行每个冻结 Worker 核验布局差异；验证升级前与回退前已提交的 Run、纯预览、原包引用及停用后不可变快照，不以版本标签代替实现变化。`TestUIChartChildAdmissionAfterUpgradeKeepsParentGraphAndService` 在激活后接纳原 generation 的真实 Child／孙级执行，检查原图及服务边界，且不给 worker Main-only 发布权限。它们进入现有 `TestUI` 必跑 Go 分片；不替代完整默认 App／浏览器生命周期验收。

图表的完整 App 专项由 `verify_ui_charts_plugins.py`／`ui_chart_plugins_browser.mjs` 接入既有 `intelligent-ui` 必跑门禁：全新默认32包、独立 Owner311例、真实 Main／production bridge、未闭合流式前缀、原 ordinal1、实际范围 CAS／CSV／坐标轴几何、SIGKILL 冷恢复；独立 UI1.22 修改 owning 布局和标题，通过 Owner 验证后检查新旧引用、回退、停用及不可变快照。停机复制真实数据后执行 `TestUIChartActualForgetRevokesOriginalViewAndExport`，立即核验真实 Forget 撤销视图和 CSV，且不重新选版。历史载体专项 `verify_ui_charts_legacy.py` 使用锁定 SHA256 的迁移前 UI1.19 二进制，直接替换 Host 后检查第一次读取、原范围版本／几何／CSV、无模型重生成和无补造快照；真实 App 的布局换版／回退／停用／冷恢复／Forget 与锁定 UI1.19历史载体均已通过本地专项；固定源码 `make test lint` 已通过；两个 Python 启动器环境变量修正后，真实 App 与锁定 UI1.19 历史载体专项再次通过。

原生表单展示准备的 `TestUIFormViewPreservesFieldsAndOriginalDraftDefaults` 覆盖八种现有字段、原顺序／约束／选项／中文与换行、multiselect 的 `[]`、boolean 的 `false` 和其他字段空初值，并验证原事实未改写及私有结果 schema。`TestUIFormViewRejectsInvalidWithoutPartialContributions` 覆盖原 ordinal 重复／越界、非 steward.form、重复字段及空批次；两者随既有 UI Go 分片执行。它们只证明 producer，不替代真实 Host／App／浏览器派发、非空草稿首次恢复、独立换版／回退／停用、form-v2 引用、提交事务与撤权验收。

表单 Host 接线的 `TestUIFormViewSnapshotReceiptRecoveryRetainsOriginalGraphAndForgetFence` 通过实际插件进程／Actor／SQLite 故障接纳，恢复时生产者不可用，首次读取保留原快照／图／ordinal／FormID；真实隐私控制撤销读取。`TestUIFormViewSnapshotFailureRollsBackWidgetWithoutEffect` 验证快照失败不留下产物或部分快照；纯准备不新增既有 definition／normalize 回执之外的 durable operation。`TestUIFormViewAcceptanceBindsOriginalFieldsWithoutFixingDisplayPolicy` 检查 owning 顺序／标签／合法初值可变，以及外来字段／选项／类型、约束变化与错误初值拒绝；`TestUIFormViewReferenceBindingKeepsHistoricalBoundaryAndFormIdentity` 分开检查历史 SQL NULL、损坏托管 receipt 和原表单身份。schema80 同步迁移 catalog、历史清理及共享系统期望。原生控件消费与静态预览定向回归已通过；默认分发、完整独立生命周期和固定源码全量门禁已通过，表单原子迁移已验收。

`TestUIFormViewRouteRetainsAuthOriginalReferenceAndPlainData` 验证认证／渠道／no-store、严格唯一 ref、禁止调用方表单身份与目标 URL、纯 JSON 原文及不可用时无私有错误。`TestUIFormViewControllerBrowserOriginalControlsDraftAndBoundSubmit` 纳入既有必跑浏览器与 CI 可达性断言，使用实际投影／React／HTTP／CAS／共享控件，验证 owning 初值、必填校验、非空草稿第一次重载后的原状态／context／版本、原身份字段提交、已提交禁用、读取失败不走 legacy；四组桌面／手机明暗截图核对原控件计算字体和几何。接纳视图及表单 ingress 为 fixture，不能替代 App 事务证明。`TestUIFormViewAdmissionKeepsOriginalFormIDTransactionAndOfflineRestore` 另用真实插件／Actor／SQLite 验证已绑定 view_ref 的原 FormID、普通 Main input 与 answer 原子回滚／重试收敛／停用后的原接纳内容恢复；已通过 race。同 Run 纯准备与静态预览定向回归已通过，真实默认 App 独立生命周期及固定源码全量门禁已通过。

`TestUIFormViewPreviewUsesOriginalPureServiceWithoutPublication` 验证真实插件／原 Main claim／未闭合字段前缀／原 ordinal，纯数据事件无 form/message/widget/view 身份、无产物／快照／durable operation，真实 Forget 拒绝后续纯准备。`TestUIFormViewStaticPreviewCountsTowardExistingBudget` 验证静态数据计入原帧预算且超限不替换原 revision。必跑 `TestUIFormViewPreviewBrowserReadOnlyAndRevision` 使用实际 createUIPreview／React／原生绘制，检查 inert／禁用、HTML 外观原文仅为输入值且无脚本执行、合成 submit 无 ingress、零状态／视图读取请求、新 revision 替换初值、缺少托管视图不走 legacy、discard 卸载及四组计算字体／截图。接纳事件仍为 fixture；与 Host 纯准备分别证明，不替代完整默认 App 流式与生命周期专项。正式控制器回归同时重跑，确保无状态绘制提取不改变草稿／提交。

默认 UI1.23 的四组 `ui-form-views-original.json` Owner 由迁移前 InputForm 初值规则与原字段描述独立构造，覆盖八种字段／选项／约束、原 ordinal0／15、重复位置及非目标组件拒绝；不从候选返回生成预期。标准32包的315例保持原512例容量及执行资源边界。`TestUIFormViewIndependentVersionsRetainPendingPreviewAndFrozenSnapshots` 构建独立1.24，实际改变标题和确认框初值，核验预览与正式快照的真实默认值、升级前在途 Run、回退前在途 Run、精确回退和停用后的原包与快照字节。`TestUIFormViewChildAdmissionAfterUpgradeKeepsParentGraphAndService` 在激活后接纳原 generation，验证实际 Child／孙级继承原图、原服务与 false 初值且无 Main-only 发布。定向与 race 已通过；完整 App 的流式、旧引用、独立版本及撤权专项和固定源码全量门禁已通过。

`verify_ui_interactions.py` 在现有必跑门禁中额外核验每个真实表单答案的原 Run 同时声明 form-answer／form-view，且原消息已有非空 owning view 快照；第一次 SIGKILL 冷恢复后，全部非空视图快照与原接纳答案逐项不变，未增加选择或写入修复。默认32包 UI1.23 已通过该真实 App 专项；独立 UI1.24 的完整 App 换版／旧引用／撤权及固定源码全量验收均已通过，表单原子迁移已验收。

`TestPrivateFormDisplayRendersMainAndBubbleWithoutEnteringClientSpec` 验证内部 envelope 的 owning 标题／字段／布尔初值同时进入主消息和气泡的转义 HTML，但不进入客户端 Spec；原 FormID／message／view_ref 不变。`TestFrozenFormViewDrivesNativeFallbackWithoutChangingFormIdentity` 检查私有展示不改变组件序列化，已接纳答案覆盖初值；`TestManagedFormFallbackNeverSubstitutesOriginalPropsForMissingDisplay` 区分托管失败与历史原生边界。真实 App 生命周期浏览器同时创建禁用 JavaScript、继承同一所有者认证的独立上下文，按原 FormID 检查冻结标题和确认框初值，不提交、不重选、不修复状态。

真实 `verify_ui_forms_plugins.py`／`ui_form_plugins_browser.mjs` 已验证默认32包／315例 Owner、实际 Main 流式 ordinal1、UI1.24 owning 标题和布尔初值变化、旧引用立即读取、双版本回退／停用／冷恢复及不变快照。每个正式阶段额外用禁用 JavaScript 的认证浏览器验证原 FormID、原 message 和各自冻结展示；真实 Forget 后原视图与草稿立即拒绝。`verify_ui_forms_legacy.py`／`ui_form_legacy_browser.mjs` 使用锁定 UI1.21／schema79 旧二进制，直接替换当前 Host，核验原 DOM／非空草稿／CAS／context，无重选、补造视图或模型重生成。专项与固定源码常规检查均已通过，表单原子迁移已验收。

时间选择器 producer 的 `TestMeetingViewRetainsOriginalChoicesWithoutInventedDates` 对照原控件的实际文案和时长覆盖规则，核验多时段原序号、中文／标签外观原文、小数分钟以及不完整／非法参数拒绝；不使用候选返回生成预期。该 producer 测试仅证明提取的纯策略；normalizer 收据、前端调用和独立生命周期由下述专项分别验证，不能以此单测替代。

`TestUIMeetingViewBindsOriginalChoicesAndHistoricalVersion` 覆盖 owning 重排／标题变化、原 choice／日期时间绑定、私有展示不改变 FormID，以及确切旧版本与新版本缺失数据的分界。`TestUIMeetingViewRealReceiptRecoveryAndForget` 经真实插件／Actor 的收据故障恢复，在关闭插件执行路径后首次读原投影／气泡，检查原展示、原身份、收据字节及仅有原 normalize／definition 持久操作；真实 Forget 后立即拒绝读取。`TestUIMeetingViewPreviewUsesSamePureReceiptWithoutPublication` 验证未闭合外层、原 ordinal、无持久操作／身份／执行字段及撤权。

必跑 `TestUIMeetingViewBrowserOriginalControlsChoiceAndPreview` 经真实投影／React／HTTP 验证选项重排后仍提交原 choice、选择与 decline 的失败重试、接纳选择恢复、缺失托管展示不回退、无 JavaScript 原身份／选项及 inert 预览 revision／discard；四组明暗／宽度截图和非空控件计算样式与具名旧控件对照。该项的展示接纳和 ingress 为 fixture，不能替代完整 App 事务和生命周期证明。静态 owning receipt 预期分别保存在 `ui-meeting-original.json` 与 `original-meeting-views.json`，来自原真实日期和旧控件规则，不由候选返回生成；现有 isolated Owner 同时验证原18组 normalizer 和新增正式／预览，完整组合增加两例为317，容量仍为512。

`TestUIMeetingViewIndependentVersionsRetainPendingRunPreviewAndReferences` 构建独立 UI1.26，真实改变标题和拒绝选项文案；Owner 预期来自独立静态收据及明确变化，逐个核验升级前待执行 Run、升级后新 Run、回退前待执行 Run、回退后新 Run 的正式展示与实际 preview。停用后经原授权消息投影首次读取两版内容，normalizer 收据字节不变。该项与 Host／Web 定向回归已通过 race；完整 App 与旧载体另由下述实际门禁验证，最终全量验收仍待完成。

`verify_ui_meetings_plugins.py`／`ui_meeting_plugins_browser.mjs` 接入现有必跑 `intelligent-ui` 门禁，使用真正默认32包、317例 Owner、生产模型桥和 Main，检查未闭合流式 preview、原 ordinal1、原选择／FormID／message 与受接纳答案进入普通 Main input 的事务、原收据和 SIGKILL 首次恢复；独立 UI1.26 改变 owning 标题／拒绝文案后，保留新旧预览、原引用、精确回退／停用与冻结收据。每个正式阶段同时用禁用 JavaScript 的所有者浏览器检查同一冻结展示与原提交身份。原选择提交后产生的普通 Main Run 先正常完成，再创建用于验证新版本的独立 Run；这不重选、不补写或修复恢复状态，也不要求在途 Run 采用新图。停机数据副本执行 `TestUIMeetingActualForgetRevokesOriginalProjection`，立即检查所有者 Forget 撤销同一原消息展示，未重新选版。

`verify_ui_meetings_legacy.py`／`ui_meeting_legacy_browser.mjs` 使用锁定 SHA256 的 UI1.23／schema80 原二进制，保留一个真实已接纳选择与一个未提交选项，直接替换当前 Host 后第一次浏览器读取逐项比较 DOM、原 FormID／message／response、非空控件计算样式、原收据／服务图／答案／产物事实；没有模型重生成、重新 setup／选择或补造展示。两类真实专项已通过，不能用 fixture 浏览器回归代替它们；时间选择器迁移已随当前完整源码完成常规检查、真实专项与综合视觉验收。

综合视觉流程仍由 `verify_intelligent_ui.run` 在全部 owning 功能专项之后必跑；私有 `_run_visual_flow` 仅允许定向复验同一流程，不提供跳过专项的默认入口。表格迁入托管 frame 后，区块顺序检查按原位置匹配固定 `data-table-view`，并在真实 frame 内检查原 `Comparison` caption 与 `Option` 列头；不再以主页面 DOM 文本代替跨 frame 内容。原计算器／SVG／主题与窄屏／脚本失败／纯预览／首次冷恢复检查全部保留。

动作反馈的独立静态 `ui-action-original.json` 固定原六类文案、成功图标和原 ordinal0／1，不从候选生成预期。`TestActionViewMatchesOriginalFeedbackAndKeepsAuthorityOut` 比较真实 normalizer 结果并检查 DTO 没有 tool／arguments／source／身份／URL／Worker，且 preview 不启用动作；`TestActionViewReleaseBoundary` 区分 UI1.27 前旧包与新／未知版本的缺失数据语义。默认32包的 Owner 增加两例为319，仍使用原512容量和进程并发。

`TestUIActionViewBindsOriginalSlotAndKeepsAuthorityOut` 验证反馈／成功图标可独立变化，原请求定义哈希与私有展示身份不变，外来／重复槽位、空文本、NUL 和新版本缺失拒绝。`TestUIActionViewActualManagedCASFrozenRequestAndDeduplicatedReceipt` 经真实插件／Actor／SQLite 检查展示不执行动作、参数在用户提交时冻结、CAS 后改变展示状态不会改写已接纳请求、普通 executor 仅执行一次、相同 source 保留原输入、停用仍保留原展示且真实 Forget 后拒绝。`TestUIActionViewCannotApproveAnExternalEffect` 复用原外部效果审批场景，证明托管反馈不会越过 pending approval。

必跑 `TestUIActionViewBrowserFeedbackRecoveryAndOriginalSource` 用真实投影／React／HTTP／CAS 和明确收据 fixture，检查 owning unknown 文案不泄露私有错误、合成点击无提交、首次恢复无 effect POST、unknown 重试保留原 source、确定成功后的新用户动作才生成新 source、真实终态收据优先于迟到失败 ack、owning 成功图标、托管数据缺失不走 legacy；四组非空原按钮计算样式与历史控件对照。fixture 不代替独立真实版本或完整 App 的执行／恢复证据，当前尚未完成这些门禁。

`TestUIActionViewIndependentVersionsKeepPendingRunsAndOriginalReferences` 构建独立 UI1.28，真实改变 unknown 提示与成功图标策略；静态 Owner 预期同步明确这两项变化，升级前待执行 Run、升级后新 Run、回退前待执行 Run、回退后新 Run 逐一使用各自冻结策略。停用后原引用保留同一 normalizer 收据字节，期间不产生 ui_action_request 或执行动作。`TestUIActionViewChildAdmissionKeepsOriginalNormalizerGraph` 在激活后从原 generation 接纳真实 Child／孙级，验证原图／服务、原反馈与成功策略，且没有 Main-only UI publication；两项及 race 已通过。

现有必跑 `verify_ui_interactions.py` 增加实际 normalizer 收据检查：已发布动作必须有非空 action_views、原 ordinal 与 action 组件匹配，DTO 不含请求或权限字段；实际普通 Main／工具账本执行与两次绑定参数变化继续保留。第一次 SIGKILL 冷恢复后原 action_views 收据字节不变，不通过额外重选或补写恢复。当前默认32包 UI1.27 已通过该实际专项。`verify_ui_actions_plugins.py` 经现有 `verify_intelligent_ui.run` 必跑，覆盖生产桥／Main、独立 UI1.28 反馈与成功图标变化、原 source／CAS／参数、升级回退停用与首次冷恢复；停机数据副本的 `TestUIActionActualForgetRevokesOriginalFeedbackAndReceipt` 验证真实 Forget 立即撤销原展示与动作收据读取。`verify_ui_actions_legacy.py`／`ui_action_legacy_browser.mjs` 使用锁定 SHA256 的 UI1.25 原二进制，生成一个成功操作及一个未点击操作；换成当前 Host 后首次读取比较原 source、状态、结果、按钮与全部原产物／服务／收据／动作／视图状态事实，不重选、不补造展示、不调用模型。两类真实专项已通过；动作反馈迁移已随当前完整源码完成常规检查、真实专项与综合视觉验收。
