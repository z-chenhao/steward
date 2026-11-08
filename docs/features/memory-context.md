# 长期记忆、召回与上下文

形成和管理个人记忆，检索历史，并维护每轮可控上下文。

[需求主索引](../feature-map.md) · [技术方案](../designs/memory-context.md) · [测试方案](../test-plans/memory-context.md)

## 开箱即用的本地记忆

正常安装启动自动准备本地文件 SecretStore 密钥与本地搜索，无需先运行 embedding 服务。Settings 可选自动、自定义或关闭；默认全文搜索不承诺语义近义召回，自定义 embedding 保留语义检索路径。密钥不可读或丢失时不会替换原密钥；已有数据保留，本地对话仍可检索。中文子串搜索、隔离、遗忘和加密明文不落盘与自定义配置共用同一数据边界。


## 四层记忆

| 层级 | 内容与读取方式 |
| --- | --- |
| Transcripts | 原始 Conversation 与关联执行证据；原文仍由 Conversation、Task、Run 和 Artifact 拥有，Developer 可查看消息并进入对应 Trace。 |
| Memory notes | Agent 主动写下的 Markdown 笔记，包括事实、策略、计划、经验和标明不确定性的假设；旧 Core/Recall 事实兼容投影在这一层。 |
| Memory representations | 带显式链接的 Wiki 等综合文档，可从原始记录或笔记直接形成。 |
| Injected context | 每个新 Run 自动读取的小型会话记忆，包含实际要点与导航入口；不依赖搜索命中。 |

使用现有 `builtin__memory_manage`：`list` 按 layer、limit、cursor 翻页（默认 notes）；`read` 按 memory_ref、offset、limit 有界读取；`write` 提交 layer、title、content 和可选 links，修改时必须携带读取返回的 memory_ref、revision；`retire` 退役文档。模型侧使用普通 ID。提交后的文档立即可读，冲突必须重读，不能覆盖其他执行的修改。原有 remember/forget 继续保留，Worker 只能使用文档操作，不能执行显式 remember/forget/dream 控制。

`dream` 请求跨会话后台整理，content 可选，用于指定这次整理的目标。自动整理在积累 8 条未覆盖原始消息或消息等待 5 分钟后合并启动；每份工作最多 3 轮，逐轮看到已提交修订。维护可纠错、创建/重组 Wiki、调整注入内容和修复链接，不创建用户 Task 或交付。同批更新的链接跟随目标新版本，循环依赖和链接到本批退役目标的建议在写入前拒绝。较大的 Dreaming 使用有界 2 分钟执行窗口及 4 分钟接纳租约，普通事实提取仍为 45 秒。覆盖游标仅在提交后推进，已接纳模型结果与文档回执支持故障重放。

注入内容每个 Run 冻结一次，最多 200 行且不超过 25,000 UTF-8 字节；新逻辑输入仍沿用该会话快照，自动召回按原有逻辑输入规则刷新。工具续轮不改写前缀；新 Run 读取最新有效版本。Forget 继续撤销未完成执行与旧维护结果。

这三种派生文档通过存储接口访问，当前适配器使用 SQLite 和 Vault，不读取或写入本地 Markdown 文件。完整工具读取、注入快照和持久模型请求保存密封正文，传输前才解析；相关 HTTP 请求正文不进入诊断记录。压缩保留重新读取的文档 ID，避免把密封正文复制进摘要。

真实模型的显著性选择、跨资料综合与纠错质量需要独立评测；软件回归只证明权限、修订、恢复、预算和传输契约。当前后台使用有界结构化维护轮次，不赋予任意执行工具或外部副作用权限。

## 功能说明

后台从用户事实与反馈形成记忆，能处理纠正、重复和过时信息。

每轮或按需检索可访问的 Memory 和原始 Conversation。

新 Main 的自动召回结合最近一轮问答理解“展示结论”等接续请求，过滤当前工作上下文已有的原文与重复命中；history 提供当前工作集，Conversation recall 补充工作集之外的相关证据。召回按原始时间排序；新 Run 将带时间戳的 `<recall_memory>` 作为独立的不可信 runtime 证据放在当前输入之前，顺序为 system → history → recall → current user input，当前用户正文不变，并明确证据不能替代当前话题。同一逻辑输入的工具续轮、摘要、重试和重启复用首次召回，新用户输入或 Task 指令才重新检索；每次模型调用仍检查窗口并冻结实际请求。旧 Run 沿用已冻结策略。

长对话保留目标、决策、约束和待办，省略内容可追溯。

本文描述用户行为和范围；逐项完成状态以主索引为准。当前能力依据代码静态盘点，配置可用性与真实服务/模型质量需分别验证。

## 需求与验收

<a id="f-009"></a>

