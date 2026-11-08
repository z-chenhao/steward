# 隔离 Computer 与桌面浏览器操作

在独立 macOS Guest 中操作应用、窗口、浏览器与文件。

[需求主索引](../feature-map.md) · [技术方案](../designs/computer-use.md) · [测试方案](../test-plans/computer-use.md)

## 功能说明

Agent 能在明确配置的隔离 Guest 中观察和操作应用与浏览器，并恢复逻辑 Computer。

本文描述用户行为和范围；逐项完成状态以主索引为准。当前能力依据代码静态盘点，配置可用性与真实服务/模型质量需分别验证。

## 需求与验收

<a id="f-027"></a>

### F-027 · 配置型 macOS Computer Use

**需求**：Agent 能在明确配置的隔离 Guest 中观察和操作应用与浏览器，并恢复逻辑 Computer。

**当前实现**：Lume 生命周期、SSH Guest、CuaDriver、AX/截图、受限 Playwright、文件传输与 epoch 已实现。

**边界与剩余工作**：范围是 Apple Silicon macOS + 指定 Guest 环境，不包含 Linux 桌面或安装后自动提供 VM；兼容性需在真实平台复验。

**验收目标**：验证指定平台上的生命周期、观察后动作、过期引用拒绝、传输、审批和中断恢复。

## 如何使用

按指南准备 Lume、专用干净 macOS 基础镜像、Guest 驱动及浏览器，显式配置 Computer 和 JavaScript。

Agent 在 Code Mode 使用 `cua.getState()` 发现资源，再绑定 App/Tab、读取文档和观察后操作。Linux 云环境不能提供该真实桌面路径。

## 详细操作参考

