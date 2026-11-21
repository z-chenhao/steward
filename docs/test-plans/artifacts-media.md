# 文件产物与多模态输入：测试方案

[功能与验收目标](../features/artifacts-media.md) · [技术方案](../designs/artifacts-media.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 完整组合与兼容验收

并发导入用暂存边界交错两个相同渠道附件，核对一个活动 Artifact 和一个映射。通过真实 HTTP 下载验证原长度、完整字节及中途读失败的客户端结果。

四服务提取必须同时证明真实 Tool、无模型所有者操作、Web／飞书上传、Coding／PR 报告与清理调用；仅通过包安装或纯方法样本不能完成迁移。标准组合覆盖 `make test lint`、核心／功能系统、Prompt、Memory 与相关竞态入口；Linux 使用实际交叉编译 Host／封存插件和真实保留的旧二十九包二进制，不能以当前 SDK 重建包冒充旧版本。

归档兼容验证须保留原 Main／Root／Child／Grandchild、已完成生成／调用与文件事实；在真实崩溃恢复后立即核对原选择与冻结版本，实际读取／发布原文件，不通过额外 select、回填配方或重载修复夹具。旧二十四包只作为保留候选边界，不能称为已发布版本。未改动实现的通过证据可以保留，但须逐文件核对实现／资源与封存字节，分别记录原来源与二进制身份；修正的系统夹具和受影响检查重新执行，原失败记录不得改写。

原生 Computer 的未装包临时副本仅为明确的历史测试入口，其输入创建显式使用原生夹具；托管 Computer 仍须通过普通 Artifact 创建路径。此差异不能成为产品的静默原生兜底。

## 范围与证据

普通对象 ID 需覆盖搜索→读取/描述→发布与回执重放，原文读取按明确 kind 打开相同附件，跨作用域仍拒绝。模型侧 ID 投影不得改变原始文件字节、用户正文和内部持久身份。

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-012](../features/artifacts-media.md#f-012) | 验证读回字节与哈希、作用域、保留期、并发保存、删除和重启恢复。 | 保护已接通的限定范围；Live/配置另行验证 |
| [F-013](../features/artifacts-media.md#f-013) | 通过真实消息入口上传图片，在最终供应商请求中验证图片存在，并验证无权限/不支持模型的行为。 | 实际浏览器/CLI → Artifact → Pi → HTTP，区分视觉与纯文本 |
| [F-014](../features/artifacts-media.md#f-014) | 逐格式验证可获取的内容、原始文件关联、失败反馈和实际任务输出；质量由独立评测衡量。 | DOCX 已有真实上传和 Agent 读取路径，其他格式逐项验收 |

## 层级与关键路径

`TestArtifactManageSearchExecutionAndReplay` 对原生历史路径与真实隔离搜索／读取插件分别运行同一业务回归，保留 Unicode／MIME 匹配、超过五十条候选后的命中、scope、原描述／字节、发布与完成回执重放断言。`TestArtifactWorkbenchSearchNeverFallsBackAfterOwnerDeactivation` 验证新 Run 保留已迁移服务边界，停用后不读取原生结果。被替代的 `ListTx` 已删除，其原 Task 保护断言直接保留在具名历史搜索入口；不保留无消费者的中间代理。

`TestArtifactWorkbenchActualReadAndSearchMatchIndependentNativeOracle` 在真实 provider exchange、Tool Executor、SQLite 与隔离插件之间重放迁移前独立捕获的二十五项读取／搜索结果，检查原字节范围、二进制引用、描述哈希／保留期、Unicode／MIME、可见性先于结果窗口及完整回执重放。夹具通过普通 `Renew` API 按现有 Actor 周期续租，不延长租约或恢复失效执行权；它不代替原版本 Run／Child／引用身份的归档兼容验收。`TestArtifactWorkbenchSearchDoesNotReserveWriter` 在另一真实 WAL writer 持有事务时执行已接纳搜索，验证纯读取不占用 writer。

`TestArtifactWorkbenchActualMediaKindsMatchIndependentNativeOracle` 读取同一原生基线，用真实所有者配方与存储覆盖十四种原类型：十三种实际保存，空 MIME 按原约束拒绝且不留元数据。不带斜线的 opaque 类型按原解析器行为接受并归为 file；不能从当前验证器推导测试期望。`TestArtifactWorkbenchActualOutputAndCleanupMatchIndependentNativeOracle` 经真实已接纳工具／SQLite／隔离插件复核另外十四项原输出、默认值及清理结果，包括内联阈值、多字节溢出、空／二进制、显式 JSON 回执、多附件、字节／哈希／可见性、完成重放及引用保护。默认清理以101个实际合格文件验证只选100项，未将未填写的所有者配置填成原生默认值。

`TestArtifactWorkbenchActualChannelImportRetainsRecipeAndOriginalDedup` 覆盖 Web／飞书实际导入的创建事实、原始字节、停机后旧资源去重及拒绝新导入；它不代替 HTTP 入口或媒体理解验收。`TestArtifactWorkbenchTaskLifetimeAndRetentionAfterTaskEnd` 与原生路径共用同一业务断言，覆盖 Task 活跃／暂停保护和结束后的原保留期。`TestArtifactWorkbenchActualCodingReportUsesIndependentCreationVersion` 将 Coding 与 Artifact 两个真实封存插件组合：实际报告消费独立产物换版、保留原字节及显式名称／MIME；只移除产物服务时，Coding 读取仍可用，新保存不回退。联合提交遇到实际 Forget、Guardian 停机或隔离时不得留下文件元数据或方法回执，见 `TestArtifactWorkbenchCodingCommitKeepsBothMethodsBehindLateRevocation`。

`internal/rsi/artifact_workbench_test.go` 编译并封存真实独立包，以所有者独立字面量覆盖四个私有服务；另一二进制真实改变保留期、读取窗口和两种排序，旧门槛必须拒绝，所有者另行接纳后验证换版、旧图、回退、隔离及停用后的冷重启。大于普通输入预算的完整目录使用明确授权的流接口，截断、多余尾字节、条数不符或分块撤权不得返回部分选择。原生语义 golden 及纯算法边界见 `internal/plugin/artifactworkbench/native_test.go`。

`TestArtifactWorkbenchOutputRecipeRealUpgradeFrozenRunAndExplicitRetention` 以另一真实二进制改变默认保留期、生成文件名／MIME 和回执，在真实工具输出上检查新版本、旧 Run、显式三十天、原始字节及持久方案不可更新／删除。`TestArtifactWorkbenchEffectPreparesBeforeDispatchAndReconcilesWithoutCandidate` 核对原审批前已有完整方案、效果只发生一次；Run 结束且图被隔离后，权威查询仍能保存原始二进制、显式元数据和原回执，不执行候选、不重发效果。该夹具独立声明两秒提供方预算；原有冻结一百毫秒的回执／流关闭／unknown 回归仍保留并运行。

`TestArtifactWorkbenchChildInheritsFrozenCreationRequest` 从实际 Main → Root → Child／Grandchild 验证原配置请求继承；`TestV68ArtifactRecipeMigrationPreservesOriginalFactsAndNativeBoundary` 验证前向迁移保留已有请求、引用、过期事实及原生服务边界，不回填新配方。`internal/config/artifact_test.go` 检查缺省、显式默认值、无效零、程序化显式设置和 YAML 往返的来源语义。共享 system schema 期望与迁移目录一致性回归保持同步；历史 schema 夹具仅补自身所需的构造依赖，未装配 Artifact 的历史路径不因此读取 Artifact 配置或方案。

`TestArtifactWorkbenchUnavailableRecipeRejectsOverflowAndKeepsInlineControl` 验证停用后普通内联结果仍可接纳；需要产物的纯输出明确返回不可用，不暴露部分正文、不静默回退，也不写入 Artifact 或新配方。

`TestArtifactWorkbenchOwnerSemanticFactSurvivesUpgradeRollbackAndColdRead` 用真实独立版本改变媒体类型，检查新文件消费新政策、旧引用类型与字节保持、回退、隔离及冷重启；原始读取／显式删除不依赖候选。创建 staging 深拷贝与提交时撤权由 `TestArtifactWorkbenchOwnerCreationRechecksAcceptedFactsAfterStaging` 覆盖。 `TestArtifactWorkbenchOwnerForgetAfterStagingRejectsPublication` 和 `TestArtifactWorkbenchCleanupLateRevocationNeverDeletes` 在实际 staging／选择完成后使用原 Forget、Guardian 停机与隔离入口撤权，拒绝新元数据及整组删除。旧所有者资格仅由真实 schema68 前向迁移建立，启用服务后不能复活，见 `TestArtifactWorkbenchOwnerLegacyEvidenceIsMigrationOnlyAndActivationClosesIt`。

`TestArtifactWorkbenchCleanupActualSelectionAndAuthority`、`TestArtifactWorkbenchCleanupRevalidatesWholeSetBeforeDeletion` 与 `TestArtifactWorkbenchCleanupActualIndependentOrderingUpgrade` 验证真实到期候选选择、引用保护、显式批量、换版、整组陈旧项回滚及 tombstone 后恢复；原清理入口拒绝已迁移所有者。`TestArtifactDefaultProductionOwnerWithoutModelAndColdLifecycle` 经实际完整三十包 App 验证243条独立例、无模型创建、冷启和停用。

原 `TestEvidenceCLIReadsOriginalAcrossReopenedBoundedPages` 的建数明确使用原生历史测试载体，之后每页仍走普通 owner CLI 与损坏执行配置，不能把裸 Go 测试进程的新建路径当作生产默认。完整三十包 stock CLI 另验证托管文件在缺失 Node 的配置下仍能通过普通 evidence 控制入口读取原字节；这与停用后的独立 raw open／describe／delete 控制分别验证，不借恢复原生默认规避服务边界。

必需 `artifact-plugins` CI 分区先执行 `make test-artifact-plugins`：纯方法／原生 oracle 全包 race、不经名称过滤；Host／存储／App 执行真实生命周期及 schema 回归。SQLite 由相同表达式发现全部测试，每个进程最多运行八个根用例，逐个核对 race 结果。各批保留 15 分钟上限，库存、实际通过的用例及耗时随 CI 归档。万行搜索夹具先按已创建的 Run 写入全部授权文件，再由真实 worker 首次领取并用同一租约执行搜索。随后 `verify_artifact_plugins.py` 使用 stock CLI、实际文件字节和默认三十包验证缺省／显式配置、冷启停用、裸 Host 缺包拒绝及独立控制。可选 `--prior29-binary` 使用真实归档二十九包，证明原始所有者引用和显式组合升级／回退边界，并分别在真实 Child 接纳后 SIGKILL：旧树恢复后新 Grandchild 继承原能力／模型／上下文／图，旧文件由 Child 实际读取与发布；迁移及恢复保持原 Main／Root／Child 身份、已完成 generation／调用及文件事实，不回填新配方。Task 结束时展示到期可按原 TTL 正常推进，不能据此要求重写或冻结底层元数据。未提供归档时明确未运行；既有 scripted children bridge 只替换外部模型，不证明模型质量。CI 可达性由 `tests/harness/test_ci.py` 保护，不能以测试被跳过或未匹配当作通过。

`TestArtifactDefaultProductionHTTPChannelsFreezeCreationTypeAcrossVersions` 使用同一真实完整三十包 App 和原 HTTP 处理器：Web multipart 与签名飞书 callback 经实际凭据／归属校验、平台下载、创建策略及 Inbound 接纳保存原像素。只替换实际 Artifact 二进制，旧 owner 用例先拒绝新媒体策略，另行批准后新图片按 file 接纳，旧输入继续按 image；回退、冷重启及停用后，原输入去重保持各自的创建事实，新输入不回退且不残留文件。伪造 Origin／签名在导入前拒绝。模型 probe 使用显式协议夹具，测试只要求真实入站接纳，不执行付费模型生成，也不证明视觉质量。 Linux 无 Go 编译器的承载器由测试控制面提供同源码交叉编译的真实 App、test2json 与独立变更插件；原安装哈希、旧用例拒绝、新用例接纳及全部 HTTP 断言保持一致，不能用平台 skip 代替。

`TestArtifactWorkbenchPRHandoffRetainsAssociationAndFencesJointCommit` 与原历史 PR 测试共用业务断言，托管路径实际组合 Coding／Artifact 两个独立包。正常 `SaveCodePRReport` 保留所有者选择的原观察仓库／head／时间与原文件元数据，原生刷新观察、Forget、Guardian 停机／隔离在最终事务前发生时均不得留下部分 Artifact 或方法回执。共同夹具保留原历史路径，避免复制整套断言或放宽绑定条件。

完整完成前仍须验证原先真实分发的 Run／Child／NULL 方法引用、输出前固定配方、显式配置来源、审批／Forget／未知效果不重发、实际换版／停用、全部原生行为与标准分发门禁；同期清除对应原生业务及死依赖，并记录确需保留的历史消费者与删除条件。

文件存储断言真实字节/hash；图片通过 Web/CLI 用户入口到最终 HTTP 请求检查，不能只测直接构造 GenerationRequest。音视频/文档逐格式验证实际解析链，不把文件名或 fixture 元数据当内容理解。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- 必需 PR CI 的 `tools-provider` 分区运行完整 `artifacts` 门禁，覆盖普通对象 ID 的附件往返、原始字节与发布边界；测试通过工具回执定位业务输出，允许独立的上下文归档存在。报告复制到 Task 的固定计划使用显式 16K 模型窗口，窄窗口失败边界由 context 回归负责。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 已有检查与缺失覆盖

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_artifacts.py](../../tests/system/verify_artifacts.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [internal/llm/bridge/images_test.go](../../internal/llm/bridge/images_test.go) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |

### F-013 回归路径

- `internal/agent/messages_test.go`：真实 user、runtime、assistant、Tool 图片的选择边界及旧引用模式。
- `internal/agent/sqlite/images_test.go`：通过 Submit/Steering/Generation 接入实际 SQLite 和图片字节，验证显式 grant、未选择/未授权 ref 拒绝、过期 claim、纯文本回退、旧 policy 恢复的请求字节一致、已完成结果不再读取图片。
- `internal/llm/bridge/images_test.go`：真实 Pi HTTP 图像 envelope、不落盘像素、去重/图片数量上限、格式/损坏/字节及像素上限、极窄图片的实际 JPEG 尺寸。
- `internal/interaction/web/model_actions_test.go`：图片选项仅形成显式候选声明；既有模型 Probe 检查继续保护声明与观察证明双条件。
- `tests/system/verify_local_provider.py` 的用户图片场景启动真实事件 broker、Web、Actor 与 Node/Pi。Chromium 实际上传 PNG，HTTP peer 检查收到 JPEG 字节；纯文本模式无图片字节且生成上下文包含限制说明。CLI save/send-parts 后在 HTTP 响应前 SIGKILL，恢复保持 generation id、request hash 和图片内容 hash。现有 screenshot 场景继续证明工具图片选择。
- 浏览器证据含桌面与移动截图。所有图片为合成运输素材，HTTP peer 只验证协议与字节，不对 OCR、视觉推理或真实模型质量评分；真实账户视觉质量另行 Live/eval。

### F-013 本地 OCR 增强回归

- `internal/media/ocr/engine_test.go` 用测试图片的真实字形像素驱动已安装 Tesseract，在内核沙箱中验证文字和原图词框，检查引擎/语言资源漂移、损坏图片与取消。取消场景使用别名临时路径，验证实际沙箱作业启动、取消回收及目录清理。缺少依赖时组件测试明确 skip；维护中的 CI 安装 Tesseract/英文/简体语言包，系统入口要求真实 OCR 成功。
- `internal/media/ocr/document_test.go` 验证不合法 TSV、框越界、非有限分数、词数上限及 UTF-8 预览截断。固定图片是确定性软件回归素材，不是模型 golden case，也不证明普遍识别质量。
- `internal/agent/sqlite/image_ocr_test.go` 经真实 Registry/SQLite/Executor 和 Node Code Mode 验证默认角色边界、授权引用、持久化调用参数不可替换、完整输出与原图关联、preview 上限、不自动发布、错误归一化、跨 claim 重放不再调用 OCR。
- 既有 `verify_local_provider.py` 从 CLI 上传同一实际 PNG，正常选择纯文本模型，由协议 peer 请求 OCR；读取真实 Tesseract 的 Tool 结果，检查 Pi HTTP 没有图片像素且含识别文字。结果提交后、下一轮模型响应前 SIGKILL，恢复必须保留 invocation/attempt、Artifact 与 generation/hash，不重复识别。沿用已有 gate，不新增脚本或 CI 评测阶段。
- 软件回归证明支持的格式、处理链路和失败边界。多语种准确率、低分辨率/手写/复杂版面质量、真实 DeepSeek 选择工具与答案效果属于独立 Live/eval；不能由受控 HTTP peer 通过推定。

### F-014 私有 PDF 正文回归

`TestPrivatePDFEvidenceUsesOriginalArtifactAndPinnedParser` 将带文字的 PDF 保存为私人原件，以真实已激活的隔离解析器读取文件。检查页码、原件引用、换版后的旧页与新读，以及删除文件后旧续页拒绝访问。

### F-014 英文 WAV 转写

通过 CLI 保存测试 WAV 后显式启用包和转写，核对每段真实起止毫秒、可重新打开的原件和字节摘要。普通证据读取、未启用、错误模型、过大或格式不支持、无声录音、缺失时间点、失败命令和沙箱撤权分别覆盖。独立原始 CLI JSON 及二进制、模型和音频 SHA-256 记录在 [语音协议夹具](../../tests/fixtures/speech/README.md)。本地准备模型和 JFK 音频后，设置 `STEWARD_TEST_WHISPER_MODEL`、`STEWARD_TEST_WHISPER_WAV` 并运行 `go test ./internal/agent/sqlite -run '^TestAudioTranscriptOfficialLocalCLI$' -v -count=1`。常规回归通过真实验证的插件消费受控协议样本并在发布前执行 Forget；该夹具不评估会议语音准确率。

### F-014 DOCX 正文回归

覆盖块、行内、嵌套和表格内容控件，比较旧原生解释，并从授权原件经独立构建插件读取大文档。

- [插件业务集成](../../internal/rsi/docx_test.go) 编译真实独立 DOCX 程序，经 Host 安装／验证／激活后走原 Main 证据工具。覆盖大于 1 MiB 的原文件、分页、历史原生解释器、新 Run 固定图、源摘要冲突、隔离及停用后不可用、独立 v1/v1.1 二进制升级回退、所有者旧分页修订及未激活版本拒绝。SDK 输入回归覆盖坏块、截断、摘要与尺寸上限；构建命令覆盖离线封装不初始化用户状态。


- `internal/media/docx/text_test.go` 验证实际 ZIP/XML 的段落与表格顺序、XML 转义、忽略其他部件、根关系/主部件歧义、坏 XML、深度和数据量上限。
- `tests/system/verify_artifacts.py --docx-only` 用 CLI 保存真实最小 DOCX，随消息提交原 ref，经 Actor 和冻结的证据工具分页读取。校验原始字节、精确来源、UTF-8 边界、无授权和 Forget、旧 Run 只见元数据，以及坏包和超限失败。完整 Artifact 套件也包含该场景。
- 音视频、其他文档格式与复杂版面的处理仍需各自入口验证。合成素材只验证软件路径。

## 详细测试入口与证明边界

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="artifacts-verification"></a>

### Verification

[托管搜索](../../internal/agent/sqlite/plugin_artifact_test.go)在一万项授权候选中查找唯一名称，并覆盖独立写事务、撤销和任务生命周期。

For focused receipt recovery, add `--recovery-only` to the command below. It
launches approved Worker effects, kills after the effect or terminal unknown
commit, reconciles through the CLI, verifies bytes/hash and caller readback,
replays without a second effect, and removes all disposable state.

`python3 tests/system/verify_artifacts.py --binary bin/steward --evidence
evidence/artifacts.json` owns Launch/Doctor/Drive/Evidence/Cleanup, using the
explicit `artifacts` model and binary/large-output Tool Fakes. It drives actual
CLI save/open/delete, large input refs, Task output publication, eight concurrent
CLI writers, forged/traversal refs and six SIGKILL boundaries. The system runner selects it; repeat count is explicit. Go contract tests add cross-user/Agent/Task/Run checks,
child-shaped writers, symlinks/tampering, quota, expiry/reference/dry-run checks,
migration rollback, failed Tool commit and stale claim fencing. Race detection
is separate (`make test-race`).

GC-15's versioned fragment preserves source hashes and reopens `visa_check.md`
through Task → Root → Main final delivery. Synthetic PNGs carry fixture fields
in metadata; they are binary transport assets, not OCR evidence. Scripted text
is not semantically graded; this Artifact fragment keeps the full case
`not_applicable`. [M6 evidence](product.md) covers additional image/privacy
mechanics without establishing model vision quality. Original M1/M2 fixtures
and locks stay unchanged. [Code Mode replay](../designs/codemode.md) and [Computer transfer](../features/computer-use.md)
are implemented and verified separately from this Artifact fixture.

`manage` accepts only fields for its selected operation. `publish` selects the
current Run's final outputs; it does not send a message or upload a file. Main's
final AssistantMessage passes those semantic references to the existing Delivery
Composer and versioned UISpec (including Web's OpenIntelligentUI table). Workers
return only to their direct Parent. Neither tool accepts ownership or filesystem
paths. Search filters before counting accessible matches, so older matches are
not hidden behind 50 unrelated files.

The five old Direct Tool names are removed. See
[Tool compatibility](../designs/tools-approvals.md#decision-005-005--consolidated-model-visible-tools) before upgrading
an installation with active Runs. The authenticated `artifact` CLI is unchanged.

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

## 工程基础重构回归

F-012/F-039：流阻塞期间另一个 state owner 能扫描回收且不删除活跃 stage；进程被杀后锁自动释放，现有恢复场景回收 orphan；publish 整组预校验与提交时版本/授权重检；继续运行配额并发、Tool deadline、unknown 效应和最终 envelope 上限回归。入口：`internal/agent/sqlite/foundation_test.go`、`artifacts_test.go`、`artifact_recovery_test.go` 与既有 artifacts system gate。

### OCR 插件迁移验收

[真实插件／原生工具回归](../../internal/rsi/ocr_test.go) 验证独立编译的图片插件、实际 Tesseract 像素、原图和完整结果引用、历史解释器、v1／v1.1 换版回退和保留、原生资源漂移、停用与隔离。未安装引擎时像素用例明确跳过，不算该平台验收；普通 TSV 解析与 SDK 预算回归仍可执行。负向回归拒绝未授权及第二次原生计算，大结果回归覆盖转义后超过 1 MiB 的合法 OCR 文档与持久结果重用。现有 Direct／Code Mode、授权／取消／重放用例继续保护原入口。

CLI 入口 `verify_artifacts.py --ocr-only` 使用独立政策验收分发包，验证 Direct／Code Mode 均读取真实像素、未授权引用失败、完成回执不重识别及 Forget 清除插件派生正文。`startup` 同时检查标准安装首次选择 DOCX／OCR，重启保留原选择。


Artifact 名称与媒体类型约束由内部 `artifact.Input.Validate` 统一维护，staging 与 Computer 导出展示接纳复用，原限制与授权不变。Computer 方法只能选择缺省展示字段，不能改原字节、路径或 ArtifactRef；集成场景见 [Computer 测试方案](../test-plans/computer-use.md)。

从审阅任务验证受信工作台引用、文本以纯文字呈现、PNG 实际格式匹配、原件下载及撤权。类型不支持或 MIME 与字节不一致时预览拒绝，下载保留原文件。


视觉模型 WebP 回归用自制的 8×4 红色无损样例，经真实模型桥转码成有界 JPEG，核对像素和原件引用。动画标志、超出像素预算、伪造 MIME 都在供应商调用前拒绝。OCR 保持原先的格式边界。


`TestArtifactWorkbenchSearchContinuesPastFiftyTiedFiles` 通过真实托管搜索查询 51 份同名、同创建时间的成果，检查跨页无遗漏或重复、恰满最后一页没有续页以及查询变更后拒绝旧游标。

`TestArtifactWorkbenchSearchAuthorizedBodyAndWithdrawals` 用跨越 512 KiB 窗口的中文条款查询真实已授权原件，对照无授权和过期的同类成果，并在撤权后再次检索。回执只包含描述符。独立 Owner 通过固定输入核对正文匹配服务。
