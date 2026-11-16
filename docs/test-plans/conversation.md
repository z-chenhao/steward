# 自然语言对话与共享循环：测试方案

[功能与验收目标](../features/conversation.md) · [技术方案](../designs/conversation.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 范围与证据

延迟工具结果使用普通 operation ID，与 pending 回执保持对应；旧 pending 回执恢复保留原关联 ID。新结果的模型投影只替换宿主身份字段，不改写正文、文件内容或外部服务的原生标识。

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-003](../features/conversation.md#f-003) | 验证输入去重、Steering、工具续轮、单 Main 所有权与回复恢复。通过真实对话入口启动受控慢工具，同时提交新问题与 Task 干预，检查响应、调用关联及最终交付；覆盖满负载、审批等待、重启及迟到结果。 | 真实 SQLite/Actor 及 CLI 已有回归；实际模型延迟与效果仍需独立验证，不以脚本模型得出性能结论 |

## 层级与关键路径

覆盖同源输入去重、模型生成中追加、工具提交后中断、已提交回复复用与并发 Main fencing。原始历史与模型工作上下文分别断言。

- Prompt：[TestContextPromptBytes](../../internal/agent/context_test.go) 固定三种角色的完整消息投影；角色文案或组装顺序变化时显式更新对应基线；其他角色保持原字节。`TestAgentStartsWithOneFrozenSystemMessage` 检查 Main 的 tools/rules、UI 优先与授权规则，以及三段冻结元数据；Root/Child 检查 role、capabilities、safety、execution 排序和 tools/rules 结构。各角色均为一条 system；Root 身份与进度位于各自段内，Host 继承文本与进度标记不进入模型。冻结资源回归经 JSON 往返后验证 Child 继承冻结共享规则，排除 Root 身份、进度和私有聊天；Bridge 拒绝拆分的系统消息。
- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 已有检查与缺失覆盖

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_main.py](../../tests/system/verify_main.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [tests/system/verify_terminal_input.py](../../tests/system/verify_terminal_input.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |

现有入口仍需在后续相关实现变更时按风险运行；需求范围变化时同步扩展场景。

## 持续响应与恢复验证

[responsive_test.go](../../internal/agent/sqlite/responsive_test.go) 使用真实 SQLite、Actor 和 ToolExecutor 验证慢工具仍运行时先回复、数据工具槽位占满时查询 Task、等待审批的新输入、模型费用等待不取消已接纳调用、费用拒绝后的重启恢复不重试未知副作用、完成/unknown 结果接续，以及协议迁移原子性。既有 Loop/Tool 测试继续验证冻结生成、所有权丢失、取消、权限和调用结果核实。

[verify_main.py](../../tests/system/verify_main.py) 通过实际 CLI 持有已接纳的工具调用，在释放前提交第二条消息并观察回复；读回唯一 Main、pending_tools、调用身份及运行状态，再验证释放后的结果和 SIGKILL 后恢复。调用暂停点位于事务外，不以锁住数据库伪造工具耗时。观察断言小于 5 秒；结果仅证明脚本模型和本机机制，不构成真实模型 SLA。

Steering 与模型切换场景在首批原生工具结果已写入 checkpoint 的 `after_turn_end` 暂停，确认工具成功且尚无回复，再追加有序输入并通过 SIGTERM/重启验证冻结模型。该场景保留精确轮数和交付次数断言，不让 200 ms 工具同步窗口决定测试路径；慢工具的 pending 回执与延后结果由上述持续响应场景独立覆盖。

验证命令与证据目录：

```sh
make test-prompts
go test ./internal/agent/... ./internal/tool/... ./internal/llm/...
go test -race ./internal/agent/... ./internal/tool/...
python3 tests/system/runner.py --gate main --gate tools --gate tasks --evidence evidence/f003-core-03
```

测试结果由对应执行日志与 runner 报告保存；全量回归中的执行沙箱/OCR 平台失败和未运行检查必须单列，不能按 F-003 的目标链路通过而宣称全仓通过。

## 详细测试入口与证明边界

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="main-loop-evidence-and-limits"></a>

### Evidence and limits

Run `make test`, `make test-race`, `make lint` and the system runner. Commands and
coverage are in the [product test plan](product.md); requirement status is in the
[Feature Map](../feature-map.md). `verify_main.py` owns isolated
launch, readiness, concurrent CLI input, drive, direct SQLite readback, nine
SIGKILL boundaries, SIGTERM, profile switching and cleanup. Its explicit
`-test-bridge main` and `-test-main-hold` hooks are never selected by production
configuration. `evidence/main.json` includes frozen requests/results and final
history, ToolInvocation, usage and delivery rows. The local/account harnesses
also execute this path through actual Node/Pi against owned HTTP fixtures.

GC-01/09/10 Main-only L0 fragments use versioned inputs and scripted Fake
responses. Reports keep their full Cases `not_applicable`, with no semantic
score. They prove protocol assertions, not real-model quality, live account
eligibility or current restaurant information. These M1 fragments alone do not
establish Security/Approval, Worker/Child or JavaScript Durable Replay; use the
[feature map](product.md) for those implemented paths and gates.
Current Runs use their frozen compaction policy; an irreducible context still
returns a context-length error. [Memory maintenance](../features/memory-context.md)
documents compaction, retained originals and privacy. Live account inference remains a separate
L2 requirement, and Device Authorization remains deferred by ADR 001.

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

Main 沟通／工作方法已接入独立版本化 `conversation` 包并进入标准分发。Host 保留身份、安全 Section、输入与冻结上下文、Task／交付事实；Root／Child 不消费 Main 方法服务。独立换版、已排队版本保留、方法回执后 ContextReady 失败恢复与工具续轮前缀见[Main 插件边界](../designs/plugins-evolution.md#已提取的-main-对话边界)及[插件验收](../test-plans/plugins-evolution.md)。`make test-prompts` 包含相应上下文回归。

独立 Main 换版回归还在回退／退役后关闭 Store、新建 Store／Catalog，直接 drain 原排队的新方法 Main，检查实际 provider 请求中的原方法、固定身份和安全文本；停用后再次重开，验证没有模型请求或 native 方法复活。不通过额外 select／update 修复恢复夹具。
