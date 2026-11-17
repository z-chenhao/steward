# 开发、验证与交付

[主索引](../feature-map.md) · [技术方案](../designs/engineering.md) · [测试方案](../test-plans/engineering.md)

## 功能与需求

<a id="f-038"></a>

### F-038 · 标准开发与显式发布流程

开发者能安装固定依赖、构建、按风险验证并准备可审查交付。现有 Makefile、测试 runner、CI 和原生打包入口已实现；成功的软件回归不代表真实模型效果或发布授权。完成状态以主索引为准。

验收目标：可复现构建，相关回归保持真实断言，失败不被当作通过；发布继续需要明确授权。

<a id="f-039"></a>

### F-039 · 运行时工程基础重构

运行时需要清晰的故障隔离、恢复和模块边界，为 Observability、Evals 与其他功能提供可维护的工程基础。

| 需求 | 当前行为与验收 |
| --- | --- |
| 故障隔离 | 已提交终态的 Main 模型失败不关闭 Web；基础设施故障仍明确返回。 |
| 启动可恢复 | 控制页面先开放，依赖准备在后台重试；只缓存成功的能力注册，修复依赖后无需重启。 |
| Artifact 短事务 | 流读取、落盘和完整性校验不持有 state.db 事务；配额、引用与 Tool 终态仍原子提交，跨进程回收跳过活跃 staging。 |
| 加密召回复用 | 同一授权作用域复用仅在内存的 FTS/向量索引；新增记录只补新增向量，Forget、换作用域和关闭清除工作索引。 |
| 查询独立 | GET 不触发 Flush 或持久化投影；Conversation 每页最多 100 条，Settings/Progress 只查询本页数据；交付按消息 ID 定位。 |
| 模块边界 | Task 生命周期判断归 Task；CodeMode 的 VM 重启/重放协调归 CodeMode；SQLite 适配器保留持久化、fencing 和原子提交。 |
| 工具分发 | Host 工具验证与分发使用同一受信目录；Artifact 独立分发，未知名称不会落入 Task。 |
| 轨迹成本 | 流式文本预览不逐片写入状态库；生命周期轨迹写入失败可诊断，最终模型结果仍持久化。 |
| 可读性与错误 | Run 角色/状态/阶段使用内部类型；Bridge 转换分成有名函数；内部错误保留原始 cause，展示文本不泄露 SQL/供应商内容。 |

这些内部机制保留模型可见协议及旧 Run 的冻结快照；Observability 的功能范围由所属文档维护。内存索引仍采用精确向量检索，Main 初始化仍按既有冻结历史边界恢复上下文；这不是大规模 ANN 或新的上下文裁剪策略。

使用方式：正常启动 Web；依赖不可用时可继续进入 Settings/Progress，修复配置对应的本地依赖后等待运行时恢复。会话页面通过 Earlier messages 查看上一页；`GET /api/conversation?before=<sequence>` 返回至多 100 条消息，有更早数据时响应头 `X-Steward-Before` 提供下一页游标。CLI 显式历史保持完整保留记录。


<a id="f-040"></a>

### F-040 · Go 模块边界与可读性

Go 工程结构减少跨领域的适配器依赖与入口内的多阶段控制流，使开发者能在所属模块中找到职责清楚的实现。

验收范围：Computer 生命周期持久化归 `computer/sqlite`，只读运维查询归 `operations/sqlite`；运维、额度、审批和工具结果类型由所属模块定义；CLI 的语法验证、输入解码、离线运维、测试注入和执行分发分别维护；Web 的消息、附件、模型、账户、通知操作各有命名处理函数；Task 接纳/重放与事务内具体操作分开；能力注册与 Runtime 实例组装分开。

正常使用方式不变。开发时从所属包进入，跨领域的原子变更继续由原 Host 事务接纳。Computer 适配器通过 `ExecutionHost` 加入既有 fenced transaction，不打开第二个数据库连接或自行调度。App 不再导出执行用的 SQLite Runs 访问器，也不向 CLI 返回 SQLite 自有的展示类型。

