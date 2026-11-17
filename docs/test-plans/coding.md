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

| 需求 | 可观察行为与验证方式 | 覆盖边界 |
| --- | --- | --- |
| [F-026](../features/coding.md#f-026) | 隔离 fixture 仓库含未提交修改，两个 Task 并行准备、修改和验证；检查修改集分离、原文件保留、准备失败隔离、重启复用与证据真实。远端受控 peer 验证幂等/unknown；真实 GitHub 授权及模型代码质量独立验证，沿用现有 coding 系统入口。 | 本地 CLI、SQLite、内核 scope 与进程重启回归已接通。远端交付与 Live/效果评测待补 |

## 仍需独立验证

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

`TestCodingPlugin*` 已接入真实独立 Go 包、Task 副本、原命令审批、报告和 Artifact；`TestImportLimitsRejectWholeTreeWithoutDroppingOwnerFiles` 检查低限额整体拒绝且不发布部分源树。独立版本用例检查旧 Task 导入不重绑、报告切换最近窗口且保留截断、原 Task 编辑及旧 Artifact；只读用例在另一 WAL writer 下生成真实报告。行为回归覆盖超上限但通过候选机械验收的恶意配方、旧 native/schema61、停用／退役、排队冻结版本、Root／Child、Forget／Guardian、发布及 Artifact commit 故障。故障移除后直接普通 Drain／Save 重试，验证原 Task／副本／graph 不被重绑；Forget 阻断新报告与证据路径，历史显式 Artifact 保留既有审计授权。App 检查完整分发缺项不能悄悄启用部分 native 组合；capabilities 六条真实 CLI 场景同时检查 Task 导入 scope／限额及报告 Artifact 的原子方法来源。该边界须通过完整分发 CLI、12 项 Core、capabilities／CodeMode／memory-maintenance／recall、完整 test/lint、prompts/memory 与 owning／RSI race；具体执行结果保留在 evidence，不把测试存在视为通过。导入与报告方法验收不代表整个 F-026 或全部业务迁移完成。
