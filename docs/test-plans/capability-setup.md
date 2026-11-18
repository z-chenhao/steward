# 能力连接与就绪引导：测试方案

[功能与验收目标](../features/capability-setup.md) · [技术方案](../designs/capability-setup.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 自动默认资源验收

- `internal/config/setup/defaults_test.go`：并发启动复用同一密钥；密钥缺失不替换；自动、关闭、重新启用跨重启保存；自定义错误密钥不能覆盖默认加密身份。
- `internal/interaction/web/capability_status_test.go`：自动与自定义均显示 Enabled／已启用，异常、准备和重启状态独立；`tests/system/channels_capabilities.mjs` 验证模式切换与保存。人工检查 Auto／自动选中态、短标签无截断及明暗主题桌面／窄屏截图。
- `internal/interaction/web/capability_forms_test.go`：四类自动能力的普通表单往返与关闭；原高级字段保留用例继续运行。
- `internal/memory/sqlite/local_test.go`：无 embedding 服务的中文加密记忆搜索、重启、scope、明文不落盘和遗忘。
- 原生 SecretStore 的实际解锁与 macOS 首次下载/启动需使用真实登录会话验收；Linux 的内存凭据 fixture 不能替代这项证据。


## 范围与证据

证明 F-030 的真实 Settings → 测试 → 保存 → 明确重启 → 新 Run 闭环。诊断与软件回归不认证外部模型语义质量、真实账户授权或 native macOS Guest 兼容。

## 需求场景矩阵

| 场景 | 可观察行为 | 验证入口 |
| --- | --- | --- |
| 五个主入口／六类资源 | 默认 Memory 已启用、检索收进高级设置；普通 Save 校验失败不落盘、修正后成功，关闭记忆同时关闭检索 | channels 的 capability-settings 场景 |
| 提交安全 | 未认证 401、伪造 Origin 403、未知详情 404、超限 413、未验证 Save 409 且文件不变 | 真实 HTTP + 浏览器 |
| 证明约束 | 精确候选、完整源文件版本、过期证明拒绝，探测中外部变更丢弃结果 | config/setup 组件测试 |
| 文件保护 | 0600、保留注释/其他 section、并发 Manager 至多一个成功、拒绝 symlink/删除源、无残留临时文件 | config/setup + discovery |
| 数据边界 | 拒绝直接密钥轮换、embedding 身份变更不等于索引迁移、Computer bootstrap 结构正确 | config/setup 组件测试 |
| 有界真实探测 | 固定 embedding 文本；实际沙箱 printf；MCP tools list 但无业务调用；实际 sidecar 握手 + sealed base 校验但无 VM | owned HTTP/stdio/Computer peers + 组件测试 |
| 生命周期 | 保存前后旧快照一致；重启后新 Run 包含新 provider；旧快照仍一致 | channels 场景读取实际 SQLite 状态 |
| 可访问操作 | 原生无 JavaScript 字段表单、320px 无横向溢出、浏览器无脚本错误 | channels browser 模块 |

## 维护入口

- `go test -race ./internal/config/... ./internal/interaction/web ./internal/computer/macosvm`：配置/文件并发、真实沙箱与协议适配器、共享 Computer 校验。
- `make test lint`：代码/组件、现有 Node/Web/测试基础设施、文档与静态检查。
- `make build` 后运行既有 `tests/system/runner.py --gate channels --binary bin/steward --evidence /absolute/path`；capability-settings 已并入该 gate，不增加 runner 或 CI gate。
- 跨模块回归按 [Testing](product.md) 执行 core，以及涉及能力/Computer/recall 的现有 extended gates。

具体结果保存在每次运行的 evidence，本文定义验收而非替代执行报告。owned peers 是明确的受控外部服务，不伪造产品内部 Run/验证通过状态。测试在可丢弃根目录创建配置和资源，结束后回收进程/监听器；从不访问个人数据目录。

## 平台与剩余证明范围

Linux 验证了内核沙箱与受控 Lume 协议、tiny sealed base 和真实 Node runtime。Computer 诊断不启动 Guest，因此不以这份证据声称验证了 macOS/arm64 的实际桌面操作；该边界由 [Computer 测试方案](computer-use.md) 保持。真实外部 MCP/embedding 服务可通过同一 owner 入口按实际凭据测试，未配置时不自动补齐服务或选择默认模型。

普通表单补充：owning `forms_test.go` 与 Web `capability_server_test.go` 验证六类往返、未展示字段保留、别名回退、MCP 顺序／权限、明文凭据拒绝、版本冲突、精确候选证明与转义。维护中的 channels_capabilities 浏览器场景覆盖默认记忆、中文／移动端、Preferences 布局、自动保存的失败／成功／修改后重新校验／保存，MCP 原生增删行、无 JavaScript 字段表单。真实工作区／Computer 诊断若因本机环境不可用而失败，必须记录未完成范围并拒绝无效保存，不能用 UI 成功状态替代。

状态与保存回归：`TestDefaultMemoryHasOneEnabledEntryAndPreferenceLayout` 和 `TestPreferenceStyleSaveChecksBeforePersisting` 验证默认启用、一处 Memory 入口、联合关闭、密钥保留、无效连接拒绝与版本冲突。channels 的 `default_capability_settings` 使用显式隔离 SecretStore 启动真实应用，验证默认密钥跨进程重用并输出新设置页截图。


`TestCapabilityStatusDistinguishesConfigurationFromConnectionProof` 以表驱动验证关闭、自动／自定义启用、准备中、不支持、不可用、已保存待重启及未保存候选；各状态分别使用有／无连接检查证明和中英文渲染，防止再次把默认启用误写成未验证，或用启用文案掩盖异常。

维护中的 capability-setup 浏览器场景还验证六类真实字段的失败修复、自动保存、MCP 权限与原生行编辑，无脚本提交；逐页覆盖 1400／390／320px 和明暗主题，测量布尔行标签与开关同排、无横向溢出；断言不包含 YAML 编辑、导入、模板与配置文件路径。服务端回归验证 guided 忽略附带整份文档、字段草稿保留和版本冲突。配置和探测仍使用真实 Manager 及 owned peers。

## 托管表单回归

用独立保留的 Owner 输入和预期校验共享 v1 的表单及展示输出。实际 `capability-form-plugins` 门禁验证打包资源、独立换版和停用，并通过浏览器与原生入口核对提交与读取行为。

- 原 `capability_forms_test` 已迁至 owning 包，保留六类默认值、字段校验、SecretStore 引用、MCP 行及高级 YAML 保留场景；两项保留原生 codec 的 Owner 样本校验实际输出。
- `TestCapabilityFormsActualUpgradeRollbackStopAndCold` 构建独立真实进程，修改 Computer 表单端口默认值，验证激活／精确回退／停用／首次冷读取，不依赖 native 代理。
- `TestCapabilityFormCandidateRetainsEveryUnshownSetting` 覆盖六类隐藏字段；`TestCapabilityFormCandidateRejectsImplicitYAMLAndForeignProvider` 拒绝隐式展开和跨 provider 高级数据；请求回归拒绝转发伪造文档、版本和无关字段。
- 原 Web 保存测试同时使用 owning 策略，证明先验证再保存及旧版本拒绝；`TestCapabilityPluginFailureCannotTestSaveOrFallBack` 证明插件故障不能调用 Test／Save，不泄露私有错误。
- 必需 `capability-form-plugins` 门禁以实际生产 bootstrap 激活默认图，再复用六类浏览器配置／检查／保存／重启场景；fixture 只替代模型生成，保留旧 Run／Child 能力快照及新工具可见性。纳入 core、startup-ui 与 Capability race；未完成的本批门禁不可由局部测试替代。

### 说明与诊断同包迁移

- 六类原说明／示例由修改前实际函数采集，诊断词表逐项对照原固定返回表达，计数仅将原数字位置换为三个明确绑定；同包 owning 输出与保留 oracle 精确比较。
- `TestCapabilityFormsActualUpgradeRollbackStopAndCold` 同时验证真实独立二进制中的标题和诊断成功文案；Owner 验证对应候选输出，实际 Manager.Detail／Test 消费新版本，随后精确回退。停用保留原已接纳证明，拒绝新诊断；首次关闭重开不恢复 native 说明。
- Manager 全包 race 覆盖 `TestCapabilityPresentationFailureCannotProbeOrInventFacts`、`TestCapabilityPresentationFreezesBeforeProbeAndRetainsAcceptedOutcome`、`TestCapabilityPresentationCannotChangeDiagnosticState`：无投影不读密钥，不凭文案改变真实结果，效果中版本变化不重读词表，后续 Detail 不改写原证明。
- 相同六类实际浏览器门禁还须重跑，覆盖标题／检索入口、失败修复、保存／重启以及旧冻结快照；局部 lifecycle 不替代该门禁。

### 同包视图验收

- `TestCapabilityOwnedViewRetainsOriginalFormSurface`／原状态回归执行 owning 模板和中英文词表；原控制形态和启用／连接证明语义不变。
- 真实进程 lifecycle 同时修改 sealed Computer 标题资源与中文词条，校验确切资源的独立选版、digest 和回退；字段默认值／诊断表达仍走同一包生命周期。
- `TestCapabilityOwnedViewRejectsEffectEscapesAndPrivateFields` 拒绝脚本、noscript 网络资源、外部表单／链接、私有字段、共享原语重定义、隐藏凭据及错误版本；`TestCapabilityOwnedViewBoundsEmptyHelperExpansionAndIntegerLoops` 覆盖空体指数辅助调用与整数迭代；缺词条回归拒绝借用原生字典。
- `TestCapabilityInvalidOwnedViewCannotProbeOrSave` 和 `TestCapabilityEncoderCannotSubstituteAuthorizedCredentialReference` 断言非法视图和偷换凭据均在 Test／Save 前拒绝。六类绑定正例包括带点工具名及外部效果权限，负例覆盖权限替换、provider 身份和原高级数据。
- 必需六类实际浏览器门禁在本批资源更改后重跑，检查无脚本、桌面／移动端及明暗主题，保留原配置证明、旧 Run／Child 和新工具可见性；共享控件所有权扫描包括 owning 视图目录。
