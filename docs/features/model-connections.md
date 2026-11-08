# 模型连接与账户

连接、验证和选择模型，并管理 ChatGPT 账户。

[需求主索引](../feature-map.md) · [技术方案](../designs/model-connections.md) · [测试方案](../test-plans/model-connections.md)

## 功能说明

用户能连接 OpenAI-compatible 端点，测试后显式选择活动模型。

用户能登录、重新认证和退出 ChatGPT 账户，凭据可安全持久化。

本文描述用户行为和范围；逐项完成状态以主索引为准。当前能力依据代码静态盘点，配置可用性与真实服务/模型质量需分别验证。

## 需求与验收

<a id="f-001"></a>

### F-001 · 本地模型连接与选择

**需求**：用户能连接 OpenAI-compatible 端点，测试后显式选择活动模型。

**当前实现**：CLI/TUI/Web 有连接、Test、Select 入口，Run 冻结模型快照。

**边界与剩余工作**：没有活动模型时，Web 用户主动添加的第一个连接成功后即成为当前模型；连接失败保持 setup_required，之后添加模型不覆盖当前选择。不会在启动、查看页面或恢复历史时任意挑选付费或 Fake 模型。

在 Web 的 Chat 点 Set up Steward，进入 Models。Accounts 和 API connections 分组管理连接，账户入口先选择实际支持的提供方，再明确发起授权；添加账户和已有账户重连分开。登录后账户目录进入 Chat 输入框的模型选择器；Chat 页面就绪后自动预取目录，首次尚未加载时显示“正在加载模型”，后续刷新保持已有可用选项可选，切换后保留完整列表。重新打开可重试读取失败，只有首次无当前模型时自动选用首项，后续连接不覆盖选择。API 连接时自动校验，失败保留恢复入口，不显示独立 Test connection。重连、断开和退出使用连接行的共享菜单。页面加载和状态轮询只读已保存配置及账户元数据，显式连接、目录加载、校验和执行才按需访问凭据。授权窗口不可用时保留手动继续入口。

**验收目标**：连接、测试、选择、工具续轮和重启均沿真实 Provider Bridge 验证。

<a id="f-002"></a>

### F-002 · ChatGPT 登录与跨平台凭据

**需求**：用户能登录、重新认证和退出 ChatGPT 账户，凭据可安全持久化。

**当前实现**：账户生命周期复用同一 Manager、Provider Bridge 和冻结模型路径；默认在 Steward 数据根下使用 `account-secrets/` 私有文件。凭据后端统一为本地文件，支持跨进程保存、读取、更新与删除，刷新与退出继续使用既有跨进程锁和恢复裁决。CLI/TUI/Web 提供凭据设置与故障反馈。

**边界**：默认文件后端使用 `0700` 目录和 `0600` 文件。OAuth 凭据由文件权限保护，同一用户下有访问权限的进程也能读取。旧版本保存在系统凭据库中的账户须重新连接以写入文件，沿用已保存模型。项目备份排除凭据文件。

**验收目标**：在每个声明支持的平台完成登录、刷新、退出、进程中断恢复和凭据隔离验证。

## 如何使用