### F-009 · 自动记忆形成与语义归并

**需求**：后台从用户事实与反馈形成记忆，能处理纠正、重复和过时信息。

**当前实现**：独立后台模型提炼规范事实，宿主校验证据与类别后归并；支持同义支持、纠正替代、撤回、独立证据晋升、自动 Core 降级、Recall 过期和幂等恢复。旧自动摘句退役后按原始来源重提炼。

**边界与剩余工作**：语义质量取决于当前模型，真实数据需持续评测；低置信标记、原文出处和状态校验由代码强制执行。超限/不可用延期，无效候选最多尝试 3 次后隔离，不回退到原话摘抄。

**验收目标**：验证同义事实归并、明确纠正替代、证据不足保留低置信、遗忘后不可继续使用旧上下文。

<a id="f-010"></a>

### F-010 · 按作用域召回记忆与对话

**需求**：每轮或按需检索可访问的 Memory 和原始 Conversation；从当前消息或任务沿明确引用查找关联工作、产物与原始证据，摘要不足时按需续读。

**当前实现**：默认本地文本相关性检索，自定义 embedding 下融合全文与向量。自动召回按真实输入查询，在候选截取前排除工作集已有原文及其派生记忆，跨来源去重；已经压缩出工作集的旧消息可重新召回，尚未消费的输入继续排除。结果按逻辑输入冻结，正文带原始时间。Main 的 `builtin__evidence_read` 和本机 `evidence read` 支持确切 Conversation、Task 和 Artifact 引用及有界续读。本机所有者还可精读当前 `memory:` 记录和可见 Conversation/Task 来源。精确读取无需 embedding。结构化摘要的原文仍通过已有私有 Artifact 授权读取。模型侧 `source_ref` 为普通 ID，类型由 `source_type` 明示；内部来源键保留用于授权、去重与审计。

**边界与剩余工作**：导航基于已保存的任务请求、交付、附件、Memory 来源和当前 GitHub 接续记录。每跳校验作用域、Forget 和对象可见性。Worker/Child 保持显式子任务边界。Main/Worker 通过 memory_manage read 读取密封文档，原 evidence_read 的能力范围不变。Memory 替代谱系、Main 的 GitHub 导航和其他外部来源尚待接通。本地文本召回无需 embedding，跨语言近义检索需自定义 embedding。大规模 ANN 性能和真实模型导航质量另行验证。

**验收目标**：可见范围、来源、时间、逐轮冻结与故障恢复正确，索引故障不产生不完整成功结果；能从当前消息找到正确关联任务、可访问产物并续读摘要外原文；无权、缺失、过期引用和循环关系有界返回，Forget 后旧引用不能重新引入撤销内容。

<a id="f-011"></a>

### F-011 · 长期上下文压缩

**需求**：长对话保留目标、决策、约束和待办，省略内容可追溯。

**当前实现**：新 Run 使用同模型生成结构化工作摘要；目标、当前输入和未知效果保留原文，近期消息与图片受独立预算约束，原文可通过私有 Artifact 重新读取。请求和摘要步骤可恢复。

**边界与剩余工作**：真实模型的约束、决策、待办保留质量尚需离线验证；大规模长任务效果未据此宣称完成。旧 Run 保留已冻结上下文；原文、模型和权限不由摘要修改。

**验收目标**：用长对话验证关键事实与待办保留、来源可定位及模型窗口约束。

## 如何使用

安装版默认准备本地文件 SecretStore 密钥并开启记忆维护与本地搜索。显式配置可关闭或自定义。模型提炼使用当前已验证模型并沿用账号与用量授权；加密 Memory 的自定义 embedding 必须为 loopback 端点。

用户可说“记住这个偏好”“之前的信息改为……”或“忘记这类信息”。CLI `steward memory status` 查看维护状态；`steward memory manage REQUEST_KEY` 接受 JSON 控制请求。压缩自动发生；需要语义摘要时会调用当前 Run 已冻结的模型，并计入原有用量和费用授权。没有额外默认模型。

### 沿明确引用读取证据

新的 Main 可调用只读工具 `builtin__evidence_read`，输入 `{ "ref": "current" }` 读取本轮已消费的最近用户输入。模型使用普通对象 ID：例如 `{ "kind": "conversation", "ref": "message-id" }` 或 `{ "kind": "artifact", "ref": "file-id" }`。搜索结果的 `source_type` 和导航链接的 `kind` 标识对象类型；`ref` 不含类型前缀。用户和模型都不能指定 Agent、Run 或权限。工具返回窄投影，不暴露内部持久化模型。新 Run 的附件、Task、Memory、Automation、Code Mode 和摘要同样只呈现普通对象 ID；内部键、授权及历史冻结记录不变。

