# 长期记忆、召回与上下文：测试方案

[功能与验收目标](../features/memory-context.md) · [技术方案](../designs/memory-context.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 范围与证据

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

Recall 系统夹具必须在真实上下文压缩后识别已完成的 Task 更新，不能因更新进入 working_state 而重复检索或创建 Task。`TestRecallFixtureKeepsTaskUpdateAcrossSummary` 通过真实摘要请求和 Provider 投影验证连续两次压缩，以及无关摘要不抑制检索；该 UT 随普通 Go CI 执行。`recall` 门禁继续验证实际 kill/restart、冻结召回与完整工作流，不增加重试或放宽原超时。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-009](../features/memory-context.md#f-009) | 验证同义事实归并、明确纠正替代、证据不足保留低置信、遗忘后不可继续使用旧上下文。 | 必须通过目标入口补齐闭环；不能只直接调用底层方法 |
| [F-010](../features/memory-context.md#f-010) | 验证可见范围、来源、时间、冻结恢复及索引故障不产生不完整成功结果。 | 保护已接通的限定范围；Live/配置另行验证 |
| [F-010](../features/memory-context.md#f-010) | 消息→任务→产物/原文的真实引用链，验证正确定位、摘要外分页续读、来源、预算及每跳/链接权限；覆盖缺失/过期/循环、跨作用域、Forget 与恢复。 | 本机另支持 Memory 原文、可见来源及确切 GitHub 接续证据。Main Memory/GitHub 导航、其他外部来源和真实模型导航质量仍待补 |
| [F-011](../features/memory-context.md#f-011) | 用长对话验证关键事实与待办保留、来源可定位及模型窗口约束。 | 必须通过目标入口补齐闭环；不能只直接调用底层方法 |

新增上下文回归验证：新 Run 的独立召回证据位于 history 与当前输入之间，旧 inline 策略保持原消息形状；两者均保留当前 user 正文及工具调用/结果顺序；历史证据按原始时间排序并转义边界；同输入的工具续轮、摘要、重试及重启复用结果，新输入重新检索。Conversation 与 Memory 两路覆盖超过候选限额的工作集重复项，保证工作集之外的证据仍可进入结果；已消费且压缩移出的原文可恢复，尚未消费的输入不可提前导入。普通对象 ID 经真实 Main 原文读取→附件→Task 的调用链验证，存储与模型投影分别检查，权限拒绝与历史回执重放保持不变。`make test-memory` 在必需 CI memory-context lane 内执行 Conversation 候选排除、工具 ID 投影及原文读取的 race 回归；普通 ID 的本机入口和 Task 追加指令另有真实调用链回归。UI 的任意状态、动作参数、Schema 与重复键 JSON 不得被当作宿主身份字段重写。

## 层级与关键路径

覆盖多语同义、明确矛盾与纠正、低置信保留、独立证据晋升、遗忘与旧 owner、索引故障/并发变化、压缩后目标和待办保留。确定性存储证明与语义质量评测分别报告。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 已有检查与缺失覆盖

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_memory_maintenance.py](../../tests/system/verify_memory_maintenance.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [tests/system/verify_recall.py](../../tests/system/verify_recall.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [internal/agent/sqlite/evidence_test.go](../../internal/agent/sqlite/evidence_test.go) | 共享 Actor/Provider/Tool 管线形成 current 消息→真实 Task→已授权附件链，UTF-8 原文续读、单跳循环、关系分页、SQLite 重开、读取回执重放、无新增 grant、隐藏/跨域/过期引用及 Forget 竞态；另一 Store 持有 WAL writer 时，本机读取与已完成 Accept/Prepare/Start 的 Main handler 均能在 500 ms 内读取原文 |
| [internal/evidence/evidence_test.go](../../internal/evidence/evidence_test.go) | 严格 schema、重复/null/伪造作用域拒绝、UTF-8 有界字节窗口与可前进游标 |
| [internal/agent/sqlite/evidence_memory_test.go](../../internal/agent/sqlite/evidence_memory_test.go) | Owner 精读覆盖跨用户/Agent、删除/替代、持有 WAL writer、未 Flush intent、同 ConversationID 的外用户归属、可见来源分页。阻塞 SecretStore 期间提交 Forget、纠正或 tombstone，迟到结果整页拒绝 |
| [tests/system/verify_github_evidence.py](../../tests/system/verify_github_evidence.py) | CLI 创建 Task 和绑定，由真实 Actor 接纳合成 PR 观察，再验证双向精确读取、UTF-8 续页、CI 结论、配置/撤权/重绑/Forget 及数据库逻辑内容不变。以 `--binary bin/steward --evidence evidence/github-linked.json` 运行 |
| [internal/agent/sqlite/evidence_github_test.go](../../internal/agent/sqlite/evidence_github_test.go) | 同代多个 Task epoch 的观察可导航，真实 Main claim 无 GitHub 链接，返回前撤销可省略旧链接 |
| [cmd/steward/evidence_test.go](../../cmd/steward/evidence_test.go) | 真实 CLI 重开续读产物和密封 Memory，覆盖无 embedding、执行依赖故障、来源校验、坏密文/缺钥/坏钥、旧明文表示和原文落盘边界 |
| [internal/agent/child_test.go](../../internal/agent/child_test.go) | Main Conversation 导航不进入 Worker/Child 的冻结工作面；旧 catalog Select 不自动追加新工具由 evidence 存储回归验证 |
| [internal/llm/fake/context_test.go](../../internal/llm/fake/context_test.go) | 确定性 Delegate fixture 消费摘要保留的已接纳回执与 Host Task update，再次摘要仍保留进度；旧摘要位于新显式输入之前时不抑制新请求，仅验证测试协议而不代表模型语义质量 |

- **F-009 质量边界**：结构化模型提炼已接入；真实模型的语义归一、遗漏和误提取质量需要持续外部评测，不能用 Fake 结果替代模型效果。
- **F-010 剩余覆盖**：Main 的 Memory 精读/解密和 GitHub 导航、替代谱系及其他外部来源尚待接通。实际模型自主导航路径的质量需外部效果评测。
- **F-011 剩余覆盖**：用外部模型评测验证长对话中的目标、约束、决策和待办保留质量；软件测试只证明上下文选择、冻结、隔离与恢复。

## 详细测试入口与证明边界

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="recall-verification"></a>

### Verification

```sh
make build
python3 tests/system/verify_recall.py --binary bin/steward --evidence evidence/recall.json
go test -race ./internal/recall/... ./internal/memory/... ./internal/conversation/...
go test -race ./internal/agent/sqlite -run TestRecall
```

The harness owns Launch (isolated data and explicit HTTP embedding/LLM Fakes),
Doctor (profile probe/activation), Drive (CLI chat, Task, Root/Child and real Tool
calls), Evidence (both indexes, scope exclusions, provenance and requests), Cleanup
(reaped CLI children, joined HTTP thread, deleted roots). It SIGKILLs the Go process
after recall commit, changes the corpus, and proves restart reuses the old turn
while a later turn sees new data. An embedding outage proves no partial recall or
model admission; restoring the same endpoint permits recovery. The current system runner and CI select this check; repetitions require an explicit runner option. Fresh/pending-v2 and populated-v1
multipart CLI scenarios prove an 80,001-byte original remains unchanged, all bytes
reach bounded HTTP requests, nine generations/four Runs complete, and a repeated
drain is inert. The actual HTTP adapter regression also covers UTF-8 boundaries,
the maximum 128 × 64 KiB original, mid-chunk failure/recovery and vector reuse.

The success fixtures control the legal Main/Root ordering with explicit test-only
CLI barriers: Main waits after Task commit until Root freezes its first recall;
Root's model dispatch waits until Main publishes and completes. Every original
assertion and command deadline remains. Unexpected CLI failures still fail once,
without retry. A sixth scenario forces the opposite ordering through the real
CLI and HTTP adapter: both Root query embeddings wait after catch-up while Main
appends its reply. That drain must fail closed with one pending assistant vector,
no Worker recall snapshot and no Worker model request. A separate explicit caller
then recovers all original Main/Root/Child assertions without rewriting frozen
recall or Conversation originals. Failed embedding barriers cannot count as a
successful conflict reproduction. Both orderings run in every five-run series.

Contracts cover lexical/vector-only fusion, ranking/time boundaries, user/Agent
scope, provenance, independent concurrent retrieval, competing supersession,
deletion, mutation/index rollback, old-owner rejection and migration. This is SC-08
retrieval/storage evidence, not full automatic formation or a product-quality
score. Deployment model quality remains release-time Live Shadow evidence.

References: [SQLite FTS5](https://www.sqlite.org/fts5.html),
[Ollama OpenAI-compatible embeddings](https://github.com/ollama/ollama/blob/main/docs/api/openai-compatibility.mdx).

```sh
make build
python3 tests/system/verify_memory_maintenance.py --binary bin/steward --evidence evidence/memory-maintenance.json
go test -race ./internal/context ./internal/memory/... ./internal/conversation/...
go test -race ./internal/agent/sqlite -run 'TestForget|TestContextLength|TestExperience'
```

The harness owns isolated Launch, profile/Fake Doctor, CLI/Tool Drive, database and
request Evidence, and process/thread/filesystem Cleanup. It covers health/goal/
finance/learning fragments, correction, encryption, restart, privacy revocation,
long history provenance and verified Skill promotion/retirement. It does not score
full GC-01–07, Computer cases or release-time Live Shadow behavior.

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

## 工程基础重构回归

F-010/F-039：`TestEncryptedRecallReusesVectorsAndInvalidatesScopeAndForget` 统计 embedding 调用；同查询第二次仅嵌入 query，追加一条只补一条向量；Forget 和换 scope 清除工作索引，持久库无私有向量。既有 scope、敏感明文不落盘、隐私 epoch 和每轮冻结证据回归继续执行。

## 上下文装配与语义压缩回归

`TestRecallFollowUpUsesRecentTopicWithoutDuplicatingVisibleHistory` 通过真实存储与 generation admission 构造旧 Pi 话题、新 Grokbot roadmap 及“展示结论”接续，验证查询保留最近问答、排除旧 UI 标题、去掉工作上下文已有证据、召回位于当前请求之前、冻结请求复用，以及后续工具交换只追加而不被召回拆开。`TestConversationalRecallQueryBoundsAndEvidenceIsolation` 覆盖 UTF-8/4096 字节上限、工具/runtime 隔离和旧策略缺省；`TestRecallFrozenPerTurnAndFenced` 覆盖同一输入内即使新增记忆或 embedding 不可用也不重查、重复文字的新输入重新检索、旧策略原始消息字节和旧 owner 拒绝。这些是装配规则的确定性证明，真实模型选题质量仍需单独评测。

以上顺序与冻结回归进入 `make test-prompts`，随 PR 必过的 Go lane 执行；召回/输入身份回归同时进入现有 capabilities-race lane。`verify_recall.py` 核对每个单输入 Run 只有一份召回，并分别核对九个业务 Generation 与工具关闭的内部摘要，不将摘要次数当作业务执行次数；SIGKILL、并发追加失败关闭、四个 Run 和三种角色的真实搜索断言仍保留。

`TestContextPromptBytes` 与 `TestSummaryPromptBytes` 对照当前约定的完整模型消息 hash，覆盖 Main/Root/Child、全部能力指导、冻结项目/Skill 资源、显式 Artifact 输入及三种摘要目标大小；从空临时目录调用，验证内置文案不依赖运行目录。这是确定性的消息兼容回归，预期 hash 仅在明确调整模型输入时更新，不评价模型效果。

[internal/agent/context_test.go](../../internal/agent/context_test.go) 检查角色组合、冻结项目/Skill 来源和显式子任务；[internal/context/working_test.go](../../internal/context/working_test.go) 检查目标与未知事实保留、工具交换边界、媒体与传输预算；[internal/agent/sqlite/context_test.go](../../internal/agent/sqlite/context_test.go) 通过真实存储和共享 Loop 检查：

- Task 续跑排除 Child 私有中间轨迹，保留未知操作状态。
- Root 最终答复已接受但尚未提交 report 时暂停并续跑，保留该答复；同一边界的内部压缩摘要不作为业务答复交接。
- 摘要结果已接受但未写 checkpoint 时恢复，按 generation 复用，不重新执行历史工具。
- 摘要继续使用原 ModelSnapshot、没有 Tools、不流式交付用户；原文通过私有 Artifact 可读取。
- 同 Task 的 Child 无法读取 Parent 私有摘要原文；新 Main 通过业务接续显式授权读取上一 Main 原文。
- 项目/Skill prompt 资源复制及冻结，读取当前资源不改写旧 capability。

这些用例不评价真实模型总结质量。现有取消、Forget、审批、Worker 树和 legacy 请求恢复测试继续适用。

`TestAutomaticMemoryUsesPrivateFilesAcrossStartup` 使用生产应用组装连续启动并核对同一文件密钥；删除已发布密钥后，启动仍可返回但 Memory 显示 unavailable 且不重新生成。`TestFilesKeepsMemoryKeyAcrossReopen` 以重开的文件后端解密真实 Vault 密文并检查目录和文件权限；路径穿越、符号／硬链接及宽权限回归拒绝不安全读取。原账户文件刷新和退出回归继续保护既有 OAuth 路径。

## 语义提炼、归并与召回回归（2026-11-06）

- `internal/memory/semantic_test.go`：一次性任务/UI 报错不成为候选；精确引用、当前 ref、facet/类别/操作和用户作者校验。
- `internal/memory/sqlite/semantic_test.go`：跨语言同义事实归并、独立来源晋升、纠正 successor、撤回、过期、Core 老化、类别遗忘/恢复、来源 suppression、退役审计、超期 facet 更新和幂等。
- `internal/agent/sqlite/memory_generation_test.go`：真实 state/memory 库、模型冻结及用量接纳事务；密封后台生成、跨库提交后重放不重调模型、无用户 Task/Run/交付、无效候选隔离、Forget 先于接纳/派发的竞态。
- `internal/agent/sqlite/recall_test.go` 与 `recall_query_test.go`：此前真实证据按轮冻结，当前输入及其派生内容被排除；Task 原始来源查询、已接受后续输入排除。
- `internal/recall/relevance_test.go`、`recall_test.go`：Grokbot 主题不匹配无关 UI 反馈，中文相关性，provenance/ref/内容去重，跳过过大 Memory 时仍能选中原始来源，私有分数不序列化。
- state 46/47→48、memory 2→3 及旧版本升级保留原始 Conversation、Task、模型冻结记录；旧自动摘句仅退役，显式记忆不变。
- `tests/system/verify_memory_maintenance.py`：实际 CLI/stdio Bridge 的有限脚本候选、隐私/恢复/压缩门禁；Fake 仅为传输和状态证据。

验证命令：`make test lint`，聚焦 `go test -race ./internal/memory/... ./internal/recall/... ./internal/agent/sqlite -run 'Semantic|Recall|Forget'`，系统 `runner.py --gate memory-maintenance --gate recall`。
本地部署前备份 state/memory/conversation 三库及配置，验证当前账号、模型和原始记录保持一致；退役和重新派生的记忆数量允许改变。

并发发布回归：Root 的真实原始查询在两路 embedding catch-up 后暂停，强制 Main 发布。发布时间晚于已冻结查询上界的原文不进入首轮 recall，且不会破坏本轮模型接纳；独立 embedding 故障仍验证无部分成功/无冻结模型调用。

## Required PR regression coverage

`make test-memory` runs the Memory/Recall domain tests with race detection and the
owning runtime privacy/recall regressions. The required `System / memory-context`
CI lane runs that target and both `memory-maintenance` and `recall` system gates;
`Ubuntu checks` requires the entire selected matrix. `tests/harness/test_ci.py`
asserts this lane and both gates stay in the PR matrix. Ordinary `go test ./...`
continues to run the complete unit/component inventory.

The enforced design invariants live in [the owning technical design](../designs/memory-context.md#memory-and-recall-invariants).

The required channels-web Memory browser scenario uses a finite structured Curator
fixture with a normalized fact and an exact user-only evidence quote. It checks
that neither the original first-person sentence nor its surrounding Tool plan is
stored as the derived Memory, while retaining pagination, Unicode search, source
navigation, correction, Forget, restart and read-only browser assertions.

`tests/harness/test_schema_version.py` checks that the system fixtures' explicit
state-schema expectation matches the contiguous migration catalog, so a forward
migration cannot silently leave foundation/local-provider/Code Mode gates testing
an obsolete schema version. The ordinary harness suite and required CI execute it.

## 四层记忆

必需 Memory/Prompt/Go 门禁执行 LayeredMemory 回归：三种派生文档独立检视，旧事实作为 notes；CAS 冲突与相同回执重放；版本 1 工具快照继续使用原 schema；超过 128 条事实仍可选取候选；链接传递失效；密文正文与标题；没有搜索命中仍加载 L4；后台修改不改变既有会话请求；Forget 撤销；三轮 Dreaming 形成 notes/wiki/injected，重放不重复模型调用或创建用户 Task；反序提案中的链接跟随同批编辑的新引用，丢失 state 侧步骤/覆盖/接纳回执后仍复用原索引文档回执；循环依赖、同批退役目标链接在写入前拒绝；普通事实与 Dreaming 的调用 deadline 各自有界，接纳租约保留清理余量。Bridge 回归通过真实 HTTP peer 验证只在传输副本解密，观察者保留密封请求且诊断不记录正文。

`make test-memory` 包含上述运行时与 Bridge race 检查；`make test-prompts` 包含注入快照回归。新迁移必须通过现有 schema-catalog harness 检查；memory-maintenance、recall 系统门禁继续必需。确定性模型仅证明传输与状态，不证明策略学习和显著性质量。

`memory_retry_bridge_test.go` 通过独立进程中的真实 Bridge Server 与本地 HTTP peer，先返回无效建议再返回有效建议，验证新会话重试、已接纳结果重放不发请求，以及连续三次无效建议被隔离后后续输入继续。测试经过真实 Dispose 终态，不使用空实现代替会话生命周期。

`TestLayeredMemoryDeployedV2SnapshotKeepsExactSchema` 验证已部署 v2 的 schema/权限在 v3 下仍按精确 predecessor 执行，拒绝未知 v2 schema；Host 拒绝负数、超限窗口与修订。`tasks` 门禁继续用原 8K 模型运行问题回应和成果审阅续轮，防止扩展说明挤占最小窗口。

显式检索／精读补充：`recall_explicit_test.go` 覆盖旧 v1 与新 v2 的工作集排除和输出预算；`memory_precision_test.go` 覆盖 v4 transcript 作用域、分页、密封、Forget，以及 CodeMode 传输投影的确定性、跨 Run 拒绝和 v3 回放。新投影在 context admission 前计算展开预算，测试纳入 `make test-prompts` 与 `make test-memory`。

时间过滤回归包含 UTC、显式偏移、缺少时区的拒绝，以及旧冻结工具规格不变；复合 memory_ref 必须逐字保留，不能截取内部 UUID。

渐进读取回归覆盖：完整 Wiki 不能遮住原始会话；同相关度原文优先，L4 不作为搜索回声；相关引文保留作者与原句；只排除当前工作集实际展示且有同 Run 成功回执的完整结果，失败 CodeMode 父帧／伪造内容／截断／压缩后的证据仍可读取；导航引用授权隔离、预算与冻结续接；原有 Forget 和历史协议不变。真实模型使用同题、同快照、同16K窗口和4K输出比较优化前四层、优化后四层与同代码L1/L2，不择优采样。

聚合回归区分事实支持与全集覆盖：限制原始会话时派生库不可用不影响授权原文读取；source_type 不能选择 Task／其他用户；同题活动计数保留部分 Wiki 误报的失败，并重新全量对比。


## 证据复用、学习反馈与云接缝

必测完整搜索／密封精读／自动召回的当前可见性、混合 CodeMode 帧、失败父帧、假 JSON、截断与压缩后再取证；核对旧策略／冻结前缀保持不变。轻量导航用 SecretStore 计数证明仅解密元数据，并覆盖用户／Agent 隔离与 Forget。跨模型轮次重复文档精读只返回已可见指示，仍核对生命周期。

学习回归通过确定的实验反馈把暂定假设改成有原始引文的规则，确认旧假设不再作为当前证据；相同提案不产生新版本，重放不重复记账。两个独立 Host 竞争一个维护 generation 仅推理一次，Busy 不产生失败记录。外部 SecretStore 与非 local Host 身份可以完整写读，重启更换所有者被拒绝，无文件凭据依赖。

受控真实模型用 tests/fixtures/memory_learning_mcp.py 的 stdio MCP 验证计划，记录失败假设、成功修正、后续新会话复用和规则变更后的纠正；导入／邮件／登录均在模拟器验证，不执行外部操作。固定策略、问题和验证器，同批问题比较旧四层、新四层及同代码 Notes/Transcripts 对照，并报告后台 formation token／耗时。MCP 成功回执是动作判据，不能只按答复自称成功评分。这是受控工作流评测，不是公开 benchmark 或真实外部操作成功率。

make test-prompts 包含统一可见性回归；make test-memory 包含新增 Memory 与外部 SecretStore race；标准 Python harness 包含验证器判据测试和 schema 50 目录一致性检查。仍须运行 memory-maintenance、recall 系统门禁与相关恢复／通道检查。


混合 CodeMode 摘要必须保留同帧非记忆回执；原始动作是否发生不能被通用记忆提示替代。该场景由 TestMemoryCompactionPreservesMixedCodeBusinessReceipt 进入 test-prompts 和 test-memory。原始 Worker 反馈、旧笔记回查、覆盖重放及 Forget／跨 Agent 拒绝由 Memory 所属回归验证。
