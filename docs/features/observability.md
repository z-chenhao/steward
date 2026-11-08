# Developer Observability 执行检查器

在 Steward Web 检查完整执行关系、逐轮模型上下文和工具轨迹。

[需求主索引](../feature-map.md) · [技术方案](../designs/observability.md) · [测试方案](../test-plans/observability.md)

## 功能说明

开发者从现有 Web 的 Developer → Observability 进入执行列表与因果关系时间线。

展开任意模型轮次查看 system/user/assistant/tool、工具定义、响应与上下文来源，并比较相邻轮次。

开发者能区分 Host 冻结请求与 Bridge/SDK 转换后实际发送的请求。

开发者能看出何时执行、等待审批或 Child、失败、重新尝试、重放或复用结果，并查看已有用量。

本文描述用户行为和范围；逐项完成状态以主索引为准。当前能力依据代码静态盘点，配置可用性与真实服务/模型质量需分别验证。

## 需求与验收

<a id="f-031"></a>

### F-031 · Developer 导航与执行关系检查

**需求**：开发者从现有 Web 的 Developer → Observability 进入执行列表与因果关系时间线。

**当前实现**：Web Developer → Observability 提供 Trace 列表，按 Main / Root、状态和当前用户时区的时间筛选；Conversation 作为 Session 关联 Main 与后台工作。点击列表行打开非模态详情侧栏，名称链接保留键盘与原生导航；选中 Trace 后进入独立详情，在同一导航区查看对齐的 observation 树/时间线，右侧检查节点详情，支持键盘/原生链接、浏览器前后退与刷新。Main 的 Task 创建/管理回执作为跨 trace 因果链接；Trace 详情的 Task execution 选择器列出各 epoch 的 Root，Child 在实际 Parent Worker 的 trace 内展示。

**边界**：一个 Main Run 或一个 Task epoch 的 Root 执行树是一条 trace；Root 没有 Main parent。每页最多 100 个 observation，分页缺席的 parent 明确标记并可打开；不同分页看到当前提交状态。只读且沿用现有所有者授权。输入/输出与 Task 标题在列表中使用有界预览，节点详情和冻结上下文有完整读取入口；旧元数据 API 仍保持原含义。

**验收目标**：真实工作能从入口定位到 Main、Task、Root 和 Child；Root 不被误标为 Main 的 Child。

<a id="f-034"></a>

### F-034 · 逐轮上下文、来源与 Diff

**需求**：展开任意模型轮次查看 system/user/assistant/tool、工具定义、响应与上下文来源，并比较相邻轮次。

**当前实现**：在 Generation 的详情 → 冻结上下文内按 step 顺序打开已接纳的模型轮次，查看有序 system/user/assistant/tool 消息、工具 Schema、模型响应、压缩来源/省略记录与冻结召回。消息有可读正文和完整存储 JSON 两种视图；JSON 保留工具调用 ID、参数、结果、图片引用及原始来源。相邻轮次可查看新增、移除、变化与移动项，并打开前后完整内容。

新策略在同一逻辑输入内仅首次产生 Retrieve context；后续 Generation 的召回区通过 preparation 中的确切 `recall_step` 读取同一冻结记录。Prepare context 仍逐次记录预算检查和实际请求冻结，旧记录没有复用引用时保留原来的逐 step 查询，不推测缺失召回。

加密记忆在 Generation 输入／输出与 Context 文本视图中明确标注为加密内容，发送前才由 Host 解密；空 `text` 不代表记忆正文为空。JSON 仍展示原始冻结信封，检查器不解密、不读取当前 Memory 替代历史正文。真正没有内容的自动召回和会话记忆不产生 agent 消息，空结果仍冻结以供续轮复用。

**边界**：读取 Host 冻结记录，不重新组装 context，不冒充 Bridge/SDK 最终请求。来源 kind/ref、召回 hit、summary_request/working_set、省略引用和 history_artifact 按已存记录呈现；旧记录没有细粒度来源时明确未知，不解析文本猜测来源或自动开放私有压缩归档。Diff 按保存的 source ref / tool call ID 对齐，旧消息按位置、工具按名称、重复键按出现顺序；比较存储项，不做语义相似度匹配。每页最多 40 条，正文每段最多 8192 UTF-8 字节，链接可读完全部正文与 JSON；不静默截断。待完成或失败 generation 不伪造响应，已保存的 clipped 标记在响应 JSON 中保留。

**验收目标**：UI 与被冻结的 Host 请求逐项一致，工具调用/结果对应正确，来源和省略可追溯，长文本可完整查看。

<a id="f-035"></a>

### F-035 · 供应商最终请求观测

