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

所有者已将配置中的确切 GitHub PR 绑定到同一 Task 时，可用 `steward coding report-pr TASK_REF EXPECTED_HEAD_SHA` 保存带关联的审阅 Artifact。先用 `github status` 查看已保存的健康观察和完整 head。报告记载仓库 ID、PR、head 和观察时间，表示所有者为这份本地报告选择了该已观察版本。补丁与远端提交是否相同仍需独立核对。绑定撤销、head 漂移或作业期间刷新观察会使保存失败，按最新状态重试。该命令只使用本地持久观察。

**边界与剩余工作**：远端分支与 PR 的实际发布和修订核对、授权远端写入、依赖供应和真实模型开发质量仍需验证。源配置授权完整工作目录，所有者应先检查待导入内容，凭据继续存于 SecretStore。链接、特殊文件和无效 UTF-8 文件名导致准备失败，仅该 Task 的 Root 进入失败。目录划分配合已有沙箱、具体审批和 unknown 语义，原生环境离线。原始仓库、报告 Artifact 和工作副本各有独立保留边界，删除工作副本由所有者另行处理。

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
