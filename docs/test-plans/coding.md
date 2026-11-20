# 文件、命令与代码工作：测试方案

[功能与验收目标](../features/coding.md) · [技术方案](../designs/coding.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 范围与证据

F-026 验证 Worker 四工具在真实隔离环境与统一 ToolExecutor 中的能力，Main 不直接持有文件/命令能力，不以 Fake 模型结果宣称模型开发质量。本页列维护中的验证；实际结果以对应版本的 PR 或 CI 证据为准。

| 层级 / 入口 | 验证内容 |
| --- | --- |
| [environment 组件](../../internal/computer/environment/local_test.go) | 实际内核沙箱：四工具互通、重复精确编辑、宿主文件和环境变量不可见、链接与越界拒绝、超时、输出上限、二进制 Artifact、无沙箱时不回退 |
| [共享进程输出](../../internal/computer/sandbox/process_test.go) / [OCR 取消](../../internal/media/ocr/engine_test.go) | io.Copy 与后续写入不能绕过输出上限；真实沙箱启动后取消需等待退出并清理作业；原生工具继续由上行验证 SIGTERM 清理与 unknown 效应行为 |
| [SQLite 组件](../../internal/agent/sqlite/environment_test.go) | 真实状态库和审批；原生调用复用 Computer、checkpoint 和 execution claim；无 JavaScript 依赖；完成和 unknown 不重发；旧 Workspace 无运行时别名；Guest cwd 来自 Guest 且组装上下文不启动 Computer、不读取 Host 文档 |
| [CLI capabilities](../../tests/system/verify_capabilities.py) | 真实 red → Worker 精确修改多个文件 → bash go test → diff → Artifact；连续脏文件编辑/新建/过时哈希；审批拒绝不执行；SIGKILL 后 unknown 不重发；保留 MCP/Skill/角色回归 |
| [memory maintenance](../../tests/system/verify_memory_maintenance.py) | Reviewer 只采纳真实 bash 的精确测试命令、退出码与完整输出证据；保留独立 Run 与晋升约束 |

## 需求场景矩阵

本地并行接纳由 `TestTaskCodeIndependentWorkspacesDoNotConsumeEachOthersConflictSlot` 驱动两个真实 Task 和具体审批，要求两个命令同时持有执行槽并在各自实际沙箱写入，源目录保持原样；`TestTaskCodeSharedWorkspaceStillSerializesApprovedCommands` 要求同一 Task 的后一个命令在前一个结束后才能取得槽。既有 Child、Guest、额度、unknown 和恢复回归继续保留。

`TestReviewSnapshotIdentifiesExactFilesAndExecutableModes` 覆盖候选身份重现、正文、执行位、空文件、二进制和删除；`TestCodingPluginActualImportAndReportPreserveSourceAndOriginalFacts` 通过真实受管方法/Artifact 检查当前候选变化不会重绑旧报告。该层只证明识别，不证明验证适用性或真实模型开发质量；完整升级按设计中的逐项结果继续验收。

候选和 Task 工作区回归同时进入必需 `capabilities-race`：`internal/coding` 的快照用例由原 Go race 命令执行，SQLite 的 TaskCode、共享 Child 和真实报告用例由原 `capabilities-sqlite` 动态分批清单执行。`test_coding_candidates_and_parallel_workspaces_remain_required_races` 从实际 Go 测试清单核对选择规则，避免用例只在普通 Go 测试中存在而失去 PR 竞态覆盖。

| 需求 | 可观察行为与验证方式 | 覆盖边界 |
| --- | --- | --- |
| [F-026](../features/coding.md#f-026) | 隔离 fixture 仓库含未提交修改，两个 Task 并行准备、修改和验证；检查修改集分离、原文件保留、准备失败隔离、重启复用与证据真实。远端受控 peer 验证幂等/unknown；真实 GitHub 授权及模型代码质量独立验证，沿用现有 coding 系统入口。 | 本地 CLI、SQLite、内核 scope 与进程重启回归已接通。受控 GitHub 草稿发布覆盖审批、丢回包、重启和只读核对；真实授权及 Live/效果评测仍需单独验证 |

## 仍需独立验证

`TestCodingCheck*` 使用实际独立插件、Task、具体审批、内核沙箱和 JavaScript runtime，覆盖从状态取得候选、批准后漂移拒绝、验证副本修改不回流、作者和验证者真实同时执行、旧 Artifact 保持原候选、原非零退出、超时/截断不通过、Code Mode 挂起恢复单次执行、计划/终态提交故障后直接 Drain 不重放。独立方法换版验证已冻结调用保留旧预算、新调用采用新预算、回退与 Host 拒绝超过上限的恶意配方；无受管服务时明确失败。

LateForget/Guardian 在计划提交前撤权，必须不接纳计划或输出；运行中 Forget 等待真实进程取消、临时副本清理并保留 ErrClaimLost。检查新工具只在本地代码 Task 注册，Main 固定角色仍不取得执行能力。`CodingCheck` 清单进入必需 capabilities SQLite race，Forget 用例同时匹配原 memory race；迁移 082 更新共享系统 schema 预期和旧 schema 重建层，并由原 migration-catalog 与兼容回归检验。上述回归不证明工具链/依赖可复现、判据充分、Guest 隔离或真实模型开发质量。

真实 Lume/SSH Guest 命令与桌面文件互通仍须在相应平台验证。真实模型质量、联网依赖、真实仓库导入与远端交付按实际接入单独验收。

## 执行与完成判定

macOS 本机 Seatbelt 回归运行真实四工具和 Task 目录/恢复场景，覆盖原生解释器发现、超时子进程清理及拒绝沙箱外和独立沙箱的信号。

沿用既有 make test/lint、system runner 与证据目录；不另建脚本框架或 golden case。状态、审批、冻结协议或取消变化需要相关 race/恢复检查。F-026 的通用开发交付目标仍保持部分完成。

本地 Task 目录通过 `verify_capabilities.py --scenario task-code-environments` 驱动真实 CLI、Task、四工具、审批和 Artifact。`--scenario task-code-recovery` 在发布前 SIGTERM 与 SIGKILL，并在发布后 SIGKILL。两个工作负载均使用 16K 测试模型窗口，以容纳系统指令、工具 schema、五项调用或多次恢复所需的确切回执；独立的小窗口裁决不变。重启比较持久 Task 位置和源脏内容，证明未发布可重备，已发布不重新导入。两个场景进入既有 Ubuntu checks，使用同一证据上传与失败门禁。

Go 回归验证 Main/Task 沙箱访问边界、Child 继承同一 Task、精确审批、准备失败隔离、链接及无效 UTF-8 文件名拒绝、非 UTF-8 补丁省略且保留原字节摘要、空文件和正文相对补丁、报告 Task 归属及 Forget 屏障。`TestTaskChildInheritsWorkScopeAndOwnerReportIsTaskBound` 还验证 Root/Child 首轮只有一条受保护的环境消息、cwd 与实际文件工具目录一致、文档正文不进入 system；Root 文档入口保持冻结，Child 按当前工作区重新确认入口。真实远端 PR 和模型判断仍需单独验收。

`task-code-environments` 在实际 Task 工作副本生成后注入规范化的成功 watch fixture，经所有者 CLI 绑定 observe 模式，验证 `report-pr` 保存仓库 ID、确切 head、观察时间和原本地证据。错误 Task、错误 head 与撤销绑定必须拒绝，原报告和已保存的历史关联可继续打开。SQLite 测试在报告完成 staging 后注入同 head 刷新或 Forget，确认提交不留下 Artifact。真实远端发布与修订等价性不由此场景证明。

## 独立文件方法与恢复验收

`TestFilePlugin*` 使用实际独立 Go 二进制、真实 Task 工作副本、原 ToolExecutor 和 Code Mode，覆盖按行读取／原字段、确切审批前无方法或写入、唯一编辑及脏字节保护、源仓库不变、VM 读—编辑—重读只执行一次、跨帧完整输入与 8 MiB 二进制 Artifact、当前环境漂移拒绝、独立 v1.1 更窄分页／旧 Run／回退／退役／停用。自改机械 fixture 可通过的非法窗口仍被 Host 独立拒绝。

选择与 Tool 回执写失败验证原 never/unknown 语义，不以修改计划推断效果完成；旧 byte/hash/工作区和 Tool 终态由 `TestV59FileMethodMigrationPreservesNativeEffectsAndScope` 验证。LateForget 同时覆盖输出前 guard 和 watcher 先终止传输的路径，保留撤权 cause。Guest 通过原 Computer 接纳、审批、checkpoint/disposal 的集成测试验证同一 instance/epoch，unknown 不重做；该测试替换 Guest transport，实际 Lume／SSH 平台仍需独立验证。

`internal/computer/environment` 的真实 sandbox 测试覆盖受限快照、无效 UTF-8、空与不存在、单链接普通文件、路径/链接拒绝、8 MiB 完整文件与超限无部分成功、位置验证、CAS、权限位、空写入／空替换、盲覆盖和歧义拒绝。方法包另与原 Python read 对照 CRLF／Unicode 换行、长行和分页，8 MiB 换行语料使用流式扫描。

现有 capabilities 的 coding、dirty-index、audit、crash、task-code-environments、task-code-recovery 场景安装真实分发包并由所有者静态 policy 激活，原文件、具体审批、进程中断、dirty/index/source 断言保留，并从同一读事务核对调用的冻结组合和成功修改的先行方法。native 本机基线使用规范化临时目录及显式可信 developer Git toolchain；不放宽生产 sandbox 或 Main 权限。`make test-prompts` 包含 FilePlugin 与 schema59→60 回归，Forget 进入现有 memory race。确定性门禁不证明真实模型代码质量或真实远端发布。


## 独立命令方法与恢复验收

`TestShellPlugin*` 使用实际独立 Go 二进制、真实 Task 副本／ToolExecutor／Code Mode，验证审批前无方法或效果、原命令与非零退出码不改写、原 VM 恢复只执行一次、独立更窄超时／输出版本、native／旧 Run／回退／退役／停用，以及候选通过自改机械 fixture 后仍不能扩大 Host 上限或原用户 deadline。`TestShellDriver*` 在实际 sandbox 中验证原输出截断、超时杀死原进程组且无迟到写入，以及非法预算在执行前拒绝。

选择提交失败与效果后回执提交失败均验证原 never/unknown：不重跑或虚构完成；append 型命令保留恰好一次效果。LateForget 使用真实暂停的独立进程，分别由返回前 guard 和 watcher 终止传输，均保持 ErrClaimLost、无方法接纳及无已批准命令效果。Guest transport 集成保留原 Computer instance/epoch、审批、checkpoint/disposal，并验证未知或缺失退出码的结果不成为成功、不重做；不代替真实 Lume／SSH 平台验收。`TestV60ShellMethodMigrationPreservesNativeCommandAndScope` 对照真实 native 命令回执、尝试次数、Run checkpoint/version、工作副本和原字节，证明 schema60→61 不回写旧执行。

capabilities 的 coding／dirty-index／audit／crash／task-code-environments／task-code-recovery 通过原所有者 CLI 安装并验证实际 File+Shell+Coding 分发组合，保留原红—编辑—绿—diff、审批、源字节与 SIGKILL 断言，并在同一读事务检查每个实际调用的冻结 graph 及成功命令的先行有界配方。`make test-prompts` 包含 CodingPlugin／ShellPlugin 和 schema60→61／61→62；Forget 继续进入 required memory race。确定性回归不证明真实模型质量、联网服务或远端发布。


## Task 导入／报告配方

`TestCodingProjectPolicy*` 验证具名需求选择、重复/缺失检查与预算拒绝。`TestCodingProjectExactApprovedLifecycleUsesFrozenImage` 从 Main → Task → 状态计划 → 具体审批 → 真实容器执行，核对各阶段、不可变镜像基准、作者目录与所有者报告。`TestCodingProjectRealCodeModeKeepsOneApprovedInvocationAndImage` 验证实际 JS cell 的审批续接不重做检查。计划/回执写失败、晚到 Forget/Guardian、未确认回收及原资源恢复，均保留原候选、方法、镜像和单次尝试；恢复后立即读取原报告，不补 select/update/reload。`TestCodingProjectRecoveryDoesNotLoseDelayedRealCreate` 在真正的 Engine 前延迟创建，断开原调用后先执行普通恢复，验证不存在不能提前释放；随后放行真实创建，再恢复并立即核对原 unknown、方法/镜像、单次尝试和实际清理。

`make test-coding-environment CODING_ENV_EVIDENCE=evidence/新的目录` 是 `coding-development` CI 分片的必需真实容器门禁。显式本地 socket 可用 `STEWARD_TEST_PROJECT_CONTAINER_SOCKET`；默认 `/var/run/docker.sock`。门禁验证 Docker CLI 版本/摘要与真实 Engine，下载官方固定 Alpine 最小 rootfs 并核对 SHA-256，导入后使用不可变镜像 ID，执行实际容器与 SQLite 管线 race。缺少 Engine 直接失败，不能用单元测试中的显式 skip 作为验收。两环境须同时到达验证屏障再释放，证明同端口和独立状态可并行；取消后验证 setsid 子进程无迟到写入。协议 fixture 另覆盖成功帧、非零退出、坏帧、缺失终态和命令故障，私有 socket/名称/标签不匹配不能清理。门禁只回收自身工作目录对应的具名资源。

门禁保留完整 `go test -json` 日志、非空实际用例清单、零跳过、源码前后身份、Engine/CLI 与镜像来源。必需模型检查在同一已验证镜像中回放 claim 变体，记录最短模型反例、实际 Go 失败断言及原/变体摘要；编译失败或其他故障不能算成功检出。`TestCodingProjectStatusSeparatesCurrentEnvironmentFromRetainedPass` 在真实检查后连续读取状态，验证当前匹配、镜像变化和运行时缺失，且原成功回执不被改写。结构约束由 `TestCodingArchitecture*` 的真实依赖图与两项错误变体验证，进入普通 Go 与能力 race；模型穷举另随普通 Python harness 执行。

`TestCodingProjectEvidence*` 通过真实容器及 Coding+Artifact 独立包组合，验证原生/真实 Code Mode 具体审批、候选回收后仍可读取图片及结构化产物、原方法/字节摘要/引用位置、终态故障的引用回滚及普通恢复不重做。晚到 Forget 不暴露证据并保留原资源回收。`TestCodingProjectMissingEvidenceDoesNotTurnZeroExitIntoAcceptance` 保留真实零退出阶段并标记缺失产物的 incomplete。文件捕获回归检查锚定路径、越界/链接/特殊文件、单文件上限、取消和删除后的字节；真实 FIFO 回归有及时失败与解阻，不依赖包超时兜底。单像素协议图片只证明传输与持久化，不是实际浏览器截图的验收。

这组证据证明容器与 shell/TCP 生命周期，状态文件不能冒充真实数据库或浏览器操作，Alpine 也不能冒充完整 Steward 工具链。真实项目冷构建、持久视觉证据与完整环境适用性仍需独立场景。schema 083 同步迁移目录及共享系统预期；历史 native 文件/命令和冻结调用身份不因新镜像配置改变。

`TestCodingPlugin*` 已接入真实独立 Go 包、Task 副本、原命令审批、报告和 Artifact；`TestImportLimitsRejectWholeTreeWithoutDroppingOwnerFiles` 检查低限额整体拒绝且不发布部分源树。独立版本用例检查旧 Task 导入不重绑、报告切换最近窗口且保留截断、原 Task 编辑及旧 Artifact；只读用例在另一 WAL writer 下生成真实报告。行为回归覆盖超上限但通过候选机械验收的恶意配方、旧 native/schema61、停用／退役、排队冻结版本、Root／Child、Forget／Guardian、发布及 Artifact commit 故障。故障移除后直接普通 Drain／Save 重试，验证原 Task／副本／graph 不被重绑；Forget 阻断新报告与证据路径，历史显式 Artifact 保留既有审计授权。App 检查完整分发缺项不能悄悄启用部分 native 组合；capabilities 六条真实 CLI 场景同时检查 Task 导入 scope／限额及报告 Artifact 的原子方法来源。该边界须通过完整分发 CLI、12 项 Core、capabilities／CodeMode／memory-maintenance／recall、完整 test/lint、prompts/memory 与 owning／RSI race；具体执行结果保留在 evidence，不把测试存在视为通过。导入与报告方法验收不代表整个 F-026 或全部业务迁移完成。

## 可执行检查地图

`TestCodingProjectDocumentRejectsFIFOWithoutWaitingForWriter` 直接读取临时工作区中的特殊文件，要求返回无效配方错误且无需等待写入端。恢复阻塞式打开时，同一断言会失败。

| 范围 | 稳定入口 | 前置条件 | 真实判据与证据 |
| --- | --- | --- | --- |
| 确切源码构建 | `coding.project.json` → build | 固定 Go/依赖镜像、当前候选与环境审批 | 离线重建候选，执行新二进制的模型桥协议版本入口；原回执/版本产物。不是完整应用启动。 |
| 前端代码约束 | `coding.project.json` → frontend-contracts | 镜像内锁定 Node 依赖 | 生成资产一致性及当前前端行为测试；不代替浏览器或人工审阅。 |
| 政策/权威归属 | `coding.project.json` → policy-boundaries | 固定 Go、当前源树 | 编译器真实依赖图及两个错误导入变体均被拒绝，诊断给出所属入口。 |
| 状态应用与恢复 | `tests/fixtures/coding-development/project.json` → state | 真实 Chromium/SQLite、独立 loopback/profile | 点击保存，重启原服务后立即核对原输入与 SQL 事实，重开浏览器保留状态；PNG 与断言 Artifact。 |
| 可用并行 | `make test-coding-development` | 两个独立验证环境 | 两个实际服务先同时通过端口 3000 的 doctor，再释放屏障；各自空数据库/浏览器起步、保存不同数据并完成恢复，截图可复查。 |
| 原审批/撤权/恢复 | `make test-coding-environment` | 本地 Engine、官方固定小镜像 | 精确输入、Code Mode、Forget/Guardian、旧 claim、迟到创建、终态故障、原 Artifact 原子性以及模型反例回放。 |

`coding-development` 是必需独立 PR 分片。默认冷准备及接受者回归记录 Docker/Go/Node/系统包/来源清单、实际非空用例、零跳过、源码稳定与拥有资源回收；显式旧镜像只证明其对应验证路径。`TestCodingDevelopmentExactFreshStewardBuild` 使用新工作树清单，通过原 Task → 计划 → 具体审批 → 容器 → Artifact，不能用镜像内旧源码或旧二进制替代。`TestCodingDevelopmentActualSQLiteBrowserRecoveryAndRetainedArtifacts` 与并行用例运行真实 UI/SQL；截图须视觉检查为非空代表内容。原脚本中的 SQL 与浏览器状态都由真实保存动作生成，不通过恢复后的额外 select/update/reload 修复。

以上地图是验证入口和范围，不是自动质量总分。需求/测试判据修改、未覆盖场景、历史通过的适用性、成本与人审，以及远端确切交付仍须在审阅包中说明。完整 `make test lint`、prompts、memory 与所属系统流程继续使用 Host 平台门禁。

## 审阅包与确切 Git 内容

`TestCodingCheckReview*` 经实际 Task 和托管方法，验证最初指令、跨指令窗口截断、原 Artifact 不回绑，以及真实 provider 缺失用量保持 null、真实 priced 费用只来自原账本。保存 staging 后新增指令必须无 Artifact 提交。`TestCodingCheckExactRemoteContentArtifactAndFinalFences` 使用真实本地 Git commit 和独立 Coding+Artifact 组合，在当前观察 fixture 的确切 head 上验证成功，再验证未跟踪文件、源配置不符、指令变化、同 head 刷新和 Forget 的失败；随后修改源码不改变历史交接。

`TestCodingCheckGit*` 用实际 Git 验证 SHA-1/SHA-256、嵌套排序、二进制、执行位、额外文件、replacement refs、缺失对象与符号链接。实际 promisor helper 先在无传输围栏时写出测试 marker，随后相同对象在生产读取边界不得执行 helper。`tests/fixtures/git-cat-file.json` 来自固定官方安装版本，保留版本、launcher/实际工具 SHA 和原始字节：成功、tag 类型不兼容及实际命令失败；解析测试使用原始帧并重算对象哈希。它不是版本 probe 冒充集成。上述 roots 名为 CodingCheck，纳入普通 Go 与 required capabilities race；确切内容测试不依赖网络或远端写入，不证明真实 GitHub 发布。

冷准备门禁先启动真实准备脚本，记录并核对原容器 ID/name/label，在运行中发送 SIGTERM；五秒内退出后立即断言原资源已消失、原准备失败且清理确认，然后才运行完整冷准备。失败清理不会把断言改成通过。取消时跳过昂贵诊断快照，优先回收进程和容器；普通准备失败可保留不完整镜像，永不当作 ready。模型错误变体文件使用 Go 忽略的下划线前缀，验证产物不进入后续源码包扫描。

确切内容入口额外验证缺少 Coding 方法、仅有 Coding 而无托管 Artifact 时均拒绝且无产物，保留旧 report/report-pr 的历史兼容。required capabilities 新增 `task-code-exact-report`：通过实际所有者 CLI、真实 Task、原 Git 对象及规范化观察，验证完整托管导入/报告、确切内容和原任务指南、错误 head、候选额外字节、源/index/refs/钩子不变，以及撤销绑定后的原 Artifact 字节。远端没有写入，不使用已知 head 代替逐文件比较。

源码构建门禁直接使用原树中的 `coding.project.json`，逐文件比对导入前后的相对路径、内容和执行位。未跟踪脚本切换执行位会使门禁证据失效。缺少必需缓存或依赖时验证停在 prepare。具名检查按项目路径和检查名分别分组，包括名称含 `#` 的情况。

## 所有者批准的 GitHub 草稿 PR

`TestCodingDraftOwnerApprovalAndUnknownReconciliation` 用真实 Worker、独立命令审批、受管 coding-workspace 进程和 SQLite，在新 CLI 进程内调用计划、批准、核对与状态。固定 GitHub REST origin 在测试载体中只路由到本地 HTTP peer。peer 独立核验 blob 内容、Git tree、父提交、仓库、基分支和草稿字段。它先保存 PR 再丢掉响应，重启后重复批准不能新增 POST，近似匹配的 PR 不能结案，正确匹配写入原始 URL 和编号。等待中的检查、变化的候选和已有分支不能获得发布。独立的 `TestDraftTreeMatchesGitObjectsAndModes` 使用 Git 写树和哈希命令核验二进制内容、嵌套目录、排序与执行位。
