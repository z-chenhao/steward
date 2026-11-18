# 能力连接与就绪引导

让用户发现、配置、验证和修复可选能力。

[需求主索引](../feature-map.md) · [技术方案](../designs/capability-setup.md) · [测试方案](../test-plans/capability-setup.md)

## 默认能力与设置

正常安装启动无需先填写能力配置。Memory 自动生成 256 位密钥并保存在本地文件 SecretStore；搜索默认使用本地全文检索；Skills 自动准备私有空目录。Computer 在 Apple silicon Mac 首次使用时自动下载固定版本的 Lume 和 macOS 镜像、校验并准备私有 Guest。其他平台显示不支持自动 macOS 桌面，本地文件与命令仍可用。

Settings / Capabilities 提供“自动（推荐）／自定义／关闭”。自定义资源优先；关闭保留已有数据。保存后重启，新 Run 使用新配置；正在执行和恢复的 Run 继续校验原冻结能力。显式 `-config` 部署保留现有配置语义，可通过 `defaults` 选择自动资源。


## 功能说明

Settings → Capabilities 保留 Memory、MCP tools、Skills、Files and commands、Computer 五个入口。Memory 包含存储与检索，可选语义检索放在 Memory 的高级设置中。自动与自定义开启均显示“已启用”，配置模式使用 Auto／自动，不附加默认、推荐或星号标记；准备中、异常和重启后生效单独呈现，连接检查证明不作为启用状态。修改字段后，旧连接反馈立即失效；重新检查后才显示对应新候选的结果。页面与 Preferences 共用左侧导航、居中窄列、紧凑分组行和共享下拉，配置后直接保存。

## 需求与验收

<a id="f-030"></a>

### F-030 · 统一能力设置与诊断体验

**需求**：用户能在设置中看见记忆、embedding、MCP、Skills、代码工作区及 Computer 是否已连接，并完成配置验证。

**当前实现**：底层保留六类资源的独立验证与迁移边界，用户只需在五个主入口配置。字段修改后自动执行有界检查并保存，失败保留草稿。用户界面不展示 YAML、模板下载或宿主配置文件路径。关闭 Memory 同时关闭检索并清除自定义 embedding 连接，保留数据和密钥引用；重新启用恢复默认本地检索。保存仍绑定成功验证的精确候选和未变化的源文件。新配置经明确重启后供新 Run 使用，运行中的配置和旧快照不会热改。

**边界**：模型与通知继续使用各自设置页；不增加模型可见的能力发现 API。自定义语义检索、Skill catalog、MCP 和 Computer 连接仍可由用户提供；自动资源走默认准备流程。诊断验证连接与资源边界，不评价模型质量，不代替 Guest 首次运行时的身份检查，也不代办账户授权或数据迁移。

**验收**：未配置 → 编辑 → 失败可修复 → 成功测试 → 保存待重启 → 重启加载 → 新 Run 使用新能力；旧能力快照保持不变。关闭 JavaScript 后仍可完成表单操作，窄屏无横向溢出。

## 如何使用

1. 打开 Settings → Capabilities，选择一项能力。
2. 选择自动、自定义或关闭。自定义时填写具体字段；MCP 按服务与允许工具行管理，Computer 按连接、Guest、兼容与运行时分组。
3. 字段修改后自动验证并保存；失败保留输入，修正字段或重试。MCP 增加行是草稿操作；删除完整服务或工具按最终候选验证保存，删除最后服务停用 MCP。无 JavaScript 时使用原生 Save。
4. 保存后需要重启的设置明确显示待重启。按原启动方式重启 Steward，新请求创建新快照；已有执行保持其冻结配置。

凭据只填写 `env:NAME` 或 `credential:NAME` 引用；真实值放 SecretStore/进程环境，不放 YAML、命令参数或截图。新环境引用需先提供给 Steward 进程，再测试。保留 Memory 的原始 32-byte 密钥和备份；更换引用须指向同一密钥。embedding endpoint/model/dimensions 的变化需要离线重建索引，单纯连接测试不会迁移已有向量。先前已停用能力的数据也不会被删除；再次启用必须保留原身份。

清空 Memory、embedding、MCP、Skills 或 Computer 配置可停用它；清空 Files and commands 会恢复默认本地工作区，不删除工作区。启用 Computer 前先清空显式本地执行覆盖；Computer 开启后文件工具与桌面工具共用 Guest。