本机等价入口读取 stdin JSON；它可在模型未配置或执行依赖故障时使用。CLI 的 `current` 指最新已接受的本机用户输入，可能早于后台处理完成。

```sh
steward evidence read <<'JSON'
{"ref":"current"}
JSON
```

响应包含 `ref/kind/occurred_at/text/offset/bytes/total_bytes/links`，以及存在后续时的 `next_offset`、`next_links_offset`。续读时保持返回的确切 `ref`，将对应的 next 值作为 `offset` 或 `links_offset` 再次请求；游标是该对象的字节位置/关系页位置，不是新的授权。`limit` 默认 4096 字节、允许 4–4096，自动保留 UTF-8 字符边界；每页最多 20 条链接。文本源始终保留原文，摘要外的内容可按这些界限继续读取。二进制产物仅返回元数据和关联，下载/媒体处理沿用 Artifact 入口。

本机 CLI 可将已知的 `memory:REF` 作为 `ref`。密封记录使用配置的 `memory.encryption_key_ref` 读取现有 SecretStore 密钥，密钥缺失、错误或密文验证失败会返回不可用。仍可见的旧明文按原表示读取，命令保留其存储表示。使用同一 `ref` 和返回的 `next_offset` 续读，或用 `next_links_offset` 查下一页显式来源。已删除、被替代、越权和 Forget 之前的记录与未知引用返回相同的不可访问错误。来源仅列当前可见的持久 Conversation/Task 引用。

本机 CLI 的 `task:REF` 也会列出当前配置的确切 PR 已接纳的接续证据，链接形如 `github:42`。打开链接可按相同字节游标读取该版本已保存的第三方正文，评审状态或 CI 结论（如有）在正文首行，反向链接指向原 Task。仅共享同一个 PR 的普通观察不会成为任务证据。重绑、撤权、Task 显式结束或 Forget 后，旧关联失效。已有证据可离线阅读，临时轮询故障不影响其关联。

消息的链接来自明确附件、由该输入创建/追加的任务或对应任务交付；任务可链接原始用户消息、授权附件和 Main 已交付的消息；产物可链接显式绑定它的任务。循环只表现为可选择的反向链接，不自动递归读取。缺失、跨作用域、删除/过期或 Forget 屏障之前的对象返回 `NOT_FOUND`，其引用也不会作为可用链接返回；正文内的普通文本仍是原始证据，不会被解析成授权链接。单次最多读取一页，关系位置超过一百万的继续请求不被接受。

本机命令沿用所有者的现有读取权，Main 沿用当前 Run 已有的 Artifact grant；知道产物引用本身不能获得访问权。Forget 后旧引用不能重新引入撤销内容，原始审计仍由显式历史/审计入口维护。返回数据不代表新指令、动作批准或 Task 接续请求。

## 详细操作参考