- [Computer 生命周期](../designs/computer-use.md)
- [桌面与浏览器操作](#computer-use-computer-use)

## 操作说明与配置细节

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="computer-use-computer-use"></a>

## Computer Use

Configure the [durable Computer](../designs/computer-use.md) and JavaScript runtime. Code Mode
then discovers live Apps, Windows, Browsers and Tabs through the existing three
private Computer bridge Tools. Direct model calls to these Tools are rejected.
There is one Agent Loop and one ToolInvocation/Security/replay pipeline.

```js
const state = await cua.getState();
const app = await cua.getApp("com.example.Editor");
text(await app.documentation());
const observation = await app.getAXStateAndScreenshot();
text(observation.ax.state);
image(observation.screenshot.image);
```

Inventory failures appear by source in `getState().errors`; surviving sources
remain available. `getApp`, `getBrowser` and `getTab` bind existing resources.
`cua.computer.launchApp({app: bundleId})` and `createBrowserTab(browserId, url)`
are explicit mutations. `getApp({windowId})` binds a discovered window when an
App has multiple windows. Window IDs use native process/window identity, so
identical titles remain independently bindable and renaming does not change
the Surface identity. A Tab can be rebound by `{url}` when that URL has one
match. Ambiguous selectors fail rather than choosing an arbitrary target.

<a id="computer-use-playwright-provisioning"></a>

### Playwright provisioning

The only delayed Capability is Tab `playwright`. Read `capabilities.list()` before
`get("playwright")`. Its documentation describes CSS locators, bounded navigation
waits, download and chooser upload. Every method creates a normal nested
ToolInvocation. Navigation/reload expires the Capability even at the same URL.
CSS resolution and actionability waits stay scoped to the bound document. A
matching control in a replacement document cannot receive the pending input;
the call fails with `STALE_SURFACE_HANDLE`. CSS may follow element replacement
within the same document, while core ElementRefs remain tied to one DOM node.
There is no evaluate, dynamic import, arbitrary browser endpoint or filesystem
API in the model facade.

Install Node 24.21.0 and this repository's `guest-browser/package-lock.json` in the
clean Guest. Run `npm ci --ignore-scripts` there. The pinned Playwright Core
1.64.1 dependency uses Apache-2.0 and adds no Go dependency or Host runtime.
Place `internal/computer/macosvm/transfer.py` beside Playwright Core's `index.mjs`
as `steward-transfer.py`. Provision one isolated Chromium profile with CDP listening
only on Guest loopback. The adapter binds that existing browser; discovery does
not launch it or attach to a Host user's browser. Add trusted configuration:

```yaml
computer:
  # existing durable Lume/Guest configuration ...
  browser:
    node: /opt/steward/node/bin/node
    module: /opt/steward/browser/node_modules/playwright-core/index.mjs
    port: 9222
```

Absent browser configuration reports no managed Browsers. A configured browser
failure is an Inventory error and does not suppress native Apps or Windows.
The module version is checked before CDP attachment; configuration and bridge
resource versions are frozen in Capability Snapshots. Reprovisioning affects new
Runs; mismatched snapshots fail closed.

<a id="computer-provisioning"></a>

### Provisioning

Use Apple Silicon macOS, Lume 0.6.1, a private APFS test store, and a clean,
stopped macOS image with a signed CuaDriver application and pregranted Guest
permissions. The tested combination is documented in [L2 acceptance](../test-plans/product.md).
The operator supplies a dedicated image without personal accounts, API
credentials or user documents. The compatibility exercise's working VM must
have its synthetic projects and exports removed before serving as this base.

The application does not install or supervise Lume. Run its HTTP API explicitly
in the foreground with a separate `XDG_CONFIG_HOME`. Configure exactly one VM
location, disable telemetry/caching, and keep `vmLocations` last in the pinned
Lume YAML parser:

```yaml
defaultLocationName: "steward"
cacheDirectory: "/ABSOLUTE/OWNED/cache"
cachingEnabled: false
telemetryEnabled: false
vmLocations:
  - name: "steward"
    path: "/ABSOLUTE/OWNED/computer/vms"
```

Use an explicit loopback IP and port. The adapter checks the server's exclusive
storage location, telemetry/caching settings and version before lifecycle work.
HTTP proxies and redirects are disabled. Runtime starts use headless mode with
VNC disabled, no shared Host directories, and Lume's disabled clipboard default.
No default VM store is discovered or adopted.

Initial allocation calls Lume HTTP `clone`, which gives the new logical Computer
its own MAC and machine identifier. A frozen clone marker is durable before
dispatch. The pinned implementation writes the new machine identifier after the
complete directory copy and MAC update; this final postcondition reconciles a
lost HTTP response. A partial clone is never retried or started. Subsequent
checkpoint restores retain this Computer's hardware identity.

Add this advanced configuration to the application's versioned YAML. Keep
`computer.root` separate from application data, coding workspaces, Skills and MCP
working directories. Use a new empty directory with mode `0700`.

```yaml
computer:
  root: /ABSOLUTE/OWNED/computer
  endpoint: http://127.0.0.1:17774
  compatibility:
    provider_version: "0.6.1"
    guest_build: "25F84"
    driver_version: "0.36.0"
    driver_sha256: "54bb23df35ae9a52b6fa7b71e607364d0711bbbf750b9edb006e87b9af586fdd"
    driver_team: "YCK386LBJ7"
    driver_bundle: "com.trycua.driver"
    architecture: arm64
  guest:
    user: lume
    port: 22
    password_ref: env:OWNED_GUEST_PASSWORD
    host_key_sha256: "SHA256:REPLACE_WITH_THE_OWNED_GUEST_ED25519_FINGERPRINT"
  manifest:
    version: 1
    applications: []
    artifact_refs: []
```

JavaScript configuration is also required; see [Code Mode](../designs/codemode.md).
Replace the SSH fingerprint with the dedicated Guest's public Ed25519 key
fingerprint. The transport resolves only the explicit SecretStore reference,
uses password authentication in memory, pins Ed25519, and never consults Host
SSH keys or ssh-agent. The existing pinned `x/crypto` module supplies SSH.
Never put the password itself in YAML, SQLite, command arguments or evidence.
Custom production configuration must use private credentials. Automatic
preparation uses the public seed login only in its verified staging Guest and
replaces it before publishing the base.

Prepare the base before opening the configured application:

```sh
bin/steward -config /ABSOLUTE/OWNED/steward.yaml computer prepare-base /ABSOLUTE/OWNED/clean-stopped-vm
bin/steward -config /ABSOLUTE/OWNED/steward.yaml computer status
```

`prepare-base` copies only `disk.img`, `nvram.bin` and `config.json`, holds the
native config lock, rejects a session marker, hashes the unit, and publishes its
ownership marker. It refuses a nonempty destination and symlink paths. This is
an offline operator provisioning command; VM lifecycle requests run through the
accepted Tool pipeline. `computer status` is a read-only operator diagnostic.

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/computer-use.md)；已有检查、未覆盖场景和验收方法见 [测试方案](../test-plans/computer-use.md)。


Web Chat 的 Computer 面板提供只读桌面预览与操作进度，使用已有配置且已处于 ready 的实例。查看不分配/恢复 VM、不激活应用、不输入内容；关闭面板或页面转入后台后停止请求。画面来自独立 Driver 会话，不更新 Agent 的 AX/截图观察凭据；实际 macOS Guest 权限与显示环境仍需 Live 验证。

自动桌面在首次真实使用时准备。重启只允许下一次 Computer 使用重新准备，不会立即下载镜像。Settings 的历史失败提示不代表当前磁盘或网络已被检查；已知取消、超时、兼容性及完整性错误会分别说明。
