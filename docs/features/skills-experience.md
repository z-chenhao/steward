# Skills 与经验沉淀

按需使用方法库，并把被验证的方法沉淀为可复用经验。

[需求主索引](../feature-map.md) · [技术方案](../designs/skills-experience.md) · [测试方案](../test-plans/skills-experience.md)

## 功能说明

Main 和 Worker 可发现可用方法，再按需读取正文和资源；Main 用于直接业务调用及委派判断，Worker 自行选择执行方法。

用户纠正、恢复失败或重复成功能进入候选、验证和晋升流程。

本文描述用户行为和范围；逐项完成状态以主索引为准。当前能力依据代码静态盘点，配置可用性与真实服务/模型质量需分别验证。

## 需求与验收

<a id="f-022"></a>

### F-022 · 受版本控制的 Skill 读取

**需求**：Main 和 Worker 可发现可用方法，再按需读取正文和资源；Main 用于直接业务调用及委派判断，Worker 自行选择执行方法。

**当前实现**：Main 和 Worker 的受控目录 list/view 与全目录指纹冻结已实现。system 不预载 Skill 目录；Main 和 Worker 通过 `skill_list` 按名称或用途搜索，正文和资源按名称读取。搜索默认返回 8 条，可用 limit 调整至 1–128 条；Host 自动绑定冻结版本，模型只接收名称和用途，Skill 不扩大角色工具权限。

**边界与剩余工作**：普通 Main/Worker 不管理目录；变化不热替换旧 Run 的冻结 Skill。

**验收目标**：验证资源范围、版本漂移拒绝、二进制资源引用与上下文按需加载。

<a id="f-023"></a>

### F-023 · 执行后的自动经验闭环

**需求**：用户纠正、恢复失败或重复成功能进入候选、验证和晋升流程。

**当前实现**：可信 Reviewer 有 propose/verify/promote/reject/retire；目前由 CLI 显式驱动，缺自动触发与编排。

**边界与剩余工作**：接入纠正/结果事件与可信 Reviewer；当前验证证据限于经审批的 bash 精确测试命令，需明确其他方法的证据标准。

**与插件自进化的关系**：本项负责方法经验学习；[F-070／F-072](plugins-evolution.md) 分别负责功能插件托管及候选验证、审计、激活与回退。现有 Reviewer 不代表插件可自行修改、验收或部署，也不替代独立 Guardian。

**验收目标**：从真实纠正或执行结果自动形成候选、按证据验证并可再次调用；普通 Worker 仍不能随意改 Skill。

## 如何使用

配置受控 Skills 目录后，Agent 按需 list/view 使用已有方法。操作员可经 `steward skill manage` 从 stdin 提交 Reviewer 请求。

当前不会仅因一次任务成功就自动形成可复用 Skill；F-023 的触发、证据和晋升编排尚未闭环。

## 详细操作参考

- [MCP、Skills 与工作区约定](../designs/integrations.md)
- [记忆维护操作](memory-context.md)

## 操作说明与配置细节

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

The Experience Reviewer is a trusted local control workflow. Ordinary Main and
Worker Skill capabilities remain list/view. `skill manage` reads a JSON operation:
`propose`, `verify`, `promote`, `reject`, `retire` or `list`. A proposal includes
`name`, `description`, a short reusable `procedure`, `reason` (`correction`,
`recovered_failure`, `success`) and accessible `source_refs`.

Verification supplies a `candidate_ref`, current `version` and `verification_ref`
(`invocation:REF`). It must resolve to a successful real builtin__bash invocation with the exact accepted command
in a completed authorized Run, reporting exit_code=0, timed_out=false and untruncated output for `go test -race ./...`.
Ordinary success requires two independent Runs; correction/recovered-failure
candidates require one. Correction candidates require an original user source;
recovered-failure candidates require a failed Run source. Later independent
verification Runs add their own provenance without rewriting the candidate.
The procedure is immutable and binds all verification
records. Promotion is impossible before that gate. Publication is staged, protects
existing files and is recoverable if the process dies before the status commit.
Retirement retains an audit copy. New catalog versions affect new Runs; existing
frozen resources fail closed on drift. Raw logs and secrets are rejected as Skill
procedures. Mechanical evidence validates the test provenance, not the semantic
quality of the written method; model-quality review remains a release check.

## 实现与验证依据

代码入口、最新架构与取舍见 [技术方案](../designs/skills-experience.md)；已有检查、未覆盖场景和验收方法见 [测试方案](../test-plans/skills-experience.md)。

方法库发现／资源入口已迁入独立策略包，冻结文件与权限仍归 Host；设计边界见[插件设计](../designs/plugins-evolution.md#已提取的方法库边界)。相关回归为 `TestSkillPlugin*`、`make test-prompts` 与 `capabilities` 实际分发包场景。经验 Reviewer 的原始证据、验证和晋升不因此开放给普通插件。

经验配方已迁入私有独立服务；候选原始方法、版本、可信最低门槛与发布归属仍由 Host 持有。组合固定、升级回退与拒绝降低门槛的边界见[插件设计](../designs/plugins-evolution.md#已提取的经验配方边界)，回归为 `TestExperiencePlugin*` 与 `memory-maintenance` 真实 Reviewer 场景。已授权纠正的自动入口与独立来源门槛见本页，重复成功和恢复失败的自动编排仍待实现。

### 本机浏览器示范

打开「开发者 → 浏览器示范」，把录制链接拖到浏览器书签。在本地样例页（localhost、127.0.0.1、[::1] 或本机文件）打开书签，点击开始，演示操作，最后点击停止并保存。每次录制仅覆盖当前页面。书签保存带标签的控件动作，不保存网址、网页正文、输入值、密码或会话。

在示范页面导入录制文件、命名草案，逐行编辑动作与标签。单独录制第二个本地样例并导入，复验成功后可下载私有 `SKILL.md`，由操作者自行决定是否放入已配置的私有技能目录。复验模拟第二次录制的状态转移，不联系目标网页。请先检查所保留的标签；它们会发给当前 Steward 实例。浏览器书签需要允许在样例页运行脚本。

### 从明确授权的纠正形成候选

在本机 Web Chat 或 CLI 对话中输入 `/learn-correction 方法键`。第二行写用途，其后每行写一个希望复用的步骤（2–16 步）。方法键使用小写字母、数字或短横线。这个标记仅授权学习本条消息中的步骤。再次遇到同一方法时使用同一个键和当时的纠正内容。不同 Main Run 的两次授权纠正会原子地生成一个私有候选；同一 Run 的后续输入和同一输入的重试只算一次。普通聊天与第三方渠道不会触发收集。

示例输入：

```text
/learn-correction release-check
发布前验证实际改动。
核对待发布文件和需求。
运行相关回归并检查原始输出。
```

用现有 `steward skill manage` 和 `{"operation":"list"}` 查看队列，`view` 加候选引用和版本可读方法及原始 Conversation 引用。用同一引用和当前版本 `reject` 可拒绝，后续同键报告不会重新打开。验证与晋升分别使用原有 `verify` 和 `promote`，仍须独立的成功执行证据。
