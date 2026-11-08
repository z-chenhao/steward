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
