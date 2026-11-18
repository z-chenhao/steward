# 自动化与安静监控：测试方案

[功能与验收目标](../features/automation.md) · [技术方案](../designs/automation.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 范围与证据

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-015](../features/automation.md#f-015) | 验证时区、重复触发、修改/取消竞态、停机补偿及重启不重复工作。 | 保护已接通的限定范围；Live/配置另行验证 |
| [F-016](../features/automation.md#f-016) | 验证跨入、持续在内、跨出再跨入、旧 fire 及缺失观察值的行为。 | 保护已接通的限定范围；Live/配置另行验证 |
| [F-045](../features/automation.md#f-045) | 确切 PR 类型和 CI 失败筛选验证关闭/观察/自动、命中/未命中、代次变更、撤权、重复接纳和重启。观察仅记录判断，无模型调用、执行 Task 或外部动作。 | 确定性筛选已接通，其他来源、语义误接单/漏接单及通知噪音需独立评测 |

## 层级与关键路径

覆盖时区、DST、到期补偿、并发调度、计划 CAS、旧 fire、取消与 Forget。阈值用受控观察序列验证安静/通知切换；真实数据源连接在 F-025 单独验证。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

## 已有检查与缺失覆盖

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_automation.py](../../tests/system/verify_automation.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [internal/agent/sqlite/github_rule_test.go](../../internal/agent/sqlite/github_rule_test.go) | 真实 Store 轮询事务和 Actor 验证同候选三模式、显式自动同 Task、CAS、旧代迟到与去重、重启、历史 Task/作用域/Forget、分页与 schema 37 迁移 |
| [internal/agent/sqlite/github_failed_check_rule_test.go](../../internal/agent/sqlite/github_failed_check_rule_test.go) | 成功或异常结论不触发失败规则，观察命中无执行，自动失败仅继续原 Task 一次。规则改动保留冻结决策，旧代迟到/重试不复活，schema 40 中已排队的类型匹配可继续 |
| [internal/agent/sqlite/github_followup_test.go](../../internal/agent/sqlite/github_followup_test.go) | 规则修订及已有授权撤销后拒绝迟到模型和 Tool，保留现有 Task 状态和未知副作用约束 |
| [cmd/steward/github_test.go](../../cmd/steward/github_test.go) | 真实 CLI 与受控 GitHub HTTP peer 验证绑定观察、认证轮询、持久决策、分页及故障时关闭，覆盖非法 CI 筛选和同一检查的成功/失败版本 |

现有入口仍需在后续相关实现变更时按风险运行；需求范围变化时同步扩展场景。

## 详细测试入口与证明边界

[调度隔离回归](../../internal/agent/sqlite/plugin_schedule_scan_isolation_test.go)验证撤权后新计划恰好触发一次、单个冻结包缺失时其他工作继续，以及恢复原包后的首个扫描仍接纳原 fire。

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="automation-verification"></a>

### Verification

```sh
make build
python3 tests/system/verify_automation.py --binary bin/steward --evidence evidence/automation.json
go test -race ./internal/automation ./internal/agent/sqlite -run 'TestAutomation|TestCalendar|TestRejectMalformed' -count=5
```

The reusable harness owns isolated Fake profile setup, CLI/model Tool ingress,
fixed clocks, state/Tool/snapshot/delivery readback, actual SIGKILL at four
fire/Outbox/admission boundaries, restart and stable duplicate drain. It joins
children and deletes disposable state. The system runner selects it; repeat count is explicit. `tests/fixtures/automation-v1.json` pins GC-02/14 scheduling fragments;
GC-08 covers reminders and delayed cancellation. These are deterministic L0/L1
protocol checks. They do not award full Golden Case semantic/model scores or
establish channel or full M7 hardening acceptance. See [channels](../features/channels.md)
and [operations](../features/operations.md) for those implementations and their own evidence.

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

## Web 手动计划回归

`automation_owner_test.go` 覆盖真实 SQLite 的创建／修改／停用、版本冲突、重复收据、相同 key 的载荷漂移、所有者与撤销意图边界、未接单 fire 失效及已接单指令／快照不变。`schedules_test.go` 覆盖六类友好频率、阈值空值／0、日期时区、DST 空缺／重复、已有精确时刻往返及超限中文草稿保留；真实 HTTP 适配测试验证创建与纳秒级单次编辑重复 POST、原始时刻证明篡改拒绝。

维护中的 `channels_browser.mjs` schedule 场景验证原生表单创建、修改、停用／恢复、两页冲突保留输入、重复请求与漂移拒绝、保存未来安排不派发 Task、无效时间反馈和320px布局。不新增 runner 或 CI gate。既有 automation 系统入口继续保护 CLI/Main、调度与重启恢复；结果留在对应运行的 evidence。

日历方法已接入 Web 所有者、CLI、Main 与原图扫描；整个功能的视图／声明余项仍待完成。回归核对原 cron／时区／DST／闰年／日与星期 OR／最后星期样例；正式接线须证明 Main、CLI、Web 所有者与扫描的真实调用、事务外计算后的版本／隐私重检、原 request key 重放、旧计划／fire／Run 保留及独立升级／回退／停用。创建独立目录或 manifest 不计为迁移完成；默认切换同批检查旧实现清理。

Web 所有者接线使用独立实现改变下一次时间，直接断言数据库原图／实际时间，以排除 native 代理；验证原回执在停机后可重放、新请求不能 native 回落、停用控制保留原图。schema73 前向迁移必须保留原计划、fire 和回执字节，旧图保持 NULL；图变更没有 schedule_version 递增必须拒绝。扫描已按保存的原图计算；CLI／Main 的临时拒绝分支已随接线删除。

扫描使用原计划图而非当前选择。验证真实 Deactivate 后的首次扫描保留原图／原 fire／新 next、同次扫描不重发、实际所有者停用发生在纯计算后时不写回旧结果；Guardian 在接纳前停机时 fire／next／outbox 均不提交，真实恢复后的首次扫描不重新 select。真实 Forget 发生在计算后时拒绝原 fire，随后不得再次向候选释放旧意图。

前向迁移同时检查原 fire 的图保持 NULL、已接纳 fire 的图不可更新；扫描写入的图须与原计划图相同，配置更改后仍保留其来源。

CLI 迁移回归须使用独立版本的真实隔离方法证明创建与更新采用候选结果及原图；停止生产者后原 key 重放保持原回执，取消仍可执行且保留原图。迁移前 CLI 的 Forget 行为保留历史解释，已迁移编辑须在候选调用前验证隐私与原版本。

Main 迁移回归通过真实 Actor／模型／工具调用：在模型返回前实际停用当前图，随后第一次依赖操作必须使用 Run 的原冻结图，保存独立插件结果和原 action 收据；不得重选或修补状态。

方法独立演进回归：独立隔离版本接受旧算法不认识的表达式，Web 禁用创建／编辑、CLI 及 Main 均须采用该方法，排除原生预验证。禁用配置的非法表达式被方法拒绝；停止方法后未修改的本地停用可执行，修改业务字段仍被拒绝。

生命周期使用独立1.0.1／1.0.2方法、正式验证激活与回退，然后实际 Deactivate。首次扫描同时检查原生旧计划、两个版本计划与回退后计划的各自 next／fire 来源，原回执不重算；不得重选。历史 Main 在模型返回前激活插件，仍须保留原服务快照和原生日历解释。默认32包的既有 Automation 系统门禁继续覆盖13个恢复／隐私／配置场景；最终完整验收须在所有余项完成后使用固定源码与二进制重新运行。

本地时间方法覆盖上海正常分钟、纽约 DST gap／fold 拒绝与 fold 后正常分钟、无效日期／Local，以及附带私有字段和输入引用拒绝。Owner 用固定正常／gap／fold 预期跨真实隔离进程验证；实际保存回归使用独立方法接受旧算法不认识的时分输入，核对保存 Spec、next 与同一图；验证 stopped producer 后原始输入重放、同 key 漂移拒绝、gap／fold 拒绝和本地停用。表单解码回归只断言未解析输入保留，精度匹配旧回归继续保留。

跨输入格式回执兼容：先真实创建旧 Spec 格式安排，再激活独立方法并停机；以新本地分钟格式重试原 key，必须立即返回原回执，原计划仍为 NULL 图、原 Spec／版本不变。变更时分必须冲突，不得咨询已停止的候选或写回转换结果。

本地分钟接线后的真实 Web 验收继续使用既有 channels 的 task-controls 场景，覆盖 gap 错误保留草稿、修正后保存、手动启停／重放／冲突、320px和无脚本路径。不得用方法单元测试代替此 HTTP／浏览器链路。

周期方法覆盖日／工作日／周／月及无效星期、日期、时分。真实独立方法令08:31输入生成09:30表达式，核对 Spec／next／图、配置读取仍为原输入；停止方法后重放与原样停用保留全部事实，改动禁用配置不能绕过生产者。真实 Forget 后新原始频率字段也不可读。另验旧 Spec 格式回执的新频率输入重试与 NULL 输入旧配置的本地停用。schema74→75不更改原 Spec／next／版本，不补造原始输入，未递增 schedule_version 的输入修改须拒绝；共享系统 schema 期望同步到75。

精确时刻匹配回归使用独立方法把08:31编辑输入识别为原08:30:45.123456789+00:00，断言保存完整原文及实际原输入，排除 Host 原生匹配；停止生产者后重放与停用不重算，伪造 original Spec 冲突，真实 Forget 后本地事实不可读。旧精确格式编辑回执在输入升级与停机后仍重放原结果。Owner／方法样例检查两种 fold 偏移及较长合法小数字符串原文保留。schema75→76不补造本地输入、不改精确 Spec／next／版本，无新版本的事实变更拒绝，共享 schema 期望同步76。

直接 Spec 投影验证既有单次／日／工作日／周／月／custom 字段。真实独立方法把日 cron 选择为等价 custom 编辑方式，核对管理入口保存该选择；实际 Stop 后首次读取／停用不调用候选，损坏声明新 editor 服务图的必需事实应拒绝读取，不能变成历史 fallback。真实旧版包不声明 editor，Main 在模型返回前实际升级新包，后续工具操作仍保存原图／原 next／NULL 编辑事实，快照不补写；不得靠手工修补快照证明兼容。

展示方法核对现有六种频率和中英文时区日期格式。独立方法改变具体 cron 的两种语言标签，核对实际业务保存／读取采用候选值、真实 Stop 后仍可读且原样停用不改展示。真实 Forget 隐藏展示；声明 display 的新图丢失展示字段须拒绝，旧图／旧 Run 则保留明确 NULL 兼容。真实 HTTP Task 页面验证语言切换及文本转义，不把候选字符串当 HTML。schema76→77不改原计划字段、不补造展示，未递增 schedule_version 的展示变更拒绝；共享 schema 期望为77。

调度 Task 标题必须通过真实 task-control 独立1.1方法得到带版本前缀的标题，原 anchor／instruction 不变，fire 标题来源与新 Root 图相同，且不调用模型。纯准备后实际 Guardian Stop 不得部分提交；实际 Resume 后首次 admission 保留选择并成功，已接纳 admission 在生产者停止后不重算。实际升级发生在准备与接纳之间须冲突，未接纳的下一次纯尝试采用真实新选择。真实 Forget 在准备后导致原 fire skipped，无 Task／标题；停用方法后新 fire 不得原生回退，但本地取消仍可执行。schema77→78保留旧 fire 字节并保持新来源字段 NULL，接纳后的标题／版本不可更新；共享 schema 期望为78。

指令摘要用独立方法产生不同前缀，真实保存后停止生产者，管理列表仍须立即返回已接纳摘要，原 instruction 字节不变；真实 Forget 后仅最小元数据可见。新图摘要缺失不得回落原 formatter。默认方法保留既有80 rune 摘要边界，拒绝无关私有引用。另以正式验证／激活精确回退 Task 原图，验证新调度 Task 使用回退标题，而先前 Task／fire 来源和标题均保持升级版；不以重编同版本包替代精确回退证据。

工具说明通过真实 Main 的 model→tool→continuation 验证独立版本文字、固定 schema／权限／replay／版本与内容哈希；中途升级不改原 capability 字节。正式精确回退后，回退前排队的 Run 仍用升级版，新 Run 用原版本；撤回后新说明为空且固定控制仍在，旧快照不变。冷说明准备后真实 Forget 必须由原能力 admission fence 拒绝原 ingress，Run 和原私有 conversation intent 不得复活。最终验收包含 test-prompts 及完整跨模块门禁，不能用文字服务单测替代原请求／回执冻结证明。

默认配方通过真实隔离1.0.1／1.0.2方法与 HTTP 新建页面验证：升级改为工作日09:15／上海时区，正式精确回退恢复每日08:00／UTC；升级版配置仍保留09:15配方。实际 Stop 后首次旧记录读取和页面打开不启动候选，新草稿拒绝；实际撤回仍拒绝新草稿且旧页可读，不产生 Task／模型调用。新图缺失配方须拒绝，不能走历史默认值。原有控件／无脚本／显式保存和完整系统门禁继续适用。

### 阈值迁移验证

`TestThresholdEntrySequenceAndLateObservations` 验证首次进入、持续在内、退出再进入和迟到观察保留原状态；`TestThresholdBoundsAndPrivateFacts` 验证零／相等／上限、越界拒绝及不接受 fire 身份或候选通知指令。纯函数回归不能替代实际接线验证。`plugin_schedule_threshold_test.go` 使用真实计划／fire／Root／Generation／ToolInvocation，验证原图与独立算法、升级、正式精确回退及撤回后的旧 Run、停机后相同值重放／异值拒绝、Guardian 提交前停机与恢复后的首次操作、真实 Forget 和所有者配置变化。受保护的观察接纳不得依赖测试额外修复选择或版本；完整验收还必须覆盖真实默认32包分发、实际 Web 管理与 Automation 系统流程、`make test-prompts` 和 `make test lint`。

`TestScheduleThresholdGuidanceFreezesRealRootRequestsAcrossLifecycle` 经真实 Actor 的模型→observe→模型续接检查独立 Root／工具说明、原消息字节前缀、固定工具权限及原事实。中途升级、正式精确回退、撤回均不改旧 Root；新 Root 缺方法时拒绝，本地取消不依赖候选。历史 description-only 元数据图生成的旧 Root 在激活新策略后仍用原文案与判断；迁移后的新 Root 不能借旧图恢复 native。`TestScheduleThresholdColdRootGuidanceForgetDoesNotResurrectIntent` 在实际元数据准备后 Forget，断言零 Task／Root／指令／模型释放，并在第一次后续 admission 将原 fire 标为 skipped。

Schedule 的 Main／Root 指引与阈值冻结回归纳入 `make test-prompts` 的现有方法／Task 分组；CI 回归保证新增同族用例仍在必跑入口中，普通全量测试继续逐根覆盖。
