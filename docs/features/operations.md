# 诊断、备份恢复与费用

检查运行状态、安全备份恢复，并观察和约束模型费用。

[需求主索引](../feature-map.md) · [技术方案](../designs/operations.md) · [测试方案](../test-plans/operations.md)

## 功能说明

操作员能检查健康、导出受控诊断、备份并隔离恢复数据。

操作员能查看用量，并按明确价格元数据控制账户费用。

本文描述用户行为和范围；逐项完成状态以主索引为准。当前能力依据代码静态盘点，配置可用性与真实服务/模型质量需分别验证。

## 需求与验收

<a id="f-028"></a>

### F-028 · 本地诊断与加密恢复

**需求**：操作员能检查健康、导出受控诊断、备份并隔离恢复数据；显式启用本地后台服务与独立巡检后，关闭交互界面仍可工作，故障恢复尊重人工停止。

**当前实现**：已有前台应用/worker、doctor/status/ops 健康诊断、日志轮转、加密备份、恢复验证和显式切换，以及持久执行恢复。macOS 新增显式安装、启停和卸载的用户级后台 Web 服务，以及独立的每分钟巡检；人工停止先持久保存，巡检只重启退出的进程，连续恢复有上限，未配置、凭据/供应商不可用和依赖失效分别可见。

**边界与剩余工作**：后台服务首个平台限 macOS 的当前图形登录会话，退出登录后停止，重新登录时仅恢复仍启用的意图；不支持 Linux/Windows、系统级开机运行或云端部署。巡检的去重提示保存在 `service status`，尚无主动渠道告警。真实 launchd 登录/重启、系统升级与系统集成需在隔离账户进行平台验证；协议替身和 CLI 回归不能证明这些系统集成已经验收。复用既有恢复器，不重试 unknown 副作用，不进行语义 Watchdog 自治修改。备份不包含凭据、Guest 镜像或服务安装状态等外部资源，不能同时运行同身份的两个恢复副本。

**验收目标**：备份认证、坏包拒绝、隔离恢复、兼容升级及激活 fencing 正确；关闭界面后后台任务继续，主服务退出时巡检仍独立工作并按故障类别处理；重启、升级、凭据失效和人工停机后行为正确，无双实例执行或通知风暴。

<a id="f-029"></a>

### F-029 · 用量记账与配置型费用授权

**需求**：操作员无需逐模型配置即可查看常见模型的费用估算，并按明确配置的价格元数据控制账户费用。

**当前实现**：provider_usage、费用快照、预留和授权已接入模型 admission。新托管调用在事务外按原 Run 图消费 reference-pricing 的费率表，按供应商地址 hash 与模型精确匹配内置目录，支持输入、输出、缓存和长上下文价格分档；方法与费率随 generation 接纳固定，手动配置价格优先。Traces 显示模型费用及“估算”标记，价格来源和版本随调用固定。

**边界与剩余工作**：无价格时不能声称零费用；不是供应商账单或按任务动态模型路由。

自动价格使用随 Steward 发布的目录，不在调用时联网拉取；升级目录只影响新调用，历史未计价记录不回填。未知地址、未知模型和缺失 usage 保持未知。ChatGPT 订阅调用使用参考价估算，不代表额外按 token 收费。费用审批和 `spend status` 继续使用显式配置的货币账本，不把参考估算当作实际已授权支出。

**验收目标**：验证价格冻结、并发预留、usage 缺失和授权等待/拒绝的执行边界。

## 如何使用

运行 `steward doctor` 检查安装；`steward status` 查看模型就绪；`steward ops health` 查看运行健康；`steward spend status` 查看费用状态。

### macOS 后台运行与独立巡检

在已安装且路径稳定的 Steward 上显式执行：

```sh
steward service install
steward service start
steward service status
# 关闭浏览器后任务仍在后台运行；停止不会取消 Task。
steward service stop
steward service uninstall
```

安装只写入两个用户 LaunchAgent，保持停止；`start` 才授权登录后持续运行。默认服务提供 `http://127.0.0.1:8787` 的既有 Web 工作台、托管本地 broker 和相同运行时；未选模型时保留设置界面，不自动选择模型。使用 `steward -config /absolute/config.yaml service install` 时，配置必须包含现有 `web` 与外部 broker 设置。自定义 data root 继续用 `STEWARD_DATA_ROOT`；安装不复制凭据或任意环境变量，仅保存配置路径、data root 和非秘密 PATH。使用本地私有文件中的凭据；shell 中临时设置的 `env:` 凭据不会自动进入 launchd。

