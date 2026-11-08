# Steward Feature Map

本页是产品需求的主索引：记录每一个可迭代需求的简述和完成状态，链接到功能说明。功能说明分别关联技术方案和测试方案。测试命令与 CI 编排继续由 [Testing](test-plans/product.md) 管理。

完成状态描述当前实现范围，不代表真实模型、线上账户或所有平台已验收；证明范围和未验证项见所属测试方案。

[界面设计规范](../design.md) · [产品架构总览](designs/product-overview.md) · [文档维护规则](#文档维护规则) · [功能模板](features/_template.md) · [技术方案模板](designs/_template.md) · [测试方案模板](test-plans/_template.md)

F-013 已接通用户图片到视觉模型的输入，并增强本地 OCR，让纯文本模型可显式读取图片文字；F-005 提供阶段进度；F-019 流式呈现 Main 回复草稿并在提交后切换到正式消息。软件回归与模型语义质量的证明范围分别见对应测试方案。

F-031 提供 Developer → Observability 执行检查入口：按角色、状态和当前用户时区的时间查找执行，从已提交回执追溯 Main 与 Task 的因果关联，并检查实际 Root/Child 树。F-034 已接通逐轮冻结上下文、来源与相邻轮次 Diff；F-035/F-036 进一步统一为 Langfuse 式 Session/Trace/Observation 检查，支持真实 HTTP 尝试、恢复复用、输入输出、时间线和已有用量/费用。Trace 列表提供元数据搜索、组合筛选、顶栏时间范围、完整查询计数直方图、TTFT、自动目录费用估算与明确未计价状态、Trace ID 首列与紧凑时间列、最新累计用量／两位小数费用、单行列显隐和点击整行打开的半屏非模态详情预览（可拖拽分栏、彩色层级树、结构化 Input/Output 与原始 JSON），完整页面及原生回退继续可用。

F-018/F-019 将 Conversation、Tasks、Settings 和 Developer 按用户任务拆分；模型来源/配置/验证/选择、通知、任务详情和 Trace 详情拥有各自入口。浏览器请求标识兼容 HTTP 来源并保留加密随机性与重试身份。设置的自动选项统一为 Auto／自动，以选中态表达当前值，不附加默认或推荐标记。Settings 复用版本化字体以避免切页闪跳；ChatGPT 目录模型不再要求连接测试，首个成功连接直接用于对话；聊天模型列表按名称降序排列，不附带账户名称。Chat 消息提供按需时间与当地日期分隔，Tasks、Worker、Traces、Memory 的时间及筛选统一跟随当前浏览器时区，近期日期使用自然标签、旧日期使用 yyyy-MM-dd；点击发送立即出泡并保留后续草稿，Intelligent UI 与最终文字按顺序分泡；支持主题气泡、安全 Markdown、认证附件链接与 Mermaid；折叠导航保留图标，标签页 Ghost 跟随主题。能力配置自动校验保存，使用结构化字段，不暴露底层配置文档。全部长期密钥统一使用本地私有文件，移除系统钥匙串后端及配置入口。完整 Context 并入 Generation，旧地址跳转保留；独立任务只读模块与纯审批投影不增加运行时状态或新控制管线。

F-041：产品品牌统一为 Steward，Agent 名为 Ghost。Web 使用非纯黑纯白的单色界面、Michroma 字标、Inter 操作字体和 Source Serif 4 回复正文；命令、配置、环境变量、存储与内部协议同步统一命名，不维护其他品牌的兼容入口。

F-020：飞书自建应用通过 SecretStore 引用自动获取/刷新 tenant access token，新增 CLI `channel status/check`；收发共用并发缓存，凭据拒绝有安全诊断，未知投递在重启后仍只查询。完成范围与真实平台验证边界见功能文档。

## 状态定义

| 状态 | 是否完成 | 含义 |
| --- | --- | --- |
| 已完成 | 是 | 本行限定范围已有非测试入口和实际调用链，并有相关回归入口；条件和证明范围见功能说明及测试方案 |
| 部分完成 | 否 | 已有局部实现，但目标用户流程仍有明确缺口 |
| 未实现 | 否 | 需求方向已确认，目标入口或闭环尚未接通；拥有底层数据不算功能完成 |
| 待定义 | 否 | 未来需求已登记，使用范围和验收条件尚需明确 |

完成状态只属于本行需求，不自动扩展为整个模块、全部平台或所有业务场景完成。配置依赖、刻意限制、待实现内容和未验证项应分别说明。

当前共 68 项需求，30 项已完成、14 项部分完成、10 项未实现、14 项待定义。此汇总随下表一起维护。

Web 检查统一两位小数耗时/费用及小额非零费用展示；F-009 维护记录提供逐次调用与安全校验原因，成功重试清除当前错误。

## 需求索引

F-002 的 Web 重新授权自动恢复同一账户的当前模型与发送；版本化目录激活保留其他账户/API 选择，不产生测试推理或 Probe。

| ID / 详情 | 需求 | 简要描述 | 是否完成 / 状态 |
| --- | --- | --- | --- |
| [F-001](features/model-connections.md#f-001) | 本地模型连接与选择 | Settings 的 Models 支持账户登录或 API 密钥连接，连接时自动校验，聊天输入框预取完整模型目录，首次加载明确提示，后台刷新保持可选，切换后保留列表。模型协议由 Steward 内部 Go 适配层实现；生成分别限制等待响应头、响应流空闲和整轮耗时。 | 是 · 已完成 |
| [F-002](features/model-connections.md#f-002) | ChatGPT 登录与跨平台凭据 | ChatGPT 账户凭据统一保存在 Steward 私有文件中，支持登录、按需刷新、重新认证、退出和中断恢复，后台状态检查只读配置与账户元数据。 | 是 · 已完成 |
| [F-003](features/conversation.md#f-003) | 可恢复的 Main 对话 | 用户能连续对话、追问和纠正要求，慢操作期间仍可交代、查询和纠正工作。Main/Worker 系统提示统一按角色、安全、执行规则和能力指导拼为一条；Main 可通过 Code Mode 调用 MCP 和 Skill，复杂工作委派 Task Worker。 | 是 · 已完成 |
| [F-004](features/tasks.md#f-004) | Task 生命周期与继续工作 | Root 失败有 Host 持久提醒，Task 保留可续作状态；支持显式 GitHub PR→已有 Task 关联，经 Main 有界接续并保留停止/隐私裁决；CLI/Web 与个人飞书具名问题、确切成果审阅接通，真实渠道与模型质量待验证。 | 否 · 部分完成 |
| [F-005](features/tasks.md#f-005) | 执行中的有意义进度 | 用户主动查看时能了解当前阶段、摘要与子任务完成情况。 | 是 · 已完成 |
| [F-006](features/delegation.md#f-006) | 可递归的 Child Worker | 复杂工作可由 Worker 拆成独立子任务，并由父级汇总。 | 是 · 已完成 |
| [F-007](features/tools-approvals.md#f-007) | 工具、审批与未知副作用处理 | Main 与 Worker 以 Code Mode 组合工具，直接声明少量高频工具，调用权限按角色冻结；MCP 不提供暴露模式，双方共用具体审批与恢复。 | 是 · 已完成 |
| [F-008](features/codemode.md#f-008) | 可恢复的 JavaScript 编排 | Agent 能用标准 JavaScript 串联工具、处理并发并显式输出结果。 | 是 · 已完成 |
| [F-009](features/memory-context.md#f-009) | 自动记忆形成与语义归并 | 四层记忆支持执行期笔记、链接表示、会话注入和后台 Dreaming；复用证据、版本、费用与遗忘屏障，兼容旧事实归并；失败重试使用独立 Bridge 会话，已接纳结果重放不重复推理；语义质量持续评测。 | 是 · 已完成 |
| [F-010](features/memory-context.md#f-010) | 按作用域召回记忆与对话 | 候选阶段排除当前工作集；Main 以普通对象 ID 沿消息、Task、授权产物续读。本机另可精读当前 Memory、可见来源及 Task 已关联的确切 GitHub 观察。Main/Worker 可经 memory_manage 分页精读密封记忆文档和同一作用域 transcript；显式搜索返回带偏移的短摘录并排除当前工作集，Dream 文档逐条绑定引文。更广外部导航待接通。 | 否 · 部分完成 |
| [F-011](features/memory-context.md#f-011) | 长期上下文压缩 | 长对话保留目标、决策、约束和待办，省略内容可追溯；新 Main 召回结合最近问答，以独立的不可信证据消息放在当前输入之前并按逻辑输入冻结；工具续轮保持消息前缀，旧 inline 策略按原快照恢复。 | 否 · 部分完成 |
| [F-012](features/artifacts-media.md#f-012) | 可重新打开的文件产物 | 用户能上传、保存、搜索、下载交付物，引用具有访问边界。 | 是 · 已完成 |
| [F-013](features/artifacts-media.md#f-013) | 用户图片视觉输入与本地 OCR | 视觉模型收到授权图片；纯文本模型可通过 Linux/macOS 本地沙箱 OCR 读取图片文字。 | 是 · 已完成 |
| [F-014](features/artifacts-media.md#f-014) | 音视频与文档内容处理 | 已支持授权 DOCX 正文与普通表格分页读取，保留原文件引用。音视频、其他格式与复杂版面仍待覆盖。 | 否 · 部分完成 |
| [F-015](features/automation.md#f-015) | 一次性与周期自动化 | 用户可在 Web 手动配置单次／周期安排、时区、指令及启停；CLI/Main 原有控制保留。 | 是 · 已完成 |
| [F-016](features/automation.md#f-016) | 阈值状态与安静通知 | 周期观察在首次进入阈值区间时通知，其余成功检查保持安静。 | 是 · 已完成 |
| [F-017](features/channels.md#f-017) | 终端交互 | 用户能完成模型设置、交代工作、查历史和状态。 | 是 · 已完成 |
| [F-018](features/channels.md#f-018) | 基础 Web 工作台 | Steward 四入口本机工作台；空白对话的标题与输入框整体居中，标题在中线之上，Tasks 与 Developer 复用紧凑列表规范；导航按钮就绪后才可操作，保留慢脚本及原生回退；默认免 token、对话、Worker 任务、审批提示、只读 Computer 预览与中英文界面；Enter 发送、Shift + Enter 换行及橙色发送按钮；Models 分组管理账户与 API 连接，连接时自动校验，Chat 输入区选择模型，工作待办定位问题、成果审阅、审批及任务故障。 | 是 · 已完成 |
| [F-019](features/channels.md#f-019) | Web 实时状态与回复更新 | 模型生成时，Web 逐段显示 Main 回复；完成后展示正式消息。审批、状态更新和输入保留仍独立工作。 | 是 · 已完成 |
| [F-020](features/channels.md#f-020) | 可维护的飞书交互与通知 | 个人原消息同步/Task 结果回复、具名问题及成果审阅卡、可选工作台成果链接与通知去重；显式 PR 接续复用原话题；真实平台验收仍待补，未知投递不盲目重发。 | 否 · 部分完成 |
| [F-021](features/intelligent-ui.md#f-021) | 富组件从生成到交互的闭环 | Main 主动选择原生组件或沙箱，有序交付正文与视觉；流式预览保持节点和阅读位置，衔接正式交互；支持中文排版、共轴多序列／数值横轴、同轮修正与本地图形／地理能力；表单、卡片及已声明动作保留 CAS 状态、冻结上下文、宿主授权、导出和响应式共享控件。 | 是 · 已完成 |
| [F-022](features/skills-experience.md#f-022) | 受版本控制的 Skill 读取 | Main 与 Worker 按名称或用途搜索冻结目录，按名称读取时由 Host 绑定版本，system 不预载 Skill 索引。 | 是 · 已完成 |
| [F-023](features/skills-experience.md#f-023) | 执行后的自动经验闭环 | 用户纠正、恢复失败或重复成功能进入候选、验证和晋升流程。 | 否 · 部分完成 |
| [F-024](features/integrations.md#f-024) | 受限本地 MCP 工具接入 | 操作员可接入允许的本地 stdio 工具；固定 Code Mode 调用、按工具或服务用途检索与 500 工具目录已接通，system 不预载 MCP Server 目录。 | 是 · 已完成 |
| [F-025](features/integrations.md#f-025) | 可认证的真实服务接入 | GitHub 精确 PR 监听可认证读取；F-071 提供 Composio 具名账户与审批动作，原生连接统一管理及真实账户验证仍有缺口。 | 否 · 部分完成 |
| [F-026](features/coding.md#f-026) | 代码理解、修改与交付 | Linux/macOS 本地显式仓库源、Task 私有工作副本、修改集和历史验证 Artifact 已接通，可手工关联已观察的 PR head，远端发布与修订核对待补。 | 否 · 部分完成 |
| [F-027](features/computer-use.md#f-027) | 配置型 macOS Computer Use | Agent 能在明确配置的隔离 Guest 中观察和操作应用与浏览器，并恢复逻辑 Computer。 | 是 · 已完成 |
| [F-028](features/operations.md#f-028) | 本地诊断与加密恢复 | 健康检查、加密恢复与 macOS 显式后台服务/独立巡检；持久人工停机和有限恢复已接通，平台 Live 与主动渠道告警待补。 | 否 · 部分完成 |
| [F-029](features/operations.md#f-029) | 用量记账与配置型费用授权 | 常见模型自动匹配目录价格估算费用，手动价格优先；显式货币授权继续独立。 | 是 · 已完成 |
| [F-030](features/capability-setup.md#f-030) | 统一能力设置与诊断体验 | 六类能力以普通表单配置、验证和保存；自动模式以 Auto／自动和选中态表达，启用状态不附加默认或推荐标记；界面不暴露 YAML，重启边界与凭据引用不变。 | 是 · 已完成 |
| [F-031](features/observability.md#f-031) | Developer 导航与执行关系检查 | 开发者从现有 Web 的 Developer → Observability 进入执行列表与因果关系时间线。 | 是 · 已完成 |
| [F-034](features/observability.md#f-034) | 逐轮上下文、来源与 Diff | 展开任意模型轮次查看 system/user/assistant/tool、工具定义、响应与上下文来源，并比较相邻轮次；加密记忆明确标注，避免将密封正文误读为空消息。 | 是 · 已完成 |
| [F-035](features/observability.md#f-035) | 供应商最终请求观测 | 开发者能区分 Host 冻结请求与 Bridge/SDK 转换后实际发送的请求。 | 是 · 已完成 |
| [F-036](features/observability.md#f-036) | 调用、等待、恢复与用量时间线 | 开发者能检查执行、等待、恢复、用量和模型流时间点，并区分各阶段超时。 | 是 · 已完成 |
| [F-032](features/evals.md#f-032) | Web Evals 入口 | 后续可从 Developer 查看评测结果，并关联对应执行。 | 否 · 待定义 |
| [F-033](features/memory.md#f-033) | Web Memory 记忆浏览 | Developer 按四层展示原始记录与记忆文档，复用 Traces 列表与详情侧栏，支持来源、关系、修订、筛选和分页；逐 Run 冻结注入在 Trace 核对，尚无版本差异或逐事件文档改动明细。 | 是 · 已完成 |
| [F-037](features/installation.md#f-037) | 安装、首次启动与显式部署 | 安装、检查、前台启动及自定义部署，并保持持久数据。 | 是 · 已完成 |
| [F-038](features/engineering.md#f-038) | 标准开发与显式发布流程 | 固定依赖、构建、风险分层验证、隔离并行 CI 与可审查交付。 | 是 · 已完成 |
| [F-039](features/engineering.md#f-039) | 运行时工程基础重构 | 故障隔离、短事务、只读分页、加密召回复用、明确模块与工具分发边界。 | 是 · 已完成 |
| [F-040](features/engineering.md#f-040) | Go 模块边界与可读性重构 | 领域持有适配器和接口类型；LLM 依赖方向与生产 Code Mode 装配有自动回归，目录规模及响应场景纳入 PR 门禁。 | 是 · 已完成 |
| [F-041](features/branding.md#f-041) | Steward 个人助手品牌与工作空间 | Steward 字标、悬停尺寸稳定的黑白头像、统一中英文界面和跨页操作布局；偏好设置用分组语言列表、自动保存；设置使用无外框字段行，删除重复分类与正常 Ready 标签，保留真实异常和辅助语义；全 Web 清理冗余介绍，必要指引用共享问号按需查看；模型同意常驻、Trace 状态可读，跨页复用 Select／Switch、字号与窄屏触控规则，通过复用门禁、首屏／状态 UT 和跨页／Task 浏览器回归约束；折叠偏好与慢加载布局稳定，编辑后清除过期连接／保存反馈，保留真实执行与审批语义。 | 是 · 已完成 |
| [F-042](features/integrations.md#f-042) | 可靠的环境事件监听 | 精确 PR 评审/CI 持久接收，可显式关联原 Task 经 Main 接续；其他来源、平台历史与原话题交付边界保留。 | 否 · 部分完成 |
| [F-045](features/automation.md#f-045) | 主动规则的观察与启用 | 确切 GitHub PR 可按事件类型或 CI 失败观察，再显式启用 Main 接续原 Task。其他来源与语义规则待验证。 | 否 · 部分完成 |
| [F-046](features/integrations.md#f-046) | Tavily 搜索与网页正文 | 已有自建 Search API 适配；独立 Extract 及真实服务 Live 待验，不将摘要当全文。 | 否 · 部分完成 |
| [F-047](features/integrations.md#f-047) | GitHub Connector | 在明确仓库读取 issue/PR/diff/CI 并完成授权协作动作；与既有精确 PR 监听和代码交付分别验收。 | 否 · 未实现 |
| [F-048](features/integrations.md#f-048) | 能力候选：macOS 本机文件与运行状态 | 在选定目录和具名服务内读取资料、检查环境和准备可审查的维护动作。 | 否 · 待定义 |
| [F-049](features/integrations.md#f-049) | 能力候选：SSH 远端设备与服务 | 逐台绑定账户、host key 和允许范围，读取远端状态并在明确授权下维护，不扩大 Guest SSH 权限。 | 否 · 待定义 |
| [F-050](features/integrations.md#f-050) | 能力候选：用户选择的 Chrome 页面与会话 | 接续选定的宿主浏览器页面和网页工作，保留登录态边界与陈旧页面／提交结果核实。 | 否 · 待定义 |
| [F-051](features/integrations.md#f-051) | 能力候选：ChatGPT 选定历史与附件导入 | 从用户提供的选定对话／导出找回旧资料，保留角色、来源和附件缺失；不将模型登录当成历史授权。 | 否 · 待定义 |
| [F-052](features/integrations.md#f-052) | 能力候选：Codex 选定工程会话导入 | 只读接入选定工程会话与产物引用，区分旧执行报告和当前事实，不修改 Codex 状态库。 | 否 · 待定义 |
| [F-053](features/integrations.md#f-053) | 能力候选：Pi 选定会话导入 | 导入选定会话及分支，接续既有主题，不迁移历史审批、认证或自动执行旧命令。 | 否 · 待定义 |
| [F-054](features/integrations.md#f-054) | 能力候选：图片生成与编辑服务 | 用明确图片服务完成生成／参考图编辑并保存版本关联；静态图与实际动画分别验收。 | 否 · 待定义 |
| [F-055](features/integrations.md#f-055) | Google Maps Connector | 解析用户给定地点及海外步行／公交路线，核实地点、入口与时区，不冒充完成预约或购票。 | 否 · 未实现 |
| [F-056](features/integrations.md#f-056) | 高德地图 Connector | 提供国内 POI、路线、天气及唤端链接，明确坐标和时效；唤起 App 不等于完成交易。 | 否 · 未实现 |
| [F-057](features/integrations.md#f-057) | Apple Health／HealthKit Connector 候选 | 确认数据来源后评估经授权的原生设备端集成；XML 文件导入不算 connector 接通。 | 否 · 待定义 |
| [F-058](features/integrations.md#f-058) | 飞书云文档与知识库 | 在明确资源范围内找资料、读取正文和形成／授权更新草稿；用户文档权限独立于机器人消息。 | 否 · 未实现 |
| [F-059](features/integrations.md#f-059) | Gmail Connector | 按用户条件查邮件／附件并先形成本地回复草稿；远端草稿、发送和受限授权分别处理。 | 否 · 未实现 |
| [F-060](features/integrations.md#f-060) | Jellyfin 家庭媒体库 | 查询指定服务器的可见媒体和播放相关状态，区分库中存在与客户端实际可播。 | 否 · 未实现 |
| [F-061](features/integrations.md#f-061) | AWS 资源与费用只读接入 | 在指定账户／区域读取资源和可用费用数据，不包含创建实例、升级计划或付费承诺。 | 否 · 未实现 |
| [F-062](features/integrations.md#f-062) | Google Calendar 候选 | 确认用户日历后定义忙闲、日程及授权创建，区分外部事件与 Steward 自动化。 | 否 · 待定义 |
| [F-063](features/integrations.md#f-063) | Apple Calendar／Reminders 候选 | 确认实际使用后定义 EventKit 本机组件及日历／清单权限，不复制内部 Task 权威状态。 | 否 · 待定义 |
| [F-064](features/integrations.md#f-064) | Google Drive 选定资料候选 | 先确认资料是否在 Drive，再定义选定文件读取／导出／保存，不承诺全盘搜索及全部原生编辑。 | 否 · 待定义 |
| [F-065](features/integrations.md#f-065) | X 帖子与讨论只读接入 | 在搜索／网页不足时读取确切帖子及可取得讨论，独立核验主张，不接私信、发帖或交易。 | 否 · 未实现 |
| [F-066](features/integrations.md#f-066) | YouTube 视频资料候选 | 确认视频委托需求后定义元数据及可授权取得的字幕，不能把元数据读取称为已看完视频。 | 否 · 待定义 |
| [F-067](features/integrations.md#f-067) | Exa MCP 搜索候选 | 验证免密钥的官方托管搜索、免费限流与来源结果；严格过滤不得静默降级，远端协议及质量待验证。 | 否 · 部分完成 |
| [F-068](features/integrations.md#f-068) | SearXNG 自托管搜索候选 | 已有自建 JSON API 适配及协议回归；真实实例、部署及维护责任待确认。 | 否 · 部分完成 |
| [F-069](features/integrations.md#f-069) | Web Access Plugin | 自建 native／Exa／SearXNG／Tavily 搜索、正文／PDF／图片、问答与来源证据；插件编排、服务适配及平台持久化已分离；更多服务／质量仍待验。 | 否 · 部分完成 |
| [F-070](features/integrations.md#f-070) | Plugin 生命周期与连接分离 | 独立安装／启停能力插件，复用平台管理的账户／实例连接；安装、连接授权与动作审批分别处理，当前尚无插件管理入口。 | 否 · 未实现 |
| [F-071](features/integrations.md#f-071) | Composio 应用目录与账户连接 | 默认读取 COMPOSIO_API_KEY，Apps 的 App settings 保留自定义连接服务配置，Apps 提供带官方 logo 的精选首页、每组六项预览与完整分组入口，搜索／全部应用保留实时分页目录、账户详情、已连接侧栏、按需认证、多账户、自动状态检查、重连与断开；工具走既有 Code Mode／审批／unknown 恢复。项目与真实服务验收待配置。 | 否 · 部分完成 |

## 实施优先级与依赖

- **P0**：F-042 环境事件监听、F-004 的跨渠道任务接续。
- **P1**：F-045 主动规则、F-004 的信息回答/成果审阅，以及 F-028 本地后台运行与巡检、F-010 关联证据导航、F-026 远端交付关联、F-018 待处理事项呈现、F-020 渠道适配交付。

优先级针对尚未实现的范围，不是交付日期。首个目标闭环为：已绑定飞书发起仓库工作 → F-025/F-026 提供实际服务与代码环境并形成可审查交付 → F-042 接收关联 PR 的评审/CI → F-004 接续同一 Task → F-020 回原话题交付。F-003 为耗时工作期间的对话响应提供基础。

执行复用现有 Task、Run、ToolInvocation、Main 交付与权限/隐私边界。来源事件和订阅上下文不授予新权限，暂停/取消/Forget 不由自动事件恢复，主动接单不替代具体动作审批。接口与迁移在相应功能实现前设计，不建立第二套任务生命周期、通用 Source/工作流平台、云端拆分或语义 Watchdog 自治修复。

<a id="personal-connector-priorities"></a>

### 个人 Connector 优先级

按[统一术语](features/integrations.md#术语与登记边界)，Plugin 是能力扩展包，Connector 是外部服务集成能力，Connection 绑定具体账户／实例，Skill 提供工作方法，Tool 执行具体操作。本规划按外部系统登记 connector，按工具、模型能力或一次性导入登记的 F-048–F-054 不参与以下排序。当前共有 **16 个新增外部系统／服务 connector 条目**，另有 **7 个非 connector 能力候选**；**F-069 Web Access Plugin** 组合搜索与网页访问，不重复计为新的外部系统；F-070 独立登记插件生命周期，F-025 维护平台连接。既有飞书消息集成 F-020 不重复计数。

以下顺序以 **ChatGPT 日常问题** 为主要依据，Codex 工程任务单独评估。优先已明确且重复委托、能补足实时／个人事实并减少截图与复制的接入，同时考虑已有工具覆盖和开发／授权成本。依据见[委托模式与证据边界](features/integrations.md#选择依据与证据边界)。这是定性建议，不是全量频次统计；旅途中连续提问不能直接视为全年频次，书签也不能证明资料来源。

顺序表示在前提成立时的建议开发顺序，不是服务授权或交付日期。标为待定义、或尚未确认实际数据来源的项先做需求／接入条件验证；不能仅因排位靠前就直接开发。公共检索服务与个人业务系统分别注明，具体供应商也需验证。

| 顺序 | 阶段 | 接入能力／外部系统 | 对应 ChatGPT 日常问题与接入收益 |
| --- | --- | --- | --- |
| 1 | P0 | [F-069 · Web Access Plugin](features/integrations.md#f-069) | 统一入口先验证当前 Steward ChatGPT／Codex 账户原生搜索，再接一个允许的后备；保持主模型与冻结权限 |
| 2 | P0 后备候选 | [F-067 · Exa MCP](features/integrations.md#f-067) | 随 F-069 验证免密钥独立搜索，只有显式允许才参与后备；免费限流与过滤保证需核实 |
| 3 | P0 | [F-056 · 高德](features/integrations.md#f-056) | 国内天气／穿衣／散步及地点路线；考虑平时国内生活先接，地域排序属于产品判断，不能当作已证明的使用频次 |
| 4 | P0 | [F-055 · Google Maps](features/integrations.md#f-055) | 海外餐厅链接、车站入口、酒店／车站间路线有直接委托证据；补旅行时的地点事实 |
| 5 | P1 | [F-059 · Gmail](features/integrations.md#f-059) | 订单解释、账户事项和个人资料找回可能减少截图／复制；这是对来源的推断，先验证资料确实在 Gmail，首版只读 |
| 6 | P1 | [F-057 · Apple Health／HealthKit 候选](features/integrations.md#f-057) | 睡眠／活动等回顾需求重复，数据价值高；先确认实际来源，确为 HealthKit 且设备端方案可行后再进入开发 |
| 7 | P1 | [F-058 · 飞书云文档](features/integrations.md#f-058) | 技术资料、面试材料和方案整理可减少重复粘贴；先确认资料存于飞书及资源授权，不能只凭已有消息渠道推断 |
| 8 | P1 可选扩展 | [F-068 · SearXNG](features/integrations.md#f-068) | 有明确实例时验证自托管路径，评估维护和上游覆盖；不自动选公共实例或部署服务 |
| 9 | P1 可选扩展 | [F-046 · Tavily](features/integrations.md#f-046) | 保留为正式 API／免费额度候选，不能仅凭功能声明优于已在 Pi 使用的方案；与前两项比较后选择 |
| 10 | P2 | [F-060 · Jellyfin](features/integrations.md#f-060) | 家庭媒体服务／播放问题有明确场景，但专用媒体库查询的覆盖面较窄 |
| 11 | P2 | [F-062 · Google Calendar 候选](features/integrations.md#f-062) | 有计划与提醒场景，尚未确认由该日历承载；不能把聊天提醒需求直接变成日历开发任务 |
| 12 | P2 | [F-063 · Apple Calendar／Reminders 候选](features/integrations.md#f-063) | 与上项是待确认的来源选择，先验证实际使用及与 Steward 自动化的分工 |
| 13 | P2 | [F-064 · Google Drive 候选](features/integrations.md#f-064) | 有附件／资料需求，但尚无充分证据指向 Drive；先确定文件实际存放位置 |
| 14 | P2 | [F-065 · X](features/integrations.md#f-065) | 帖子核验需求明确，先由搜索／网页覆盖；直连只有在结构化上下文或覆盖产生增量时再开发 |
| 15 | P2 | [F-047 · GitHub](features/integrations.md#f-047) | 在 ChatGPT 日常问题榜靠后；工程协作需求主要来自 Codex，仍保留独立工程路线的高优先级 |
| 16 | P2 | [F-061 · AWS](features/integrations.md#f-061) | 云账户／费用任务主要见于 Codex，不能据此占据日常个人助手首批开发资源 |
| 17 | P2 | [F-066 · YouTube 候选](features/integrations.md#f-066) | 只有消费入口信号，尚未证明经常委托视频分析及可取得字幕范围 |

首批交付是 **F-069 Web Access Plugin＋高德＋Google Maps connectors**。Web Access 按当前冻结账户／端点的已验证搜索能力选路，使用已验证的 Steward ChatGPT 托管搜索链路，再用 F-067 Exa 提供一个显式允许的后备。F-068 SearXNG 与 F-046 Tavily 已有独立适配，真实实例／凭据及各自 Live 分别验收。既有主模型不会因搜索失败自动切换，原生能力也不凭模型名猜测。具体边界见[Web Access 方案](designs/integrations.md#f-069--web-access-的最小组合方案)和 [自主实现边界](designs/integrations.md#参考行为与自主实现边界)。第二批在确认真实资料来源后选择 Gmail、健康数据或飞书文档；GitHub 的工程优先级仍独立维护。


F-071 已确认通过 Composio 广泛预备日常应用目录，用户按需授权，不受上述原生 Connector 实施排序限制；目录可见不代表账户已连接或领域目标已全部验收。F-020 继续维护既有飞书消息连接，机器人身份不代表已具备个人云文档或日历权限。F-025 持有认证集成共性缺口，F-042/F-045 持有事件和主动接续；新增 connector 独立验收，不把全部目录变成某个既有编号完成的附加门槛。

本机、SSH、浏览器是执行能力；ChatGPT/Codex/Pi 选定历史属于导入；图片生成属于模型能力。F-048–F-054 仅保留为非 connector 的待定义候选，需结合原能力归属另行确认，不按本榜推动实现，也不把定性习惯分析直接扩成新开发任务。

读取、写入、事件订阅及结果核实按所连系统分别定义；不是每个 connector 都需要监听或写操作。架构边界见[集成技术方案](designs/integrations.md#个人-connector-待实现方案)，验收见[集成测试方案](test-plans/integrations.md#个人-connector-验收计划)。上述需求尚未实施，登记不启用或授权任何 connector。

## 文档组织

```text
docs/feature-map.md                 需求索引、稳定 ID、是否完成
docs/features/<feature>.md          解释、需求、边界、如何使用、验收目标
docs/designs/<feature>.md           当前架构、流程、设计理由、兼容约束、实施缺口
docs/test-plans/<feature>.md        验收场景、测试层级、已有入口、缺失覆盖
```

一个 feature 可以包含多项增强和验收场景，用原稳定 ID 与功能文档中的小节定位。既有能力的完善维护在原编号的需求、缺口和验收范围内，只有独立新增的用户能力才分配新编号；不要把独立待办藏在设计说明或泛化“模块完成”中。

## 文档维护规则

内容归属与禁止事项统一遵循 [文档规则](README.md#强制维护规则)；本节规定需求与验收如何同步。

1. **登记需求**：既有 feature 的增强在原 ID 下补范围、缺口和验收，独立新增的用户能力才添加新 ID。范围扩大但尚未全部实现时更新为部分完成，保留已实现范围说明；已有 ID 不复用，不因改名而更换，合并时记录去向。
2. **解释需求**：功能文档说明用户问题、行为、使用条件、已有能力、剩余缺口和验收标准。尚无入口时明确“未实现”，不写成可执行教程。
3. **维护技术方案**：架构和流程始终更新为当前有效版本。区分当前运行实现、已确认待落地方案和待讨论建议；正文不堆叠旧版本。
4. **维护取舍**：架构或流程变化时原位更新当前选择、比较依据、理由、代价及兼容约束；失效说明及时移除。决策过程与历史版本通过 PR 和 Git 追溯，不追加日期记录。
5. **维护测试方案**：按需求 ID 对齐验收场景、层级、已有测试、未覆盖项及必要 Live 检查。计划与执行报告分开；测试结果保存到 PR、CI 产物或忽略的 evidence/，不能把“有测试代码”写成“测试已通过”。
6. **结束迭代**：实现、功能用法、最新技术方案、测试计划和本页状态一起更新。只有目标链路完成并有对应验证证据才能把新迭代标为已完成；范围缩小时明确记录，不能删掉缺口后宣称完成。
7. **保持边界**：跨功能契约维护在 designs 内，有效设计理由归所属技术方案，安装/操作/排障归入 features，统一测试与 CI 边界分别归入 test-plans/product 和 designs/engineering；不再保留独立 guides、ADR 或技术基线体系。真实模型效果评测仍遵循 [测试与评测边界](test-plans/product.md#testing-agent-evaluation)。

复杂功能写独立场景矩阵；简单功能的测试方案可简短引用已有回归入口，无需为文档层级另建测试框架。

Main 新建执行按输入逐条提交回复，Task Worker 独立运行；高频 Tool search、UI、Skill 与 Main Task 协调直接声明，其余工具保持按需 Code Mode 发现。旧冻结执行保持原有输入和工具面。

Web Settings 五个分类共用内容容器、标题位置与配置行选择器尺寸；配置行与命令对齐、输入区账户模型选择见 [渠道功能](features/channels.md)；超宽组件的无滚动条横向操作见 [Intelligent UI](features/intelligent-ui.md)。

Tasks 按真实 Root 派发分离[执行记录与任务配置](features/tasks.md)，短标题独立于完整任务简报，历史轮次保持可查。

Tasks 的执行记录、任务配置采用共享二级侧栏；详情选中态及列表筛选边界见[任务说明](features/tasks.md)。

F-010 的新 Run 复用完整可见证据并用有界密封元数据导航；F-009 的维护记录区分 token／参考费用、知识修改与后续曝光。Host 所有者绑定和外部 SecretStore 接入边界见[记忆技术方案](designs/memory-context.md#云端接入前的身份与密钥边界)。


Memory 当前实现还覆盖：混合工具结果压缩保留、跨表示事实纠正、全语料相关候选与固定 L4、原始 Worker 回执核验及精确续页、历史支持裁剪、Dream 维护列表／详情及有界 Transcripts 检视。边界和必需 CI 回归归 [Memory 方案](designs/memory-context.md#原始执行证据纠错身份与有界检视) 与 [测试方案](test-plans/memory.md)。

任务执行详情按本轮进展／Main 交付优先，说明默认折叠，提供同任务最近运行与 Root Trace；首轮工具调用也计入 TTFT。新生产 Run 的 Web Access 面仅保留 web_search／web_fetch，旧冻结入口维持恢复兼容。

任务执行列表以 Developer Trace 的共享非模态抽屉检查详情，保留背景筛选、实时更新、前后执行及完整页面／无脚本入口；任务控制沿原受控页面。

任务执行抽屉采用单列结果阅读布局与紧凑属性行；说明、技术信息和多轮历史按需展开，单次历史与旧进展不重复展示。

Trace 节点详情按输入／输出和诊断详情分工；默认隐藏内部噪声但保留原始证据，逐模型调用展示冻结模型、TTFT、输出速率、输入／输出与缓存用量及费用，长消息按明确边界完整读取。

Trace 详情默认采用可缩放、可定位的真实执行时间线并提供整条运行概览；列表时间直方图支持 Tooltip、键盘、点击／刷选过滤及触屏预览，与同一筛选的表格计数和边界一致。