## 诊断范围

| 能力 | 实际检查 | 不执行 |
| --- | --- | --- |
| Memory | SecretStore 中 32-byte 密钥可用，完成固定文本 HMAC | 读写用户记忆、密钥轮换 |
| Memory search | 向所选端点发送固定诊断句，校验模型、维度与向量 | 发送对话、选择隐式模型、重建索引 |
| MCP | 现有内核沙箱中 initialize / tools list，核对 allowlist | tools call 或业务副作用 |
| Skills | 实际 catalog、SKILL.md 与有界资源校验 | 执行 Skill 内容 |
| Files and commands | 私有工作区中用现有内核沙箱运行固定 printf | 用户提交的 shell、网络访问 |
| Computer | Node sidecar 握手、Lume 版本/独立存储、sealed base 完整性、凭据引用 | 下载、克隆、启动、停止 VM 或访问 Guest |

## 配置来源与恢复

显式 `-config` 文件是编辑目标；未指定时读取 `STEWARD_DATA_ROOT/config.yaml`，默认是 `~/.local/share/steward/config.yaml`。文件不存在则沿用内置默认值，首次成功保存才创建 0600 文件。显式文件必须是普通文件，设置页拒绝符号链接；其他 section 和注释保留。

设置页使用完整配置校验，路径必须遵守各能力的隔离要求，配置文件也不能放进 Agent 或 provider 可读写的工作目录。手工把整个文件改坏导致进程无法启动时，需先从终端修复文件；Web 不能修复一个尚未启动的进程。只读 `doctor` 仍不读取凭据或探测服务，设置保存的连接检查只由用户修改配置触发，不在后台自动探测未修改的连接。

## 详细操作参考

- [MCP、Skills 与工作区约定](../designs/integrations.md)
- [记忆维护操作](memory-context.md)
- [Computer 生命周期](../designs/computer-use.md)

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/capability-setup.md)；检查和平台证明范围见 [测试方案](../test-plans/capability-setup.md)。

## 插件迁移状态

六类 guided forms 的解析、默认值、MCP 增删行、校验文案及 YAML 候选生成归属同一个 `capability-settings` 包，使用私有实验级 `steward.capability-form/1`。GET 准备与 POST 的候选提交分别读取托管实现；提交结果一次给出草稿或候选，保存后不再重新读取策略。普通 Web 旧实现已删除；历史解释器仅服务从未激活该服务的图和没有托管回调的嵌入，随这些消费者退役删除。当前分发为 36 包／373 项 Owner。

Host 继续持有所有者认证、实际配置、Test／Save、凭据、密钥／索引身份、成功证明及配置版本；插件停用或错误不能进入 native 保存。表单外高级配置受到独立 Host 校验，不能借普通字段保存修改隐藏设置。原业务模板与专属中英文词条已迁入同包封存资源，原位置只保留 Host 页面槽；共享控件继续复用。

实际浏览器已验证六类保存／重启、无 JavaScript、明暗／窄屏及原快照保留；说明 metadata、列表顺序／检索入口归属和诊断展示也已同包托管；仍需完成本批最终整体验收与跨 feature 收口；六类表单已迁移不表示 F-030 整体完成。不新增候选自生成或自动部署流程。

配置说明／示例及诊断表达使用同包 `steward.capability-presentation/1`。Manager 在实际诊断前固定一份完整词表；driver 返回真实结果与数量，Host 绑定计数并保留原证明。插件停用时，已有权威状态和已接纳证明仍可读取，展示缺失单独标记，新诊断不进入普通 native 路径。

设置页使用同一表单读取返回的 sealed HTML／copy 资源，维持原共享控件、五个入口、六类表单与自动保存。Host 仅传显示 DTO，不传原配置正文、管理器方法或其他页面状态；校验同源表单目标、版本／能力身份、MCP 原行引用及有限字段。候选编码也必须绑定所有者实际提交的凭据引用、路径、端点与工具权限，不能替换成另一个合法值。模板和词条可随同包换版／回退，已移除重复普通视图及专属原生字典，未知词条保留原文，不读取另一版本的词表。

能力设置保留普通保存按钮，浏览器绑定自动保存后收起该按钮。在保存期间撤回字段修改会按最新版本保存。