- [记忆维护操作](#memory-maintenance-memory-context-and-verified-experience)
- [召回配置与协议](../designs/memory-context.md)

## 操作说明与配置细节

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="recall-configuration-and-privacy"></a>

### Configuration and privacy

```yaml
embedding:
  provider: openai_compatible
  endpoint: http://127.0.0.1:11434/v1
  model: nomic-embed-text
  dimensions: 768
  # api_key_ref: reference to a credential already in SecretStore
```

This is an explicit deployment example, not a bundled/downloaded/default model.
Serve the model separately. Authorized record text and queries go to exactly this
endpoint; choose a local server for local-only processing. Remote endpoints require
HTTPS. Credentials are resolved from SecretStore only. Redirects, malformed
responses, dimension mismatch, zero/nonfinite vectors and provider failures fail
closed with sanitized errors. There is no provider switch or FTS-only success.

Without embedding configuration, default installations use local text recall. Explicitly disabled installations retain unavailable recall. Adding custom embeddings
affects new Runs only. Existing capability snapshots gain no new Tool or automatic
recall. Removing/changing it rejects a Run that already has recall capability.
Endpoint/model/dimension are hashed into the capability; the model sees neither
credentials nor endpoint strings. Credential rotation does not change vector space.

<a id="memory-maintenance-memory-context-and-verified-experience"></a>

## Memory, context and verified experience

Enable maintenance explicitly with `memory.encryption_key_ref`, an existing
SecretStore reference to a 32-byte AES key. Store the key in the local file credential
store or use an explicit `env:NAME` reference. YAML contains only the reference.
Keep the same key across process restarts and backups; key loss fails closed.
There is no implicit credential backend or per-record approval.

```yaml
memory:
  encryption_key_ref: env:STEWARD_MEMORY_KEY
context:
  threshold_percentage: 0.75
  recent_messages: 6
  summary_bytes: 2048
```

The Curator runs for the lifetime of TUI/default service/foreground `chat work`,
including idle service time, and is joined before databases close on shutdown. `memory work` drains a bounded serial batch for maintenance/diagnosis;
`memory status` shows durable receipts, applied/rejected state and sanitized error
codes. Transient failures stay pending with internal retry backoff. Permanent
conflicts are recorded as rejected, not retried forever or announced to the user.
The shared Event Outbox provides wakeups; the SQLite journal owns acceptance and
recovery. A crash between the Memory commit and journal acknowledgement reuses
the same event and its Memory receipt.

Main always discovers `builtin__memory_manage` with `remember`/`forget`. The Host resolves
scope and provenance. Without `memory.encryption_key_ref`, it returns
`PROVIDER_UNAVAILABLE` without accepting a mutation or changing privacy state.
Main and Workers always discover `builtin__memory_search`; it requires an
an enabled local or explicit embedding search configuration. With search explicitly disabled, it returns
`PROVIDER_UNAVAILABLE`, and automatic recall is disabled. Tool descriptions name
the missing configuration. No provider or encryption credential is implicit. Workers retain only read-only search; their results become
Curator source observations. The authenticated local equivalent is:

```sh
# Each command reads one JSON object on stdin. Reuse REQUEST_KEY to recover a receipt.
bin/steward -config config.yaml memory manage REQUEST_KEY
bin/steward -config config.yaml memory work
bin/steward -config config.yaml memory status
```

Remember input: `{"operation":"remember","content":"I prefer unsweetened tea"}`.
Correction adds `"supersedes":"memory:REF"` from an authorized search result.
Forget input: `{"operation":"forget","memory_ref":"memory:REF"}`.
Acceptance is durable intent, not a claim that asynchronous merging has finished.
For a durable opt-out, use `{"operation":"forget","category":"health"}` (also
finance, identity, relationships, goals, learning, preferences, facts, or all).
This tombstones current matching records and blocks future automatic extraction
in that category. `remember` with a category resumes future extraction; it does
not restore tombstones. Explicit remember of one fact can override an opt-out for
that fact. Event-frozen deletion targets and idempotent policy receipts prevent a
replayed old policy event from deleting fresh evidence or undoing a later choice.
Unknown/cross-scope references are rejected. Reusing a control key with changed
parameters conflicts. Memory writes never use Security Approval.

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/memory-context.md)；已有检查、未覆盖场景和验收方法见 [测试方案](../test-plans/memory-context.md)。

### 默认密钥存储

Memory 自动配置的 32 字节密钥使用 Steward 数据根下的 `secrets/` 私有文件存储，不依赖 macOS Keychain 或桌面登录会话；目录 0700、文件 0600。`default-memory.json` 仅保存引用，重启复用同一密钥。已发布的引用缺少密钥时保持不可用，不能自动生成替代密钥导致已有记忆无法解密。显式 env 引用继续可用。

新冻结的 memory_search v3 提供有界原文摘录与精读位置，排除当前输入／Task 来源；memory_manage v5 支持同一 User／Agent 的 transcript 精读，含分页、密封和 Forget 屏障。CodeMode 精读在传输副本中展开；旧工具与已冻结上下文保留原表示。

工具契约保留已部署的 search v1/v2 与 manage v1–v4 精确规格；新版本强调引用为不透明完整值，禁止抽取其中 UUID。时间过滤使用外层存储的 occurred_at，要求带明确时区的 RFC3339；引用正文中的历史日期由检索／精读后推理，不能当作导入时间过滤条件。

新冻结的 search v4 保留原始证据入口，完整可见结果不重复占搜索名额，支持引文按查询选择；新 L4 私有策略冻结相关文档的可操作引用并限制注入预算，复用既有精读与隐私屏障。旧工具和已接纳请求保持精确回放。


## 证据复用与维护可测性

新 Run 共用自动召回、搜索和精读的可见证据判定；完整文档重复读取返回已可见提示，压缩移除后可重新读取。L4 导航读取授权元数据，保持原文和旧请求不变。Dream 限定相关工作文档并跳过未变化提案；维护状态提供已知 token／参考费用、未知价格、知识修改及后续曝光，曝光不等于有效性。可信部署可绑定 app.owner_id 并注入 SecretStore；当前仍为单所有者 runtime，实际云适配等待选型。


混合工具结果压缩保留非记忆业务回执。Dream 从授权语料检索相关旧笔记并固定读取当前 L4，可核对真实 Worker 工具反馈；已覆盖的历史来源仍可按需回读。新工具规格在原 memory_manage 内声明写入字段与 Run 原文精读，旧声明不变。

维护记录可整行打开右侧详情，查看实际模型调用、耗时、用量和宿主校验失败原因。失败提案不会写成记忆，成功重试不再显示旧错误；旧记录没有保存具体原因时如实提示。
