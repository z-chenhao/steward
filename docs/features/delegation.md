# Worker 子任务委派

Worker 通过显式任务和文件引用委派、等待并汇总 Child 结果。

[需求主索引](../feature-map.md) · [技术方案](../designs/delegation.md) · [测试方案](../test-plans/delegation.md)

## 功能说明

复杂工作可由 Worker 拆成独立子任务，并由父级汇总。

本文描述用户行为和范围；逐项完成状态以主索引为准。当前能力依据代码静态盘点，配置可用性与真实服务/模型质量需分别验证。

## 需求与验收

<a id="f-006"></a>

### F-006 · 可递归的 Child Worker

**需求**：复杂工作可由 Worker 拆成独立子任务，并由父级汇总。

**当前实现**：委派方法和可收紧的树深度／数量政策已接入独立 `delegation` 包并完成迁移验收；Host 原硬上限、计数和角色／成果权限保持权威。agent.run 创建一个 Child；继承冻结模型及削减后的能力，结果只返回直接 Parent。

**边界与剩余工作**：Main 不直接创建 Child；Child 不获得任务控制、凭据或记忆写入权限。

**验收目标**：验证递归、并发限额、等待时释放执行槽、取消传播及恢复不重复创建 Child。

## 如何使用

用户在对话中交代需要拆分的目标。Main 创建 Task 后，由 Root 判断是否委派。

Worker 在 Code Mode 中可调用 `agent.run({task, artifactRefs})`，返回 `{text, artifactRefs}`。用户不需要选择子 Agent 或管理执行树；开发检查器由 F-031 单独提供。

## 详细操作参考

- [子任务协议与操作](../designs/delegation.md)

## 操作说明与配置细节

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="children-configuration-and-upgrade"></a>

### Configuration and upgrade

```yaml
children:
  max_depth: 8
  max_descendants: 4096
  max_tool_calls: 10000
```

Depth excludes the Root at depth zero. Descendants count all admitted Children
including terminal ones. Tool calls count first Worker invocation admissions
across the tree; replay does not consume another call. Beyond a ceiling, calls
return nonretryable `RESOURCE_LIMIT`; models receive no budget-query interface.
Deployment maxima are depth 16, descendants 10000, calls 50000. Limits are
frozen in `run_tree` for each new Root. Changing configuration affects only new
trees. These are safety ceilings; no small tree token/cost/time budget is added.

Migration 013 upgrades empty and existing schema 012 databases forward. Existing
Run capabilities and ModelSnapshots remain immutable. Old Roots without the
Agent Tool do not gain it on upgrade. Existing Root counters initialize from
stored Runs/invocations. Downgrades fail closed; back up the data root before
upgrading. No credentials or external account are needed for deterministic CI.

Fair queues and the larger Direct Tool batch scenario are described
below. M5 adds CodeCall and JavaScript `agent.run()`
using this same admission and ToolInvocation path. This slice does not claim
M4 completion, JavaScript execution, general Durable Replay or full GC-16/17/18/20
product-case scores.

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/delegation.md)；已有检查、未覆盖场景和验收方法见 [测试方案](../test-plans/delegation.md)。

委派方法与更窄树预算的独立换版回归包含真实 Store／Catalog 冷恢复：回退、退役后直接推进原排队 Root，保留原方法；停用后重开也不接纳新 Child。Host 的原树上限、授权、关系和结果接纳不由方法包改写。
