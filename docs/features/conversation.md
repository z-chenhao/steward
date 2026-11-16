# 自然语言对话与共享循环

统一处理用户对话、工具续轮、追加输入和最终回复。

[需求主索引](../feature-map.md) · [技术方案](../designs/conversation.md) · [测试方案](../test-plans/conversation.md)

## 功能说明

用户能连续对话、追问和纠正要求。Main 负责理解、协调和交付，可通过 Code Mode 完成聚焦 MCP 业务操作，复杂工作委派给 Task Worker。

本文描述用户行为和范围；逐项完成状态以主索引为准。当前能力依据代码静态盘点，配置可用性与真实服务/模型质量需分别验证。

## 需求与验收

<a id="f-003"></a>

### F-003 · 可恢复的 Main 对话

**需求**：用户能连续对话、追问和纠正要求。Main 使用控制、记忆、证据、交付和 Skill 工具，通过 Code Mode 调用已配置 MCP 完成聚焦查询和单步变更；复杂工作由 Task Worker 执行。Main 使用工具期间，用户仍可交代、查询和纠正要求，并在明确的执行边界得到响应。

**当前实现**：Actor 管理输入与执行权，共享 Loop 保存模型交换、工具调用和回复。新 Main 的工具有 200 ms 同步窗口；窗口后仍未完成且有新输入时，Host 保存待交付关系、返回 pending 回执，并在同一个 Main 中处理新要求。实际结果持久化后以 runtime 消息回到原 Main。后台 Task 仍独立并行，Task 查询和控制不占用数据工具的执行槽。

新 Main 使用简短角色、`<tools>` 和 `<rules>`，优先通过 UI 表达对比、选项、计划、解释和结果，简短回答与确认仍用文字；授权边界并入规则。Root Worker 与 Subagent Worker 也使用 tools/rules，共享执行与授权规则；Root 对整个 Task 的执行、汇总和阶段进度负责，Subagent 只向直接父级返回显式子任务的结果。三者在 Agent 入口各拼成一条 system message。Worker 首次执行前收到自己的实际 cwd 与工作区内可读的 AGENTS.md/README.md 入口，不复制父级运行时上下文。文案只对新建 Run 生效，已有冻结上下文不重写。

**边界与剩余工作**：pending 只表示最终结果尚未交付，不代表完成或批准，也不创建 Task。多步、耗时、批量、编码和浏览器/桌面工作由 Main 显式委派；Task 暂停/取消使用原控制面，operation_ref 使用普通 ID，只关联结果，不是通用后台作业或取消 API。已派发调用不承诺即时撤回，副作用不明仍保持 unknown。旧 Main 保留其冻结交付协议，新协议只对新 Main 启用；后台结果不改写已接受的模型请求。200 ms 窗口与 50 ms 扫描是宿主默认值，不是实际模型响应 SLA；真实模型的延迟与理解质量需独立验证。Web 实时更新和语义压缩分别见 F-019、F-011。

**验收目标**：输入去重、Steering、工具续轮、单 Main 所有权和回复恢复正确；慢工具仍在运行时可先回复新问题，之后交付原结果；工具容量占满时仍可查询和控制 Task。审批等待后的新输入不复用旧批准；中断恢复复用已完成结果，unknown 不重试；原生调用和 pending 回执都保留正确关联与来源，模型切换不改变当前 Run。

## 如何使用

启动 `steward` 后直接输入需求。命令行可将文本经 stdin 交给 `steward chat send REQUEST_KEY`，再由 `steward chat work` 推进；`steward chat history` 读取语义对话。

用户通过自然语言追加背景或纠正要求。工具等待期间仍可发送新问题；Main 先回应当前要求，原工具结果随后回到同一对话。耗时或需要独立 Worker 的工作由 Main 显式建立 Task，参见任务功能。升级到 schema 32 前备份数据根；旧 Run 不自动切换工具交付协议，旧二进制不能打开升级后的数据库。

## 详细操作参考

