# JavaScript Code Mode 与持久恢复

在隔离 JavaScript 中编排工具、并发处理和恢复已接受工作。

[需求主索引](../feature-map.md) · [技术方案](../designs/codemode.md) · [测试方案](../test-plans/codemode.md)

## 功能说明

Agent 能用标准 JavaScript 串联工具、处理并发并显式输出结果。

本文描述用户行为和范围；逐项完成状态以主索引为准。当前能力依据代码静态盘点，配置可用性与真实服务/模型质量需分别验证。

## 需求与验收

<a id="f-008"></a>

### F-008 · 可恢复的 JavaScript 编排

**需求**：Agent 能用标准 JavaScript 串联工具、处理并发并显式输出结果。

**当前实现**：Main 和 Worker 统一使用 Node/QuickJS Sidecar，支持嵌套调用、Store、固定源重放和等待释放；模型直接声明 Code Mode 与少量高频发现、Skill、UI 和 Main 任务协调工具，其余工具经 Code Mode 调用；权限仍由各角色冻结能力决定。

**边界与剩余工作**：运行时不提供任意网络、文件、凭据或不可重放时间随机源；不是通用 shell。

**验收目标**：验证真实 QuickJS 执行、等待/重启、Store 观察复用、参数漂移拒绝及已完成调用不重做。

## 如何使用

标准安装启动会配置随包 JavaScript 运行时；自定义配置按指南指定可信 Node 和脚本路径。

Agent 调用 Code Mode，可使用 `tools`、`agent.run`（仅 Worker）、`cua`（配置 Computer 后）、`store/load`、`text/image` 等。只有显式输出进入父级结果。

## 详细操作参考

- [Code Mode 约定](../designs/codemode.md)

## 操作说明与配置细节

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="codemode-script-contract"></a>

### Script contract

```javascript
const matching = await searchTools("echo", {limit: 10});
const detail = await describeTool(matching[0].name);
const results = await Promise.allSettled([1, 2, 3].map(n =>
  tools[matching[0].name]({text: String(n)})
));
store("summary", {count: results.length});
text(load("summary"));
return results.map(result => result.status);
```

`ALL_TOOLS` contains canonical names and short descriptions. Complete schemas, TypeScript declarations and usage instructions
remain behind discovery. MCP tools default to codemode exposure and are omitted
from model tool declarations until explicitly loaded through tool search.
`searchTools` shares the ranked frozen-catalog search, defaults to 8 matches,
and does not load model declarations. Hidden and model-only tools are absent
from scripts. Canonical bracket calls always work; an escaped alias
is available only when unambiguous. Discovery searches only the frozen Run
view. New Main Runs use Code Mode for their own frozen tools; Computer and native workspace work remains delegated through Task.
Connected Apps uses one exact account namespace, `apps__b__<binding>` or
`mcp__apps__b__<binding>`, for its original `search_tools` and `execute` tools.
`describeNamespace` and namespace search filters never combine accounts under
a broad `apps` namespace. Ordinary configured MCP server names keep their
existing matching behavior.
Workers with the frozen capability get exactly
`agent.run({task, artifactRefs?}) -> Promise<{text, artifactRefs}>`. No Run ID,
usage, output schema, agent selector or control handle enters this API.

The CUA facade provides inventory, explicit binding, and lightweight Surface
proxies. Binding alone emits no output. `documentation()` and `capabilities`
resolve through the same nested Executor. Surface handles stay within one cell;
Store rejects them, including nested handles. Private Computer bridge helpers
are absent from public tools/discovery, so scripts cannot extract raw lease
tokens for persistence. The Fake exposes read-only
observations and documentation; it does not pretend to implement GUI actions.

Only explicit `text()`, `image()` and top-level `return` reach the parent Tool
result. Console and nested business results stay internal. Errors reject
Promises with normalized `code`, `message`, and `retryable` fields. `exit()`
ends successfully. A script failure does not roll back accepted work.

`store(key, value)` commits synchronously to invocation-local JSON state before
returning. Writes survive later script failure and process restart. Reads
return copies; a missing key returns `undefined`. Keys are 1–256 bytes, each
JSON value is at most 256 KiB, and total stored keys/values at most 1 MiB.
Repeated writes use a versioned last-write-wins update. Other cells and Child
Runs do not inherit this state. Large data belongs in Artifacts.

<a id="codemode-upgrade-and-operations"></a>

### Upgrade and operations

Install the pinned Node/QuickJS dependencies before starting `chat work`.
After a crash, run `chat work` with the same installed runtime and configuration;
a killed Go owner's five-second lease must expire before work is claimed.
Missing or changed runtime bytes fail closed and leave recovery intent intact.
Restore the original runtime to continue. Never rewrite source, hashes, claims
or accepted arguments in the database to force replay.

Migration 17 preserves v16 ledger/Store/events and all earlier state. v1 cells
lack the v2 replay manifest and source-position identities. They are **not**
automatically upgraded: finish active v1 trees with their original binary and
Runtime before deploying v2. Frozen incompatible capabilities are rejected
rather than switched. Empty/v16 migration, rollback and preservation are tested.

This slice proves deterministic protocol/recovery behavior. It does not award
Golden Case semantic scores or complete M5. The [Core evaluation guide](../test-plans/product.md)
records the due case evidence and ungraded dimensions. Production Computer
lifecycle/input and the dated macOS compatibility result are documented in
[Computer operations](../designs/computer-use.md) and [L2 notes](../test-plans/product.md);
this Code Mode fixture does not prove live compatibility.

The M5 exact dataset and five-run Core gate is documented in [Core evaluations](../test-plans/product.md). It adds GC-17/19/20 evidence to these protocol/recovery checks without assigning semantic scores to scripted providers.

The top-level description names structured `tools[name](../test-plans/product.md)`, computer use via
`cua`, and delegation via the singular `agent.run({task, artifactRefs?})`. Discover
CUA with `describeNamespace("cua")`, observe inventory, bind a surface, read its
`documentation()`, and re-observe after actions. A delegating Worker discovers
`describeNamespace("agent")`; Main has no `agent` namespace. There is no plural
`agents` API. Child completion returns only `{text, artifactRefs}`. Memory and
Artifact schemas are available through the same discovery functions; unavailable
Memory configuration is explicit in their descriptions.

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/codemode.md)；已有检查、未覆盖场景和验收方法见 [测试方案](../test-plans/codemode.md)。

## 版本化执行配方与工具说明

标准分发包含独立 `codemode` Go 插件：在 Host 原上限内选择执行资源，格式化已授权工具的参数说明。新 Run 固定组合；旧 Run、已接受脚本和说明继续复用原版本及字节。升级／回退不改源或 Tool 回执，停用后新执行明确不可用，已提交事实保留。迁移验收已完成；固定 VM、具体工具权限、审批、恢复与凭据仍由 Host 持有。机制及刻意保留的边界见[技术方案](../designs/codemode.md#独立执行配方与参数说明私有实验接口)。

工具发现的原 BM25／分词／摘要选择已由独立托管插件持有，原生 tool_search 与 Code Mode searchTools 共享已授权的原快照，插件只能选择原候选。已接纳 Code Mode 回复先持久化，原生嵌入、所有者 prior23 选择及旧 Run 边界明确保留；当前标准分发已接入，真实独立排序换版、回退／退役／停用、原 VM 冷恢复及原生／Code Mode 两个入口已有本地验收证据；协议仍为仓库内部实验级，Linux 平台证据由必需 CI 另行证明。见[工具发现设计](../designs/codemode.md#工具发现排序的私有托管边界)。
