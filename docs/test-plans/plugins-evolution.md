# 功能插件与受控自进化：测试方案

[功能与需求](../features/plugins-evolution.md) · [技术方案](../designs/plugins-evolution.md) · [协议](../designs/steward-rsi-plugins.md) · [需求主索引](../feature-map.md)

## 范围与证据

覆盖 F-070／F-072 的 0.1 协议基础：Go SDK、真实隔离进程、不可变包和组合、Run 绑定、独立验证、事务激活、调用结果与生命周期事件。DOCX／OCR 已接入独立业务包，其他功能尚未整体迁入插件，自动生成代码、质量监测、自动回退与跨 Run 状态迁移不由这些基础测试代替。

主要入口为 [SDK 协议回归](../../pkg/rsiplugin/protocol_test.go) 与 [Host 集成回归](../../internal/rsi/rsi_test.go)。Host 回归使用真实 SQLite、真实插件子进程、实际 Actor／模型账本和工具接纳管线；受控模型输出不调用生产模型。平台沙箱不可用时隔离用例会明确跳过，跳过不能作为该平台安全验收。

## 全量迁移的完成审查

按[全量插件化规则](../designs/plugins-evolution.md#plugin-first)逐功能核对实际剩余业务职责，不能以包数、manifest、代理调用或单项策略回归代替全量迁移证据。迁移审查同时核对已被替代实现、过渡代理、无用配置／依赖与死测试已删除，必要历史兼容有确切版本／消费者与保留理由。新／变更功能须说明插件归属、最小内核机制与迁移边界，使用现有真实业务、版本／生命周期及适用的故障门禁证明；各项进度维护在 Feature Map 与所属方案，不新增只检查文本标志的替代测试。

## 已实现机制的验收

[服务选择回归](../../internal/rsi/rsi_test.go)以 1 与 32 个绑定验证原图选择、无关包不可读、重复提供方冲突及选中包损坏拒绝执行。

| 场景 | 可观察断言 | 入口 |
| --- | --- | --- |
| 严格 JSON、版本与内容标识 | 拒绝重复键／未知字段／尾随值；大整数不经浮点舍入；摘要分域，数字字面量保持身份 | `TestStrictFramesAndCanonicalIdentity` 及 SDK 同文件用例 |
| Hook 越权与生命周期乱序 | 系统消息注入、错误阶段修改、prepare 前 call 等被拒绝 | SDK 协议回归 |
| 安装、内容漂移与真实隔离 | 未声明文件／哈希错误不可接纳；私有文件、网络与派生进程不可访问；已安装字节被改后拒绝调用 | `TestPackageGraphAndIsolation` |
| 同版本内容不可覆盖与补丁共存 | 真实封存资源变化且沿用原版本被拒绝，不新增安装事实；新补丁可安装，原包保留，切换后旧 Run 仍调用原图 | `TestPackageVersionRejectsChangedContentAndRetainsOldRun` |
| 依赖与组合约束 | 缺依赖、重复实例、错误协议被拒绝；真实双进程依赖 RPC 只能路由到组合内具名目标，完成调用不重发 | `TestGraphRejectsMissingDependenciesAndDuplicateInstances`、`TestDependencyRPCUsesFrozenNamedBinding` |
| Hook 限制不可被后续插件放宽 | 前一个 Hook 移除的工具不能被后续 Hook 加回；无效输出在状态／结果提交前拒绝 | `TestHookChainCannotRestoreRemovedCapability` |
| 真实 DOCX 业务与大输入 | 超过单帧的文件分块传输，原证据工具输出分页；历史解释器保留、回执输入身份冲突拒绝、隔离阻止读取；首次分发不覆盖所有者停用 | `TestDocxPluginReadsLargeInputAndExistingEvidenceTool`、`TestDocxBootstrapRespectsExistingSelectionAndDeactivation`、`TestDocxIndependentUpgradeRollbackRetainsActualBinaries` |
| 真实 OCR 与本地计算边界 | 原图片工具调用独立插件和真实 Tesseract；旧解释器保留、停用拒绝新调用、隔离撤销；拒绝未授权／第二次计算；超过单帧的合法文档分块且重放一致；原生资源身份漂移不能激活 | `TestImageOCRActualPluginNativeToolAndLargeResult`、`TestImageOCRComputeCannotExpandAuthority`、`TestChunkedResultPreservesEscapedTextAndBudgets` |
| 独立 Trace 解释与只读性 | 真实页事实／Diff／工具回执走独立进程；旧引用、升级回退保留；隐私 epoch 先于输入释放；查看不写调用／状态／审计 | `TestTracePluginProjectsOwnerEvidenceWithoutWritingLedger`、`TestTracePrivacyEpochRevokesBeforeInputRelease` 及 `channels` 原生 Trace 场景 |
| 原子激活与旧 Run | 新 Run 使用新组合，旧 Run 保持原组合；操作 ID 不可复用不同请求；注入审计失败后组合、epoch 和回执均不半提交 | `TestActivationPinsOldRunsAndAuditsAtomically` |
| 上下文独立策略与冻结恢复 | 默认策略对照；只传成本、匿名标识；拒绝伪造图片、工具组切断与超预算；真实 Loop／Child 调用、独立二进制升级回退；已接纳摘要恢复、私有归档与不重复工具；v52 前向迁移保留原请求 | `TestContextPluginSelectionPreservesNativeWorkingSetAndPrivateBytes`、`TestContextPluginRejectsForgedImagesSplitGroupsAndOverBudgetPlans`、`TestContextPluginUsesFrozenRunVersionAndDurableSelection`、`TestContextPluginChildInheritsFrozenFeatureSnapshot`、`TestSemanticCompactionPluginRecoversAcceptedSummaryWithoutToolReplay`、`TestV52FeatureSnapshotMigrationPreservesOldPoliciesAndRequests` |
| Recall 独立策略与来源完整性 | 默认排序／预算／引文对照；拒绝伪造来源、非 UTF-8 切割和引文；原工具与自动 Recall 实际进程；冻结候选后中断／语料漂移恢复；Memory 候选加密、换钥固定与 Forget 清理；独立 v1/v1.1 升级回退 | `TestRecallPluginSelectionPreservesNativeEvidenceAndBudgets`、`TestRecallPluginCannotInventSourcesRewriteTextOrForgeProof`、`TestRecallPluginAcceptedNativeSearchPreservesOriginalReadWindow`、`TestRecallPluginFrozenPerTurnAndFenced`、`TestRecallPluginCandidateSnapshotSurvivesCorpusDriftAndForget`、`TestRecallSnapshotVaultPinsKeyWithoutEnablingMemory`、`TestRecallIndependentUpgradeRollbackAndLegacyRun` 和 `recall` CLI 系统门禁 |
| Main 方法独立换版与初始化恢复 | 默认方法字节相同；Host 身份／安全／消息来源保留；真实 v1.1 改变模型请求但不改已排队组合；方法回执后 ContextReady 失败恢复；工具续轮前缀／一次交付；停用不复活原生方法 | `TestConversationMethodsPreserveHostSafetyIdentityAndSources`、`TestConversationIndependentPolicyPinsPendingRunAndPreservesSafety`、`TestConversationPluginRecoversPolicyBeforeContextCommit`、`TestConversationPluginKeepsToolContinuationPrefixAndDelivery` |
| 私有 Memory 方法／调度与独立事件版本 | 真实方法包进入加密模型请求；原 intent／已接纳结果保留；独立 v1.1 改方法、v1.2 改触发频率；拒绝伪造引文、Forget 竞态；Dreaming 全层恢复／UTF-8 续页；停用保留 intent、显式恢复与隔离；不创造用户任务／交付 | `TestSemanticCuratorPluginAcceptedReplayAndNoUserTask`、`TestSemanticCuratorPluginPreservesLegacyIntentAndSealsPolicyProvenance`、`TestSemanticCuratorPluginIndependentUpgradePinsIntentAndAcceptedReplay`、`TestSemanticCuratorPluginIndependentlyChangesBoundedDreamCadence`、`TestSemanticCuratorPluginRejectsForgedEvidence`、`TestSemanticCuratorPluginForgetBeforeAcceptanceAndDispatch`、`TestSemanticCuratorPluginDreamRecoversAllRepresentations`、`TestSemanticCuratorPluginDreamPagesLongUTF8WithoutReplay` 及 `memory-maintenance` CLI 门禁 |
| 调用与状态恢复 | 同请求重放返回既有结果，异输入冲突；pending 返回 unknown；状态按 Run 隔离；stop 拒绝新调用 | `TestStateJournalRejectsUnknownReplayAndSeparatesRuns` |
| 工具与回复贯穿 Loop | 原始模型结果不变，Hook 参数修改进入正常工具接纳；纯插件工具实际执行；回复只改变投影；settled 事件持久消费一次 | `TestLoopAdmitsTransformedToolArgumentsAndProjectsReply` |
| 停用与卸载 | 活动包不可直接卸载；停用只改变新工作默认组合；卸载保留旧 Run 所需代码，拒绝新选择 | `TestDeactivateRetainsOldBindingsAndUninstallBlocksNewSelection` |
| 插件提案与独立权限门槛 | SDK 只能创建候选，不改变活动组合；未获 Hook／权限授权的候选验证失败；持久 stop 拒绝新提案 | `TestPluginCanProposeButCannotActivate` |
| 验证环境绑定 | 不同 Host 内容的证据不可用于激活，失败不改变活动组合 | `TestActivationRejectsEvidenceFromDifferentHost` |
| 历史数据库升级 | 历史 schema 夹具可升级；既有 Run 绑定内置组合；既有审批／回放测试继续通过 | Agent SQLite 迁移与现有回归 |

独立用例输入与期望由本机 Owner 创建的不可变 Policy 提供，不从候选包读取。每个声明的 service／hook 都必须有通过的契约用例；这证明功能门槛和权限下限，不等于通用代码安全证明或真实质量收益。

证据分页还需通过 `TestEvidencePlugin*`：真实 Main／Owner、UTF-8 原文完整续读、关系完整续页、独立 v1.1 换版与回退、旧 Run／旧引用、退役／隔离、伪造边界拒绝、Forget 竞态，以及持有另一 WAL writer 的 Main／Memory 原文只读。`artifacts` 系统门禁的 managed-evidence 场景使用实际分发包与所有者静态 policy，验证授权、原始字节、回执重放和 Forget。

## CLI 与系统验收

真实默认组合通过 `first-reply` 验证首次启动后可正确执行；模型策略的独立验收通过 `model-recovery` 验证升级、回退、停用以及原模型继续可用。两条旅程由[产品测试入口](product.md#critical-user-journeys)运行。

跨模块变更执行 `make test lint`，相关 Agent／工具行为执行 `make test-e2e E2E_EVIDENCE=<fresh-path>`；插件状态并发执行 `go test -race ./pkg/rsiplugin ./internal/rsi/...`。Linux 需另外进行真实 bubblewrap／seccomp 运行验证，交叉编译只证明构建兼容。macOS 验证 Seatbelt 的文件／网络／fork 拒绝，不宣称提供硬内存配额。

## 仍需补齐的目标验收

| 目标 | 必须补充的证据 |
| --- | --- |
| 每个完整功能原子的无损迭代 | 逐个迁移真实业务；覆盖旧 Run／Child 树、审批、连接撤权、取消、Forget 与升级竞态 |
| 跨 Run 可持久复用状态、迁移与回退 | 当前仅有 Run 内状态；需真实新旧格式、迁移中断与回退兼容夹具 |
| 计划／观察器插件 | 双版本游标交接、租约失效、重启及重复 fire 不重复推进 |
| Skills／Widget／Artifact 挂载与回收 | 当前仅声明／保留资源；需旧引用读取、解释器兼容、所有权和引用回收验证 |
| 自动演化与部署策略 | 真实失败触发、预算、独立评测、分批启用、效果监测、自动停派／回退的完整闭环 |
| 更强隔离 | Linux 实机负向用例与资源压测；macOS 硬内存限制需新的载体机制 |
| 演化来源的隐私清理 | 当前 Run 结果／状态／事件正文支持 Forget；未来自动生成的候选源码、资料和验证产物需独立清理契约 |
| 真实质量收益 | 同任务／环境对照，报告成功率、成本和延迟；不把机制通过当作效果提升 |

外部动作与审批始终由 Host 原有管线处理；插件纯计算重试、状态回退不表示外部业务已撤销。测试不得复制个人资料或生产凭据，不以空插件注册成功代替上述业务迁移验收。

遵循 [文档规则](../README.md#强制维护规则)：本页维护当前场景与缺口，实际命令、版本、执行结果和清理记录进入 PR／CI 或忽略的 `evidence/`。

独立政策文档与内容身份最多 12 MiB，RPC 帧仍为 1 MiB；SDK／CLI 回归覆盖大合法结果的独立期望、实际隔离验证与权限上限，不从候选输出生成期望。

方法库策略由 `TestSkillPlugin*` 验证 Main／Worker／真实 Node Code Mode，私有正文渐进披露、原目录漂移拒绝、二进制资源 Artifact、位置去重／边界、相对路径与显式路径保护、独立 v1.1／回退／退役旧 Run、停用无原生回退及迟到 Forget／隔离。`make test-prompts` 包含该系列。`capabilities` 的 discovery／main-business 场景安装实际分发包，通过所有者静态 policy 激活后继续原 MCP、审批、unknown、原始 Skill／二进制资源断言；不能用这些软件门禁代替方法语义质量评测。

经验配方通过 `TestExperiencePlugin*` 和 `TestV55ExperiencePolicyMigrationRetainsCandidateAndMethod` 验证：默认完整 Reviewer／CAS／容量／发布中断恢复、独立 v1.1 方法引言与更严格门槛、native／旧候选冻结、回退／退役后继续证明、同名发布归属与隔离拒绝晋升。实际低门槛独立二进制即使通过机械 policy，也不能越过 Host 的最低证据门槛。`memory-maintenance` CLI 门禁组合实际 Curator 与 Experience 包，继续原真实命令、独立 Run、完整晋升／退役和容量断言，并检查每个候选的冻结组合与原始提案。自动经验触发和方法语义质量不由这些门禁代替。

Task 方法由 `TestTaskPlugin*` 覆盖真实 Main／Root、once/ongoing 去重、原始 brief／canonical action hash、调用选择已接纳但 Task／工具回执未提交的恢复、显式标题保护、独立二进制排序／标签换版与回退、退役旧 Run、停用时 Host 取消、搜索 scope/status/原字段、另一 WAL writer 下只读、超过单帧的完整保留指令与超预算不报部分成功，以及实际进程读取私有输入前 Forget。`make test-prompts` 包含该系列，Forget 变体进入 `make test-memory`；另跑该系列 race。`tasks`、`task-control` CLI／PTY／SIGKILL 门禁安装实际分发包、由所有者静态 policy 激活，保留全部原有控制／Reporter／快照／审批断言，并核对每个新 Task 的先行标题回执。原 Trace 隐私门禁同时覆盖纯读 RPC 异常的撤权优先级。

Root 进度的两个窄贡献由 `TestProgressPlugin*`、`TestProgressGuidance*` 及真实 `tasks` 提交前后 SIGKILL 覆盖，包含换版／回滚／退役、Child 隔离、先行选择恢复、停用不阻断业务；边界与检查见[任务测试方案](tasks.md#独立阶段方法验收f-005)。

问答／审阅配方由 `TestFeedbackPlugin*`、`TestV56FeedbackPolicyMigration*` 与真实 Task 系统组合覆盖；原问题／成果／回答、固定配方、期限上限、旧问题回答／旧 Run、纯计算事务及停用边界见[任务测试方案](tasks.md#问答审阅配方验收f-004)。

委派方法／更窄树预算由 `TestDelegationPlugin*`、`TestDelegationMethods*`、`TestV57DelegationPolicyMigration*` 与真实 Child 系统组合覆盖；迁移／事务／旧树／权限／预算和 Forget 证明范围见[委派测试方案](delegation.md#独立方法与预算迁移验收)。

`TestCodeModePluginMissingRunnerCannotBypassDurableGuardian` 通过真实已接受脚本、Run graph 和嵌套 Tool 接纳验证：运行器未装配时，quarantine 与 stop 仍阻断 CodeCall 和 ToolInvocation，不能因 nil 适配器跳过 Host 持久控制。native 回归继续保留无受管 graph 的原语义。


文件与命令方法的原字节、精确审批、先行选择、unknown 不重放、独立版本、Guest 生命周期及 schema 前向兼容由[代码环境测试方案](coding.md)维护；`TestShellPlugin*` 与 `TestV60ShellMethod*` 进入现有提示词门禁，Forget 进入 required memory race。默认组合的 startup 门禁核对实际分发包集合，capabilities 使用真实文件／命令组合而非代理标志。

Task 标签纯方案的接纳、历史已完成／未完成调用兼容、Forget 与 Guardian、schema64 前向升级由[任务测试方案](tasks.md)维护；通用 opaque `Call` 的未知结果仍由 `TestStateJournalRejectsUnknownReplayAndSeparatesRuns` 验证，不允许把标题恢复扩成任意调用重试。

Computer 十一服务的完整默认组合、真实 Root／Child、历史图／引用、独立换版与停用、审批／撤权和固定 Driver 原语由[Computer 验收方案](computer-use.md#默认组合与完整业务门禁)维护；required `computer-plugins` 系统 lane 不以测试手动激活替代正常启动选择。

Web／Extract 的二十三包首启、原资源捕获、自定义源／大文档冷重启及正常模型 → Code Mode → Web 业务链由[Web 验收方案](integrations.md#默认分发与冷重启验收)维护；required `web-plugins` lane 使用正常 provider bridge，不通过会屏蔽 Web 的旧 fixture 目录。已有选择／停用不自动重选。 MCP 的方法／目录、实际业务与兼容清理已验收；当前标准二十三包门禁已验收；required `mcp-plugins` lane 继续保护完整默认分发、独立移除、冷重启及审批／unknown 不重发，并与完整软件、提示／记忆和相关 race、系统门禁共同运行。

`TestVerificationBoundedFreshProcessesAndLateFailure` 观察测试自身的真实子进程，不读取其他进程参数：不含依赖／计算子进程的夹具中，最多四个顶层用例并行，每例的进程内计数必须从一开始；声明原生计算的包仍逐例串行。超过两个批次的末例失败必须拒绝裁定，不以早期服务覆盖冒充全部用例完成。此根进入必需 Web race，原进程／资源与工具期限保持不变；冷启动 Host 就绪等待独立由系统夹具约束；不缓存候选执行结果或跳过环境重检以缩短首启。

卡片资源的原始 oracle、真实包生命周期、坏资源拒绝及不消费卡片的恢复路径见[集成测试方案](integrations.md)；`TestAppsCardView*` 保留在 required race 与实际 browser 门禁。迁移清理须检查原内联实现已删除、旧顺序的真实消费者及删除条件。

`TestAppsSidebarView*` 纳入 required race 与真实 browser 门禁，覆盖原始输出、资源身份、实际换版／引用／停用、效果时换图及坏侧栏的原接纳回执，见[集成方案](integrations.md)。检查普通内联组合删除、仅保留有消费者和删除条件的旧顺序。

`TestAppsMarkup*` 保留在 required race／browser 入口，验证真实标记资源与有限安全接纳、旧引用／效果回执和同批删除普通原生标记。原始资源身份 fixture 不替代独立原始输出和产品／安全验收，参见[集成方案](integrations.md)。

标准30组合含原127条、工具发现8条、Apps 四包96条、GitHub 8条与 Artifact 4条，共243条独立所有者验收。当前33包组合实际需要333条，Host 整份政策上限为512条，四个顶层进程并发／原生计算串行、每例资源期限及控制正文上限保持不变。`TestVerificationPolicyCaseBound` 检查空／边界／超限，`TestVerificationBeyondFormerCaseLimitIncludesLateFailure` 证明第315条失败阻止裁定；两者随原隔离验证进入 required Web race。CLI与历史制品证据范围见[工具发现验收](codemode.md#工具发现插件验收)。

`TestAppsBootstrapIndependentOwnerResourcesAndColdSelection` 使用真实四包和98条含 DOCX 的有限组合，验证19个新增服务的全部独立样例；合法资源重排不能改变所有者期望，验证失败不产生选择，冷启不替换原图、不复活所有者停用。该根进入普通 Go、RSI race 和 required Web race；完整30包的实际业务、历史图及原生兼容范围见[集成验收](integrations.md#apps-标准组合验收)。

required Apps 竞态使用[同一动态分批机制](engineering.md#apps-竞态分批)核对全部用例；默认组合系统入口仍要求 managed29 与 current SDK prior28／24／23／22 五种场景实际通过。

Trace 文案迁移的完成验收须覆盖：封存资源摘要与非法／重复字典拒绝；原中英文对应值；当前页面实际消费与未知词条不回退原生字典；独立换版、原冻结引用、回退、停用与冷读取；共享 Context 阅读器不被 Trace 字典改写。资源专项通过不替代页面或整功能验收。

工具输出页面回归须通过真实 loadTrace 选择 tool 节点，断言恰好一次托管调用、原 Selected／ReaderRevision／Field／Offset、原始回执不变和独立预览实际显示；覆盖已托管空预览、调用错误、managed=false 和缺少 projector，当前图均不得静默回退。只测插件函数或 Store 投影不足以证明页面接线。

RSI 验证器的真实候选测试载体遵守生产32MiB单文件包限制。race 或平台调试符号令载体超限时，夹具编译去调试符号的非 race 候选载体；Host 仍按当前 race 模式验证，生产大小边界、新鲜隔离进程数量、完整 Owner trace 与晚失败断言不变。

Owner 晚失败回归核对原例号与契约，确认输入和预期标记不进入错误文本。缺失 describe 的真实候选退出回归核对已清理进程的状态、原 unknown 类别，并拒绝原始 stderr 泄漏；退出状态可能包含 Host 清理，启动报告分类不视为权威原因。

Linux 域资源的真实进程回归运行 `TestResourceDomainDriveUsesRealProcessWithoutAnotherPendingLedger`，验证 25,200,000 字节的多语言文本经声明的单次回调与摘要校验完整返回。`TestResourceAllocatorEnvironmentIsPrivateToLinuxResourceChildren` 在同一张图比较域资源和纯读进程，并检查 Host 自身的分配器设置。`TestPluginNativeLauncherRetainsHardEnvelopeAndCannotRaiseIt` 检查原有的地址空间、CPU、文件和描述符硬限制。