标准安装后运行 `steward`，按引导连接本地模型或登录 ChatGPT。Web 首个成功连接自动用于对话，后续模型通过「使用此模型」切换。ChatGPT 模型的图片能力与 token 上限来自账户目录；缺失字段补充自内置 Pi AI 1.5.0 聊天模型目录。兼容端点默认请求图片能力，实际行为验证后才冻结。图片发送规则见 [F-013](artifacts-media.md#f-013)。

CLI 可运行 `steward model list` 查看配置，使用 `steward model test PROFILE_ID` 测试。ChatGPT 凭据默认使用私有文件。原生后端的准备步骤见下文。明确配置的环境变量 API Key 仍只用于兼容端点，不充当 OAuth 凭据后端。

## 详细操作参考

- [模型连接操作详情](#provider-bridge-chatgpt-account-lifecycle)
- [安装与部署详情](installation.md)

## 操作说明与配置细节

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="provider-bridge-chatgpt-account-lifecycle"></a>

### ChatGPT account lifecycle

Use the same explicit Bridge configuration shown below. ChatGPT credentials use
private files under the Steward data root. No system credential backend or session
bus is needed. Environment API keys cannot authenticate this entry point. No Codex
credential file is read or written. Missing or unreadable files fail closed with a
sanitized error; reconnect an existing account to save fresh OAuth credentials.

```sh
bin/steward -config local.yaml model login-chatgpt
bin/steward -config local.yaml model accounts
bin/steward -config local.yaml model account-catalog ACCOUNT_ID
bin/steward -config local.yaml model account-add ACCOUNT_ID RETURNED_MODEL_ID
bin/steward -config local.yaml model select PROFILE_ID PROFILE_VERSION CONFIG_VERSION
bin/steward -config local.yaml model refresh ACCOUNT_ID
bin/steward -config local.yaml model logout ACCOUNT_ID
```

`login-chatgpt` prints a private `open_url` JSON event. Open that URL in your
system browser to **Continue with ChatGPT**, select the account/workspace and
grant plan usage. Keep this foreground CLI running while the browser returns to
its `127.0.0.1` random-port callback. Login expires after five minutes; Ctrl-C
cancels it and closes the listener. The final JSON object contains safe account
metadata. Do not redirect login output into logs, evidence or bug reports: the
URL includes private authorization state and installation identity, and returning
sign-in may include an ID-token hint. The URL is an interaction, not diagnostics.

`accounts` is the local account picker: masked account/workspace labels and
connection IDs identify each registration. Multiple accounts have separate
credentials. For reauthorization use `model login-chatgpt ACCOUNT_ID`; it reuses
that registration's issued client ID and validates the same identity before
replacement. Logout retains this non-secret mapping and history for later login.

`account-catalog` requests the current account's `/v1/models`; only server-returned
`visibility=list` slugs can be chosen. Unknown/hidden IDs are rejected before
inference. `account-add` uses account limits and image modalities, filling missing fields from the bundled Pi AI 1.5.0 OpenAI Responses chat catalog. Unknown models without limits use a 128,000 context / 16,384 output fallback, bounded by the Host context ceiling. Account selection checks live authentication and current catalog membership without generating a behavioral Probe. Compatible endpoints continue to require the streaming/tool/cancellation checks.
Requests use Steward's native Go `openai-responses` transport to the fixed public API,
with `store=false`, `stream=true` and namespace-wrapped function tools. Unsupported
sampling, reasoning and modalities stay disabled; `response.completed` is required.

`refresh` ensures the token is fresh; it makes no network call if comfortably
before expiry. Every account request also performs this check. Kernel locks
serialize rotation and logout across local Host processes. Refresh failure or an
interrupted rotation closes readiness, removes credentials and requires explicit
reauthorization; an unknown rotating call is never replayed. Startup reconciles
interrupted auth state after obtaining the same lock. Keep all processes for one
installation on a local filesystem supporting advisory locks.

| Persisted auth state | Legal next states | Runtime behavior |
| --- | --- | --- |
| `connected` | `refreshing`, `reauth_required`, `logged_out` (metadata-only self-update is allowed) | Fresh credentials admit requests |
| `refreshing` | `connected`, `reauth_required`, `logged_out` | One lock owner rotates; new callers wait |
| `reauth_required` | `connected`, `logged_out` | Explicit validated login is required |
| `logged_out` | `connected` | Only explicit same-registration login reopens admission |

Every state transition uses version CAS and an atomic Outbox event. Auth loss
increments a persistent account epoch; old generations remain fenced even after
the same account signs in again. A successful same-account refresh changes auth
version without changing that epoch. A stale token's 401 cannot revoke a newer
credential version. Failed SecretStore/metadata commits do not reopen admission.

Logout persists closed admission, aborts the connection's Bridge work, clears
local credentials and reaps the Bridge, while retaining snapshots/history/usage.
The output reports `remote_revocation_confirmed`; if false, disconnect the app in
ChatGPT Settings to confirm remote revocation. A SecretStore deletion failure is
an error, retried on startup or another logout. Other Host processes reject final
results by rechecking their captured epoch and account status inside the usage
write transaction, serialized with auth mutations. Completion reaches the sink
only after that transaction succeeds. Logout or reauthorization between the
observer Guard and persistence records `AUTH_REQUIRED` with no accepted result
or token counts. Same-identity refresh preserves the epoch. HTTP history/cache
deletion is not promised. Account requests disable provider cache retention.

`tests/system/verify_account.py` uses an isolated local OAuth/Responses server and
the production file backend by default. Its optional native test uses a
disposable Secret Service session. Web tests can inject a separate
`-test-secret-store SOCKET` fixture. Reports record outcomes without credentials or
private authorization URLs.

`go test ./internal/agent/sqlite -run TestAccountFinalAcceptance -v` drives the
real Host → isolated Go bridge completion path, pauses before usage persistence, then mutates
auth through a second Host/DB handle. It covers logout, reauthorization, auth loss,
same-identity refresh and storage failure. Node contracts also capture the actual
Tool continuation wire and require the fixed `steward` namespace on replay.

`go test -race ./internal/agent/sqlite -run TestAccountCancellationAtFinalAcceptance -v`
checks Generate and Continue with Cancel, Dispose and caller-context cancellation
at observer, open-transaction and post-commit barriers. Registrations remain
active through persistence. A Host-only acceptance gate serializes acknowledged
cancellation with the actual usage commit; it does not lock an observer wait,
RPC or sink. SQLite writer admission precedes this gate. A pre-commit ack rejects
success, records `GENERATION_CANCELLED` with no accepted token counts and emits
no completed event. A post-commit cancellation preserves the committed result.
Auth transaction rejection keeps `AUTH_REQUIRED`. No database migration or RPC
version change is required; the gate is excluded from JSON and snapshots.
`TestCancellationDeadlineDuringUsageCommit` holds an admitted commit, verifies
Cancel/Dispose respect their caller deadline, then drives an independent catalog
RPC before releasing storage. Failed disposal reaps its peer as usual.

[ADR 001](../designs/model-connections.md#decision-001-adr-001-current-openai-account-authentication-and-deferred-device-flow) records approved Device
Authorization deferral. It remains a future requirement, not a passing check.
Real interactive login/provider Live Shadow remains **inconclusive**
until explicitly performed; deterministic fixtures do not establish account
eligibility or real service availability.

<a id="provider-bridge-connect-a-local-model"></a>

### Connect a local model

Build Steward with `make build`. Default startup uses the binary's native Go
model bridge. An explicit configuration can use the same private RPC:

```yaml
version: 1
llm:
  provider_bridge_command: /absolute/path/to/steward/bin/steward
  provider_bridge_script: -model-bridge
  provider_bridge_version: steward-go-v1
```

The implementation lives in `internal/llm/provider` and `internal/llm/bridge`.
It is an internal part of Steward, with no standalone SDK module or publishing
contract. It references Pi AI 1.5.0 protocols and catalog data; it does not load
any Pi SDK. The previous `pi-ai-1.3.0-v1` external RPC configuration remains
accepted; the bundled Node entry only forwards streams to the native binary.
Wire observations use `steward-wire-v1/go-ai-v1`.

The reference inventory includes 42 provider catalogs, ten chat protocols,
four classifier protocols and one image protocol. Internal protocol fixtures
exercise these encoders and decoders. This does not certify live services or
complete feature parity. The product's current connection controls still expose
OpenAI-compatible chat and public OpenAI account Responses. Additional provider
login flows, cloud credential chains and product configuration controls remain
to be integrated before claiming all providers are usable through Steward.

Use an isolated `STEWARD_DATA_ROOT` while evaluating the CLI. The Bridge is optional;
unconfigured startup still provides setup/diagnostics without a provider.

```sh
bin/steward -config local.yaml model discover http://127.0.0.1:8000/v1
bin/steward -config local.yaml model add http://127.0.0.1:8000/v1 MODEL_ID
bin/steward -config local.yaml model catalog PROFILE_ID
bin/steward -config local.yaml model select PROFILE_ID 1 1
bin/steward -config local.yaml status
```

`add URL MODEL_ID [AUTH_ID]` creates an unverified candidate with conservative
8,192 context/1,024 output token limits. The existing `model add` JSON form allows
explicit token limits and other candidate metadata. If discovery is unavailable,
manually supply the ID to `add`; catalog support is never an activation condition.
Inspect profile/config versions with `model list` and `status` before selecting.
Every mandatory Probe behavior must pass. Optional compatibility, reasoning,
modalities, and sampling claims remain disabled until individually verified.
Responses API is rejected on the local path. Unsupported frozen settings fail
closed instead of being silently dropped. Clipping Probe uses an eight-byte Host
output limit while keeping the frozen model token limit, so it actually tests
clipping rather than depending on a model exceeding its requested token limit.

API keys are optional. `model login` reads a key from stdin and writes it to the
private file SecretStore without credential process arguments. It returns an
AuthConnection ID to use with `add`. `model login ENV_NAME` explicitly registers
an environment reference; that variable must be present in every Host process
using the connection. Missing credentials fail closed. Neither method prints or
stores the key in SQLite; metadata fields accept references, never secret bytes.

Privacy: model requests, conversation content, and Tool schemas go only to the
configured endpoint. `localhost` resolves from the Go Host network namespace,
not the Computer Guest. This notice is also printed before CLI activation.
The Host retrieves only the referenced API key for each catalog/generation call
and sends it in a private, transient connection RPC envelope. The Go bridge process
inherits PATH only, receives no domain DB paths or Tool/Computer handles, and
keeps no credential file. This is a logical process boundary, not an OS sandbox.
There is no provider fallback. All redirects are rejected, including same-origin
redirects, so Authorization cannot be forwarded. A Bearer header is sent only
when the user supplied an actual key; no placeholder credential is created.

The transport retries connection failures or HTTP 429/502/503/504 at most twice,
with 50/100 ms delays, before a response stream is accepted. It does not retry
partial streams, malformed events, auth errors, or model incompatibility.
Each generation HTTP attempt has 30 seconds to receive headers. After headers,
90 seconds without response-body data ends the stream. The Bridge also applies a
180-second absolute deadline that includes account catalog lookup, attempts and
stream consumption. The Host allows 195 seconds for the generation RPC.
A Bridge timeout aborts HTTP and returns PROVIDER_UNAVAILABLE. The generation's
Overview distinguishes the three timeout phases. The Host Probe has its own
25-second deadline per capability check and independent one-second cleanup
contexts. Direct Cancel/Dispose RPCs have a five-second
bound. The error body is discarded,
except for an allowlisted machine code read from at most 8 KiB for HTTP 400/422
context-length classification. Raw response streams are limited to 4 MiB before
protocol parsing, with at most 65,536 raw SSE events. Small text fragments are
coalesced before the bounded Host RPC; valid reports are independent of provider
chunk boundaries. The first text remains immediate. Normalized Tool Calls retain the Host bounds of 32 calls and
64 KiB of arguments. Malformed or non-object arguments fail with
INVALID_PROVIDER_RESPONSE before any Tool Call is forwarded. Raw provider parse
diagnostics are never exposed. An explicit empty object remains valid.
Provider errors contain stable codes only; Host Probe errors add fixed stage
labels and timeout durations without provider text.

Readiness checks the saved proof, current credential availability, and Bridge RPC
health. It does not require catalog support or certify current endpoint health;
actual inference and explicit re-tests observe provider availability. Failed
re-tests revoke proof and close ingress. A crashed Bridge is restarted only for
the next independent operation. No generation is automatically replayed.

Schema v5 retains immutable endpoint locations separately from mutable profiles.
Resolution uses the frozen endpoint hash; editing a profile cannot redirect an
old snapshot. Migration backfills endpoints available in prior-schema profiles.
An endpoint lost through historical edits before v5 cannot be reconstructed and
fails closed. Snapshots and credentials retain their existing boundaries.
`provider_usage` stores the snapshot, generation/Run IDs, observed token counts,
Host elapsed milliseconds, and normalized failure. Missing token usage is NULL,
including cancellation/crash; zero is recorded only if supplied by the provider.
Local monetary cost is zero. The shared Run ledger atomically accepts generation results and usage under
ownership fencing; see [Main recovery](../designs/conversation.md#main-loop-ownership-and-persistence).

<a id="provider-bridge-reproduce-the-cli-path"></a>

### Reproduce the CLI path

Use an isolated absolute `STEWARD_DATA_ROOT`. Run `make build`, then provide metadata
through `model add` as documented in [local model setup](#provider-bridge-connect-a-local-model). Copy its `profile_id`.
Profiles are unverified candidates, and metadata cannot activate one.

```sh
bin/steward -test-bridge success model catalog
bin/steward -test-bridge success model test PROFILE_ID
bin/steward -test-bridge success model select PROFILE_ID 1 1
bin/steward -test-bridge success status
bin/steward status
```

`select` requires the expected profile version and Agent config version, visible
in `model list` and `status`. It increments both versions atomically, commits
ready/verified metadata, retains the stable AuthConnection reference, and creates
both activation Outbox events. Test alone stores Probe evidence and opens no
ingress. Concurrent selections have one config-CAS winner. Metadata edits or auth
version changes invalidate the prior Probe. A failed re-test revokes only the
tested version's existing proof, marks it invalid/unavailable/reauth_required,
and degrades an active config atomically with events. Cancellation by the Test
caller preserves prior state. Failure of the Probe's internal cancellation check
revokes prior proof even when the error includes `GENERATION_CANCELLED`; a
responsive replacement Bridge alone cannot restore readiness. A failed commit
changes neither the profile, Probe version, config nor events. Existing SecretStore registration
writes credentials first and removes a new orphan on failed metadata commit.
SecretStore/SQLite still have no distributed transaction: a hard crash between
those commits can orphan a credential; automatic orphan enumeration/reconciliation
is not implemented.

`model cancel` cancels setup without changing durable state. Web automatically selects only the first model the user actively connects, when no model is selected; later additions and restarts never replace that selection. Every process that uses a test profile must explicitly supply
`-test-bridge success` again. Without it, an existing active test profile reports
`degraded` with closed ingress. Incompatible Bridge versions, missing credentials,
unavailable/invalid active profiles, or stale Probe evidence also close ingress.
Diagnostics and metadata remain usable for repair. A compatible, valid active
profile reports `ready`; this readiness flag is a gate for later Agent admission,
and governs the existing Main/Worker execution path.

The Fake fixtures are named `success`, `incompatible`, `crash`, `partial-crash`,
`completed-crash`, `no-tools`, `bad-sequence`, `drift`, `malformed`, `auth-required`,
`raw-error`, `event-error` and `error:CODE` for the eight baseline provider codes.
Only explicit test injection starts them. Fixture names are not
ProviderKind values or saved deployment defaults. A failing Probe never selects
a different provider or retries a generation automatically.

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/model-connections.md)；已有检查、未覆盖场景和验收方法见 [测试方案](../test-plans/model-connections.md)。

非账户 API 凭据默认与 Memory 密钥一样存入数据根下的 `secrets/` 文件 SecretStore。账户 OAuth 仍沿用 `account-secrets/`，不自动移动或重写既有账户凭据。

Web 的 Models 只管理 ChatGPT 账户生命周期；登录后全部可用目录模型在 Chat 输入区选择，无逐模型添加或测试步骤。首次登录且无当前模型时自动激活目录首项；后续选择仍使用现有版本校验及冻结配置机制。直连 API 保留独立连接与验证路线。

Web 的所有模型连接（包括 Local/API）不提供独立 Test connection。首次连接和聊天模型切换复用服务端 Control 校验，并保留版本、授权及失败恢复；旧 POST 路由仅兼容已有调用，不再由页面引导。

Models 为 Settings 的独立分类，包含「Sign in with an account」和「Connect with an API key」。账户目录自动进入聊天模型选择器；API 连接使用现有私有文件 SecretStore，凭据与首个模型元数据一次事务保存，失败清理新文件。连接后自动验证；首次无当前模型自动选用，后续连接不覆盖选择。连接操作复用共享菜单和原生表单，断开采用模型及凭据双版本确认并撤销后续使用，保留历史冻结配置。Apps 工具栏 App settings 进入服务配置；原 Models/Resources 服务入口移除，旧 setup 与 services/apps 深链接保留重定向。浏览器回归覆盖实际 HTTP provider 的 API key、聊天模型切换、断开及明暗／窄屏布局。

Models 将账户与 API 连接分组；账户入口先呈现实际支持的 ChatGPT 提供方，明确确认后才发起授权，重连绑定已有连接 ID，添加其他账户是独立操作。等待状态与取消保持配置行，完成后刷新账户投影。不能仅凭相似名称合并不同注册身份，现有账户记录不自动删除。协议、凭据生命周期、当前模型和冻结快照不改变。浏览器回归断言进入提供方页不发送 auth/login，并保留 API 连接、原生路径、明暗和窄屏验证。Apps 服务高级授权配置使用应用标识／配置 ID 行，删除 JSON 编辑框；显式验证保存和版本检查保留，目录布局不变。

聊天模型选择器按可见模型名降序排列，账户模型不附带账户名称；同名模型保持独立账户键和稳定顺序。原生首屏、目录刷新和切换响应共用排序投影，保持当前模型与草稿。`composer_models_test.go` 验证多账户、已保存／未保存目录模型、稳定同名路由、无账户名和只读首屏。
