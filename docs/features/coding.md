# 文件、命令与代码工作

[需求主索引](../feature-map.md) · [技术方案](../designs/coding.md) · [测试方案](../test-plans/coding.md)

## 功能说明

Worker 默认能够在专属隔离环境里读取、新建、精确修改文件，运行命令、验证工作并生成可审查的 Artifact。Main 将文件和命令工作委派给 Task，通过证据与 Artifact 核对结果并交付。

## 需求与验收

<a id="f-026"></a>

### F-026 · 代码理解、修改与交付

**需求**：用户能委派仓库理解、修改和验证，每项代码任务保留明确工作目录、修改集、真实验证证据与远端交付关联，结果可供审查。

**当前实现**：Worker 的默认 `builtin__read/bash/edit/write` 支持新建文件、连续修改脏文件、精确内容哈希校验、通用命令、限量输出和文件 Artifact。新 Main 不获得这四个工具；Worker 继续使用统一审批与重放，配置 Computer 时与桌面浏览器共用 Guest。

**本地代码任务**：安装所有者设置 `execution.repository_source` 指向适合交给 Agent 的实际本地仓库，配合原有私有 `execution.root` 使用原生离线执行。Task 首次准备时保存独立工作副本和冻结导入基线，包含源目录的未提交和未跟踪文件。准备不会打开 Git 元数据，源目录保留原样。Root 和 Child 使用该 Task 的工作副本；每个 Worker 首轮会收到实际 cwd 与可读项目文档入口，Main 通过 Task 与 Artifact 查看结果，不直接进入工作区。显式更改源或执行配置不会重绑旧 Run。

使用 `steward coding inspect TASK_REF` 查看状态和历史命令回执，`steward coding diff TASK_REF` 查看相对导入基线的文件变化与文本补丁，`steward coding report TASK_REF` 将快照保存为关联原 Task 的 Artifact。报告中的命令时间、尝试次数和结果来自实际已接受调用。检查命令退出码和超时字段，命令历史不代表验证了当前文件快照。若 diff 输出含无效 UTF-8，报告会标明补丁不可用，并保留文件变化和原字节摘要。

`diff` 与保存的审阅报告包含 `candidate_sha256`，覆盖同一加锁快照的完整文件路径、原字节及执行位；摘要和 diff 都来自工作区挂载之外的 Host 私有副本，不依赖补丁渲染或命令历史。代码变化产生新候选身份，旧 Artifact 保留原身份，临时副本在读取结束后清理。该字段是候选识别基础，不表示检查已覆盖该候选。新接纳的本地私有 Task 文件/命令调用按 Task 工作区仲裁，可与其他独立 Task 同时执行；同一 Task 的 Root/Child 仍共用序列化边界，共享执行目录和 Guest 保留原冲突键，已接纳调用不改写。

配置本地 `execution.repository_source` 时，Worker 还可用 `builtin__coding_status` 获取当前候选和检查历史，再用 `builtin__coding_check` 提交确切命令、候选 SHA-256 及可选超时。检查需批准确切参数，并由已选择的 `coding-workspace` 方法提供预算；源码在批准后改变会拒绝执行。命令在 Task 挂载之外的一次性副本运行，写入不回流作者目录，检查期间作者可继续工作。该能力当前不支持共享目录或 Guest，不改变四工具原有行为。

检查回执保留原输出、退出码、超时、截断、方法修订、驱动和环境配置作用域；`passed` 只表示该命令零退出且没有超时或截断。报告的 `checks` 从原调用读取，并按命令或项目文件与检查名标明 `latest_attempt`，等待、失败或 unknown 不被历史通过覆盖；`candidate_matches` 只比较这份报告的确切候选，inspect 未取候选时省略。依赖及工具链内容尚未封存，因此 `environment_verified:false`，也不据此宣称需求、测试判据或交付已验收。旧报告和旧方法身份保留，停用或缺失方法不回退原生命令。