- [对话 CLI 与恢复细节](#main-loop-main-loop-cli-and-recovery)
- [安装与部署详情](installation.md)

## 操作说明与配置细节

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="contract-1-产品定位"></a>

### 1. 产品定位

Steward 是一个通用私人助理，目标能力对标 Muse、dots、Manus Cue、Grok Bot、Hermes Agent，并参考 Pi 1.2.0 的 Agent Harness 设计。

<a id="contract-11-当前阶段"></a>

#### 1.1 当前阶段

- 一期只做好一个 Agent。
- 一期采用本地部署，不依赖云端运行时。
- 首次启动必须完成模型连接与主模型选择；一期同时支持本地 OpenAI-compatible 模型端点和 OpenAI “Sign in with ChatGPT” 订阅登录。
- Agent 核心由宿主 Go Host 执行；需要桌面能力时，在与宿主隔离的 macOS Guest 中安装和操作应用，由 Computer Use 或配置的工具访问。
- 一期采用模块化单体；模块边界和协议提前抽象，以便未来拆分为云服务。

<a id="contract-12-长期形态"></a>

#### 1.2 长期形态

- 一个用户可以创建多个 Agent。
- 多个 Agent 彼此平等，均由用户直接调用，不设置默认的上下级关系。
- 本地与云端使用相同的核心协议和接口，仅替换底层 Provider 与部署组件。

<a id="contract-13-用户交互原则"></a>

#### 1.3 用户交互原则

- 用户与 Agent 的主要交互永远是自然语言对话，而不是任务管理页面。
- Web 用于用户在电脑前交代和查看工作。
- TUI 用于研发、测试与调试。
- 飞书是未来便携下达任务和接收通知的主要渠道，默认通知到飞书，但可配置。
- 用户只感知 Agent 是“空闲”还是“忙碌”，以及当前有哪些持续进行中的工作。
- 用户通过对话追加指令、暂停、恢复或取消工作；进度页面只提供读取能力。
- 原始模型调用、工具调用和工具返回轨迹仅对研发调试开放。

<a id="main-loop-main-loop-cli-and-recovery"></a>

## Main Loop CLI and recovery

Main and Worker use one shared Agent Loop. Set up and explicitly select a probed local
or ChatGPT account profile using [the provider commands](model-connections.md).
Then use the same configured data root for all commands:

```sh
printf '%s' 'Hello Steward' | bin/steward -config local.yaml chat send greeting-1
bin/steward -config local.yaml chat work
bin/steward -config local.yaml chat history
```

`chat send [REQUEST_KEY]` reads up to 16 KiB of text from stdin. It returns a
durable acceptance receipt, not a model answer. Omit the key to generate a fresh
one; retain a supplied key when retrying an uncertain ingress response. The same
key and content return the original receipt, including after Run completion.
Changed content under that key fails closed. Unconfigured/unavailable profiles
close ingress. No implicit model or Fake is selected.

`chat work` drains accepted work in the foreground. Run it in one terminal while
other terminals submit steering. It waits for a live ownership lease to expire
or its owner to finish; it never runs a second Main concurrently. When currently
accepted work ends it exits. Invoke it again for later inputs. The default
no-command startup opens the [TUI](../test-plans/product.md) on a terminal; redirected
stdin retains diagnostics/wait behavior. The TUI uses the same Actor and
executor, including recovery and Steering while Main is busy.

`chat history` returns ordered semantic user/assistant messages. It also fills
missing accepted Conversation intents before reading. This is an intentional
recovery operation, not a read-only SQLite diagnostic. Tool calls/results and
partial model text appear only in Run context/RunTrace. A failed model produces
a durable failed Run and a safe CLI error; the Host does not retry it. New user
input creates a new Run after the old one ends.

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/conversation.md)；已有检查、未覆盖场景和验收方法见 [测试方案](../test-plans/conversation.md)。

### 连续请求

新 Main 按输入顺序处理并分别提交回复。需要后台工作时，Main 可先创建 Task 并告知用户，然后处理下一条输入，Worker 独立执行；后到消息不会撤销较早请求尚未提交的回复。旧冻结 Main 沿其原接纳政策恢复。

Main 沟通／工作方法已接入独立版本化 `conversation` 包并进入标准分发。Host 保留身份、安全 Section、输入与冻结上下文、Task／交付事实；Root／Child 不消费 Main 方法服务。独立换版、已排队版本保留、方法回执后 ContextReady 失败恢复与工具续轮前缀见[Main 插件边界](../designs/plugins-evolution.md#已提取的-main-对话边界)及[插件验收](../test-plans/plugins-evolution.md)。`make test-prompts` 包含相应上下文回归。

Main 的沟通／工作方法由冻结的 `conversation` 包提供；实际关闭并重开 Store／Catalog 后，排队 Main 仍使用原退役版本，停用不复活原生方法。身份、安全、原消息与单次交付仍归 Host；历史 prompt 消费者与删除条件见[插件设计](../designs/plugins-evolution.md#maintask-的历史消费者与冷恢复边界)。
