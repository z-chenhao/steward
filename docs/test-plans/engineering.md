# 开发、验证与交付：测试方案

[功能说明](../features/engineering.md) · [技术方案](../designs/engineering.md) · [产品测试方案](product.md)

## 验收场景

F-038：固定依赖和构建入口可用；文档改动只运行适用检查；代码/未知变更运行完整 PR 路径；失败和取消保留失败状态与清理；原生打包不自动发布正式版本。

## 已有入口

[工程辅助回归](../../tests/harness/test_project_layout.py)、[Makefile](../../Makefile)、[PR CI](../../.github/workflows/ci.yml) 与 [扩展回归](../../.github/workflows/regression.yml)。运行方式及边界见产品测试方案。实际运行结果以对应 PR 或 CI 证据为准。

## PR 并行检查

[分片回归](../../tests/harness/test_ci.py) 核对十一项 core gate 无遗漏、无重复，每个分片都有实际命令；汇总状态必须拒绝缺失、失败、取消及未经文档路径授权的跳过。软件检查保留 `make test lint` 的全部入口、prompt、Web Access、四个 capabilities 场景和定向 race。Intelligent UI 的取消/提交、宿主参数权威、RPC opt-in 与 SSE 撤权回归在 Agent、SQLite、Web、Bridge 包中启用 race；`startup-ui` 继续运行真实 CSP 下的流式预览及既有交互门禁，harness 防止遗漏这些包、入口和浏览器依赖。`connected-apps` 分片额外执行 `make test-connected-apps`；Apps 授权与共享帮助浏览器回归归 `browser-infra`，其浏览器和前端依赖由分片计划保证安装。每个分片使用独立 checkout、构建和证据目录，继续由 process owner 验证源状态及取消清理。`memory-context` 的完整 race 与两项系统门禁使用 15 分钟 job 上限，其余软件／系统分片为 10 分钟；不得通过跳过或忽略取消来满足时限。GitHub 的 `Ubuntu checks` 必须等待全部选中分片；不以单个分片通过代替整个 PR 通过。

五分钟目标使用完整远端 workflow 起止时间验证，同时区分排队、安装与实际检查时间；冷缓存、外部服务和 hosted runner 排队不能通过删除覆盖或放宽断言规避。文档-only 仍实际执行文档检查，并产生同名 required status。

必需的 `channels-web` 分片运行完整 Preferences 交互、设置一致性、账户/API 连接和任务导航场景；`test_required_web_lane_reaches_settings_and_task_regressions` 实际调用该分片选择的 dispatcher，防止场景仅能手动运行而未接入 CI。布局断言、通知 CAS／丢失确认／无脚本回退和真实模型连接仍由浏览器与 HTTP 场景验证。CI 无法检查未提交的代码；本地修复清单、PR 范围及合并后部署的 revision／资源核对记录在 PR，而非用文档字串测试替代。

Main 交接回归等待实际 `reply_committed` 通知后检查两条持久回复，Worker 在整个断言期间保持阻塞；保留五秒测试挂起保护；假模型按协议同时识别原生 ToolResult 和响应式 Main 的 runtime tool_results 证据，按最近普通用户输入作答，不把合法的异步投递当成模型错误。原生下拉的外部点击检查等待弹层完成打开后再点击，保留关闭与焦点断言，不通过人工关闭掩盖失败。

## 工程基础重构回归

`internal/llm/architecture_test.go` 使用 `go list` 的实际传递依赖保护模型边界：`llm` 共享契约不依赖适配器、`provider` 协议不依赖 Steward 领域状态，`account` 与 `sqlite` 不依赖 `bridge`。该检查随 Go 包测试进入每次代码 PR，间接引入的越界依赖也会失败。

F-039：`internal/agent/sqlite/foundation_test.go` 验证流暂停时状态查询与跨进程回收、Web Run 失败后的下一条请求、流式预览无逐片状态写入、trace 写失败诊断和内部错误 cause。`internal/app/runtime_test.go` 验证实际能力依赖修复后可重试且只发布成功 registry。Task/CodeMode 相关既有暂停、Child、重放、未知副作用和 SIGKILL 测试必须继续通过；使用 race 检查受影响并发边界。

[渠道观察器回归](../../tests/harness/test_channels.py) 使用真实 SQLite WAL 连接，在两次查询之间提交 Run 完成和新投递，验证等待条件使用一致快照，并验证成功、查询异常均关闭连接。系统测试中的连接关闭继续保留事务提交/回滚语义。