内部职责拆分须保持 CLI 命令参数、JSON 字段、HTTP 路由、审批/重放语义、schema 及冻结指纹一致。LLM 契约、协议实现与 RPC 适配器的依赖方向由 Go 传递依赖测试保护；生产工具装配与 Code Mode 的大目录、审批、慢调用响应场景进入 PR 门禁。模块依赖与边界详见并列技术方案；测试方案列出已有集成回归如何覆盖拆出的模块。

## 如何使用

以下维护开发命令与迭代流程。当前 CI 技术组织见并列技术方案，测试层级与执行说明统一在 [产品测试方案](../test-plans/product.md)。

## 操作说明与配置细节

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="ci-local-use"></a>

### Local use

```sh
make test lint
make test-prompts  # Agent prompt order, frozen recall/prefix, inheritance and progress
make test-e2e E2E_EVIDENCE=evidence/my-change
make test-full E2E_EVIDENCE=evidence/full-check
make test-race
make audit  # Current network advisory checks, separate from deterministic tests
```

The system runner refuses to overwrite a previous summary. Select a fresh
output directory when rerunning; retain a failed run when diagnosing it. Use
`python3 tests/system/runner.py --list --suite full` to inspect the runnable
software inventory. Results are ignored local outputs, not committed audit ledgers.

<a id="contributing-contributing-to-steward"></a>

## Contributing to Steward

