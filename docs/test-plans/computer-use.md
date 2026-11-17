# 隔离 Computer 与桌面浏览器操作：测试方案

[功能与验收目标](../features/computer-use.md) · [技术方案](../designs/computer-use.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 自动桌面准备验收

`internal/computer/macosvm/managed_test.go` 检查只读发现不下载、不支持的平台不写文件、拒绝路径穿越/链接压缩包、固定镜像磁盘损坏及不完整 manifest。`internal/agent/sqlite/default_computer_test.go` 通过真实 ToolInvocation/SQLite 验证准备时进入 resource 等待且未创建 Computer，准备完成后恢复同一个调用且只执行一次。

Apple silicon 原生验收必须覆盖全新目录首次下载、Guest 私有密码与主机密钥重用、seed 正常关闭与 base 发布、下载中断、密码轮换中断、进程崩溃后恢复、磁盘空间不足、已准备环境重启及 Settings 自定义/关闭。当前 Linux 受控验证不能替代这些原生证据。


## 范围与证据

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-027](../features/computer-use.md#f-027) | 验证指定平台上的生命周期、观察后动作、过期引用拒绝、传输、审批和中断恢复。 | 保护已接通的限定范围；Live/配置另行验证 |

## 层级与关键路径

Host 故障注入与真实 Guest 兼容性分开；覆盖实例重建、过期 Surface/元素引用、桌面互斥、动作后观察、传输和审批。Linux 上跳过真实平台测试应标为未执行。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 已有检查与缺失覆盖

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_computer.py](../../tests/system/verify_computer.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [tests/system/verify_computer_use.py](../../tests/system/verify_computer_use.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |

现有入口仍需在后续相关实现变更时按风险运行；需求范围变化时同步扩展场景。

## 详细测试入口与证明边界

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="computer-use-output-and-verification"></a>

### Output and verification

Nested observations, actions, capability calls, files and errors stay in the
Tool/Code ledger. Only `text()`, `image()` and `return` select model context.
`image(screenshot.image)` selects its scoped Artifact. Image-capable snapshots
receive bounded JPEGs resolved in the Host only at Provider dispatch; the
persisted request keeps references. Text-only snapshots receive reference hints. Existing user-input media projections
keep their text-only reference shape; only explicit Code Mode image outputs gain
the dispatch-time image field, preserving previously accepted requests without that field.
No screenshot Base64 crosses QuickJS or enters SQLite, RunTrace or errors.
To activate image input normally, declare `capabilities.image_input:true`, run
`model test`, then `model select`. The Host verifies a synthetic image before
admitting that capability. Undeclared or unverified image input cannot enter a
new snapshot. Text-only profiles keep the reference fallback.

Offline Launch/Doctor/Drive/Evidence/Cleanup:

```sh
npm ci --prefix guest-browser --ignore-scripts
node guest-browser/node_modules/playwright-core/cli.js install chromium
make test
python3 tests/system/verify_computer_use.py --binary bin/steward --evidence evidence/computer-use.json
npm test --prefix guest-browser
python3 tests/system/verify_local_provider.py --binary bin/steward --evidence evidence/local-provider.json
```

The system runner selects the Computer Use CLI contract; repeat count is explicit. `make test` includes
real Chromium against a local synthetic website and the actual openat transfer
helper's traversal/symlink/hardlink/FIFO regressions. No online account or website
is used by tests. Browser binaries are installed separately before offline tests;
on Ubuntu use `install --with-deps chromium`.

For the previously authorized isolated Lume test root, run:

```sh
python3 tests/system/verify_computer_use.py --binary bin/steward --live-config TEST_ROOT/product-live.json --live-root TEST_ROOT --evidence evidence/computer-use-live.json
```

This command owns the foreground Lume server, compiles the project-local AppKit
fixture, installs it only into a disposable Guest clone, and drives it through
the production CLI/Code Mode/ToolExecutor/SSH/Cua adapter. It records screenshot,
AX and nested invocation evidence and checks checkpoint/release. Guest/browser
compatibility is distinct from offline conformance and Golden Case scoring.

To add the pinned browser runtime to that disposable Guest checkpoint and then
exercise the full Guest browser path, use its retained `steward25-cua-use-*` root:

```sh
python3 tests/system/verify_computer_use.py --binary bin/steward --live-config TEST_ROOT/product-live.json --live-root TEST_ROOT --resume-root TEST_ROOT/steward25-cua-use-INSTANCE --native-actions-only --provision-browser --evidence evidence/computer-use-native-live.json
python3 tests/system/verify_computer_use.py --binary bin/steward --live-config TEST_ROOT/product-live.json --live-root TEST_ROOT --resume-root TEST_ROOT/steward25-cua-use-INSTANCE --live-browser --evidence evidence/computer-use-browser-live.json
```

Provisioning downloads checksum-verified Node and locked Playwright/Chromium into
an issue-owned Guest directory. Guest LaunchAgents start the synthetic local site
and isolated headless browser at the next login. Doctor explicitly bootstraps
those owned fixture services if the Guest session has not loaded them, then
checks CDP and the local site before product discovery. The browser drive reads back
locator fill/click state, exact downloaded Artifact bytes, chooser upload state,
and navigation capability expiry. Each retained-root drive uses a fresh ingress
key so a repeated command cannot pass by reusing an old completed Run. Evidence
includes nested frozen approvals, AX results, screenshot hashes and checkpointed
cleanup. Screenshot PNGs stay in the owned test root's `evidence/` directory.

To collect every recorded screenshot from a completed live report without
starting a VM or sending input:

```sh
python3 tests/system/verify_computer_use.py --binary bin/steward --live-root TEST_ROOT --resume-root TEST_ROOT/steward25-cua-use-INSTANCE --collect-observations evidence/computer-use-native-live.json --evidence evidence/computer-use-native-live.json
```

Collection reads each opaque ref through the Artifact CLI and records the PNG
filename and SHA-256 beside the nested AX/action evidence.

<a id="computer-verification"></a>

### Verification

```sh
go test ./internal/computer/... ./internal/agent/sqlite -run 'TestComputer|TestLifecycle|TestCheckpoint|TestSSH|TestUnknown|TestCorrupt|TestNoCheckpoint|TestStopped|TestUnit'
python3 tests/system/verify_computer.py --binary bin/steward --evidence evidence/computer.json
```

The maintained offline harness launches the real CLI and production HTTP/SSH
adapter against explicit protocol Fakes. It drives Code Mode, records nested
ToolInvocations, kills the Agent at readiness and before/after checkpoint
publication and after instance disposal but before the final metadata commit,
then proves a fresh cell succeeds and reads back all three files and Artifact bytes.
It also cancels a Worker Task through Main at readiness and after disposal. Both
cases require a successful fresh cell and unchanged cancelled Task/Run/Code
terminal states. After disposal it verifies the committed file hashes and that
the new ensure uses the original checkpoint. All owned processes are joined.
The system runner selects it; repeat count is explicit. Contracts cover empty
and populated v20 migration, illegal state changes, CAS/fencing, concurrent
ownership, corrupt NVRAM, blocked shutdown, ambiguous start, symlinks and actual
test-process death at publication boundaries.

For the authorized dedicated L2 root, first seal the clean base and run the
foreground `lume_live.py --profile product serve` with its isolated product
configuration. Then use:

```sh
python3 tests/system/verify_computer.py --binary bin/steward --live-root TEST_ROOT --live-config TEST_ROOT/product-live.json --evidence TEST_ROOT/evidence/computer-provider-live.json
```

This mode uses the real Lume/macOS/Driver path and a deterministic model fixture.
It creates only a synthetic Guest text file and TextEdit document intent, proves
cold and SIGKILL recovery (including deletion before the final metadata commit),
proves resumed and fresh cells succeed, independently hashes the full unit, checks the
stable Artifact, and retains the private state/receipts for inspection. The
foreground API is stopped and joined separately after the instance cleanup.
Actual Jianying compatibility remains a separately recorded [L2 matrix](product.md).

The latest [native lifecycle report](product.md) includes
five scenarios. In the post-disposal SIGKILL case, the instance is absent and
the committed checkpoint ID remains unchanged when phase advances from
`checkpointing` to `checkpointed` under the replacement claim. The following
fresh cell restores the same logical Computer at the next instance epoch and
reads the saved Guest document. The reviewed fixture was archived intact; the
repair run used fresh isolated CLI state because its predecessor referenced an
expired worktree's JavaScript installation.

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。


Web 只读预览：macosvm 的 SSH/Lume peer 验证读取已存在桌面、缺失实例不被启动；Web 模块验证认证与绑定撤销、只读端点及 no-store。Inspector 的真实只读 SQLite 验证 Agent 归属、20条上限和参数不外露。对实际 Guest 的屏幕权限、桌面窗口、断连及持续刷新另行 Live 验证，peer 不冒充真实 macOS 图像质量。

`managed_version_test.go` 覆盖官方纯版本号、空白、错误版本、额外输出及命令失败；验证版本检查不下载或准备 Guest。错误状态回归覆盖取消、超时、兼容性、完整性和权限分类，不回显原始错误。Apple silicon 上另验旧状态只读投影，不改写或发起准备。真实签名运行时的下载 SHA、签名和 CLI 检查不能替代完整 Guest/VM 端到端验收。


## 私有观察方法与迁移门禁

`TestComputerPlugin*` 经独立 Go 包、封存／独立验证、真实 SQLite、原 ToolExecutor 和 Code claim 验证业务分派；保留原 native 控制回归，受管模式复用原具体审批、同一实例恢复和 unknown 不重发断言。不同版本验证旧 Run 保留 diff、新 Run 改用 full、回退与退役后的原执行、停用不能恢复 native 或启动 Computer。

候选通过机械验收后输出不完整窗口，Host 仍须拒绝正文与截图引用；只接受能完整重建新快照的窗口。大规模 JSON 转义文本应经过原分块通道，并保留原输出 RESOURCE_LIMIT，而不是变成协议失败或越过输出限额。Forget 分别在真实暂停方法的 end guard 和取消 transport，以及与 stop／quarantine 一起在观察 Artifact／回执提交前发生；均须拒绝新证据和可操作引用。

同一测试进程只复用不可变 Host 可执行文件，候选仍逐例编译／封存／安装／验证，每例的数据、图、进程和状态保持隔离；不能缓存验收决策或模拟产品接纳。原 AX 原文、revision、截图独立性、元素 stale、DesktopLease、审批与 unknown 等回归继续维护。默认组合、真实 CLI／QuickJS／SSH 受控 peer、完整 test/lint 与 race 为发行验收要求，当前实验接入不表示这些门禁或整个 Computer 迁移已经完成；执行结果只记入 evidence。


等待配方验证独立 200 ms 轮询确实驱动原观察循环，保留 sinceRevision、变化后原始差分与 WAIT_TIMEOUT 摘要。候选扩大其 schema 后的超上限默认仍在 Ensure／观察前拒绝；合法 10 秒候选默认遇到用户 5 ms 时限，Driver 收到的实际 deadline 仍不超过 5 ms。原父 Tool 的总期限与本次 wait deadline 分开测量，不把父期限误当候选扩权。`verify_computer_use.py --managed-plugins` 观察正常启动自动验证／选择的完整十九包，不单独安装或激活 Computer-only 图，在每个真实 Node／SSH 受控 peer 场景核对十一项服务的冻结 graph，保留原审批、截图、传输和 unknown 断言；该入口由 required system lane 通过完整默认分发执行。


Computer 系统计划必须由真实 Task Worker 执行，Main 仅委派与取消，不恢复 Main 的 Computer 权限。共用 ComputerHarness 在首次设置时声明能容纳 Worker 工具、GUI／审批回执的 64K 合成上下文及图像输入能力；恢复已有 fixture 时不重新选模型。图像隔离比较原同一个 Artifact 的内部限定引用与公开普通 ID，仍要求只有显式 image() 选中的那一份进入模型请求，并证明其余截图只留在 Artifact。生命周期的 SIGKILL／取消／checkpoint 原断言继续运行，fixture 修正不改变权限、隐私、预算或结果断言。


说明资源：`TestComputerPluginDocumentation*` 验证实际字符串回执、AX 状态不变、独立修订、旧 Run／旧回执保持、回退／退役／停用及明确 native 边界。候选扩大其 schema 后，Host 的 16 KiB 限额仍拒绝超长说明。Forget、Stop、Quarantine 在真实 Tool 提交前发生时不保存新回执；元数据与原 lease 在同一事务校验，不模拟产品接纳。真实 CLI 的 Surface／browser documentation() 均须经冻结图中该服务，保留其余 GUI／审批／截图原断言。

`TestPluginSnapshotWithoutCatalogKeepsPersistedManagedBoundary` 证明载体不可用不会改变已激活服务的新执行边界，也不会获取 Computer。`TestV62NativePluginBindingMigrationPreservesOriginalRequestsAndReceipts` 重建真实 schema62 的无载体缺行历史，升级后仅补空图／空服务记录，保持原 policy、模型请求、checkpoint/version、Code manifest 与 Tool 回执，已有 managed 绑定保持不变，immutable 触发器继续保护两类边界。两项均进入 `make test-prompts`；schema catalog 与系统 fixture 版本必须同步。

选区接入以 `TestComputerPluginSelection*` 覆盖实际 Go 隔离进程、原 ToolExecutor／Code claim、审批、独立首／末锚点换版、旧 Run、回退／退役／停用、明确 native 边界、错误范围、迟到 Forget／Guardian 及包裹底层故障的 unknown 持久状态、私有 cause 脱敏与不重发；`TestComputerSelectionProofAcceptsEquivalentCaretPrograms` 核对双向等价方案、重叠匹配 first occurrence、长文本原支持范围与错误／越界方案，`TestComputerNativeSelection*` 在固定原语边界检验选区语义、原窗口／lease、逐输入撤权检查与不确定后停止。`make test-prompts` 已纳入 ComputerPlugin 系列，名称含 Forget 的新回归由 required memory race 发现。

`verify_computer_use.py --managed-plugins` 继续原全部断言，新增真实 CLI／Node／SSH／固定原生适配器到受控文档光标的选区 readback，并核对两次原 selectText 审批和旧 revision 拒绝；native 对照执行同一业务。该协议 peer 不是真实 GUI/Lume 证明，live 原生元素／选区场景继续保留。


清单／绑定：`TestComputerPluginTargets*` 使用真实独立 Go 包与原 ToolExecutor，验证独立排序／大小写匹配换版、旧 Run、回退／退役／停用、旧四服务包与明确 native 边界；已绑定句柄在后来同名对象替换后仍作用于原对象。大于 8 MiB 的原清单必须找到最后批次，跨批歧义保持 NOT_FOUND；保留长 selector、windowId 优先级与 browser scope。候选机械验收通过后返回重复排列、错配精确 ID 或越界位置，仍在 Host 拒绝且不产生新句柄。

`TestComputerInventoryRejectsIncompleteOrInventedPermutation` 与 `TestComputerTargetIdentityPinRejectsRedirectionAndDuplicateIdentity` 检验遗漏／重复／越界／负位置和重复精确身份。`TestComputerMatchTextPreservesCompleteUnicodeMatches` 检验 UTF-8 与显式 incomplete 前缀。`TestComputerPluginTargetsLateForgetGuardianAndEpochRejectCommit` 在真实回执提交前执行 Forget／Stop／Quarantine／Idle，要求无新成功回执；Forget 命名使其进入 required memory race。`TestComputerPluginTargetsCreateLateGuardianNeverReplaysEffect` 在真实 create 审批与已发生效果后撤权，恢复 Guardian 并通过原 Run／Code 接纳 API 回收执行权后立即重试原调用，必须保留 durable unknown、attempt=1 且不再次创建。

`verify_computer_use.py --managed-plugins` 除原 GUI／选区等全部断言，还通过实际 CLI／QuickJS／SSH peer 核对按名称和 windowId 绑定、原 ref、browser/tab 发现与缺失目标，无额外动作审批；每个 Computer Run 须冻结全部十一服务。native 对照执行同样业务，受控 peer 不代替真实 GUI／Lume 兼容门禁。


动作参数：`TestComputerPluginParameters*` 通过原 ToolExecutor／真实隔离 Go 方法验证默认距离独立升级、显式距离优先、旧 Run、回退／退役／停用、实际旧六服务包和 native 边界。非确定性候选在另一次真实计算中产生不同数值，原审批后 Driver 必须仍收到首次固化值；更换显式文字的候选在审批／Computer 效果前被拒绝。原 request hash 与完整计划 hash、纯审批投影、无 pending RSI Call 和不可变／不可复活约束均需核对。

迟到 Forget／Stop／Quarantine 与写入中断发生在完整计划提交前时，不得新建审批或执行；去掉注入存储故障后立即重试原调用，应能准备完整计划并按原审批等待，不需要额外选择／SQL 修复。Forget 已固化计划后正文必须清空、hash 与身份保留，审批参数投影不再披露正文。`TestV64ComputerActionPlanMigrationPreservesRunRequestsAndApprovals` 从真实 schema64 升级，保留原 checkpoint／版本、请求、审批 hash／状态和 native 迁移边界，不造参数行，并进入 `make test-prompts`；共享系统 fixture 版本必须同步。

`TestComputerActionParametersPreserveIntentAndClosedFields` 检查目标／URL／显式值／额外字段与键串无损分解；`TestComputerNativeKey*` 检查原 scope、单一固定原语、逐发送执行权与 unknown 停止。真实 CLI 场景新增原生 Escape 与小数距离滚动，并在批准前逐项核对 domain plan、graph、request hash 和控制投影；原全部选区、审批、refs、恢复及 unknown 断言继续保留。

审批 Web 回归 `TestApprovalParametersBrowser` 纳入 `make test-browser`，以非空 50.5 参数检查明暗／桌面／手机、真实计算文字颜色与按钮尺寸、键盘焦点和无脚本表单／原 hash。使用真实宿主与只读控制投影 fixture；业务归属与 SQL 固化由 owning tests／CLI 证明。截图仍需视觉复核，不能以生成截图或空元素查询代替检查。

托管 Computer 集成夹具在真实编译／验收期间通过原 `Runs.Renew` 维持正在运行的脚本 owner，模拟生产 Loop 的续租。续租使用原 claim／fence，任何续租失败即停止，不重新 Claim、不复活过期或撤权执行；显式过期／恢复回归继续使用不带续租的原夹具。不能通过延长产品租约或事后修补状态掩盖 CLAIM_LOST。


Playwright 参数：`TestComputerPluginPlaywright*` 经实际隔离 Go 方法验证独立缺省超时升级、显式值、旧 Run／七服务包／native、回退、退役和停用。恶意选择器或显式超时替换必须在审批／Computer 效果前拒绝；不同进程给出不同缺省时，Driver 仍收到原批准值。完整计划接纳前的 Forget／Stop／Quarantine／写故障不得泄露正文、产生审批或输入；移除注入故障后直接重试同调用。实际 release／wake／claim 后立刻用原 capability 执行原批准 fill，证明 hash、实例身份与原参数保留，不允许额外重选或 SQL 修复。unknown 只派发一次，原 attempt=1，私有原因不进入展示。新增 Forget 用例须纳入原 memory race 自动清单。

`TestComputerPlaywrightParametersPreserveOriginalIntentAndExcludeAuthority` 检查候选描述符不含路径／文件／token／导航／tab 身份，未使用字段不能变成新效果。系统 `verify_computer_use.py --managed-plugins` 同时检查真实 CLI／QuickJS／SSH 的 fill、count、两个显式等待预算、上传／下载的审批前完整计划与十一服务冻结；native 对照保留同一业务。`TestApprovalParametersBrowser` 使用四个真实形状的审批样本：非空 50.5 滚动距离、4000ms Playwright 超时、export.bin／application/octet-stream 导出元数据及上传 selector／预算／名称／MIME，检查这些字段及明暗／桌面／手机、原 hash、无脚本回退。真实 Chromium 的文档／导航／节点替换与上传约束沿用 `guest-browser` 门禁；受控 SSH peer 不证明真实 Guest GUI／Lume 兼容。


导出展示：`TestComputerPluginTransfer*` 经真实 ToolExecutor／隔离 Go 包验证默认名独立变更、显式名称／媒体类型优先、旧 Run、回退、退役、停用、实际旧八服务和 native。两个实际进程给出不同默认名时，执行必须用首个批准名；检查 Artifact descriptor 名称与读取字节，而非只检查插件 JSON。原导出 attempt=1、unknown 不重放、未创建 opaque pending Call。另一个 Run 即使知道物理路径并获导出审批，也不能读取未授权文件。

真实 release／wake／claim 后立刻执行原导出，不经额外 select／reload／SQL 修复；核对原实例、完整计划 hash、原路径与展示值。迟到 Forget／Stop／Quarantine／写故障发生在接纳前时不能创建审批或 IO；移除注入故障后立即重试同调用。Forget 已固化源名称后只擦除正文，保留 hash，不可复活，审批参数视图也不得披露已擦除值；两项新 Forget 测试须进入原 memory race 自动清单。`TestComputerTransferPresentationOriginalMetadataAndNoResourceRelease` 检查输入描述符不含路径／资源 token，并证明显式元数据和关闭字段。

CLI／QuickJS／SSH 原生与受管系统流增加默认／显式导出元数据场景，受管导出必须在原审批前已有完整计划，保留当前十一服务组合；读取两份 Artifact 核对原字节。独立 owner goldens 允许一个非法默认名以证明 Host 的 Artifact 约束不由候选／机械认可放宽。Artifact 原边界、坏名称、内容类型、并发／恢复与撤权回归继续执行；共享校验器不改变既有 staging 语义。浏览器资源版本迁移另行验证，当前受控 peer 不能替代真实 Guest／Lume 兼容。

浏览器上传：`TestComputerPluginUpload*` 通过原 ToolExecutor／真实隔离 Go 版本验证名称／MIME 独立换版、显式选择器／预算保护、旧九服务与 native、回退／退役／停用。非确定性进程给出不同名称时，原审批后仍只能用首次计划；缺具体展示参数也不能恢复 native。验证原字节和原授权路径、attempt=1、unknown 不重发及无额外 opaque Call。未知资源摘要／缺配置须在审批和 Ensure 前拒绝，不信任候选的机械验收代替 Host 资源检查。

真实 release／wake／claim 后第一步立即执行原 capability 上传，活动图已选用另一个资源摘要时，仍须保留原图资源、具体计划 hash、实例和原字节；不能额外 select／reload 或 SQL 修复。跨 Run 知道路径并获元数据审批也不能获得文件。迟到 Forget／Stop／Quarantine／写故障不得接纳计划、批准或 IO；移除注入写故障后直接重试原调用。Forget 单向擦除解析出的名称／MIME、保留 hash，审批视图不可披露；新增 Forget 父用例须由原 memory race 自动发现。

`TestComputerBrowserUploadPreservesFilenameDomainAndClosedParameters` 与真实 `guest-browser/browser-upload.test.mjs` 保护超过 255 UTF-8 字节的中文名称、原字节和媒体类型；新原语与旧程序分别证明真实 FileChooser 结果、helper 漂移在点击前拒绝及原文档等待不跨导航。SSH 协议 peer 独立固定新原语及 helper 摘要，拒绝旧程序冒充新资源、丢失已批准名称／MIME。CLI 的上传展示场景验证两次独立同意、审批前具体参数与字节摘要；native 与受管均保持其余既有场景。四个原生审批卡片通过原 hash、明暗／桌面／手机、非空 selector／预算／名称／MIME、原生无脚本表单及共享控件尺寸验证；不修改样式来适配夹具。


### 浏览器观察投影与独立快照资源

`TestComputerPluginBrowserProjection*` 使用原 ToolExecutor 和独立编译版本，验证标签来源、正文／值裁剪与行顺序确实改变，原索引／token 不变；原 Run／回退／退役保留各自视图，停用不回退。真实十服务1.0.6及 native 对照保留原路径，当前独立政策候选保留其他已接纳服务。即使机械验收通过，重复行、无效来源版本或缺失证据也不得发布 AX 引用。未知资源在 Ensure／IO 前拒绝，不能把原配置数据作为程序注册权限。

真实 release／wake／claim 后第一步直接用原 Tab token 观察，活动图已换未知摘要也须保留原资源、来源、引用和实例；不做额外 select／reload／SQL 修复。原待审批动作仍保留精确参数及一次执行。迟到 Forget／Stop／Quarantine 与提交写故障不得提交截图 Artifact、成功回执或可操作引用；新增 Forget 父用例由普通 memory race 发现。

`TestComputerBrowserProjection*` 覆盖源字段／排列／值存在性证明及 JavaScript UTF-16 拼写，包括孤立代理项、被截断的 emoji、控制字符、引号、HTML 字符、重排后的原索引。`tests/fixtures/computer-browser-projection.json` 来自固定 Playwright／真实 Chromium 与旧 facade-v1 原始输出，记录版本／程序摘要；Host 重建必须逐字匹配。`guest-browser/browser-projection.test.mjs` 同时检查当前固定程序及实际来源与记录一致、原生引用替换、无源码的截图读取、密码／hidden 的 role=button 绕过拒绝和缺失 Tab。它属于 `guest-browser` 必跑测试／语法检查；受控 SSH peer 独立固定实际发送的程序摘要，不能以旧程序冒充新资源。完整 CLI 冻结十一服务；这些测试不声称覆盖真实 Guest GUI／Lume 或真实 Guest GUI／Lume。


`TestComputerPluginChildUsesRetainedOriginalBrowserPackage` 经过真实 Task Root → Child → Node Code Mode → Computer：Root 已冻结原图后，活动组合切换新展示政策并退役原包，随后新接纳的 Child 仍继承原图及全部十一服务，读取原快照与原标签，保留原索引；Parent 私有对话和任务指令不进入 Child 请求。验证实际成功嵌套 Tool 回执与无额外 opaque Call；仅检查继承字段或运行时握手不能替代真实业务调用。普通 Go 与现有 SQLite race 清单必须包含该用例。


## 默认组合与完整业务门禁

- `TestComputerBootstrapVerifiesActualPackageAndPreservesOwnerSelection` 编译实际隔离 Go 包，走 Host 独立六十样例验收与原子激活，检查十一服务、确切资源配置、真实默认投影结果，并证明重启／停用／旧选择／Guardian stop 不被覆盖。
- `TestShippedPluginDistributionRejectsPartialNativeFallback` 对缺失 Computer 等包拒绝部分分发；实际 startup 门禁核对全部十九包。
- `computer-plugins` 为 required system CI lane，先 `make build`，再通过 `runner.py --gate computer-plugins` 执行原十五场景。测试只读取正常默认选择，不安装／宽松验证／激活另一个 Computer 图；核对完整十九包、资源摘要及业务／恢复前后选择不变。CI 可达性由 `test_default_computer_business_gate_is_a_required_system_lane` 保护。
- `verify_computer_use.py` 不带 `--managed-plugins` 时复制完全相同 host 字节到明确无包载体做 native 对照，避免完整分发下误把插件行为当 native；十五场景仍检查原审批／文件／恢复／unknown。
- `guest-browser/browser-selection.test.mjs` 经固定原浏览器程序与真实 Chromium 验证九种 live selection：UTF-16／孤立代理项、观察前缀之外及超过 8 MiB 的完整原值、原非重叠唯一性与缺失／重复后的未知回执，拒绝时没有物理选区或焦点输入。该测试属于必跑 guest-browser npm test／lint，既有节点／导航与固定资源指纹回归继续保留。

固定生命周期继续运行原 `TestComputer*`／macosvm 恢复、checkpoint、资源配置及 unknown 不重放回归；审批、隐私与 memory race 门禁继续覆盖新领域计划。完整默认组合不改变测试证明范围：受控 SSH／Lume peer 与 Host Chromium 不能证明真实 Guest GUI、应用可操作性或 Linux 平台兼容。