**需求**：开发者能区分 Host 冻结请求与 Bridge/SDK 转换后实际发送的请求。

**当前实现**：新版本生产 Bridge 在 SDK 完成 system 合并、工具转换及图片投影后，在实际 HTTP fetch 边界记录独立 attempt。选中 Generation 或 Provider HTTP 节点，打开 **详情 → Provider request** 查看脱敏最终请求、格式版本、原始时间与脱敏正文 hash。HTTP 重试各有独立 attempt，不把 generation 恢复接纳计数当网络次数。

**边界**：不保存请求头、connection、API key 或 access token；Node 与 Host 都移除已知凭据。图片像素替换为 Artifact 引用（已知时）、预览 JPEG hash、原图/预览尺寸，不保存 Base64。脱敏正文最多 256 KiB，超限保留 hash 与明确省略原因；详情按 8192 字节续读。capture 为 best-effort，旧 Bridge/历史记录、队列溢出、数据库故障或强制退出可留空；缺失不能证明没有发送。开始事件表示 fetch 尝试，响应头事件不代表模型成功或服务端业务接受；原始 SSE 响应正文不另存。

**验收目标**：HTTP peer 接收的请求与观测记录对应，差异明确标记为脱敏或媒体引用；请求头凭据不可见。

<a id="f-036"></a>

### F-036 · 调用、等待、恢复与用量时间线

**需求**：开发者能看出何时执行、等待审批或 Child、失败、重新尝试、重放或复用结果，并查看已有用量。

**当前实现**：统一 Agent、Generation、Tool、Retriever、上下文准备、审批/费用授权等待、工具执行片段、HTTP 尝试和恢复事件。节点详情展示保存的输入/输出、模型、时间信息与可用来源；generation 提供可读角色消息、工具调用卡片、完整上下文、Diff 与最终请求入口。汇总唯一已接纳 generation 的 token/cache、已有 provider operation 耗时与按币种区分的已记账费用。

**边界**：生命周期耗时含等待，不标成模型推理时间；HTTP 条仅测 fetch 到响应头，provider operation 含准备/流式读取。没有起止记录的召回/准备事件是时间点，未知耗时/usage/价格保持未知，部分 usage 标明已知记录数。恢复使用已接纳结果产生 reuse 事件，不新增 generation、usage 或 HTTP 尝试；工具 unknown 不由浏览触发重试。新工具事件携带原始 invocation ID，旧事件无该字段时不以重复的 provider call ID 猜测对应关系。

**验收目标**：受控中断恢复后，UI 不把复用结果显示成新外部调用；未知结果、缺失 usage 和未 dispatch 等待如实展示。

Developer 的 [Memory 页面](memory.md)展示真实记忆正文与来源；记忆来源中的实际 memory_manage 调用可跳转到 Trace，两者沿用同一工作台权限。

## 如何使用

登录现有 Web，点击侧栏 **Developer · Traces**。页面与 API 沿用工作区 token/session 和 Web binding 授权；不需要另行配置开发者服务或选定模型。