开发环境 quality / quantity 升级正在实施，完整目标与成熟产品基准见[升级方案](../designs/coding.md#开发环境升级目标与验收基准)。候选及环境身份、固定准备脚本、功能检查地图、真实数据库/浏览器恢复与并行验证、结构约束和有界状态机变体回放已有实现；审阅事实与确切内容核对也已有显式入口。一般模型质量和远端发布仍有独立边界。

所有者已将配置中的确切 GitHub PR 绑定到同一 Task 时，可用 `steward coding report-pr TASK_REF EXPECTED_HEAD_SHA` 保存带关联的审阅 Artifact。先用 `github status` 查看已保存的健康观察和完整 head。报告记载仓库 ID、PR、head 和观察时间，表示所有者为这份本地报告选择了该已观察版本。补丁与远端提交是否相同仍需独立核对。绑定撤销、head 漂移或作业期间刷新观察会使保存失败，按最新状态重试。该命令只使用本地持久观察。

审阅包还保存最初接受的 Task 指令、原附件引用、跨 epoch 的最近 32 条指令及截断标记，以及 Task 全部尝试截至最近持久活动的耗时、冻结模型请求数、已报告 token 和按币种分组的实际费用。用量和价格缺失保留为空与缺失计数，不按零成本处理；这些是 Task 历史事实，不是当前候选的质量分数。活动截止点及 Task 状态同时保存；活跃任务的耗时尚非最终总时长，未变事实的报告字节保持稳定。`coding-workspace` 提供有界审阅问题，最终接受仍由审阅者决定。

需要核对内容时，先确保 Coding 与 Artifact 方法均已托管；所有者可显式使用 `steward coding report-pr-exact TASK_REF EXPECTED_HEAD_SHA`。它只读取当前配置且与原 Task 导入源相同的本地仓库 Git 对象，要求原始 commit 与已观察 PR head 一致，逐层校验对象哈希，再把完整普通文件路径、字节和执行位摘要与报告候选比较。未跟踪或额外文件也会导致不一致。缺失对象、符号链接、子模块、过滤器导致的物化内容差异或不安全对象不产生确切内容报告；不下载对象、checkout 或远端写入。保存前重新核对观察、Task 指令、隐私与插件。已保存报告保留当时的确切内容证明；它不是未来 head 的证明，也不是人类接受或部署回执。

**边界与剩余工作**：远端分支与 PR 的实际发布、授权远端写入、更广泛依赖供应和真实模型开发质量仍需验证。源配置授权完整工作目录，所有者应先检查待导入内容，凭据继续存于 SecretStore。链接、特殊文件和无效 UTF-8 文件名导致准备失败，仅该 Task 的 Root 进入失败。目录划分配合已有沙箱、具体审批和 unknown 语义，原生环境离线。原始仓库、报告 Artifact 和工作副本各有独立保留边界，删除工作副本由所有者另行处理。

**验收目标**：真实多文件修改完成理解、修改、测试和 diff 交付；两个任务的修改互不污染，保留用户原有未提交内容；失败/重启后可识别原目录与交付，验证失败如实展示；命令批准、取消和未知结果可追溯，不重复执行未知副作用或重发结果未知的 PR 创建操作，链接生成不等于交付完成或用户验收。

## 如何使用

四工具默认向 Worker 提供。默认目录是数据目录旁的 `<app.data_root>.computer`，首次调用创建；自定义只需设置执行范围：

```yaml
execution:
  root: /absolute/private/agent-workspace
  toolchain_roots: [/absolute/read-only/compiler-install]
```

工具链可省略。工作区需独占、权限 0700，与应用数据、Skills、MCP 和可信运行时分开，不放凭据。Linux 需要系统 Python 3、bash、bubblewrap 及 user namespaces；macOS 本地执行使用系统选定的 Xcode 或 Command Line Tools 提供的 Python 3 和 Seatbelt。解释器需位于受支持的系统 Developer 目录。`steward doctor` 检查执行依赖。配置已有 macOS Computer 时无需 execution，四工具改用同一 Guest。

先 read 项目 AGENTS.md 和文件，再 edit（当前 before_hash、唯一 old_text、new_text）。write 创建时省略 before_hash；覆盖时必须提供当前哈希。用 bash 执行搜索、Git 和测试；每次在工作区开始。bash/edit/write 都会请求具体动作批准。读文件时指定 `as_artifact:true` 保存完整产物，再用 Artifact publish 选为交付内容；二进制读取自动生成 Artifact。

## 升级

删除旧 workspace 配置，按需设置 execution；旧 audit/edit/protected 不再是配置项。升级前先用旧版本排空旧 Workspace Run 和待决副作用，新版本不为旧工具提供别名。详见 [迁移边界](../designs/coding.md#默认设置与迁移)。

## 文件方法的独立版本

标准分发包含 `file-tools` Go 包：授权文件的分页／原字节摘录和唯一编辑定位可独立升级、回退和停用。旧 native Run 保留原实现，已冻结 Run 保留组合，旧 Tool／Artifact 回执保留原字节；停用后不恢复 native 文件方法。新写入仍须批准确切内容及原哈希，不因方法版本变化获得新权限。边界见[文件方法设计](../designs/coding.md#独立文件方法与固定提交机制f-026私有实验接口)。


## 命令资源方法的独立版本

标准分发包含 `shell-tools` Go 包：命令超时与输出预算可独立缩小、升级、回退和停用。插件只看到数值上限；命令正文、工作区、具体审批、进程组、原输出／退出状态及未知结果仍归 Host。已冻结 Run 保留版本，历史 native 调用保持原实现，停用不恢复受管 bash 的 native 方法。设计与恢复边界见[命令方法设计](../designs/coding.md)。


## Task 导入与报告方法

标准分发包含 `coding-workspace` Go 包：首次完整导入的资源限额与所有者报告的历史窗口／补丁预算可独立换版。首次导入方法固定到 Task，后续 Root／Child 复用原发布回执；新报告可选择当前已验收方法，原 Artifact 字节保留。完整复制、路径安全、原命令／文件事实、Task／PR／Forget 校验仍由 Host 强制。接口范围与强制验收见[设计](../designs/coding.md)和[测试方案](../test-plans/coding.md)。

## 项目环境升级中的实验入口

`coding-workspace` 1.0.3 增加私有项目配方选择：项目文档声明准备、可选前台服务、健康检查、具名验证和清理，每项检查保留需求文字和确切命令。Worker 用 `coding_status` 的 `project_file/check` 获取候选及计划，再将完整计划、项目文档哈希、候选哈希、所有者选择的镜像 ID 和状态中的 `environment.sha256` 提交给 `coding_verify` 审批。文档、候选、计划、镜像或实际运行时基准变化会拒绝原批准；Task 首个实际环境基准固定后不重绑。

所有者先在本地 Docker Engine 准备好工具链及离线依赖，再配置完整镜像 ID，例如：

```yaml
execution:
  root: /absolute/private/agent-workspace
  repository_source: /absolute/selected/repository
  project_container:
    socket: /absolute/local/docker.sock
    image: sha256:<64位实际镜像ID>
```

`coding_verify` 在独立容器中挂载一次性候选，五个阶段共用该容器。根文件系统只读，网络仅有独立回环，源码、临时目录、进程与运行状态随验证回收。环境不联网下载依赖，不挂载宿主 socket 或凭据。`STEWARD_STATE_DIR`、`STEWARD_BROWSER_PROFILE` 和 `PORT` 提供状态目录、浏览器 profile 及默认端口 3000；项目仍须提供真正验证需求的命令。镜像身份固定不等于供应链或测试判据已可信。

实际回执分开保留准备、服务、健康检查、验证和清理事实；准备成功不能替代验证成功。取消/撤权停止业务阶段并回收整个容器。回收未确认时保持 unknown，普通恢复仅清理原资源，不重复运行检查或改写原结果。报告的 `resource_cleanup` 区分尚未开始、创建未知、等待清理和确认清理；清理后原检查仍可保持 unknown。未配置固定镜像时，不提供此入口，也不暗中退回原生执行。真实数据库/浏览器参考项目已有单环境及同端口并行入口，验证原服务与浏览器恢复并保存真实截图；完整嵌套 Steward 运行仍受平台边界约束。

项目状态对历史证据分别给出 `candidate_matches`、`environment_matches`、`method_matches` 和 `driver_matches`。环境身份包含镜像、实际 Engine/内核、执行用户与固定隔离 profile；更换镜像不会复用旧通过，运行时无法读取时不编造匹配结果。历史报告保留原事实；这些输入比较仍不等于需求或人工审阅已通过。

检查可以声明 `evidence: [".evidence/flow.png", ".evidence/assertions.json"]`。确认容器回收后，文件经原 Artifact 管线与原调用终态原子保存，临时环境删除后仍能按结果引用查看；回执记录路径、原字节摘要和引用位置。最多四个文件，每个 8 MiB、合计 16 MiB，拒绝越界、链接和特殊文件。声明的产物不可用时不把零退出宣称为完整通过；提交失败不补跑检查生成证据。捕获字节不证明测试判据正确，也不会自动发布为最终交付。

## 固定准备与可执行检查地图

所有者执行 `python3 scripts/prepare_coding_image.py --socket /absolute/local/docker.sock --evidence evidence/新的准备目录`，从固定 Debian 根文件系统、官方校验的 Go/Node、仓库锁定依赖和 Playwright 构建本地镜像。公开下载可复用原生缓存，每次均核对身份；源码按明确 Git 工作树清单复制，安装后只保留依赖和工具，删除准备时源码与旧二进制。`image.txt` 是本次成功准备的完整镜像 ID；失败只保存诊断及未完成 checkpoint，不标记就绪。该脚本是显式所有者联网准备，Worker 的每次验证仍离线。

源码内的 `coding.project.json` 提供 build、frontend-contracts 和 policy-boundaries 三个具名入口。Worker 读取原项目/AGENTS 指引，再用对应文件名与检查名取得具体计划。原需求文字、确切命令与产物声明均保留；改动配方会体现在文档变化和候选身份中。完整入口、判据及范围见[检查地图](../test-plans/coding.md#可执行检查地图)。

`make test-coding-development CODING_DEVELOPMENT_EVIDENCE=evidence/新的验收目录` 默认实际准备新镜像，再运行真实数据库/浏览器、服务恢复、同端口并行及确切候选构建的必需 race。显式设置 `STEWARD_TEST_CODING_DEVELOPMENT_IMAGE` 可复用所有者已准备的镜像；这次运行会注明不证明冷准备。PR 的独立 coding-development 分片使用默认路径，缺 Docker、缺依赖或跳过场景均不能通过。

固定容器配置下的嵌套 bubblewrap 已观察到 namespace 权限拒绝，因此不承诺完整 Steward 插件运行时或全部系统测试可在此容器内自托管。构建与协议入口不代表应用启动，浏览器参考项目也不代替 Steward 产品流程；原 Host 平台的受托管系统门禁继续验收这些行为。不会为满足示例而扩大容器提权或增加原生项目验证兜底。
