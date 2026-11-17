# JavaScript Code Mode 与持久恢复：测试方案

[功能与验收目标](../features/codemode.md) · [技术方案](../designs/codemode.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 范围与证据

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-008](../features/codemode.md#f-008) | 验证真实 QuickJS 执行、等待/重启、Store 观察复用、参数漂移拒绝及已完成调用不重做。 | 保护已接通的限定范围；Live/配置另行验证 |

## 层级与关键路径

使用真实 Node/QuickJS，而非脚本结果 Fake；覆盖 Promise 并发、Store 读写、资源等待释放 VM、固定源漂移及重放命中。记录未 await 的已接受调用，不声称失败脚本会回滚效果。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 已有检查与缺失覆盖

`codemode.TestConnectedAppNamespacesKeepExactAccountBoundary` 覆盖 Apps 两种精确账户 namespace、另一账户／宽泛名称／畸形工具拒绝以及普通 MCP 匹配。SQLite 的 `TestConnectedAppsGuidanceActualAccountDiscoveryUpgradeRetainedRollbackAndDisable` 通过真实 JavaScript 查询两个账户，验证每次只返回对应两项工具、冻结文字和原执行属性；实验工具包的默认分发与完整迁移仍按[集成方案](integrations.md)验证。

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_codemode.py](../../tests/system/verify_codemode.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [tests/system/verify_code_recovery.py](../../tests/system/verify_code_recovery.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |

现有入口仍需在后续相关实现变更时按风险运行；需求范围变化时同步扩展场景。

## 详细测试入口与证明边界

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="codemode-launch-and-verify"></a>

### Launch and verify

Install Node **24.21.0**, then install the exact WASM dependency:

```sh
npm ci --prefix runtime/javascript --ignore-scripts
make build
python3 tests/system/verify_codemode.py --binary bin/steward --evidence evidence/codemode.json
python3 tests/system/verify_code_recovery.py --binary bin/steward --evidence evidence/code-recovery.json
```

The reusable harness owns Launch, Doctor, Drive, Evidence, and Cleanup. It
creates temporary configuration and databases, activates the explicit
`codemode` model fixture, then drives `chat send`/`chat work`. The model fixture
selects requests; the real Node/Pi/QuickJS VM executes the JavaScript. The
Computer Fake is injected only with this test fixture. Production has no Fake
Computer fallback. The configured production [Computer](../designs/computer-use.md) and
[Computer Use](../features/computer-use.md) adapters have separate harnesses and provisioning
requirements.

The Code Mode and 100-child recovery gates select a 16,384-token fixture context
window and keep the 1,024-token output limit. Their Main receives the current
Tool catalog and explicit cell sources/results; recovery also retains eight
approval notices before final delivery. An 8K window cannot always retain the
protected input plus the minimum summary reserve. This workload-specific
profile does not change production budgeting or the independent small-window
compaction and rejection checks. The 37
completed / 8 waiting / 55 remaining calls, exact approvals, dispatch counts,
kill/restart variants and cancellation assertions remain unchanged.

The report includes exact explicit outputs, nested invocation identities,
source/Store/event readback, a real Child Worker result with inherited frozen
model identity, approval rejection without Provider dispatch, schema version,
before/after-effect SIGKILL recovery with one durable identity and one external
receipt, and cleanup. The system runner selects this harness; repeat count is explicit. `make test` includes
real Sidecar transport, Promise, resource, cancellation, and security fixtures.

To enable Code Mode in an ordinary installation:

```yaml
version: 1
javascript:
  command: /absolute/path/to/node
  script: /absolute/path/to/steward/runtime/javascript/src/sidecar.mjs
```

Both paths must be trusted and outside an editable coding workspace. There is
no implicit executable discovery or default model. Capability snapshots freeze
the versioned ToolSpec for each new Run. Existing snapshots retain their
original Tool surface. A Sidecar protocol failure produces a sanitized Tool
error, never a raw exception or inherited credential.

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

## 工程基础重构回归

F-008/F-039：现有真实 Sidecar、挂起/恢复、Child、事件落库失败、旧 owner fencing 和 SIGKILL 重放测试覆盖提取后的协调器；相同 source/runtime/prelude/ToolSpec fingerprint 必须复用既存结果，unknown 外部作用不重新派发。Provider Bridge 既有 Node 协议测试覆盖拆分的上下文转换函数。

统一 Code Mode 与 500 工具目录的配置、Main/Worker 脚本发现、未声明调用拒绝及历史冻结面恢复覆盖，统一见[工具与审批测试方案](tools-approvals.md#渐进工具发现与-500-工具目录)。CLI capabilities 的 `tool-loading` 已纳入完整门禁，读取同一角色冻结目录。

`TestCodeModeAcceptsLargeBoundedSource` 通过真实 Sidecar 执行超过 16 KiB 的合法脚本，验证模型结果接纳遵循 Code Mode 的 64 KiB 参数边界；其他工具继续使用 16 KiB 边界，总帧和上下文限制仍生效。

生产直接声明名单与角色过滤由 `TestProductionToolSurfaceKeepsOnlyFrequentControlsDirect` 验证；Code Mode-only 测试保存显式冻结面的兼容覆盖，并非当前生产声明全集。

## 版本化执行配方与说明回归

`TestCodeModePlugin*` 驱动实际独立 Go 二进制与 QuickJS：默认说明与 native 原字段一致；连续读取、嵌套调用挂起和 VM 重建保持 JSON.stringify 字节；独立 v1.1 缩小等待预算并改变类型说明／提示，升级前排队 Run、retired 包、回退及停用均保留各自方法。恶意 v1.2 即使通过其自扩 Schema 和独立所有者 fixture，也不能超过 Host 资源上限，且不能创建 manifest 或触发嵌套效果。

同组回归验证 Main／Worker 权限、20 KiB 源、Store 和终态事件写入失败、CodeCall 参数漂移、旧 VM epoch、已冻结说明不绕过 Guardian、缓存写失败无确认、拒绝回复冻结、真实 Forget 在 formatter 计算后阻断输出。`TestV58CodeDescriptionMigrationPreservesNativeManifestAndReplay` 保留真实 native manifest／hash／source／lease，前向升级不改旧执行或追加策略；新表保留不可变回复。角色／恢复测试同时保留 native 版本；LateForget 纳入现有 memory race 发现，插件族纳入 test-prompts 和普通 Go 检查。

现有 `codemode` 与 `code-recovery` 系统入口显式安装 Task／Progress／Feedback／Delegation／CodeMode 组合，独立核对每个实际 manifest 的 frozen graph、服务边界和资源配方；真实三种 kill／shutdown 的 100 Child 工作负载还比较恢复前后完整说明行与原提供方派发审计。未改变原 37 完成／8 审批等待、剩余 55、unknown 副作用及精确取消断言。启动门禁独立核对标准分发包清单。确定性验证不证明说明的真实模型质量，也不证明可任意替换 VM 字节；运行时指纹 driver 的平台验证继续独立维护。

## 工具发现插件验收

`make test-tool-discovery` 为 CI 必需竞态门禁；普通 Go 门禁继续覆盖全部新根。对照候选前 288 个原始输出，包括 500 候选、已加载／隐藏／内部资格、CodeModeOnly、schema／namespace／中英文分词、精确匹配、空词、截断和限制。用实际原生模型调用与 Worker JavaScript 查找并执行所选工具，证明独立排序版本改变真实结果、未预加载整个目录、未提升授权或跳过审批；验证实际9MiB冻结元数据不引入普通输入限制。

验证索引／窗口越界、重复、整数溢出、未知字段与原输出预算；Code Mode 回复在确认前保留，写失败不得确认，回执不可 update/delete/replace，隔离后的新查询明确拒绝，旧 VM 不得重读。真实冷恢复／暂停重放、升级与已冻结 Run／Child、回退／退役／停用、晚 Forget 及声明合法但索引越界的坏计划已有定向回归；仍须在固定源码完整验收，并完成默认29／真实旧23／归档24及 Linux，不把纯原始对照或单次握手算作完整交付。Forward schema 必须同时更新实际 Go migration catalog 与共享 system fixture，普通测试检查每个源文件均实际嵌入。

`TestToolDiscoveryBootstrapRejectsCorruptPlanAndRetainsOwnerDisable` 用真实排序进程证明独立 owner 期望拒绝声明合法但伪造索引的候选，首启失败不产生选择，冷启动与所有者停用不被修复。标准29包的 `tool-discovery-plugins` 系统门禁使用正常 CLI、provider bridge 和 Code Mode，检查完整包集合、冷启动不重选、实际原生／脚本发现及先行冻结回复。可选 `--prior-binary` 必须使用归档的真实23包二进制和原封存包：既验证保持原23包／资源的单包升级，也逐一安装完整当前29包，要求23个已变内容的原包有独立新版本且原 digest 仍保留，再经所有者验证切换并回退整套原图。切换后立即消费旧程序接纳的排队 Run，回退后立即消费新版已排队工作；旧图／新图分别保持原解释，移除后新工作拒绝原生兜底。另用明确的原 scripted children bridge 在旧版真实 Root／Child 接纳后 SIGKILL，首个新版 status 只迁移读取；显式安装／验证／切换完整新图后立即 chat work，核对原子级身份、图／能力／模型／上下文和已完成 Generation 原字节，要求恢复后确实创建并完成 Grandchild，继续继承旧图，冷恢复不重放。旧 scripted 夹具的初始23包由所有者在接纳输入前显式装配，不把它宣称为普通启动或旧树实际排序查询；普通 CLI 原生／CodeMode 发现则使用独立的 HTTP 模型替身。`--prior24-binary` 另验证真实归档24实物，要求原24个 digest 保持一致；回退后发现服务仍可用，不能让模型替身误期待停用错误。归档候选是否完成整体验收须单独说明。`--prior28-binary` 另外驱动实际原28包的整套升级／回退和冻结子树恢复，要求28个原 digest 不变；当前29只增加 GitHub 包。三种真实旧树路径均要求实际新建 Grandchild，旧24／28树保留原发现服务。未提供相应旧制品时分别报告 `not_run`；当前 SDK 的 prior24／prior23／prior22 组合用例不能冒充历史制品验收。

实际9MiB元数据的竞态回归保持 `policy_json.timeout_ms=10000`，同时验证不可变快照／引用／参数不可修改、完成 invocation 不再释放输入、原生读取中 Forget 拒绝晚结果。完整夹具设有自己的有界运行期限，不能用放宽工具超时绕过回归。

`TestToolDiscoveryActualFrozenRootUpgradeRollbackRetireAndDisable` 只声明真实 Root 生命周期覆盖。`TestToolDiscoveryActualChildrenAndGrandchildrenKeepRootVersion` 另创建六棵实际子树（共12个已完成 Child／Grandchild），在 Root 固定后、子级实际接纳前切版／回退／退役／停用，逐个核对原图、服务清单、模型、Task epoch、能力／scope、真实 JavaScript 排序结果和冻结回复。原生旧树没有新缓存，受管树每个子级恰有一条已接纳发现回复；已迁移后的停用新树逐级明确不可用。断言必须包含正向的子级数量／祖先关系，不能仅查询不存在的子级是否有不一致。此根由 `make test-tool-discovery` 与普通 Go 门禁自动覆盖；当前 SDK 的原生树不冒充实际旧发行制品。

Schema67 的回归包括：真实 Actor（保留生产心跳）驱动500工具的六棵 Root／Child／Grandchild 子树；原五秒 Run 租约及工具期限保持不变。`TestChildStorageRejectsAncestryAndCapabilityExpansion` 直接验证完整描述符变化、越权能力、NULL、祖先／模型错误拒绝及合法子集接纳，并进入 required tool-discovery race。`TestToolDiscoveryV66ChildAdmissionMigrationKeepsFrozenTree` 验证原66数据升级后，Run、能力、上下文、图绑定、模型／工具回执、子级身份、树计数与 claim 原字节不变。实际 Go migration catalog 和共享系统版本同时更新。