`service status` 分别返回安装、启用和实际进程锁状态。`setup_required` 表示尚未配置模型；`credential_or_provider_unavailable` 与 `dependency_unavailable` 表示仍存活的运行时需要修复，巡检不会反复重启。心跳过期返回 `health_stale`，不会假装健康或杀死仍持锁的进程。退出进程最多每分钟尝试一次恢复，连续三次且未观察到 ready 后转为 `restart_limit`；修复后显式 `service start` 重置次数。`configuration_error`/`startup_failed` 也需要修复后手动启动。去重 `notice` 与 `notice_sequence` 是本地诊断提示，不代表渠道消息已投递。

人工 `stop` 先保存禁用意图，再停止主服务和巡检；信号失败、排队中的巡检及下一次登录都不能覆盖禁用意图。若配置文件损坏，可跳过配置解析直接执行 `steward service stop /absolute/data-root`，`start/status/uninstall` 同样支持明确 data root。相同 root 的后台实例由 OS 锁排他；不要同时启动该 root 的另一前台 Web 服务。

升级安装后先 `service stop`，再用新安装的可执行文件重新 `service install`，最后按需 `service start`；重新安装保持停止，不将版本切换当执行授权。卸载仅移除受管 LaunchAgents，保留数据库、产物和禁用意图。服务状态位于 data root 的 `.service/`，不是 Task 或审批状态，也不包含模型冻结快照。

备份恢复按现有指南提供 32 字节密钥的 SecretStore 引用，先恢复到隔离数据根。现有 `steward ops trace RUN_ID` 只提供简化轨迹；完整可视化由 F-031 跟踪。

## 详细操作参考