1. 列表页顶部提供名称、Trace ID 或模型的字面子串搜索（ASCII 大小写不敏感），全局顶栏为独立时间范围与 Refresh。角色/状态按分组单选呈现，筛选侧栏可收起（手机默认收起），按角色、状态和当前用户时区的创建时间组合查询；起点包含、终点不包含，支持最近 24 小时／7 天／30 天及自定义边界。条件与分页写入 URL，可逐项清除；修改条件与刷新从第一页读取。点击 Session 查看同一 Conversation 的相关 Trace。
2. 图表显示完整筛选范围的真实 Trace 时间计数；悬停／键盘聚焦展示时段和数量，点击或拖选缩小时间范围并联动表格，保留其他条件，浏览器后退恢复。手机先预览并点击“查看此时段”应用，空桶不跳转。时间桶使用可读间隔与排他边界，无数据明确显示空态；表格按 Trace ID、创建时间、类型、状态、Trace、Output、Wall time、TTFT、模型、Tokens 与费用紧凑分列；Trace ID 在新浏览器默认可见，创建时间采用紧凑列宽；列显隐按字段迁移并在当前浏览器保留。未产出的 Output／Wall time 显示“—”，Tokens 与费用展示最新累计统计，不逐行追加 Partial；费用保留两位小数，完整精度与来源可在金额提示中查看。缺失指标保持未知；页脚报告完整筛选计数及当前页条数。
3. 点击名称在默认半屏的右侧非模态面板展开详情，保留底下列表的筛选与滚动位置；关闭及浏览器后退恢复列表，支持前后 Trace、全屏展开，Open full page 打开独立 `/developer/observability/{run}`。直接书签和无 JavaScript 时仍使用完整详情页。顶部概览区展示整个 Trace 的状态、总耗时、调用数、Tokens、费用和失败节点数。左侧 **Trace tree** 默认展示类型颜色、真实层级、可折叠分支及对齐时间线；悬停／聚焦读取节点时间，支持缩放、拖选范围、定位所选节点、恢复全域和键盘平移，按名称／类型／ID 搜索并保留匹配祖先；面板外边缘与树/正文分栏支持鼠标和键盘调宽，偏好只在当前浏览器保留，右侧为 **Observation details**；返回 Traces 保留列表条件。选择节点时保留树/时间线滚动位置；浏览器前后退恢复 URL 中的选择。Refresh 读取新提交状态，不自动轮询。
4. 节点只保留 **Input / Output** 与 **Details** 两个主视图。前者直接展示保存的输入和输出，后者展示模型配置、请求时间线、原始观测及证据入口，不重复输入输出。模型轮次统称 **Model call**，有实际用途的内部模型调用保留具体名称。Generation 顶部显示该次调用的模型、Latency、TTFT、输出 Token/s、输入总量、输出、缓存读取／写入 Token 与费用；未采集的指标显示未知，不从文字估算。输入总量包含未缓存输入及两种缓存用量，缓存分项是其子集。输出速率只使用结束该调用的同一 attempt 的首个模型输出到流终止时间，不混入重试与接纳耗时。
   输入／输出起始页只读组装最多 2 MiB 保存内容，完整消息不再限制为前 20 条／每条 1400 字；系统消息可展开，其余角色默认展开。工具调用参数、回执 ID、图片引用均保留；超限仍提供 JSON 分页与完整 Context 入口。**Details** 下的冻结上下文继续提供 messages / tools / response / provenance / recall、来源和连续读取；Provider request 按实际请求的指令、消息、工具与参数分组，JSON 保留原始脱敏证据；相邻输入对比为次级入口。条目和格式切换留在同一侧栏。
   树默认隐藏 Prepare context、正常 HTTP 与工具内部执行段、进入／结束事件等内部记录；通过“显示内部记录”核对全部已加载证据。压缩上下文、业务检索、审批、等待和失败步骤保持可见，显式深链接选中的内部节点及祖先也保持可见。记录、API 全量树与分页游标均不删除或重写。Worker 输入读取首个冻结请求中的原始任务说明，不以当前任务编辑内容冒充历史输入。
5. 选择 Tool、Retriever、等待或 HTTP 节点，核对各自保存的数据和时间语义。API/Page 不提供重试、重放、审批或取消动作；无 JavaScript 时原生导航、筛选和分页仍然可用。
6. 沿 Task 因果链接或 Parent 导航核对角色关系；Conversation 和 Tasks 返回日常工作入口。长时间 Task 的继续会创建新的 epoch/trace。

旧 `/developer/context` 与带 `?run=` 的书签自动转向规范 Trace 详情，保留 step、section、item、format 与正文偏移；原 API 不变。

这是安装所有者授权下的敏感诊断。原有冻结 context 与新增脱敏最终请求均保留在本地 state.db，沿用其保留及加密备份边界；页面/API no-store，不新增公开访问、导出或第三方发送。观测故障不决定执行或重放。旧元数据 API、CLI `steward ops trace RUN_ID` 保留。

当前对齐 Langfuse 的 Session/Trace/Observation 数据与浏览体验；不依赖 Langfuse 服务，未接入 OTel 导出、Prompt Management、Dataset/Evals、聚合 Agent Graph 或全文搜索。Prompt 继续由项目文件/Git 管理，模型效果评测继续离线维护。

## 详细操作参考

- [运维操作详情](operations.md)

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/observability.md)；已有检查、未覆盖场景和验收方法见 [测试方案](../test-plans/observability.md)。


列表提供 TTFT，表示首次模型调用从首次 HTTP 请求开始到首段模型输出（文本、推理或工具调用）的耗时，包含该调用的重试等待；缺少对应请求开始／首段输出证据时显示未知，不使用响应头或首段网络数据代替，也不对多次调用求和。Generation 详情显示所选调用的同口径值。工具调用无需等待完整参数；旧证据有 first_tool_call 时也可计算，但不伪造缺失的首段时间。

Model cost 优先展示配置价格与已接纳用量的结算值；新调用没有手动价格时自动匹配内置目录并显示“估算”，hover 可查看目录来源及版本。未知价格／用量显示“未计价”，部分覆盖标明“部分”；历史记录不回填，订阅参考价不冒充账单。Traces 与 Memory 的筛选选择立即通过原生 GET 应用，搜索保留 Enter／搜索按钮；无 JavaScript 时保留 Apply filters。
