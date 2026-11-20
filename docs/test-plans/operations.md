# 诊断、备份恢复与费用：测试方案

[功能与验收目标](../features/operations.md) · [技术方案](../designs/operations.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 范围与证据

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-028](../features/operations.md#f-028) | 验证备份认证、坏包拒绝、隔离恢复、兼容升级及激活 fencing。 | 保护已接通的限定范围；Live/配置另行验证 |
| [F-028](../features/operations.md#f-028) | macOS 后台安装保持 inactive；start/stop/卸载、关闭浏览器、进程死亡、未配置/凭据/依赖退化、重启限速、修复升级及人工停止竞争。OS 锁保持单实例，独立巡检只恢复进程，不重放副作用，固定提示去重。 | 服务包使用真实文件/OS 锁与 launchctl 注册协议 peer；CLI 运行真实 Web/runtime；隔离 macOS 账户的 launchd/登录/私有文件权限 Live 验收仍待补，主动通知以受控飞书对端验收 |
| [F-029](../features/operations.md#f-029) | 验证价格冻结、并发预留、usage 缺失和授权等待/拒绝的执行边界。 | 保护已接通的限定范围；Live/配置另行验证 |

## 层级与关键路径

覆盖备份损坏、错误密钥、路径越界、恢复隔离与显式激活；费用覆盖价格冻结、整数边界、并发预留及 usage 缺失。仅使用 disposable root，不读取个人数据。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 已有检查与缺失覆盖

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_operations.py](../../tests/system/verify_operations.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [tests/system/verify_spend.py](../../tests/system/verify_spend.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [原生历史报价回归](../../internal/llm/pricing/legacyv1/catalog_test.go) | 供应商地址／chat 模型精确匹配、同名 classifier 排除、缓存与长上下文分档、未知模型／代理、订阅参考价、十进制整数转换 |
| [internal/agent/sqlite/cost_estimates_test.go](../../internal/agent/sqlite/cost_estimates_test.go) | 接纳事务中的固定估算、只读 Trace 投影、重放不重计、不可改写、缺失 usage、手动覆盖与货币审批隔离 |
| [internal/operations/service/service_test.go](../../internal/operations/service/service_test.go) | 安装不启用、XML/argv、凭据不复制、持久停止及信号失败、双实例拒绝、独立巡检、健康过期、有限崩溃恢复、提示去重、配置修复/升级与元数据 fail-closed；外部 launchctl 用注册协议 peer，不更改宿主登录会话 |
| [cmd/steward/service_test.go](../../cmd/steward/service_test.go) | 真 CLI service install/run/status/inspect 与 loopback Web/setup_required；浏览器断开不关闭服务、持久停机被运行时观察到、排队启动不恢复停止意图；macOS 才执行实际后台 CLI，其他平台验证明确拒绝 |

现有入口仍需在后续相关实现变更时按风险运行；需求范围变化时同步扩展场景。

后台变更运行 `go test -race ./internal/operations/service ./cmd/steward`，再运行跨模块门禁。CLI 测试以隔离 HOME/data root 安装不活动 jobs，并以 enabled 元数据作为启动进程边界 fixture；完整 start/launchctl 注册流程由服务包 peer 验证，不能将此称为真实 launchd 安装启用验收。真实平台补充场景：隔离 macOS 用户中从已安装二进制启用，关闭浏览器、SIGKILL 主进程、注销/重新登录、凭据文件缺失/权限错误、升级重装、人工 stop 与检查并发；核对已有 Task/冻结审批/unknown 状态未被重置。现有 runtime 恢复门禁证明持久工作边界，不代替该平台验证。飞书通知回归使用隔离 data root、真实 OS 锁与 loopback HTTP 对端。核对明确选择的单一 chat、60 秒连续异常、主进程退出后跨巡检重开、已接纳响应丢失不重投、恢复回执和关闭通知；旧配方静默。本地验证不发送到真实账户。

## 详细测试入口与证明边界

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="operations-forward-upgrade-rehearsal-and-evidence"></a>

### Forward upgrade rehearsal and evidence

Restore a backup into an isolated root first. All three schema versions are
checked before any forward migration; a future/incompatible database rejects
startup without partially upgrading another database. State migration 026 adds
only Outbox quarantine fields/index. Empty and every supported prior schema
follow the same forward migration path. Failed DDL rolls back version and schema
together. Never lower `user_version` or downgrade in place. Retain the old binary
and original cold backup until the rehearsal completes.

When a state migration changes the current schema, update the independent
`STATE_SCHEMA_VERSION` fixture in `tests/system/verify.py` and the populated
upgrade fixtures together. Foundation, local-provider and Code Mode system
checks retain exact version assertions; do not derive the expected version from
the database under test or remove the assertions to make an upgrade pass.

```sh
make test
make test-race
make lint
make test-full
python3 tests/system/verify_operations.py --binary bin/steward --evidence evidence/operations.json
python3 tests/system/verify_capacity.py --binary bin/steward --evidence evidence/capacity.json
```

Real interactive acceptance shares `tests/system/event_broker.py`: an owned, pinned
JetStream test peer, built from the Go integration tests, with a versioned JSON
handshake and EOF shutdown. No broker server enters the product binary. TUI and
Web OAuth fixtures exercise production broker startup and immediate first input.

The system runner owns operations and capacity checks, including default-limit Child batches in a reused state root. Its existing event gate adds a real CLI/NATS/Sidecar lifecycle,
authentication, poison isolation/restart, stream upgrade, broker outage and
shutdown evidence. Existing suites retain process/Sidecar kills, late/duplicate/
out-of-order events, approval/side-effect crash windows, cancel fences and
query-only unknown handling. No separate execution or replay pipeline is added.
`TestOperationsPriorSchemaRollbackAndActiveWorkUpgrade` reconstructs real v25,
retains accepted work/snapshots, rejects partial migration, upgrades and drains
the work exactly once. The v25 operations and v29 inspection fixtures temporarily
seed the current Main constructor's GitHub columns, including source methods,
before admitting retained work. Shared cleanup then removes those dependencies;
both assert the original schema version and absence of later GitHub/RSI tables
before forward migration. Old work is not retrofitted with current metadata.
Operations harness cleanup joins processes and removes its isolated roots,
ciphertext archive and restored state.

The capacity sampler runs the unchanged 100-item batch/fairness/cancel/restart
harness three times by default. Compare the same machine, fixture, binary
boundary and harness; retain individual samples, medians, ranges and binary
hashes. The 1000-item test is a separate capacity observation, not a semantic
Golden Case score. Model/Tool calls keep their existing per-call watchdogs (45 s
for a model step; frozen Tool deadlines). The provider breaker counts consecutive
infrastructure errors, cools down and resets on success; business item failures
do not trip it. Existing account monetary admission remains independent of Tool
approval and does not become a Token budget. Default scheduler and safety limits
remain unchanged until measurements justify a change. Release-time live accounts,
macOS compatibility and semantic Golden Case scoring remain separately reported.

Recorded local samples are in [operations-capacity.json](product.md).
On one Apple M4 Max (16 logical CPUs, 64 GiB), the unchanged 100-item harness had
these observations; all six samples passed with the original limits.

| Observation | Before | After |
| --- | --- | --- |
| Three samples (seconds) | 9.327, 8.726, 8.893 | 9.041, 8.971, 9.012 |
| Median (seconds) | 8.893 | 9.012 |
| Max–min (seconds) | 0.601 | 0.070 |

The median changed by about +1.3%, within the observed before range; three
samples do not establish a general performance difference. Defaults stay at
16 Children/tree, 32 global Workers and the original safety ceilings. A separate
three-cycle test reused one state root for 3000 Children: 2994 completed, six
injected business failures were aggregated, and every cycle released all Worker
leases. These are bounded deterministic capacity observations, not a production
duration or model-quality certification.

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

`TestOfflineRestoreUsesSourceFileKeyAndNewDestination` 在真实 SQLite/维护锁/加密归档上，以源数据根中的文件密钥备份并恢复到新目录；核对目标数据文件、凭据排除及源密钥不变。

### 参考报价插件迁移

- 候选前执行的原生对照保存在 `tests/fixtures/plugins/reference-pricing-native-v1/`，记录原源码 hash、账户／端点、未知模型、负 usage 及长上下文临界点。默认四项 Owner 预期由该原始输出生成，不调用候选生成期望。
- `TestOriginalExecutedQuotesKeepExactTierAndSource` 保护原汇率、来源与分档；`TestReferencePricingActualRatesAndDeactivationNeverRestoreNative` 使用真实插件与 usage 接纳，停用后不借历史估价器恢复报价，普通工作仍完成，费用授权不变。
- `TestReferencePricingIndependentUpgradeRollbackRetainedRunAndCold` 通过真实独立二进制改变 input 费率，并核对旧 Run、新 Run、精确回退与退役版本首次冷执行的实际金额和原图。
- `TestReferencePricingPendingColdUsesOriginalRatesWithoutProducer` 在真实派发前已接纳费率后关闭 Store；新建 Store／Catalog、停用且移除运行器后的首次 Drain 仍使用原费率、只派发原请求一次。没有重新选择或补修恢复 fixture。
- `TestReferencePricingRatesAndGenerationAcceptAtomically` 在真实接纳处注入失败，要求 generation、费率回执与 provider dispatch 均不泄漏；旧 generation／显式价格／未知 usage 保留原回归。Schema 81 同步共享系统版本与迁移目录检查。
- 跨模块最终检查使用固定源码 `make test lint`，并运行真实 `spend` 系统门禁；独立费率改变只是观察价演进，不代表供应商账单或费用授权变更。
