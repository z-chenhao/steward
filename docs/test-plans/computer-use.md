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
