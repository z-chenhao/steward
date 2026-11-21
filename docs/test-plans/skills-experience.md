# Skills 与经验沉淀：测试方案

[功能与验收目标](../features/skills-experience.md) · [技术方案](../designs/skills-experience.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 范围与证据

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-022](../features/skills-experience.md#f-022) | 验证资源范围、版本漂移拒绝、二进制资源引用与上下文按需加载。 | 保护已接通的限定范围；Live/配置另行验证 |
| [F-023](../features/skills-experience.md#f-023) | 从真实纠正或执行结果自动形成候选、按证据验证并可再次调用；普通 Worker 仍不能随意改 Skill。 | 必须通过目标入口补齐闭环；不能只直接调用底层方法 |

## 层级与关键路径

`TestMainBusinessToolsDelegateWorkerExecution` 验证 Main 和 Worker 均保留搜索与读取工具，目录与正文均按需返回。`TestOldMainCapabilitySnapshotSurvivesTaskRegistryUpgrade` 验证历史 Main 能力保持冻结，新 Main 才采用当前策略。系统回归通过 Main 读取真实目录及正文，通过 Worker 读取目录、正文和二进制资源；两者均无普通 Skill 管理权限。

读取覆盖目录版本与资源漂移；经验闭环从真实纠正/执行结果开始，不直接调用 Reviewer 伪造自动沉淀。晋升要绑定同方法的验证证据，并检查普通 Worker 无写权限。

`TestSkillsUseSearchWithoutPromptCatalog` 验证 Main/Root/Child 的 system 不包含目录，并通过实际 Skill 搜索和按名称读取正文。`TestSkillSearchRanksBoundsAndFreezesResults` 覆盖中英文名称/用途匹配、默认与显式数量上限、空结果、非法参数及新发布方法不进入旧冻结目录。`intelligent-ui` 系统门禁经真实 Code Mode 搜索、读取 Skill 后展示 UI。目录适配器回归验证文件变化和旧快照选择被拒绝；显式传入 version 也被拒绝。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 已有检查与缺失覆盖

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_capabilities.py](../../tests/system/verify_capabilities.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [tests/system/verify_memory_maintenance.py](../../tests/system/verify_memory_maintenance.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |

- **F-023 剩余覆盖**：接入纠正/结果事件与可信 Reviewer；当前验证证据限于经审批的 bash 精确测试命令，需明确其他方法的证据标准。

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

方法库发现／资源入口已迁入独立策略包，冻结文件与权限仍归 Host；设计边界见[插件设计](../designs/plugins-evolution.md#已提取的方法库边界)。相关回归为 `TestSkillPlugin*`、`make test-prompts` 与 `capabilities` 实际分发包场景。经验 Reviewer 的原始证据、验证和晋升不因此开放给普通插件。

经验配方已迁入私有独立服务；候选原始方法、版本、可信最低门槛与发布归属仍由 Host 持有。组合固定、升级回退与拒绝降低门槛的边界见[插件设计](../designs/plugins-evolution.md#已提取的经验配方边界)，回归为 `TestExperiencePlugin*` 与 `memory-maintenance` 真实 Reviewer 场景。多次标记的纠正已有自动入口，恢复失败和重复成功的自动编排仍待实现。

### 从本机示范生成私有草案

参考 Grok Bot 的示范教学。操作者在本机样例明确启停两次，修改步骤后看见不匹配的位置，恢复正确顺序后下载私有技能。Steward 的复验只消耗第二份样例的状态转移。运行 `tests/system/verify_channels.py` 的 Web Channels 场景（`browser_demonstration`）调用 [Chromium 驱动](../../guest-browser/demonstration.mjs)，检查密码与输入值均不在录制文件、同一次录制被拒绝、编辑后重新校验、下载正文及目标服务器的请求计数；留存桌面／手机与明暗截图。托管 Owner 用例另测错误终态，`TestExperiencePlugin*` 验证包真实升级、回退和停用。

### 独立授权纠正

`TestExperienceAuthorizedCorrectionsBecomeOneRejectableCandidate` 经实际 Main 入口提交普通输入、同键重试、同一 Run 的第二条输入和另一 Run 的纠正。检查候选引用原始两条证据、拒绝未验证晋升、CAS 拒绝后不再创建、停用后拒绝新学习且旧输入仍可读回。memory-maintenance 系统门禁复验真实 CLI Chat 和本机 Reviewer 列出、查看、拒绝自动候选。