- [运维操作详情](#operations-production-recovery-and-operations)

## 操作说明与配置细节

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="operations-production-recovery-and-operations"></a>

## Production recovery and operations

The runtime state is authoritative. JetStream is an at-least-once wakeup bus.
Restarts reuse accepted model results, frozen Model/Capability Snapshots,
ToolInvocation identities and fixed-source Code Mode ledgers. There is no
exactly-once guarantee for external effects. `unknown` stays query-only or
requires an operator decision; a backup cannot reveal effects performed after
its recovery point.

<a id="operations-run-and-diagnose"></a>

### Run and diagnose

`steward -config CONFIG web serve`, `tui`, and `automation work` own the
foreground production lifecycle. Setup remains available without a usable
profile. The broker and durable consumer initialize before the Tool Registry,
ComputerProvider and Sidecar handshake. The existing Outbox/Inbox and recovery
scan start before execution and work ingress. Runtime publication uses an atomic
core reference, so a Setup UI never captures an obsolete Actor after activation.
Interactive model selection wakes startup immediately and waits for actual
runtime readiness before completing the Web response or printing the TUI input
prompt. The wait is bounded at 15 seconds and stops during shutdown.

The scan is the same `Runs.Recover` used by `Next`: due Automation admission,
Code execution claim/wake reconciliation, Approval expiry, query-only unknown
outcomes, controlled stops, immutable conversation/result intents and Main
follow-ups. Run scheduling reclaims expired leases through the existing CAS and
epoch fences. Artifact startup reconciliation and channel delivery recovery
retain their existing boundaries. See [replay](../designs/codemode.md),
[Computer](../designs/computer-use.md), [channels](channels.md) and [delivery](../designs/channels.md).

SIGTERM stops ingress and joins accepted executions at durable boundaries. It
stops Memory maintenance, drains internal wakeups with a two-second bound, closes
the consumer/broker, then closes the Provider and databases. It never cancels a
Task. Sidecar executions remain per invocation; the startup handshake process
receives `runtime.shutdown` and is joined before ingress. No idle VM is retained
by the handshake. A broker disconnect retains intents, closes new work ingress,
and reconnects without failing accepted work. Setup/progress endpoints remain
available. Explicit one-shot CLI commands (`chat work`, `delivery work`, etc.)
can inspect/recover the local authoritative state without a running broker.
Only `-test-bridge` selects a Fake; `-test-events` additionally drives the real
NATS lifecycle in isolated CI.

```sh
steward -config CONFIG ops health
steward -config CONFIG ops broker
steward -config CONFIG ops trace RUN_ID
steward -config CONFIG ops report > bug-report.json
```

`health` contains counts of live/expired claims, pending Code wakes, conversation
intents, due Automation/Approval, pending/unknown delivery and Tool outcomes,
active Root queue ages/service tickets, circuit breakers and configured scheduler
limits. Root detail is limited to 100 rows; traces return the latest 1000 points.
`broker` reads retained messages/bytes, consumer pending/redelivery and DLQ count.
These are operator commands; they are never model Tools, Web progress data,
notifications or model-visible budget discovery. Querying them never drives an
Agent or sends a message.

`report` is a bounded allowlist of counts, limits and opaque correlation IDs.
`trace` includes Run/Parent/Root IDs, sequence, event kind and Tool call ID; it
omits the event text. Never attach raw SQLite, logs, full RunTrace, prompts,
Tool arguments/results, endpoints, account/destination identifiers, config,
Artifacts or archives to a bug report. Reports remain local until an operator
explicitly shares them. Credentials are resolved only by SecretStore. Structured
logs contain fixed messages/codes and correlation metadata. App/trace files each
rotate at 16 MiB with two backups (96 MiB total by default); rotation is serialized
across CLI processes. Raw domain audit rows are retained, not silently pruned.

<a id="operations-broker-authentication-bounds-and-dlq"></a>

### Broker authentication, bounds and DLQ

Use a dedicated `stream_prefix` per data root. Literal loopback can use an
unauthenticated development broker. Every remote broker requires `tls://` with
normal certificate verification and `token_ref` resolved through SecretStore.
URLs reject embedded credentials, query strings and fragments. Never place the
token itself in YAML, command arguments, SQLite or reports. Missing/invalid
credentials fail closed with a constant error, without the token or provider
error. Rotate the referenced token in SecretStore and restart the service.

Defaults: event Stream `_events_v1`, File storage, DiscardNew, seven days,
1 GiB, 1,000,000 events, 1 MiB/event. Durable consumers permit 32 unacknowledged
messages and pull one at a time. AckWait defaults to 30 seconds (10 ms–5 min),
retry interval to one second (1 ms–1 min). Ingress/recovery does not depend on
broker deduplication or global event ordering.

Provision broker storage for both configured Stream reservations plus operating
headroom. Reconnect reads and strictly validates existing Stream configuration;
it does not request another reservation or silently alter retention or access.

After five unsuccessful consumer deliveries, the adapter durably writes a
content-free quarantine receipt before acknowledging the original message. A
crash between those writes can duplicate the receipt; its consumer/stream
sequence is its stable identity. The receipt contains only consumer, stream
sequence, attempt count, body SHA-256 and a fixed failure code. Raw bodies and
headers stay in the original protected stream. The independent `_dlq_v1` Stream
retains receipts for 30 days, at most 64 MiB/10,000 entries/4 KiB per entry.
Configure `poison_attempts` (2–100) for both consumption and non-transient Outbox
publication failures. Malformed stored envelopes quarantine immediately. Healthy
Outbox rows are served before previously attempted rows. Broker outages do not
increment poison failures or exhaust their threshold. No DLQ event notifies the
user. The local structured warning and `ops broker`/`ops health` are the developer
alert surface. A full broker/DLQ retains authoritative intent and requires
operator remediation; it does not authorize dropping records or replaying effects.

```sh
# Requeue a valid internal wakeup using its displayed attempt count as a fence.
steward -config CONFIG ops retry-event EVENT_ID ATTEMPTS
```

This operation cannot retry a Tool or channel side effect and refuses malformed
envelopes and stale decisions. Broker receipt inspection is a privileged broker
operation using the displayed stream sequence, not a user channel. Diagnose the
handler first; authoritative recovery scans continue independently.

Existing unbounded v1 Streams fail validation instead of silently acquiring new
retention policy. Stop Steward processes, back up state, and explicitly run:

```sh
steward -config CONFIG ops upgrade-broker
```

The command verifies the owned subject/storage/retention contract and refuses
bounds that would immediately discard retained messages by count/bytes/age.
It preserves messages, subjects and durable positions. Set a supported larger
retention window or settle/export old wakeups before retrying. Normal `broker`
and serving commands validate policy; they never silently alter existing bounds.

<a id="operations-cold-encrypted-backup-and-isolated-restore"></a>

### Cold encrypted backup and isolated restore

Stop **every** process using the data root, including temporary CLI commands and
external database writers. Every Steward process holds a shared OS lock. Backup
requires the exclusive lock and refuses a live instance; process death releases
the lock. Do not copy live WAL files. The backup verifies all three databases,
checkpoints their WALs, checks foreign keys and live Artifact hashes, then streams
a consistent archive directly into encryption. No plaintext staging archive is
created. Symlinks, hard links and special files in the snapshot are refused.

Create/escrow a dedicated 32-byte encryption key in SecretStore first. A reference
uses the local private-file SecretStore, or an explicit `env:NAME` reference. The
archive never contains key material, and is useless if its key is lost. The
archive uses a fresh 256-bit salt, HMAC-SHA256 key derivation and AES-256-GCM
records, with authenticated order/length and a final marker. Truncation, append,
corruption and a wrong key fail closed. Maximum snapshot size is 64 GiB/65,536
files, with an 8 MiB manifest. The private manifest records version, database
schemas, sizes and SHA-256 hashes. Its CLI response is local operational data,
not the shareable bug report.

```sh
steward -config SOURCE_CONFIG ops backup /private/backups/steward.steward KEY_REF
steward -config SOURCE_CONFIG ops restore /private/backups/steward.steward KEY_REF /private/restored-data
# Inspect/retire the source and reconcile post-backup external outcomes first.
steward -config ISOLATED_CONFIG ops activate source-retired-and-outcomes-reconciled
steward -config ISOLATED_CONFIG status
```

The archive includes `state.db`, `conversation.db`, `memory.db` and immutable
`artifacts/` bytes, including tombstone/claim/approval/replay state. It excludes
credentials, logs, auth locks, configuration, workspace/Skill directories,
Computer VM/checkpoint storage and external provider ledgers. Back up those
resources separately using their owners' policy. A new machine must provision
the same credential references and Memory decryption key separately; missing
credentials enter repairable setup, never a paid/Fake fallback. Memory's existing
encrypted material remains encrypted. Sensitive Conversation/Artifact material
is encrypted in the archive; its restored at-rest representation follows the
original storage policy.

Restore writes only a new, isolated data root and never overwrites an existing
root or archive. It verifies authentication, path/type bounds, exact manifest
inventory, file hashes, integrity, foreign keys, Artifact bytes and compatible
versions before atomically publishing the root. Partial restores are removed.
Files/directories use 0600/0700. A sibling-parent maintenance lock serializes
publication. The restored root remains fenced until the explicit cutover command;
neither model work nor external effects run before activation.

Activation attests that the source is retired and the operator checked effects
after the backup recovery point against authoritative external records. Never
run two copies of one identity. Preserve provider idempotency receipts and
reconcile `unknown`; a cold snapshot cannot promise exactly-once effects or
recover work accepted only after its recovery point. The existing fixed-source
and frozen-snapshot compatibility checks still apply after restore.

Preserve the frozen execution environment configuration when continuing accepted
Runs. In particular, the default local workspace is derived from `data_root`;
restoring to a different root changes that default and fails compatibility
checks. An explicit `execution.root` can preserve the original environment after
the source is retired. Workspace files and toolchains are outside this archive:
provision them separately. Installing different OCR language data or changing
other frozen capabilities likewise does not upgrade existing Runs in place.

<a id="security-security"></a>

## Security

Keep credentials out of issues, logs, prompts, YAML and artifacts. Configuration
uses SecretStore references. Never upload a real data root or authentication URL
as a bug reproduction.

For vulnerability reporting, supported fixes and current dependency checks, follow
[SECURITY.md](../../SECURITY.md). Do not put vulnerability details in a public
issue when requesting a private reporting channel.

The [technical baseline](../designs/product-overview.md#contract-0-文档效力与实现规则) defines approval, capability,
credential and replay boundaries. [Operations](#) describes
backup and recovery. Offline test success does not establish live-provider or
Computer compatibility.

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/operations.md)；已有检查、未覆盖场景和验收方法见 [测试方案](../test-plans/operations.md)。

离线备份密钥的 credential 引用默认从本地文件 SecretStore 读取；显式 env 引用保留。`secrets/` 与 `account-secrets/` 不进入数据备份白名单，解密所需密钥单独保管。

文件密钥来自命令配置的数据根。恢复到新目录时，`ops restore ARCHIVE KEY_REF NEW_ROOT` 从源配置读取文件密钥，并将数据发布到尚不存在的 NEW_ROOT；不需要为了放密钥而预先创建恢复目标。原无 NEW_ROOT 形式继续适用于显式 env 密钥。

### 服务重启插件边界

`service-watchdog` 私有插件提供次数／退避配方，现有后台启动会话把实际图与配方冻结到部署控制文件。独立巡检继续只读取服务控制事实，不打开业务库、不运行插件；当前部署不被后续版本重释。缺失配方关闭自动重启，人工控制保持可用。旧部署心跳不能作为新部署的致命健康证据。独立插件升级／回退／停用及冷读已有定向回归，最终整批验收尚未完成；真实 macOS launchd Live 缺口仍保留。

原控制文件 v1 的部署在崩溃恢复时继续使用具名历史解释器；只有显式 Start／Install 才创建 v2 接纳边界。恢复本身不会隐式迁移部署，也不会要求旧组合补选新插件。新部署缺失插件时仍禁止原生回退。