Start with [CONTRIBUTING.md](../../CONTRIBUTING.md), [AGENTS.md](../../AGENTS.md), the code being changed and its nearby tests.
Use the [testing guide](../test-plans/product.md) for test ownership and commands, and the
[feature designs and runtime contracts](../designs/product-overview.md#contract-0-文档效力与实现规则) for product protocols and invariants.

<a id="contributing-development"></a>

### Development

Use Go 1.26.11+, Node 24.21.0, Python 3.10.12+ and a C compiler. On Linux,
coding isolation requires Bubblewrap and user namespaces. Install dependencies:

```sh
make setup
make build
make run
```

On a clean Ubuntu host, browser system libraries can be installed with
`python3 scripts/setup.py --part browser --with-deps`. Node must match
`.node-version`. Keep npm, Go and Playwright's native download caches; do not
share mutable `node_modules`. Setup uses frozen npm lockfiles, ignores lifecycle
scripts and verifies cached packages. Use `SETUP_FLAGS='--mode offline'` when
all required downloads are already cached. Offline audit output does not prove
current advisory coverage. Do not disable TLS, checksum or signature verification.

<a id="contributing-product-work-first"></a>

### Product work first

Start from a user problem and an observable result. Fix product defects with
focused regressions. Standalone CI tuning, new verification frameworks, Golden
Case expansion and clause-audit work are paused unless explicitly requested by
the owner. Do not create a new script when an existing test or command can prove
the behavior. An explicitly requested engineering-governance change is in scope.

Multica is retired. Old STEWARD tasks and historical coordination notes do not
authorize new work. Link an issue if one exists; a branch or PR does not require
an issue identifier. Do not recreate the old task hierarchy in another system.

<a id="contributing-feature-documentation"></a>

### Feature documentation

Use the [Feature Map](../feature-map.md) as the product requirement index.
Register each requirement with a stable ID, a short description, completion
status and a link to its feature document. Feature documents explain behavior,
requirements, acceptance and current usage, then link a technical design and a
parallel test plan. Keep simple test plans small by referencing existing checks.

Follow the [documentation policy](../README.md#强制维护规则). Update architecture,
flows, active decisions, reasons and compatibility effects in place. Keep
obsolete diagrams, dated reviews and append-only histories out of `docs/`.
Clearly distinguish implemented behavior, accepted plans and open proposals.
Test plans map requirement IDs to observable scenarios, existing entry points
and coverage gaps. Execution reports remain separate from plans.

Update the index, feature usage, design and test plan with the implementation.
Do not mark a requirement complete merely because a schema, renderer, fixture
or test file exists. See the [maintenance rules](../feature-map.md#文档维护规则)
and templates for the exact structure. Cross-feature protocols live in the runtime contracts and owning designs;
decision history and execution evidence remain in PRs, Git history or CI artifacts.

<a id="contributing-working-on-a-change"></a>

### Working on a change

1. Define the concrete behavior and the evidence needed to prove it.
2. Inspect the implementation, callers, closest instructions and existing tests.
3. Reproduce a bug where practical. Preserve unrelated user files and changes.
4. Implement one complete change. Keep approval, credentials, data integrity and
   replay guarantees intact. Ask about actual product/protocol conflicts.
5. Run focused checks while editing, then the proportional gates below.
6. Review the diff and remove temporary outputs. Update current documentation.
7. Report the behavior, checks/results and remaining limitations. Distinguish
   failed, skipped and unrun checks; never turn scripted responses into model scores.

Local deployments may include unmerged fixes. Before PR preparation, reconcile
the requested behaviors against tracked and untracked sources, templates,
generated assets, documentation and tests. List any requested fixes excluded
from the PR. Before replacing that deployment with `main`, compare its source
and local patches against the actual merged commits; preserve and report any
remaining difference. Delivery evidence identifies the merged revision, the
served JS/CSS and the affected UI behavior, not only a successful startup.
CI can reject regressions present in its checkout; it cannot discover a patch
that was never submitted. The change inventory and deployment comparison close
that separate gap without requiring unrelated local work to be committed.

| Change | Evidence |
| --- | --- |
| Documentation | Inspect content and run `python3 scripts/check_docs.py` |
| Local logic | A focused existing unit regression or necessary new one |
| Agent prompt assembly | `make test-prompts`, frozen message byte checks and Root/Child scope tests |
| State/store/adapter | Real store or protocol integration tests |
| Approval, cancellation, concurrency, replay | Relevant failure/restart tests and race detector |
| CLI/UI/API | Drive the real interface and inspect resulting state |
| Cross-module change | `make test lint`, relevant `make test-e2e` gates |
| Release | Extended regression, race checks, native packages and relevant live compatibility checks |

Go package tests currently mix unit and component integration checks; `go test
./...` is not called a pure UT suite. The eleven deterministic core system gates
run in isolated parallel PR CI lanes, with one required aggregate status that
rejects failed, cancelled or missing work. The software/system jobs allow a 60-minute outer budget; individual test deadlines remain unchanged. The five-minute latency target is currently unmet and is measured against remote workflow timing. Extended system coverage and the full race inventory run separately
on a weekly schedule or manual request. See [CI](../designs/engineering.md#ci-continuous-integration-and-delivery).

`make lint` also runs pinned Staticcheck and actionlint. Compatibility exceptions
must stay local to the relevant diagnostic; do not disable entire rule sets.

<a id="contributing-runtime-and-delivery"></a>

### Runtime and delivery

`STEWARD_DATA_ROOT` selects an explicit data directory. Tests use disposable roots;
never use a personal application root as a fixture. `doctor` is read-only;
`status` can initialize or migrate state. Credentials belong in SecretStore,
never YAML, source, test outputs or logs. No command selects a paid/default/Fake
model implicitly. Packaging uses `sh scripts/package.sh /absolute/output`.

Keep PRs focused. Describe the user behavior or engineering problem, resulting
change and relevant validation. CI success is software regression evidence;
it does not certify real-model quality, real-account login or release acceptance.
Creating a version tag or publishing a release requires an explicit request.

开源治理：项目采用 [Apache-2.0](../../LICENSE)，协作与分支策略见 [GOVERNANCE.md](../../GOVERNANCE.md)。README 提供首次安装和开发路径；GitHub Issue 表单与 CODEOWNERS 提供问题描述和评审入口。Dependabot 定期提出依赖 PR，仍需审核冻结能力兼容性、构建产物和第三方许可。原生安装包保留项目、Go 链接依赖及 Web 授权说明。

Go 全量、提示／冻结上下文与工具发现 race 使用三个独立必需软件分片，分别调用现有 Make 目标；聚合 Ubuntu checks 仍要求全部成功。分离累计时长，不减少检查或复用可变构建目录。