调度查询优化复用 `internal/agent/sqlite/scheduler_test.go` 的默认容量、跨进程额度、过期所有权和公平性回归，以及 capacity/scheduling system gates。容量 gate 在同一持久化目录执行三批各 1,000 个 Child，记录逐批耗时并检查唯一身份、既有并发上限、预设业务失败和租约回收；不新增不稳定的绝对时间断言。

Race 入口在 SQLite 分片之前分别运行 Bridge 与诊断记录器包，避免压力分片挤占 RPC / 100 ms 诊断写入契约的 CPU 时间。测试断言、生产时限与 `-race -count=1 -timeout=15m` 保持；harness 核对完整入口和两个分组入口都覆盖全部包且无遗漏或重复。失败日志尾部直接出现在 CI 输出中，被取消的兄弟分片不覆盖原始失败摘要；完整日志继续留在 artifact。

## Go 模块化重构验收（F-040）

| 范围 | 必须保持的行为 / 已有入口 |
| --- | --- |
| 包和接口边界 | `go test ./...` / `go vet ./...` 验证调用方编译；Computer 新适配器实现 LifecycleStore；App 不暴露 SQLite DTO 和执行访问器。 |
| CLI 语法与生命周期 | `cmd/steward/command_test.go` 独立列出合法/非法形式，非法命令在创建状态前拒绝；已有 metadata/control/barrier 用例；startup/main/account/operations system 场景驱动实际 CLI。 |
| Computer 事务 | `agent/sqlite/computer_test.go`、`computer_use_test.go` 通过新适配器验证过期 Code owner、重复计划、恢复/取消、checkpoint 与 fenced event；该跨模块集成测试留在事务权威处。 |
| 运维只读报表 | `agent/sqlite/operations_test.go` 验证真实状态库的计数、trace 和脱敏字段；operations system gate 验证实际 CLI。 |
| Task 接纳与执行 | 既有 tasks/task_control/task_completion 测试验证 receipt 重用、epoch、continue 新 Root、暂停/取消与失败 rollback；拆 helper 不新增事务。 |
| Web 和模型设置 | web package tests 与 channels system gate 验证认证、CSRF、JSON/form/multipart、模型选择、账户与关闭；HTTP 路由/字段不变。 |
| 能力组装 | `app/runtime_test.go` 验证失败不发布部分 registry、修复后可重试；core 工具/CodeMode/Child 场景验证冻结能力与真实调用路径。 |

跨模块变更运行既有 `make test lint` 和相关 core system gates；Computer/Task claim 与取消相关场景运行定向 race。环境导致的未执行、失败和跳过独立报告，不以编译成功代替行为验证。不为拆文件新增测试框架。

开源治理验收：`make lint` 检查 GitHub workflow；HTML 图片链接回归防止 README 引用失效，正式 Ghost 图标通过真实 Chromium 首帧像素核对；startup gate 实际打包、解压、安装并核对项目/Web/Go notices。`make audit` 记录当前漏洞库结果，网络失败与漏洞发现均不能记为通过。远端分支清理需要保存原 SHA、合并证据或所有者明确退役授权、经独立恢复验证的 Git bundle 和带 lease 的删除记录；管理权限不足应明确报告。

手动执行 native packages 需实际通过四个平台的启动契约、安装诊断、打包和重新下载后的 checksum 校验；Linux runner 必须安装 Bubblewrap，不能跳过 doctor 的缺失依赖断言。Actions 更新固定完整提交，并核对官方输入/输出兼容性；手动分支构建仍不得创建或发布 release。

## 当前验证限制

上下文契约的必过保护复用现有 Go lane 的 `make test-prompts test-go` 和 capabilities-race lane：首轮 system/history/recall/user 顺序、工具续轮前缀字节一致、同一输入不重复检索、新输入身份重新检索及旧策略重放由所属 Go 回归证明。真实 CLI 召回系统场景另行核对一次冻结与业务/内部摘要轮次，避免把合法压缩当作重复业务执行。

容量门禁的模型元数据持久化偶发失败根因尚未确认，追踪证据见 [PR #118](https://github.com/z-chenhao/steward/pull/118)。后续通过不等于该问题已修复；再次出现时保留具体失败位置、上下文及存储等待证据，不放宽生产时限、写入或恢复保护。

文档检查复用 `scripts/check_docs.py`：页面需可从 README 导航到达，本地链接有效，且遵循当前主题布局、稳定文件名和非日志式章节规则。对应 harness 覆盖违规文档的 CLI 失败与真实协议日期、代码示例的合法使用。内容准确性和有效约束仍须人工审查。
