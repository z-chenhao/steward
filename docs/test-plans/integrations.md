# MCP 与真实外部能力：测试方案

[功能与验收目标](../features/integrations.md) · [技术方案](../designs/integrations.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 范围与证据

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-024](../features/integrations.md#f-024) | 真实协议验证发现/调用、漂移拒绝、取消回收与外部效果审批。 | 保护已接通的限定范围；Live/配置另行验证 |
| [F-025](../features/integrations.md#f-025) | 在明确服务上完成真实读取/授权动作/失效恢复；无凭据泄漏，审批及 unknown 语义保持一致。 | 必须通过目标入口补齐闭环；不能只直接调用底层方法 |
| [F-042](../features/integrations.md#f-042) | 受控来源重复/乱序、补读、重启、撤权、自身回流、stop/Forget fencing 与真实故障状态；完整扫描后才提交事件和成功边界。 | 已接通监听→SQLite→CLI；Main/Task/飞书接续、真实 GitHub 权限及历史保留边界需后续入口与 Live 验证 |

## 个人 Connector 验收计划

F-046–F-070 的[分类与范围](../features/integrations.md#术语与登记边界)、[接入顺序](../feature-map.md#personal-connector-priorities)和[待实现方案](../designs/integrations.md#个人-connector-待实现方案)是以下验收的依据。16 项外部系统／服务 connector、F-069 Web Access Plugin、F-070 插件生命周期与 7 项非 connector 能力候选分别验收，最后一类不构成已确认开发任务。F-069 及其 F-067 后备已有工具入口和专项测试，F-071 已提供 Composio 应用通道；原生条目与各服务领域验收仍按计划分别验证，不用目录可见代替真实业务证明。已有 MCP、Guest、Artifact、Memory 或精确 PR 测试不能证明新服务已可用；待定义项先确认数据来源、平台和行为范围，再固化场景。

### 分项场景矩阵

| 独立需求 | 正常场景与可观察交付 | 关键失败／边界 | 计划层级与当前缺口 |
| --- | --- | --- | --- |
| [F-046 Tavily](../features/integrations.md#f-046) | 找到指定原文→读取正文→返回可打开来源与时间，摘要／全文可区分 | 零命中、正文提取失败、截断、旧文章／转载、429、恶意网页指令 | HTTP 协议 peer＋产品系统链路；真实站点／中文覆盖需 Live，全部待补 |
| [F-047 GitHub](../features/integrations.md#f-047) | 在指定仓库读 PR/diff/CI，经授权发草稿 PR／评论后核对确切结果 | 错账户／仓库、权限不足、head 已变化、限流、未知写结果、重复发布 | 协议＋系统；复用现有监听回归但新协作链路待补，真实 App/PAT 需 Live |
| [F-055 Google Maps](../features/integrations.md#f-055) | 解析海外地点链接，核对城市／入口并给出指定方式路线 | 同名地点、站点入口偏差、短链失效、无公交方案、时区错误、过期营业信息 | 协议＋系统；真实地点／地区数据质量需 Live，待补 |
| [F-056 高德](../features/integrations.md#f-056) | 国内 POI／天气／路线可追溯，导航／打车链接正确交接 App | 坐标体系混淆、天气过期、配额耗尽、URL 日志泄钥、把唤端当交易完成 | 协议／MCP 视实际方案＋系统；真实 API 配额与 App 唤端 Live，待补 |
| [F-057 Apple Health／HealthKit](../features/integrations.md#f-057) | 来源确认后通过授权设备端组件读取选定时间窗，区分单位／设备 | 时区跨日、重复设备、缺失值被当正常、设备离线、授权撤销 | 先确认来源；设备端协议及隐私系统链路、真实 HealthKit 授权需 Live，XML 文件导入不计本项接通 |
| [F-058 飞书云文档](../features/integrations.md#f-058) | 搜索并读取明确资源／版本，生成草稿或授权更新确切位置 | token 身份混淆、知识库未授权、权限撤销、并发编辑冲突、写入未知 | 协议＋产品系统；用户／应用身份和真实租户授权 Live，待补 |
| [F-059 Gmail](../features/integrations.md#f-059) | 找到指定线程和附件，形成未发送的本地草稿 | OAuth 过期／撤销、分页、过滤条件被误当 scope、恶意邮件、发送未知 | 协议＋产品系统；真实 OAuth／scopes／适用验证条件需 Live，待补 |
| [F-060 Jellyfin](../features/integrations.md#f-060) | 读取选定库和媒体／播放状态，指出真实阻塞环节 | 服务器离线、库不可见、媒体存在但客户端不能播、认证失效／版本差异 | 协议＋受控媒体库系统；实际客户端播放 Live，端点仍需核实，待补 |
| [F-061 AWS](../features/integrations.md#f-061) | 在指定账户／区域查资源，并解释可取得费用及时间范围 | 未查其他区域、无费用权限、账单延迟、余额／credits 混淆、凭据过期 | 协议＋系统；受控只读账户 Live，不创建付费资源，待补 |
| [F-062 Google Calendar](../features/integrations.md#f-062) | 来源确认后读忙闲／日程，经授权创建明确事件 | 跨时区、重复日程例外、同步游标过期、并发冲突、邀请误发／重复创建 | 先确认来源；协议＋系统、真实日历 Live，全部待补 |
| [F-063 Apple EventKit](../features/integrations.md#f-063) | 来源确认后只访问选定日历／清单，正确展示日程或提醒 | OS 拒绝／撤权、只写与完整权限不同、重复提醒、本机组件离线 | 先确认来源／分工；组件＋系统及真实 OS 权限 Live，全部待补 |
| [F-064 Google Drive](../features/integrations.md#f-064) | 来源确认后选择文件→读取／导出／保存，范围与操作明确 | `drive.file` 被误当全盘搜索、原生类型不支持、下载禁止、协作冲突 | 先确认来源；协议＋系统、真实 Picker／权限 Live，全部待补 |
| [F-065 X](../features/integrations.md#f-065) | 对网页覆盖不足的确切帖子取得可用上下文，再独立核验 | 删除／受限、引用缺失、媒体不可读、费用／额度不足、转载冒充完整来源 | 先核实直连接入增量；协议＋系统，真实账户覆盖 Live，待补 |
| [F-066 YouTube](../features/integrations.md#f-066) | 确认任务后区分元数据和已取得转录，以时间戳引用实际内容 | 无字幕、无编辑权限、语言不符、只凭标题生成完整视频摘要 | 先确认任务；协议＋系统、真实字幕权限／内容质量 Live，全部待补 |
| [F-067 Exa MCP](../features/integrations.md#f-067) | 免密钥查询得到实际来源；正文有无明确，实际服务／工具可追溯 | 429、工具／schema 变化、缺正文、无效响应；严格过滤不能静默变成提示，取消后不换源 | 目标传输协议 peer＋产品系统，F-069 中已有协议 peer 和 opt-in 在线测试；长期限流／中文质量仍待验证 |
| [F-068 SearXNG](../features/integrations.md#f-068) | 查询选定实例，展示来源及已知上游失败／部分结果 | JSON 未启用、缺结果字段、403／429、跨 origin 丢认证、域名筛选／时间保证变化 | HTTP peer＋产品系统；实际实例与上游质量需 Live，未部署，全部待补 |
| [F-069 Web Access Plugin](../features/integrations.md#f-069) | 按冻结的已验证账户能力搜索，读公开网页并续读 Artifact；原生及允许后备的真实来源可追溯 | 主模型／账户不切换，零命中不换源，取消／撤权终止；预算、过滤、来源／live 模式不能静默退化 | 已有模型／HTTP peer、SQLite 工具与 Artifact 集成；Steward 自有账户和内容质量仍需 Live |

搜索源比较使用同一批公开的中文原文查找、技术说法核验及英文资料查询，分开记录成功率、来源可核查性、时效、正文覆盖、延迟和实际费用。免密钥不代表无配额，代码支持不代表当前网络可用。Pi 插件的 `source_check` 返回证据不表示自动判真伪；若复用片段组织，只验证来源和引用正确，语义结论另行评测。实现过滤降级／后备源前，验证取消、认证拒绝及未授权目的地／费用不会触发自动切换。比较与执行证据留在 PR／CI 或忽略的 evidence/，不写入长期文档。

### F-069 的链路与失败矩阵

专项入口为 `make test-web-access`，在 Ubuntu checks 中对 connector／provider／bridge／SQLite 的相关用例运行 race；完整检查为 `make test lint`、`make test-prompts` 与 core E2E。

| 场景 | 证明入口 | 证明边界／缺口 |
| --- | --- | --- |
| 自主实现边界 | `TestWebAccessHasNoAgentSDKDependency`：解析运行时仅允许锁定通用库，无外部 agent 实现目录 | 依赖变更仍须许可证、安全与架构审查，不以测试代替人工审查 |
| 选路与服务协议 | `expanded_test.go`：SearXNG／Tavily 请求、凭据、域名、时间条件、限流与终止错误、禁用目的地；旧搜索回归验证零命中和不允许后备的错误 | SearXNG 真实例和 Tavily 真账户另验，不宣称统一日期硬保证 |
| 公开网络与解析 | 既有地址／重定向／大小 peer，加真实本地解析进程的 Markdown／raw／PDF／图片、宿主路径拒绝 | 不执行网页脚本，无登录浏览器／视频；fake-IP 拒绝需与实际服务失败分开 |
| 文本与证据 | Unicode 大小写、字节坐标、按序 fuzzy、完整原文片段与双哈希 | unclear 只表示待人工判断，不证明 claim 为真 |
| 模型交换与生命周期 | SQLite 辅助调用的 frozen 模型、预算、usage、重入哈希、拒绝旧 claim／未接受查询；既有账户／Forget fence 回归 | 模型费用和外部搜索费用分别报告 |
| Main 与持久化 | 真实 Code Mode 调用→图片和状态 Artifact 原子提交→后续调用读取全文／查找；多附件配额与终态失败回滚 | 外部内容为确定性 Fake；真实联网单独验证 |
| Responses 协议 | bridge／provider peer：真实搜索 SSE／来源／usage，域名条件；SIWC 无 max_tool_calls，namespace 含 description | 实际账户已验证原生搜索及 Main Code Mode，不能扩展为所有模型／订阅保证 |
| Live | 显式免费 Exa／公开网页探测；经本人授权的临时 Steward 账户跑 Main→Code Mode→native→Artifact，并核对来源／用量／终态 | 不读取其他助手凭据；凭据、账户身份和授权 URL 不进入 PR 或测试日志。其他服务、中文质量和长期额度独立验收 |

F-069 保持部分完成：已接通的核心链路不证明未接入供应商、视频／登录网页或所有账户可用。真实执行证据留在 PR／CI／忽略的 evidence/，不写入长期运行日志；macOS 临时路径或浏览器系统差异与 Ubuntu 必需门禁分别报告。

### 非 Connector 能力候选的验收方向

以下七项保留原 ID 供追溯，不是 connector 专项验收，也不表示已确认独立开发任务。只有在相应能力范围另行确认后，才纳入对应功能的实施／测试计划。

| 能力候选 | 可观察方向 | 关键边界 | 未确认的验证范围 |
| --- | --- | --- | --- |
| [F-048 macOS](../features/integrations.md#f-048) | 选定目录／服务后解释占用／状态，维护前展示具体目标 | 链接逃逸、越界目录、文件并发变化、OS 权限拒绝、错误清理目标 | 可丢弃本机夹具＋系统；真实 macOS helper／权限需 Live，待补 |
| [F-049 SSH](../features/integrations.md#f-049) | 对具名主机核对身份、读服务和日志；授权维护后验证状态 | host key 变化、错主机、密钥拒绝、断连、动作已执行但回执丢失、回滚失败 | 受控 SSH peer＋系统；真实目标机 Live，现有 Guest SSH 不能替代，待补 |
| [F-050 Chrome](../features/integrations.md#f-050) | 读用户选定页面，明确同页导航／表单步骤，核实最终页面状态 | 陈旧标签页、重定向／登录失效、页面关闭、用户抢占、提交未知、越界标签页 | 浏览器受控站点＋系统；宿主浏览器和 helper Live，待补 |
| [F-051 ChatGPT](../features/integrations.md#f-051) | 导入选定对话，沿来源找回旧资料及实际存在的附件 | 多分支／编辑、重复导入、残缺导出、缺附件、格式未知、历史提示注入 | 脱敏导出夹具＋产品导入／召回系统链路，格式兼容待验证，全部待补 |
| [F-052 Codex](../features/integrations.md#f-052) | 从选定工程会话给出原要求、未决事项和确切产物引用 | 来源格式升级、已删除产物、旧“通过”报告与当前失败、重复／撤销 | 合成会话＋系统；不复制用户状态库，真实版本兼容待验证，全部待补 |
| [F-053 Pi](../features/integrations.md#f-053) | 从选定分支导入主题，按来源接续设备或工程讨论 | 分支混合、截断 JSONL／其他格式、未知版本、旧命令诱导执行、来源移除 | 合成会话＋系统；格式和分支兼容待验证，全部待补 |
| [F-054 图片服务](../features/integrations.md#f-054) | 文生图／参考图编辑返回可保存重开的图片；实际动画需播放验证 | 原图缺失／错引用、服务拒绝、费用上限、取消、超时未知、静态文件冒充动画 | 协议＋Artifact 系统；生成／编辑质量及实际动画为独立 Live/评测，待补 |

### 共同验收与完成标准

1. **真实入口**：每个 connector 经实际 CLI/Web/Main/Task 的工具发现与调用交付；不能只运行 SDK 示例或直接调用内部方法。未配置、缺凭据、缺 OS 权限、不可用和健康状态分别可见，不为尚无入口的功能写可执行教程。
2. **范围与授权**：用合成超范围资源验证账户／仓库／目录／主机／标签页／文件边界；认证刷新不能扩大权限。外部文本及历史会话不能创建新授权，具有写能力的供应商 scope 不表示 Steward 自动拥有写动作权限。
3. **取消与恢复**：覆盖取消、重启、重复调用、token 拒绝／刷新、限流和断连；写请求发出后的结果未知保留 unknown，只有供应商提供真实查询／幂等语义时才按具体契约恢复，不能假定所有服务都可重放。
4. **隐私与来源**：日志、错误、URL 和模型输出不得出现凭据；选定原文／附件来源可追溯。撤销导入范围和 Forget 清除可召回材料及索引，旧 Run／旧导入不能重新暴露数据；历史消息不是当前事实或新指令。
5. **事件另行验收**：仅当该服务确需主动监听时，测试分页／增量游标、过期重同步、重复／乱序、删除／中间版本边界、订阅续期、stop 与健康；资料读取工具不自动获得监听或任务接续能力。
6. **实际效果**：能读取／生成内容不等于语义正确、收到通知或动作成功。地点正确、来源忠实、视觉效果、实际播放及模型范围判断按 [Testing 的质量边界](product.md#testing-agent-evaluation)另行评测。服务失败不得用模型补全结果冒充成功。

### 数据、Live 与文档登记检查

使用一次性 `STEWARD_DATA_ROOT`、合成邮件／文档／会话／健康记录、受控主机与浏览器站点，不复制个人数据库、证件、健康实值、面试原文或真实凭据到测试夹具。Live 单独使用明确账户／资源和适用权限；健康设备、地图数据、OS 授权、实际媒体播放及服务验证条件必须记录真实证明范围，未运行项保留缺口。

仅更新需求、设计约束和测试计划时，适用检查为 `python3 scripts/check_docs.py` 与文档内容审查；无需因登记需求运行 Go/Node 产品测试。未来实现每项后，正常和关键失败／恢复路径须经目标入口验收，更新当前功能说明、技术方案、测试入口与该项状态；其他 connector 的完成不能代替本项证据。执行结果留在 PR／CI 或忽略的 evidence/，不写入本文。

## 层级与关键路径

本地 stdio 测试协议与沙箱、允许列表及 schema 漂移；远程能力落地时增加真实认证生命周期、服务失败与副作用未知测试。线上服务质量不由本地 MCP Fake 证明。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 已有检查与缺失覆盖

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_capabilities.py](../../tests/system/verify_capabilities.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [internal/observer/github/client_test.go](../../internal/observer/github/client_test.go) | 实际 HTTP 协议 peer 驱动分页、认证头、自身份排除、评论更新身份、review/CI、恶意 Link/redirect、撤权及异常响应；不需要真实 PAT |
| [internal/agent/sqlite/github_test.go](../../internal/agent/sqlite/github_test.go) | 真实 SQLite 去重、观察/Outbox 原子接受、故障边界、重启补读、repository ID pin、并发/停止/Forget fencing 与当前 epoch 读取 |
| [internal/agent/sqlite/github_followup_test.go](../../internal/agent/sqlite/github_followup_test.go) | 真实 HTTP peer→SecretStore→观察→Main→同 Task append；重启/重复只接续一次，重复 Tool 参数复用回执，不同动作拒绝；自然完成与人工结束、解绑/监听停机/暂停/取消/Forget、晚模型与工具、unknown 和新用户优先；Store/App 移除或切换配置在 Actor 前撤销、重加不复活；旧 scope 调度不占用新 Main 或饿死普通 Worker；assigned 遇网络/歧义错误重启后只接续一次，明确身份/凭据拒绝撤销；GitHub 接续结果经真实 Feishu HTTP peer 回原 Task 消息 |
| [cmd/steward/github_test.go](../../cmd/steward/github_test.go) | 真实 CLI 调度与重开 data root，在执行依赖不可用时保持状态/停止/恢复/配置移除可用，缺凭据失败可诊断 |

- **F-025 剩余覆盖**：真实 PAT 权限、到期/更换与 GitHub 保留历史需 Live；OAuth、授权写动作、其他服务及对应入口仍未实现。
- **F-042 剩余覆盖**：已接通精确 PR→Main→原 Task；真实飞书原话题交付见渠道方案，实际模型范围判断需独立评测。真实来源删除、旧 head、中间修改与平台历史保留不由协议 peer 证明。

## 详细测试入口与证明边界

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="capabilities-reproduction-and-evidence"></a>

### Reproduction and evidence

```sh
make build
python3 tests/system/verify_capabilities.py --binary bin/steward --evidence evidence/capabilities.json
go test -race ./internal/tool/... ./internal/skill ./internal/computer/... ./internal/config
```

The harness launches private data/provider/Skills/checkout roots, doctors an
explicit Fake model, drives ordinary chat/Task/approval CLI commands, reads SQLite
and file hashes, kills after a real edit before result commit, restarts, and
cleans every child and disposable root. The system runner selects it; repeat count is explicit.
The coding fixture starts with a real failing `--status paused` regression,
edits only its CLI through a Worker, then passes its real race tests. Audit reads
the deliberately defective retry ordering without changing any byte. Go
regressions additionally probe host credential reads/writes, environment/network
denial, link escapes, catalog drift and executable Git config.

This extends the M3 Tool/Approval/Artifact fragments in `verify_tools.py` and
`verify_artifacts.py`; it does not assign a semantic Golden score. GC-19 is a
coding prerequisite here. This fixture marks GC-20 Child/recovery and full
GC-19/20 Durable cases `not_applicable`; the [Core gate](product.md) covers
their additional mechanics. Production Computer has [separate verification](../designs/computer-use.md).
The GC-11 checkpoint now drives both original turns in six locked variants,
reads all three actual provider quotes with services and user weights, preserves
oral-cashback uncertainty, delivers two scripted replies and reopens both
comparison Artifacts. It also rejects purchase/approval/Task effects and proves
duplicate-input state equality. The same five-round capability gate includes
every variant. Its deterministic scripted model does not establish live-model
reasoning quality; see [release evidence and remaining obligations](product.md).

The local MCP path needs no new domain states: schema v12 already freezes arbitrary
ToolSpecs, Skill set hashes, policy and invocation outcomes. GitHub observation
adds schema v33 watch/observation records separately and does not extend ToolSpecs. Empty/prior schema
migration tests remain in the existing SQLite gate. Old capabilities without
these optional fields keep their old tools; enabling an adapter never expands an
already frozen Run. Changing a configured scope fails old-Run selection closed.

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

渐进工具暴露与 500 工具目录的配置、Main 搜索加载、Worker 脚本发现、隐藏/未声明调用拒绝及恢复覆盖，统一见[工具与审批测试方案](tools-approvals.md#渐进工具发现与-500-工具目录)。CLI capabilities 的 `tool-loading` 已纳入完整门禁，读取同一角色冻结目录。

### Plugin、Connector 与平台边界

术语按[功能说明](../features/integrations.md#术语与登记边界)统一：内容审查核对能力插件、外部服务连接器、具体连接、技能与工具的区别，以及安装、连接授权和动作审批的独立性。Web Access 仍是内置插件；Exa／SearXNG／Tavily 的适配器证明对应服务协议，Tavily Search 不代表 Extract 已接通，研究配方测试不代表已有可安装 Skill。独立安装、共享连接和多账户选择继续按 F-025／F-070 的实际入口验收。

`make test-web-access` 覆盖 `internal/plugin/webaccess`、三个服务 connector、`internal/webcontent` 与 SQLite Host 的 race 回归，CI 继续由必需的 `Ubuntu checks` 执行。

| 场景 | 现有入口与证明范围 |
| --- | --- |
| 依赖方向 | 插件的 `architecture_test.go` 检查实际 Go 传递依赖；connector 不依赖插件／工具／平台，内容处理不依赖服务／模型，插件不依赖 Agent／SQLite／App |
| 插件研究配方独立于 SQLite | `plugin/webaccess/plugin_test.go`：受控 Host 驱动两次原生查询、去重读取、部分失败、摘要草稿、完整证据及预览，再从保存证据查找；预算／认证／取消终止后不再换源或生成 |
| 各服务协议与来源政策分离 | Exa 所属包协议 peer；插件 `expanded_test.go` 经实际 SearXNG／Tavily adapter 验证认证、请求、限流换源和无效响应终止 |
| 处理机制复用 | `webcontent` 所属测试验证公开 DNS 固定、地址／重定向拒绝、HTML／PDF／图片／编码和本地解析，不需要服务连接或 SQLite |
| 平台安全语义 | SQLite 既有辅助交换测试验证冻结 Snapshot、预算、请求哈希／重放／过期 claim；Main→真实 Code Mode→Artifact 测试验证作用域续读，媒体／证据／终态原子提交与失败回滚 |
| F-070 待实现 | 将来需经实际安装／启停／移除入口验证共享连接保留、断连与卸载独立、冻结权限不扩大；内部模块拆分不视为生命周期验收通过 |

核心 channels 的 tracing 只读验收先等待完整因果链与终态轨迹，再比较一致快照，详见[可观测性测试方案](observability.md)。

工具名、配置序列化和资源身份保持兼容；重构兼容检查比较旧版冻结定义与当前定义。真实账户／实例及模型质量继续按各服务 Live 的证明范围验收。


## F-071 · Composio 应用连接验收

聚焦入口：`go test ./internal/connector/composio ./internal/connectedapps ./internal/plugin/connectedapps ./internal/tool ./internal/interaction/web ./internal/app ./internal/agent/sqlite -run '(Client|Apps|ConnectedApps|RegistryGroup)' -count=1`；并对这些包运行 race。新的连接交互浏览器门禁由 `make test-browser` 执行：`STEWARD_TEST_APP_BROWSER=1 go test ./internal/interaction/web -run '^TestAppsBrowserConnectionLifecycle$' -count=1`。PR 的 `connected-apps` 软件分片执行 `make test-connected-apps`，覆盖全部 Composio／账户管理／图标／Registry 定向 race；浏览器生命周期随 `browser-infra` 强制运行。跨模块门禁为 `make test lint` 与 core E2E。

| 场景 | 证明入口及可观察结果 | 边界 |
| --- | --- | --- |
| 默认项目引用 | `TestConnectedAppsDefaultEnvironmentKey` 与 `TestConnectedAppsExplicitReferenceOverridesEnvironmentDefault`：缺失环境变量保留离线目录，存在时无需设置保存即可查询目录和连接；读取不写库、不创建绑定，显式 Connect 保存引用；已有引用优先，凭据缺失时不退回默认项目 | 使用真实环境解析和受控 HTTP peer，不证明真实项目 key 有效 |
| 项目与目录 | HTTP peer 核对项目 key header、目录查询／游标传递、托管 session 的关闭选项与认证链接主机；无项目显示待配置推荐，不能创建账户工具 | 不证明项目权限、供应商目录规模或特定应用受支持 |
| Web 账户管理 | `TestAppsOfflineDirectoryAndOwnerConnectionFlow`：真实 Server→Service→SQLite→HTTP peer，验证配置、连接、检查、断开、双语呈现、HTML escaping、分页及同源／身份拒绝 | 不自动替用户在真实 OAuth 页面同意 |
| 升级与禁用 | `TestConnectedAppsMigrationV45PreservesUIAndRollsBack` 验证 v45 升级、失败回滚、UI 数据保留和安装身份稳定；`TestConnectedAppsDisabledAccountWithdrawsTools` 验证禁用账户不执行、状态转失效且撤下工具 | 不证明供应商真实撤权时延 |
| 持久化与撤权 | `TestConnectedAppsLifecyclePersistentIsolationAndRevocation`：同应用双账户、项目切换限制、CAS、身份核对、重开数据库、断开后迟到检查及上游撤权失败 | 本地关闭不代表已撤回远端的既有效果；记录删除不保证原服务撤权 |
| 工具执行与恢复 | `TestConnectedAppsApprovedExecutionReplayAndUnknown`：真实 SQLite ToolInvocation／审批→HTTP 执行；未批准不调用，批准后固定账户／版本，成功重放不重复，502 持久 unknown；撤权、错误用户、描述漂移均拒绝 | 协议 peer 不证明 Gmail／日历等真实业务成功 |
| 冻结与并发 | `TestRegistryGroupReplacementKeepsSnapshotsAndOtherGroups`、生产装配测试：新增工具不扩权旧快照，撤销不能被旧声明重新选择，不影响其他工具；并发读取／替换 race | 每个执行闭包仍需独立重检撤权状态 |
| Code Mode | `TestConnectedAppsRealCodeModeDiscoveryApprovalAndFrozenConnection`：真实 JavaScript Sidecar 发现工具、挂起审批、恢复执行；新增第二账户不扩权旧 Root | 远端业务内容由 HTTP peer 提供 |
| 公开应用 | `TestConnectedAppsPublicToolkitRequiresExplicitEnableAndNoAuthTool`：未启用无工具，显式启用后只调用免认证操作，禁止变成有账户操作 | 真实目录的 `no_auth` 标记仍需验证 |
| 凭据与异常 | 客户端测试覆盖重定向拒绝、错误文本不泄漏密钥、账户私密字段不进入状态／页面、模糊 mutation 不重试、响应上限及取消 | 真实 Composio 托管凭据／令牌刷新由其服务持有，需要真实项目验证 |

Live 缺口明确保留：创建项目后，用受控测试账户完成至少邮件／日历／文档各一次授权、读取和批准写动作；核对 scope、业务来源、实际结果与供应商用量，再验证 token 过期、重连与原服务撤权。授权 URL、密钥、token 和私人正文不进入测试日志或 PR。暂无项目时不把协议 peer 标为 Live 成功；F-071 维持部分完成。


部署／使用分层与 OAuth 交互验证：`TestAppsBrowserConnectionLifecycle` 启动真实 Web Server、SQLite、Composio HTTP peer 与 Chromium，执行无环境凭据时离线浏览→供应默认凭据后无需保存即可出现 Connect→自定义服务设置保存→Apps 无技术配置表单→首次 Connect→复用授权链接→自动变为 Connected→额外账户必填别名→弹窗拦截后的手动继续→按账户断开→失效后显式 Reconnect。将早先 ACTIVE 轮询响应延迟到 Disconnect 之后，证明前端也不会复活已断开账户；检查链接未进入浏览器存储，并覆盖中英文与 1400／390／320px。该门禁不访问真实 Composio 或账户。

`TestConnectedAppsAuthorizationReuseAndExplicitReconnect` 验证进程内链接复用、重启后无链接、重连生成新绑定及迟到授权拒绝；客户端 auth-config peer 验证分页、最新启用配置、明确覆盖、错误 toolkit 拒绝及 scoped key 的托管认证退路。配置与账户状态分开验证，轮询不对同一状态重复推进 CAS revision。


Apps 图标与目录：`TestAppLogosBundledForEverySuggestedApp` 检查每个推荐应用的同源 SVG 响应、可解析形状与缓存头；`TestAppLogoNewProviderMetadataUsesAnonymousBoundedCache` 验证新应用采用官方元数据、匿名读取与缓存，拒绝任意主机、带凭据／端口／查询参数的来源和未登记名称。`TestAppLogoSanitizationRetainsBrandShapesAndRejectsActiveContent` 覆盖脚本、事件、嵌入页面、外链、多根／损坏 XML、过大响应和重定向，同时保留品牌渐变。客户端目录测试校验 `meta.logo` 投影。

`TestAppsBrowserConnectionLifecycle` 另外验证完整目录中 65 个编辑应用全部加载真实图片、中英文发现页在 1400px 为双列、390／320px 为单列且无横向溢出，以及深色显示；账户详情弹窗可用 Escape 关闭，连接后侧栏显示品牌图标，断开后移除侧栏项。宽目录标题按自身内容边界对齐，保留 Settings 标题的纵向位置和字号。截图只含合成账户，浏览器门禁不访问外部账户或图标 CDN。


目录呈现验收：`TestAppsDiscoveryPreviewsAndCollectionsKeepEveryAppReachable` 验证六个精选顺序、每组最多六项、首页无重复、完整分组名称排序、所有编辑应用仍可达、保存的连接状态与未知分组拒绝；`TestAppsBrowserConnectionLifecycle` 验证精选／分组／全部应用导航、原生无脚本分组与实时目录入口，保留完整目录分类筛选、图片及分页覆盖；Settings 的 Apps 四宫格图标与 Resources 服务器图标可直接区分。保留中英文、移动端、搜索、应用详情及连接流程检查。


合入统一组件规范后的 Apps 浏览器场景复用真实 Base UI 分类选择器，验证取消筛选不提交；断开确认默认聚焦取消、Escape 保留账户、确认后仅断开目标。截图覆盖明暗主题的桌面／手机目录及账户详情，并检查 200% 缩放下弹窗及其内容留在可操作范围内。数据库迁移在已有 044／045 后追加 046，回滚夹具移除新表后按原有迁移路径重开。


目录浏览器场景核对无项目初始化／服务配置入口和冗余筛选确认按钮，服务配置通过 Resources 进入；选择沟通类别会直接得到对应 8 项推荐，恢复全部类别回到 65 项，不创建账户授权。

`TestProductionToolSurfaceKeepsOnlyFrequentControlsDirect` 使用生产组合检查 Main／Worker 均排除四个退役入口，保留的 web_search 可发现，冻结旧入口仍可选取并在 Code Mode 调用。`TestDefinitionsFreezePolicy` 检查 web_search v2 不再推荐退役入口且 v1 可恢复。`TestToolOutputTimingPrecedesCompleteArguments` 覆盖工具元数据／参数 delta 均可开始计时，空协议帧不能计时。
