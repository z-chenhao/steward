# MCP 与真实外部能力：测试方案

[功能与验收目标](../features/integrations.md) · [技术方案](../designs/integrations.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 范围与证据

本方案回答如何证明需求完成，不是测试执行报告。已有入口只表示测试存在，不能推导特定版本已通过或真实模型效果已达标。

以功能文档中的需求为验收源；配置依赖、待实现分支、平台限制和未验证项单独报告。具体命令及 core/extended 编排以 [Testing](product.md) 和 [runner](../../tests/system/runner.py) 为准。

## 需求场景矩阵

| 需求 | 需要验证的可观察行为 | 当前重点 |
| --- | --- | --- |
| [F-024](../features/integrations.md#f-024) | 真实协议验证发现/调用、漂移拒绝、取消回收与外部效果审批。 | 保护已接通的限定范围；Live/配置另行验证 |
| [F-025](../features/integrations.md#f-025) | 在明确服务上完成真实读取/授权动作/失效恢复；无凭据泄漏，审批及 unknown 语义保持一致。 | 必须通过目标入口补齐闭环；不能只直接调用底层方法 |
| [F-042](../features/integrations.md#f-042) | 受控来源重复/乱序、补读、重启、撤权、自身回流、stop/Forget fencing 与真实故障状态；完整扫描后才提交事件和成功边界。 | 已接通监听→SQLite→CLI；Main/Task/飞书接续、真实 GitHub 权限及历史保留边界需后续入口与 Live 验证 |

## 个人 Connector 验收计划

本节覆盖已有 Web Access 及其搜索适配器、MCP／外部连接的产品接入边界。其他服务形成可操作场景后再扩展验收。

### 分项场景矩阵

| 独立需求 | 正常场景与可观察交付 | 关键失败／边界 | 计划层级与当前缺口 |
| --- | --- | --- | --- |
| [F-046 Tavily](../features/integrations.md#f-046) | 找到指定原文→读取正文→返回可打开来源与时间，摘要／全文可区分 | 零命中、正文提取失败、截断、旧文章／转载、429、恶意网页指令 | HTTP 协议 peer＋产品系统链路；真实站点／中文覆盖需 Live，全部待补 |
| [F-067 Exa MCP](../features/integrations.md#f-067) | 免密钥查询得到实际来源；正文有无明确，实际服务／工具可追溯 | 429、工具／schema 变化、缺正文、无效响应；严格过滤不能静默变成提示，取消后不换源 | 目标传输协议 peer＋产品系统，F-069 中已有协议 peer 和 opt-in 在线测试；长期限流／中文质量仍待验证 |
| [F-068 SearXNG](../features/integrations.md#f-068) | 查询选定实例，展示来源及已知上游失败／部分结果 | JSON 未启用、缺结果字段、403／429、跨 origin 丢认证、域名筛选／时间保证变化 | HTTP peer＋产品系统；实际实例与上游质量需 Live，未部署，全部待补 |
| [F-069 Web Access Plugin](../features/integrations.md#f-069) | 按冻结的已验证账户能力搜索，读公开网页并续读 Artifact；原生及允许后备的真实来源可追溯 | 主模型／账户不切换，零命中不换源，取消／撤权终止；预算、过滤、来源／live 模式不能静默退化 | 已有模型／HTTP peer、SQLite 工具与 Artifact 集成；Steward 自有账户和内容质量仍需 Live |

搜索源比较使用同一批公开的中文原文查找、技术说法核验及英文资料查询，分开记录成功率、来源可核查性、时效、正文覆盖、延迟和实际费用。免密钥不代表无配额，代码支持不代表当前网络可用。Pi 插件的 `source_check` 返回证据不表示自动判真伪；若复用片段组织，只验证来源和引用正确，语义结论另行评测。实现过滤降级／后备源前，验证取消、认证拒绝及未授权目的地／费用不会触发自动切换。比较与执行证据留在 PR／CI 或忽略的 evidence/，不写入长期文档。

### F-069 的链路与失败矩阵

专项入口为 `make test-web-access`，在 Ubuntu checks 中对 connector／provider／bridge／SQLite 的相关用例运行 race；完整检查为 `make test lint`、`make test-prompts` 与 core E2E。

| 场景 | 证明入口 | 证明边界／缺口 |
| --- | --- | --- |
| 自主实现边界 | `TestWebAccessHasNoAgentSDKDependency`：解析运行时仅允许锁定通用库，无外部 agent 实现目录 | 依赖变更仍须许可证、安全与架构审查，不以测试代替人工审查 |
| 选路与服务协议 | `expanded_test.go`：SearXNG／Tavily 请求、凭据、域名、时间条件、限流与终止错误、禁用目的地；旧搜索回归验证零命中和不允许后备的错误 | SearXNG 真实例和 Tavily 真账户另验，不宣称统一日期硬保证 |
| 公开网络与解析 | 既有地址／重定向／大小 peer，加真实本地解析进程的 Markdown／raw／PDF／图片、宿主路径拒绝 | 不执行网页脚本，无登录浏览器／视频；fake-IP 拒绝需与实际服务失败分开 |
| 文本与证据 | Unicode 大小写、字节坐标、按序 fuzzy、完整原文片段与双哈希 | unclear 只表示待人工判断，不证明 claim 为真 |
| 模型交换与生命周期 | SQLite 辅助调用的 frozen 模型、预算、usage、重入哈希、拒绝旧 claim／未接受查询；既有账户／Forget fence 回归 | 模型费用和外部搜索费用分别报告 |
| Main 与持久化 | 真实 Code Mode 调用→图片和状态 Artifact 原子提交→后续调用读取全文／查找；多附件配额与终态失败回滚 | 外部内容为确定性 Fake；真实联网单独验证 |
| Responses 协议 | bridge／provider peer：真实搜索 SSE／来源／usage，域名条件；SIWC 无 max_tool_calls，namespace 含 description | 实际账户已验证原生搜索及 Main Code Mode，不能扩展为所有模型／订阅保证 |
| Live | 显式免费 Exa／公开网页探测；经本人授权的临时 Steward 账户跑 Main→Code Mode→native→Artifact，并核对来源／用量／终态 | 不读取其他助手凭据；凭据、账户身份和授权 URL 不进入 PR 或测试日志。其他服务、中文质量和长期额度独立验收 |

F-069 保持部分完成：已接通的核心链路不证明未接入供应商、视频／登录网页或所有账户可用。真实执行证据留在 PR／CI／忽略的 evidence/，不写入长期运行日志；macOS 临时路径或浏览器系统差异与 Ubuntu 必需门禁分别报告。

### 共同验收与完成标准

1. **真实入口**：每个 connector 经实际 CLI/Web/Main/Task 的工具发现与调用交付；不能只运行 SDK 示例或直接调用内部方法。未配置、缺凭据、缺 OS 权限、不可用和健康状态分别可见，不为尚无入口的功能写可执行教程。
2. **范围与授权**：用合成超范围资源验证账户／仓库／目录／主机／标签页／文件边界；认证刷新不能扩大权限。外部文本及历史会话不能创建新授权，具有写能力的供应商 scope 不表示 Steward 自动拥有写动作权限。
3. **取消与恢复**：覆盖取消、重启、重复调用、token 拒绝／刷新、限流和断连；写请求发出后的结果未知保留 unknown，只有供应商提供真实查询／幂等语义时才按具体契约恢复，不能假定所有服务都可重放。
4. **隐私与来源**：日志、错误、URL 和模型输出不得出现凭据；选定原文／附件来源可追溯。撤销导入范围和 Forget 清除可召回材料及索引，旧 Run／旧导入不能重新暴露数据；历史消息不是当前事实或新指令。
5. **事件另行验收**：仅当该服务确需主动监听时，测试分页／增量游标、过期重同步、重复／乱序、删除／中间版本边界、订阅续期、stop 与健康；资料读取工具不自动获得监听或任务接续能力。
6. **实际效果**：能读取／生成内容不等于语义正确、收到通知或动作成功。地点正确、来源忠实、视觉效果、实际播放及模型范围判断按 [Testing 的质量边界](product.md#testing-agent-evaluation)另行评测。服务失败不得用模型补全结果冒充成功。

### 数据、Live 与文档登记检查

使用一次性 `STEWARD_DATA_ROOT`、合成邮件／文档／会话／健康记录、受控主机与浏览器站点，不复制个人数据库、证件、健康实值、面试原文或真实凭据到测试夹具。Live 单独使用明确账户／资源和适用权限；健康设备、地图数据、OS 授权、实际媒体播放及服务验证条件必须记录真实证明范围，未运行项保留缺口。

仅更新需求、设计约束和测试计划时，适用检查为 `python3 scripts/check_docs.py` 与文档内容审查；无需因登记需求运行 Go/Node 产品测试。未来实现每项后，正常和关键失败／恢复路径须经目标入口验收，更新当前功能说明、技术方案、测试入口与该项状态；其他 connector 的完成不能代替本项证据。执行结果留在 PR／CI 或忽略的 evidence/，不写入本文。

## 层级与关键路径

本地 stdio 测试协议与沙箱、允许列表及 schema 漂移；远程能力落地时增加真实认证生命周期、服务失败与副作用未知测试。线上服务质量不由本地 MCP Fake 证明。

- 单元/组件：规则、实际 SQLite 和协议边界在所属模块验证。
- 系统：用真实 CLI/Web/Actor/Bridge 链路证明目标行为；允许受控外部 peer，不能伪造产品内部接受结果。
- Live/质量：涉及真实账户、Guest 平台或语义质量时单独记录环境与证据；不适用项说明理由，不用软件回归代替。

### MCP 方法插件与默认分发验收

`TestMCPPluginActualCodeModeUpgradeRetainedRollbackAndDisable` 通过真实隔离 Go 插件、stdio peer、Node CodeMode 与 SQLite Tool 接纳，比较独立版本的原文分段，覆盖 pre-service Run、排队 Run、旧包退役、回退、停用与原效果账本；`TestMCPPluginMissingCarrierKeepsFrozenManagedBoundary` 保证缺失可执行 catalog 不把已有执行改回 native。

`TestMCPPluginActualApprovalAndUnknownNeverRepeats` 验证批准前不执行、确切批准后实际 unknown 只发生一次。`TestMCPPluginRealReleaseRecoveryKeepsOriginalNeverCallAndGraph` 在实际效果已返回、原回执未接纳时释放真实 Run claim，再升级活动方法；恢复后的首次依赖执行必须保留原图和 never／unknown，不重新派发或补选版本。

`TestMCPPluginLateForgetAndGuardianCannotPublishPrivateResult` 在来源返回、方法返回和最终结果事务三个位置撤销隐私或隔离图，不得产生成功私密回执、Artifact 或后续模型请求；根名保留 Forget，使其进入必需 memory race 清单。`TestMCPManagedHostRejectsEarlyDispatchChangedCursorAndForgedResult` 以跳过候选验证的恶意 runner 操作真实 peer，证明提前调用、换游标、重复效果、伪造位置、断开 UTF-8 和候选失败不能扩大 Host 权限或虚构确定性。

`TestMCPPluginActualPagedFiveHundredToolsUseCompleteFrozenCatalog` 使用十页实际 stdio 目录，正常冻结五百工具，经 CodeMode 渐进描述再调用最后一页的真实工具；托管方法必须完整走过原分页。`TestMCPPluginOriginalSixteenPageFourThousandNinetySixEntryCatalog` 经完整十六页／4096 项实际目录，以明确允许的五百项冻结集调用最后一页；非允许项不进入执行快照。`TestMCPManagedCatalogBoundaryRejectsInvalidRediscoveryBeforeEffect` 在原发现后替换真实目录，分别验证 17 页、4097 项、重复名、停滞游标、Schema 漂移／超限、描述超限和非法名均在效果前拒绝，同时保留最大合法目录的实际调用。`TestMCPPluginOriginalFrameWithFortyThousandTextBlocks` 以接近原 1 MiB peer 帧上限的四万合法文本块验证完整输入和超过普通帧的结果位置方案，不能用任意块数限制缩小旧能力。原 MCP 响应缺失／null／非文本、进程隔离、取消与原历史指纹的回归继续运行，不能被新方法样例替代。

`TestMCPPluginNamespaceActualDiscoveryRetainedSourceUpgradeRollbackAndDisable` 经真实 CodeMode 目录，覆盖 pre-service 与单服务原图、512→32 rune 独立迭代、旧快照、退役、回退及停用。`TestMCPPluginNamespaceProjectionLeavesWriterAndRechecksForget` 在纯计算前后及完成计算到重试事务之间取得真实独立 SQL 写事务，并分别替换／移除原来源、Forget、隔离或停用；旧请求不得被重试到新隐私 epoch，创建事务不得丢失或重复接纳。根名保留 Forget，进入必需 memory race。

`TestMCPPluginNamespaceOriginalLargeInstructionsUseBoundedDefaultProjection` 消费原近 1 MiB server instructions，经分片读取及默认窗口输出，验证同图同源投影复用、不自动序列化原始来源；`TestMCPPluginAcceptedNamespaceCannotSplitOriginalUnicode` 让独立政策接纳 ASCII 样例上的坏候选，再以真实 Unicode 来源证明 Host 仍拒绝错误窗口，不能用 native 修复。Registry 的 `TestNamespaceSourceIsPrivateCopiedAndSeparateFromFrozenDescription` 与 MCP 的 `TestMCPNamespaceSourceKeepsOriginalInstructionsAndExplicitOwnerChoice` 分别保护原始来源隔离、复制和显式来源优先级，元数据不得重绑执行身份。

`TestMCPPluginActualChildRetainsMethodNamespaceAndPrivateScopeAfterRetirement` 在父执行固定后同时修改方法和目录政策、退役旧包；真实 Child 仍使用父图／服务清单、原结果分段与目录，实际模型请求不含父级私密消息。`TestMCPPluginActualMalformedReplyAndAcceptedBadMethodKeepOriginalEffectTruth` 经原确切审批执行真实效果，再分别返回 malformed／null／非文本、真实 peer 错误或已被独立政策认可的错误方法；不重复效果、不泄露诊断正文，不能由候选改变确定性。

`TestMCPPluginCurrentConfigurationFencesDispatchReleaseAndCommit` 在实际 list 后、call 后和完成事务前移除／替换原配置，不能派发旧动作或转发到替换 peer；效果已发生的撤销及 Guardian stop／quarantine 在原回执记录 unknown，恢复不重放。只改目录描述仍允许原冻结动作，不把展示改动当成执行撤权。

required `mcp-plugins` lane 先运行 MCP 与原查询恢复的 race 回归，再经正常 provider bridge、真实 CodeMode 和受限 stdio peer 验证标准30包首启（包括四个 Apps 包）、原文多块返回、目录投影、冷进程首个依赖调用、确切审批和 unknown 不重发。通过所有者控制面选定明确缺少 MCP 与 Apps 工具方法的组合，验证运行时不会补选 MCP；从已迁移组合只移除 MCP，保留其余功能并拒绝新 MCP 调用，随后全局停用和停止均不得被冷启动覆盖。该初始组装沿用当前 SDK，不冒充归档二十一包发行版。所有 SQLite 证据读取关闭连接并使用一致读事务；本地 peer 不等于真实供应商 Live。

MCP 现有本地 stdio 范围的托管迁移已验收，持续回归须保留默认构建／源码与预构建安装／首启／冷重启、独立换版／回退／停用、旧 Run／Child 与兼容清理，并同时运行完整软件、prompts、memory、MCP／Web／RSI race 及核心和适用功能系统门禁。Linux ARM64 使用实际解释器与生产 bubblewrap 验证同一正常业务链及六项生命周期场景，并清理自建容器；这不证明完整 Linux 产品、真实 Guest 或远程认证／供应商 Live。共享 unknown 查询恢复另按[工具验收方案](tools-approvals.md)保护未冻结操作不能获得后来安装的查询权限。

## 已有检查与缺失覆盖

### GitHub 托管方法验收

原生读取的固定27组输出与 HTTP 次序由迁移前独立捕获，包含资格过滤、Unicode／完整正文版本身份、分页、错误及异常响应；`TestGitHubPluginActualWholeScanMatchesOriginal27Oracles` 用实际隔离包、原 SecretStore／HTTP driver／SQLite 接纳对照，而非把候选输出当期望。原生27组和预览6组兼容解释器仍保持固定对照，临时捕获代码不进入产品源码。

`TestGitHubPluginActualNativeInflightUpgradeRollbackDisableAndCold` 在真实 HTTP 等待期间先启用、再换版，验证旧票据、原来源键／正文、独立展示及配方、回退、停用前置拒绝和冷启动首个依赖调用，恢复不补 select／update。`TestGitHubPluginAcceptedRecipeMainScopeAndSourceGuardian` 经实际观察→原 Main→同 Task append，验证来源图与新 Main 图不同仍用旧配方、已排队输入不改写、停用保留接纳因果、来源图隔离优先及至多一次追加。`TestGitHubPluginLateForgetCannotAcceptSourceOrRecipe` 在实际纯方法返回后执行原 Forget，拒绝接受来源或配方。

`TestGitHubV67SourceMethodMigrationKeepsOriginalPendingMainAndRows` 比较真实 native 观察、已排队 Main 和原执行／能力／Generation／Tool 等十二组行的完整值，前向68不得改写；旧来源保持 NULL 方法且不可后填／替换。共享系统 schema fixture 与嵌入迁移清单同步，旧迁移 fixture 按其实际版本移除新增列，不删除既有升级断言。

`TestGitHubPluginDefaultCadenceAndReadOnlyHealthUseAcceptedInterval` 以独立版本改变真实 HTTP 读取次序与默认间隔，验证状态按接纳的间隔判定 stale、不触发 IO，显式间隔仍不被改写。`TestGitHubPluginPreservesOriginalRowAndQuotaFailureOrder` 覆盖末页时间错误优先于历史上限，以及早先总额度错误优先于后续坏身份；整轮不接受部分观察。`TestGitHubPluginMechanicallyAcceptedBadPlansCannotCommitPartialOrEscape` 先真实验证通过仅覆盖首步的对抗包，再证明重复／越界原行、提前宣称扫描完成、重放页与改写显式间隔均被 Host 拒绝。

`TestGitHubBootstrapIndependentOracleAndOriginalSelection` 用实际包和独立字面期望验证初次选择；诚实封存但改变默认间隔的合法候选须被拒绝，不能替换旧选择或复活停用。`TestGitHubPluginBadInitialPlanCannotReadCredentialOrSource` 证明坏首步不取凭据／HTTP；`TestGitHubPluginExpandedMethodSpendsOriginalScanBudget` 验证转义后的方法字节消耗原8 MiB；`TestGitHubPluginLastStepGuardianAndActualStorageFailureStayDistinct` 区分最后一步后的隔离和真实 SQL 故障。

required `github-plugins` lane 通过 `make test-github-plugins` 构建真实完整30包并运行 owning 生产者／原生 oracle／SQLite／CLI／App／Host 验证的 race。`TestGitHubDefaultProductionControlPollAndColdLifecycle` 必须消费确切分发、完成实际验证 TLS 的来源读取及冷启首个依赖动作；缺失分发时普通 Go 的 skip 不计该系统门禁通过。`verify_github_plugins.py` 在 Linux 使用 stock CLI、固定 api.github.com origin 和临时受信测试证书，TLS 验证保持开启；macOS 原生证书信任不接受 `SSL_CERT_FILE`，因此分别证明 stock CLI 无模型启动／缺凭据／停用，与真实 App 的受信 TLS 路径，不冒充 stock HTTPS 成功，也不改系统信任或产品端点。

可选 `--prior28-binary` 只在 Linux 使用真实原28制品：旧二进制先创建原 Task、绑定来源并崩溃于冻结模型请求边界，新版本前向迁移、显式更新完整包后立即恢复；检查完整旧请求／来源／确切分页版本引用、同 Task 至多一次 append、原键去重及新来源确切方法图。未提供归档制品明确未运行；重建制品只有与原报告逐字节同 hash 才能称为原制品。工具发现的独立原 Root／Child／Grandchild 恢复和整套升级／回退另走原门禁，不能互相替代。

完整软件／系统、平台与历史制品验收仍须在固定源码／产物上闭环；以上入口和定向用例不证明全功能、Live PAT、真实模型范围判断或自动 RSI 部署完成。

| 入口 | 覆盖用途与限制 |
| --- | --- |
| [tests/system/verify_capabilities.py](../../tests/system/verify_capabilities.py) | 已有相关回归入口；具体断言以实现为准，不代表覆盖全部目标 |
| [internal/observer/github/client_test.go](../../internal/observer/github/client_test.go) | 实际 HTTP 协议 peer 驱动分页、认证头、自身份排除、评论更新身份、review/CI、恶意 Link/redirect、撤权及异常响应；不需要真实 PAT |
| [internal/agent/sqlite/github_test.go](../../internal/agent/sqlite/github_test.go) | 真实 SQLite 去重、观察/Outbox 原子接受、故障边界、重启补读、repository ID pin、并发/停止/Forget fencing 与当前 epoch 读取 |
| [internal/agent/sqlite/github_followup_test.go](../../internal/agent/sqlite/github_followup_test.go) | 真实 HTTP peer→SecretStore→观察→Main→同 Task append；重启/重复只接续一次，重复 Tool 参数复用回执，不同动作拒绝；自然完成与人工结束、解绑/监听停机/暂停/取消/Forget、晚模型与工具、unknown 和新用户优先；Store/App 移除或切换配置在 Actor 前撤销、重加不复活；旧 scope 调度不占用新 Main 或饿死普通 Worker；assigned 遇网络/歧义错误重启后只接续一次，明确身份/凭据拒绝撤销；GitHub 接续结果经真实 Feishu HTTP peer 回原 Task 消息 |
| [cmd/steward/github_test.go](../../cmd/steward/github_test.go) | 真实 CLI 调度与重开 data root，在执行依赖不可用时保持状态/停止/恢复/配置移除可用，缺凭据失败可诊断 |

- **F-025 剩余覆盖**：真实 PAT 权限、到期/更换与 GitHub 保留历史需 Live；OAuth、授权写动作、其他服务及对应入口仍未实现。
- **F-042 剩余覆盖**：已接通精确 PR→Main→原 Task；真实飞书原话题交付见渠道方案，实际模型范围判断需独立评测。真实来源删除、旧 head、中间修改与平台历史保留不由协议 peer 证明。

## 详细测试入口与证明边界

本功能协议在此维护。既有编号用于追溯协议，不代表所有设计目标均已实现；完成状态以需求主索引为准。

<a id="capabilities-reproduction-and-evidence"></a>

### Reproduction and evidence

```sh
make build
python3 tests/system/verify_capabilities.py --binary bin/steward --evidence evidence/capabilities.json
go test -race ./internal/tool/... ./internal/skill ./internal/computer/... ./internal/config
```

The harness launches private data/provider/Skills/checkout roots, doctors an
explicit Fake model, drives ordinary chat/Task/approval CLI commands, reads SQLite
and file hashes, kills after a real edit before result commit, restarts, and
cleans every child and disposable root. The system runner selects it; repeat count is explicit.
The coding fixture starts with a real failing `--status paused` regression,
edits only its CLI through a Worker, then passes its real race tests. Audit reads
the deliberately defective retry ordering without changing any byte. Go
regressions additionally probe host credential reads/writes, environment/network
denial, link escapes, catalog drift and executable Git config.

This extends the M3 Tool/Approval/Artifact fragments in `verify_tools.py` and
`verify_artifacts.py`; it does not assign a semantic Golden score. GC-19 is a
coding prerequisite here. This fixture marks GC-20 Child/recovery and full
GC-19/20 Durable cases `not_applicable`; the [Core gate](product.md) covers
their additional mechanics. Production Computer has [separate verification](../designs/computer-use.md).
The GC-11 checkpoint now drives both original turns in six locked variants,
reads all three actual provider quotes with services and user weights, preserves
oral-cashback uncertainty, delivers two scripted replies and reopens both
comparison Artifacts. It also rejects purchase/approval/Task effects and proves
duplicate-input state equality. The same five-round capability gate includes
every variant. Its deterministic scripted model does not establish live-model
reasoning quality; see [release evidence and remaining obligations](product.md).

The local MCP path needs no new domain states: schema v12 already freezes arbitrary
ToolSpecs, Skill set hashes, policy and invocation outcomes. GitHub observation
adds schema v33 watch/observation records separately and does not extend ToolSpecs. Empty/prior schema
migration tests remain in the existing SQLite gate. Old capabilities without
these optional fields keep their old tools; enabling an adapter never expands an
already frozen Run. Changing a configured scope fails old-Run selection closed.

## 执行与完成判定

使用隔离配置与可丢弃数据，保留产品版本、实际命令、环境、结果和清理情况。区分通过、失败、跳过、未执行以及未知外部结果。

需求完成需要对应入口可用、正常与相关失败/恢复场景有证据、使用说明与技术方案一致。报告沿用现有 evidence/流程；不为本方案建立另一套测试平台或复制历史评测资产。

渐进工具暴露与 500 工具目录的配置、Main 搜索加载、Worker 脚本发现、隐藏/未声明调用拒绝及恢复覆盖，统一见[工具与审批测试方案](tools-approvals.md#渐进工具发现与-500-工具目录)。CLI capabilities 的 `tool-loading` 已纳入完整门禁，读取同一角色冻结目录。

### Plugin、Connector 与平台边界

术语按[功能说明](../features/integrations.md#术语与登记边界)统一：内容审查核对能力插件、外部服务连接器、具体连接、技能与工具的区别，以及安装、连接授权和动作审批的独立性。Web Access 仍是内置插件；Exa／SearXNG／Tavily 的适配器证明对应服务协议，Tavily Search 不代表 Extract 已接通，研究配方测试不代表已有可安装 Skill。独立安装、共享连接和多账户选择继续按 F-025／F-070 的实际入口验收。

`make test-web-access` 覆盖 `internal/plugin/webaccess`、三个服务 connector、`internal/webcontent` 与 SQLite Host 的 race 回归，CI 通过 `web-plugins` 分片执行，纳入必需的 `Ubuntu checks`。

| 场景 | 现有入口与证明范围 |
| --- | --- |
| 依赖方向 | 插件的 `architecture_test.go` 检查实际 Go 传递依赖；connector 不依赖插件／工具／平台，内容处理不依赖服务／模型，插件不依赖 Agent／SQLite／App |
| 插件研究配方独立于 SQLite | `plugin/webaccess/plugin_test.go`：受控 Host 驱动两次原生查询、去重读取、部分失败、摘要草稿、完整证据及预览，再从保存证据查找；预算／认证／取消终止后不再换源或生成 |
| 各服务协议与来源政策分离 | Exa 所属包协议 peer；插件 `expanded_test.go` 经实际 SearXNG／Tavily adapter 验证认证、请求、限流换源和无效响应终止 |
| 处理机制复用 | `webcontent` 所属测试验证公开 DNS 固定、地址／重定向拒绝、HTML／PDF／图片／编码和本地解析，不需要服务连接或 SQLite |
| 平台安全语义 | SQLite 既有辅助交换测试验证冻结 Snapshot、预算、请求哈希／重放／过期 claim；Main→真实 Code Mode→Artifact 测试验证作用域续读，媒体／证据／终态原子提交与失败回滚 |
| F-070 协议基础已接入 | Go SDK／CLI 基础与完整验收见[插件与自进化测试方案](plugins-evolution.md)；服务接入需验证共享连接保留、断连与卸载独立、冻结权限不扩大；内部模块拆分不视为生命周期验收通过 |

核心 channels 的 tracing 只读验收先等待完整因果链与终态轨迹，再比较一致快照，详见[可观测性测试方案](observability.md)。

工具名、配置序列化和资源身份保持兼容；重构兼容检查比较旧版冻结定义与当前定义。真实账户／实例及模型质量继续按各服务 Live 的证明范围验收。


## F-071 · Composio 应用连接验收

### Apps 标准组合验收

`connected-apps-plugins` 通过普通 CLI 验证标准30包、首选图与冷启保持，再以同一实际分发驱动生产 App／bridge／Code Mode／审批和 unknown 回执。`TestConnectedAppsDefaultProductionCompositionAndColdLifecycle` 明确区分 managed29、current SDK prior28／prior24／prior23／prior22；所有子场景必须有通过的 terminal，skip 不计通过。普通 Server 还实际消费同图目录／界面资源、带图 Logo 引用和真实账户方法；Go 夹具仅把外部服务端点及真实 CLI 载体接到现有 App 接缝，不让测试可执行文件冒充生产 launcher。

`tool-discovery-plugins` 可选的真实旧制品检查见[工具发现验收](codemode.md#工具发现插件验收)：原23包整套更新／回退，以及 `--prior24-binary` 指定的归档24实物更新／回退；后者要求原24个内容摘要完全一致。归档候选的验收状态须随证据记录，不能因参与兼容回归而宣称其完整验收通过。两条真实旧 Root／Child 崩溃路径都要求升级后创建并完成一个新 Grandchild、保留原图和已完成 Generation；受控模型不证明真实服务质量。

`TestShippedPluginDistributionRejectsPartialNativeFallback` 拒绝缺少任一 Apps 四包或 GitHub 包的半套分发；`TestAppsBootstrapIndependentOwnerResourcesAndColdSelection` 验证独立期望与合法重排资源的拒绝。构建必须实际带上三份界面资源，完整239条政策须在原256条限额与原控制正文大小内；源码与产物固定后的软件、系统、竞态、浏览器和 Linux 证据才构成当前组合验收。既有私有包回归／截图不能替代默认组合证明；其他模板／CSS／客户端、通用资源挂载与回收仍单列。



聚焦入口：`go test ./internal/connector/composio ./internal/connectedapps ./internal/plugin/appsmethods/... ./internal/plugin/connectedapps ./internal/rsi ./internal/tool ./internal/tool/codemode ./internal/interaction/web ./internal/app ./internal/agent/sqlite -run '(Client|Apps|ConnectedApps|RegistryGroup)' -count=1`；并对这些包运行 race。新的连接交互浏览器门禁由 `make test-browser` 执行：`STEWARD_TEST_APP_BROWSER=1 go test ./internal/interaction/web -run '^TestAppsBrowserConnectionLifecycle$' -count=1`。PR 的 `connected-apps` 软件分片执行 `make test-connected-apps`，覆盖全部 Composio／账户管理／图标／Registry 定向 race；浏览器生命周期随 `browser-infra` 强制运行。跨模块门禁为 `make test lint` 与 core E2E。

| 场景 | 证明入口及可观察结果 | 边界 |
| --- | --- | --- |
| 默认项目引用 | `TestConnectedAppsDefaultEnvironmentKey` 与 `TestConnectedAppsExplicitReferenceOverridesEnvironmentDefault`：缺失环境变量保留离线目录，存在时无需设置保存即可查询目录和连接；读取不写库、不创建绑定，显式 Connect 保存引用；已有引用优先，凭据缺失时不退回默认项目 | 使用真实环境解析和受控 HTTP peer，不证明真实项目 key 有效 |
| 项目与目录 | HTTP peer 核对项目 key header、目录查询／游标传递、托管 session 的关闭选项与认证链接主机；无项目显示待配置推荐，不能创建账户工具 | 不证明项目权限、供应商目录规模或特定应用受支持 |
| Web 账户管理 | `TestAppsOfflineDirectoryAndOwnerConnectionFlow`：真实 Server→Service→SQLite→HTTP peer，验证配置、连接、检查、断开、双语呈现、HTML escaping、分页及同源／身份拒绝 | 不自动替用户在真实 OAuth 页面同意 |
| 升级与禁用 | `TestConnectedAppsMigrationV45PreservesUIAndRollsBack` 验证 v45 升级、失败回滚、UI 数据保留和安装身份稳定；`TestConnectedAppsDisabledAccountWithdrawsTools` 验证禁用账户不执行、状态转失效且撤下工具 | 不证明供应商真实撤权时延 |
| 持久化与撤权 | `TestConnectedAppsLifecyclePersistentIsolationAndRevocation`：同应用双账户、项目切换限制、CAS、身份核对、重开数据库、断开后迟到检查及上游撤权失败 | 本地关闭不代表已撤回远端的既有效果；记录删除不保证原服务撤权 |
| 工具执行与恢复 | `TestConnectedAppsApprovedExecutionReplayAndUnknown`：真实 SQLite ToolInvocation／审批→HTTP 执行；未批准不调用，批准后固定账户／版本，成功重放不重复，502 持久 unknown；撤权、错误用户、描述漂移均拒绝 | 协议 peer 不证明 Gmail／日历等真实业务成功 |
| 冻结与并发 | `TestRegistryGroupReplacementKeepsSnapshotsAndOtherGroups`、生产装配测试：新增工具不扩权旧快照，撤销不能被旧声明重新选择，不影响其他工具；并发读取／替换 race | 每个执行闭包仍需独立重检撤权状态 |
| Code Mode | `TestConnectedAppsRealCodeModeDiscoveryApprovalAndFrozenConnection`：真实 JavaScript Sidecar 发现工具、挂起审批、恢复执行；新增第二账户不扩权旧 Root | 远端业务内容由 HTTP peer 提供 |
| 公开应用 | `TestConnectedAppsPublicToolkitRequiresExplicitEnableAndNoAuthTool`：未启用无工具，显式启用后只调用免认证操作，禁止变成有账户操作 | 真实目录的 `no_auth` 标记仍需验证 |
| 凭据与异常 | 客户端测试覆盖重定向拒绝、错误文本不泄漏密钥、账户私密字段不进入状态／页面、模糊 mutation 不重试、响应上限及取消 | 真实 Composio 托管凭据／令牌刷新由其服务持有，需要真实项目验证 |

工具插件的普通 Go 检查包括 `TestConnectedAppsPlugin*`：真实隔离包与 Code Mode 的换版／回退／退役／停用、原审批、unknown 不重复、Child 隐私和真实 release/reclaim，以及当前账户／Forget／Guardian 的迟到结果拒绝。原 8 MiB 响应经遮盖／JSON 转义后仍需逐字验证完整 Artifact；owner 接纳的错误方法也不能跳过授权、重复 POST 或发布非法输出。缺失载体不得恢复 native。

required `connected-apps-plugins` 系统门禁首先驱动实际标准30包二进制的首启／冷启动，检查选版不创建凭据或账户。随后以同一完整标准包运行 `TestConnectedAppsDefaultProductionCompositionAndColdLifecycle` 的 race：正常生产 App／Node provider bridge／Code Mode／ToolExecutor 连接到外部模型与 Composio HTTP peer，验证已有组合的原解释不被补选、连接前排队 Main 不扩权、新／冷 Run 的指引和实际读取、跨 close/open 的确切审批与一次 POST、unknown 不重发、单独移除工具包和继续检查账户。仅外部协议端点与可信测试载体被注入，直接 Actor 流程使用 CLI 控制面相同的无 broker 模式；不伪造内部结果或在恢复后修复选版。系统 launcher 要求 managed29／current SDK prior28／prior24／prior23／prior22 五个真实子场景均 pass，skip／缺失也失败；这些旧形状不冒充归档发行版。普通 Go 未提供完整分发时明确 skip，不能当作该门禁证据。此项和完整软件、提示／记忆、相关 race、系统与 Linux 验证共同闭环，HTTP peer 不等于真实供应商 Live。

`TestConnectedAppsGuidanceActualAccountDiscoveryUpgradeRetainedRollbackAndDisable` 比较原工具每个执行属性，驱动两个账户的真实 `describeTool`／`describeNamespace`，保留旧图文字且只返回本账户两项工具。`TestConnectedAppsGuidanceProjectionLeavesWriterAndRechecksOriginalAdmission` 验证 writer 可用、投影前后及重试前 Forget／隔离、停止、停用、跨进程账户替换和显示名更新。`TestConnectedAppsGuidanceAndMCPColdProjectionShareOriginalAdmission` 以真实双包组合验证两个冷投影共享原三次事务预算，并在两个消费者之间撤销原输入；后续调用必须马上使用真实冻结目录，不能额外修复 fixture。

`TestConnectedAppsPluginActualPagePolicyUpgradeKeepsOpaqueCursorWithoutDropOrReplay` 用真实 REST driver 和 Code Mode 比较 8／4 项独立版本，升级／回退后原 cursor 省略新 limit，验证精确末页、全部操作无丢失／重放及原查询／toolkit／描述符不变。`TestConnectedAppsMethodPagePortRejectsInvalidRecipeBeforeOriginalIO` 与 `TestClientMethodPageSizeBoundsAndOriginalOpaqueContinuation` 检查初始 1–1000 边界、无效数量和续页改数量在 IO 前失败。以上为本地协议 peer 证明，不替代真实 Composio cursor 的兼容验证。工具包已接入标准二十三包且完整分发已验收；F-071 目录／账户／页面及 Live 剩余门禁仍待完成；相关包正常／race 检查均须运行。

Live 缺口明确保留：创建项目后，用受控测试账户完成至少邮件／日历／文档各一次授权、读取和批准写动作；核对 scope、业务来源、实际结果与供应商用量，再验证 token 过期、重连与原服务撤权。授权 URL、密钥、token 和私人正文不进入测试日志或 PR。暂无项目时不把协议 peer 标为 Live 成功；F-071 维持部分完成。


部署／使用分层与 OAuth 交互验证：`TestAppsBrowserConnectionLifecycle` 启动真实 Web Server、SQLite、Composio HTTP peer 与 Chromium，执行无环境凭据时离线浏览→供应默认凭据后无需保存即可出现 Connect→自定义服务设置保存→Apps 无技术配置表单→首次 Connect→复用授权链接→自动变为 Connected→额外账户必填别名→弹窗拦截后的手动继续→按账户断开→失效后显式 Reconnect。将早先 ACTIVE 轮询响应延迟到 Disconnect 之后，证明前端也不会复活已断开账户；检查链接未进入浏览器存储，并覆盖中英文与 1400／390／320px。该门禁不访问真实 Composio 或账户。

`TestConnectedAppsAuthorizationReuseAndExplicitReconnect` 验证进程内链接复用、重启后无链接、重连生成新绑定及迟到授权拒绝；客户端 auth-config peer 验证分页、最新启用配置、明确覆盖、错误 toolkit 拒绝及 scoped key 的托管认证退路。配置与账户状态分开验证，轮询不对同一状态重复推进 CAS revision。


Apps 图标与目录：`TestAppLogosBundledForEverySuggestedApp` 检查每个推荐应用的同源 SVG 响应、可解析形状与缓存头；`TestAppLogoNewProviderMetadataUsesAnonymousBoundedCache` 验证新应用采用官方元数据、匿名读取与缓存，拒绝任意主机、带凭据／端口／查询参数的来源和未登记名称。`TestAppLogoSanitizationRetainsBrandShapesAndRejectsActiveContent` 覆盖脚本、事件、嵌入页面、外链、多根／损坏 XML、过大响应和重定向，同时保留品牌渐变。客户端目录测试校验 `meta.logo` 投影。

`TestAppsBrowserConnectionLifecycle` 另外验证完整目录中 65 个编辑应用全部加载真实图片、中英文发现页在 1400px 为双列、390／320px 为单列且无横向溢出，以及深色显示；账户详情弹窗可用 Escape 关闭，连接后侧栏显示品牌图标，断开后移除侧栏项。宽目录标题按自身内容边界对齐，保留 Settings 标题的纵向位置和字号。截图只含合成账户，浏览器门禁不访问外部账户或图标 CDN。


目录呈现验收：`TestAppsDiscoveryPreviewsAndCollectionsKeepEveryAppReachable` 验证六个精选顺序、每组最多六项、首页无重复、完整分组名称排序、所有编辑应用仍可达、保存的连接状态与未知分组拒绝；`TestAppsBrowserConnectionLifecycle` 验证精选／分组／全部应用导航、原生无脚本分组与实时目录入口，保留完整目录分类筛选、图片及分页覆盖；Settings 的 Apps 四宫格图标与 Resources 服务器图标可直接区分。保留中英文、移动端、搜索、应用详情及连接流程检查。


合入统一组件规范后的 Apps 浏览器场景复用真实 Base UI 分类选择器，验证取消筛选不提交；断开确认默认聚焦取消、Escape 保留账户、确认后仅断开目标。截图覆盖明暗主题的桌面／手机目录及账户详情，并检查 200% 缩放下弹窗及其内容留在可操作范围内。数据库迁移在已有 044／045 后追加 046，回滚夹具移除新表后按原有迁移路径重开。


目录浏览器场景核对无项目初始化／服务配置入口和冗余筛选确认按钮，服务配置通过 Resources 进入；选择沟通类别会直接得到对应 8 项推荐，恢复全部类别回到 65 项，不创建账户授权。

`TestProductionToolSurfaceKeepsOnlyFrequentControlsDirect` 使用生产组合检查 Main／Worker 均排除四个退役入口，保留的 web_search 可发现，冻结旧入口仍可选取并在 Code Mode 调用。`TestDefinitionsFreezePolicy` 检查 web_search v2 不再推荐退役入口且 v1 可恢复。`TestToolOutputTimingPrecedesCompleteArguments` 覆盖工具元数据／参数 delta 均可开始计时，空协议帧不能计时。

### Web Access 的托管搜索迁移

`TestWebPluginSearchActualTaskUpgradeRetainedRollbackAndDisable` 经真实 Task Root、ToolExecutor、独立 Go 进程、Host 来源端口、真实 HTTP 适配和 Artifact 发布，核对原查询、缺省值独立变化、显式数量、旧 Run、回退／退役、旧证据读取与停用不回 native；验收样例不能触发生产搜索。`TestWebPluginSearchDefaultPriorityIsVersionedButExplicitOwnerOrderIsFixed` 验证默认优先级确实属于插件，显式 owner 顺序仍固定。

`TestWebPluginSearchHostRefusesAcceptedMaliciousPlansAndNeverRepeatsSource` 即使独立 policy 认可超限数量、伪造来源位置、认证后继续换源或改写显式参数，Host 仍在原边界拒绝；已发生来源调用的失败保留 unknown，重放不访问来源。`TestWebPluginSearchLateForgetAndGuardianCannotReleaseSourceBytes` 从真实来源返回边界撤权，不能提交 Artifact 或成功私有正文，stop／resume 不能复活未知调用；Forget 父用例必须留在 required memory race 清单。

资源通道的 `TestResourceDomain*`、`TestResourceVerificationRequiresExactCompleteOwnerTrace` 与 SDK `TestResourceClient*` 检查实际隔离进程、超过 24 MiB 的有界往返、相同 key 只执行一次、不同输入拒绝、撤权、纯调用无权、原总期限与累计插件期限、篡改／截断／重复 JSON、独立且完整的验收轨迹。普通 Go 必需 lane 保留这些 owning tests；一般 SQLite race 自动发现这些新父用例。

这些用例覆盖搜索；完整 Web 迁移还必须同时通过下述研究／内容解析、24 MiB 证据读取／发布、原引用解释器、Child／恢复，以及完整软件／memory／系统与默认二十三包门禁。各组单独通过不替代整体验收。协议 peer 不替代真实账户、外部服务质量或 Linux 资源隔离证明。

### Web 证据阅读托管回归

`TestWebPluginReadPublishedInterpreterSurvivesUpgradeRetirementAndDisable` 通过真实 Main／Tool／Artifact 发布，比较独立包的 8000→6000 默认窗口，验证排队 Run、旧发布引用、任意导入文件、回退／退休／停用、全局停止与原图隔离。每次续读通过正常输入引用授权，不用重写历史绑定修复夹具。

`TestWebPluginReadComplete24MiBDocumentAndExactUnicodeFind` 读取完整上限文档的尾部 Unicode 标记，保留旧 JSON 重复字段／未知字段解释；`TestWebAccessReadPolicyPreservesSelectorsModesAndByteWindows` 覆盖多页／query 选择、原 UTF‑8 字节、极小显式窗口、缺失匹配和跨度超过 32000 的有序 fuzzy。`TestWebPluginReadRejectsAcceptedForgedMatchWithoutNativeFallback` 使用机械验证已通过的恶意包证明 Host 拒绝错误原文位置；`TestWebPluginReadProofRejectsFalseAbsenceAndChangedExplicitWindow` 证明不能伪造不存在或改写显式窗口。

`TestWebPluginReadLateForgetAndGuardianCannotAcceptPrivateResult` 在实际插件返回后触发原 Forget／Guardian，不能持久接纳私密结果；解除停止后重新领取原 Run 才允许 safe 读取。`TestWebPluginReadOriginalVersionAfterRealReleaseRecoveryAndClaim` 在真实 running Tool 后换版、release、扫描恢复并重新 claim，第一笔读取仍使用原版本，不补选或重写夹具。真实 Main→Code Mode→已保存文档同时运行 native 和 managed 两条历史／托管入口，媒体／证据／子工具终态保持原子提交。原引用测试另在停用后的方法成功返回与 Tool 提交之间隔离原图，验证同一原 Tool 事务拒绝迟到接纳。新 WebPlugin 根用例进入 `make test-web-access` 的必需 race 选择器，Forget 根也进入 memory race 的可达性检查。

以上是搜索／阅读切片的定向证据；完整 Web 功能仍需研究／解析／预览、Child／原媒体资源、固定源码完整软件／适用 prompt／memory／system 及默认分发验收。不能把默认十九包或部分服务测试计为 Web 全部迁移完成。

### Web 研究编排隔离实验（整体仍在迁移）

`TestWebPluginResearchActualBatchSummaryAndCompleteEvidence` 运行真实 Task／两条原 query／去重正文／部分失败／原模型摘要账本与完整 Artifact；`TestWebPluginResearchPageAnswersUseOriginalFrozenModelAndReceipt` 验证两个原页的模型、key、usage 与重放不重发；`TestWebPluginResearchAssessmentHashesOriginalPageBytes` 验证原文跨度、URL 与源／摘录双哈希，结果只为 unclear。托管 Main→真实 Code Mode→媒体与完整证据续读继续覆盖研究服务。

`TestWebPluginResearchRealPreServiceRunUpgradePreviewRollbackAndDisable` 构建真实两服务旧包，先接纳 Run 再激活三服务包；证明历史 Run 仍用原编排，4096→1024 预览独立换版、排队 Run、完整原文、回退／退休与停用无 native 兜底。`TestWebPluginResearchAcceptedBadPackagesCannotFetchForgeOrSpend` 机械验证通过的坏包仍不能扩大来源位置、伪造摘录跨度或在未授权 mode 下调用问答；发生搜索后的不确定结果沿用原 unknown，重放不重复 IO。

`TestWebPluginResearchLateForgetAndGuardianAtPublicationBoundary` 在实际方法返回后和原 Tool 提交前分别触发 Forget／原图隔离，无成功私密结果或 Artifact。`TestWebPluginResearchBudgetApprovalBeforeAuxiliaryModel` 核对原审批回执、model_generation 的预算拒绝与模型调用为零；不把既有公开通用错误标签误当作预算绕过。`TestWebPluginResearchPendingModelCannotPublishOrRepeat` 的供应方返回正文但不完成原模型回执，结果必须 unknown、无 Artifact，真实 release／claim 后也不能重新调用模型。

`TestWebPluginResearchLargeCompleteEvidenceDoesNotUsePureResultLimit` 通过八个原 URL 保存超过 20 MiB 的完整文档；每页原文与 URL 必须相等，预览保持 4096 字节，不能因为纯输入／结果上限裁短全文。直接 Tool 长用例按生产 Actor 的同一租约机制续租，不修改生产超时。全部根进入 `make test-web-access` 的必需 race 选择器，Forget 根另在 memory race 可达性检查中保留。

这些证明研究／搜索／读取真实接入，不代表旧 fetch、解析／规范化、完整 Child／媒体资源和默认分发已接纳；整体还需完成迁移后运行固定源码完整软件、适用 prompt、memory／system 及默认分发门禁。

### Web fetch／来源规范化隔离实验（整体仍在迁移）

`TestWebPluginFetchActualUpgradeOriginalRunRollbackAndSafeReplay` 构建真实三服务旧包，验证原 Run／已排队 Run、4096→1024 换版、回退／退休、原完整 plain-text Artifact 和停用不回 native。`TestWebPluginFetchRealReleaseRecoveryKeepsOriginalPreviewAndReceipt` 在真实 running Tool 后换版、release／恢复扫描／重新 claim，首个交互仍使用原 4096 预览和同一 Tool／完整 Artifact，完成回执重放不发第二次 HTTP。`TestWebPluginFetchAcceptedForgedRangeCannotInventPreview` 的坏包虽通过机械验证，仍不能伪造字节范围或发布引用。`TestWebPluginFetchLateForgetAndGuardianBeforePublication` 覆盖方法返回与原事务提交前的 Forget／隔离。

`TestWebPluginSourceFormattingActualHTTPUpgradeRawRowsAndRetainedRun` 经真实 SearXNG HTTP peer、原凭据和原 query 验证旧快照、标题 1024→2048、摘要 2048→4096、从原第 25 行反向选择与回退；不能以旧 Host 已裁剪字段伪造独立演进。`TestWebPluginRawSourceFactsKeepFullSanitizedRows` 对 SearXNG／Tavily 保留超过旧上限的字段／行，同时拒绝 credential URL 并在全字段中遮盖 raw／URL 编码凭据；原 legacy 格式仍保持旧软上限。

`TestWebPluginSourceFormattingAcceptedBadPlanCannotReleaseOrReplay` 的已验证坏格式计划不能取得引用，原来源 unknown 后重放不发第二次 HTTP。`TestWebPluginSourceFormattingLargeSanitizedInputUsesOriginalDescriptor` 将原合法响应展开到超过 1 MiB 的已遮盖输入，证明分片／摘要输入没有收窄原能力。`TestWebPluginSourceFormattingExaOriginalLimitAndIndependentUpgrade` 经过实际四步 MCP peer 保留原 query／numResults／无账户头，比较 20000 与 30000 文本预算，验证正文和标题原值；不是 Exa Live 验收。`TestWebPluginSourceFormattingLateForgetBeforeSourceRelease` 在格式计算后、返回来源前触发 Forget／隔离，无私密 Artifact／成功回执。

以上根均进入 `make test-web-access` 必需 race；新增 Forget 根也进入 memory race 可达性检查。网页／媒体解析、完整 Child／原资源与最终默认分发仍需继续迁移和最终完整门禁，不把当前定向证明计为全功能完成。


### 简单 HTML 解析托管回归

`TestWebPluginHTMLActualUpgradeRetainedRunRollbackAndDisable` 构建真实旧五服务包与新六服务包，独立改变 nav 提取算法，验证历史 native Run、排队 Run、回退／退休／停用和完整 Artifact；停用后的新 Run 不执行原生兜底。`TestWebPluginHTMLCompleteEightMiBSourceAndDerivedText` 在原 content Tool 中解析完整 8 MiB 原始输入，保存超过普通 2 MiB 返回值的派生正文；没有额外 rsi_call 账本。`TestWebPluginHTMLLateForgetAndGuardianCannotReleaseDerivedText` 在解析返回、资源释放前触发 Forget／隔离，不能产生成功私密引用。以上根进入必需 Web race，Forget 根保留 memory race 可达性。

`TestWebAccessHTMLDecoderReceivesOriginalBytesAndNeverFallsBack` 验证真实 fetch／content 解码位置、原始 HTML 字节、失败与取消无兜底、raw 模式及原完整 plain-text 边界。旧 HTTP、DNS、重定向与 UTF‑8 检查仍由原 owning 回归验证；没有把可注入 decoder 当作另一条网络通道。Node HTML／PDF 和图片算法由下述独立回归验证，不以简单 HTML 通过代替全功能验收。


### 图片转换托管回归

`TestWebPluginImageActualUpgradeRetainedRunRollbackAndDisable` 从真实旧六服务包升级为七服务包，修改 2000→1200 像素、JPEG 质量／说明及 JPEG→PNG；检查实际已保存图片的尺寸、编码、MIME、文件名、原来源时间和证据引用，并取样来源对应像素，拒绝只有合法 header 的空白／替换图片。覆盖排队 Run、旧 native Run、退休包仍有在途引用、回退和停用后不回 native。`TestWebPluginImageCompleteEightMiBSourceAndLargeDerivedOutput` 消费完整 8 MiB 原始 JPEG 输入并保存超过 2 MiB 的真实编码图片，不以小 fixture 掩盖普通返回值上限。

`TestWebPluginImageAcceptedForgedMetadataCannotPublishOrFallback` 的三个实际坏包通过机械 owner fixture，分别伪造尺寸、将 JPEG 误标为 PNG、截断正文但保留可解析 header；Host 必须拒绝实际图像，并且重放原失败回执不再读取来源、不用 native 修复。`TestWebPluginImageLateForgetAndGuardianCannotPublishAttachment` 在实际方法返回后撤销权限／隔离图，不能提交图片附件或文档。

`TestWebPluginImageChildUsesRetainedOriginalParserAndPrivateScope` 在父执行建立后激活新包并退休父包，真实 Child 首次读取仍产生原 2000 像素图片；核对父子图／服务清单与私密消息隔离。`TestWebPluginImageRealReleaseRecoveryDoesNotReplayUnknownSource` 在真实解码返回时 release 原 claim，经恢复扫描与重新 claim 后立即执行，保持原 unknown 不重发、不补 fixture／重新选择算法。

`TestWebAccessImageDecoderKeepsOriginalScopeAndFailsClosed` 检查实际 fetchContent 原字节／检测 codec、raw／answer 原限制、8 MiB 与声明像素硬界限、错误／取消无兜底。新业务根纳入必需 Web race，Forget 根保持 memory race 可达性。Node HTML／PDF、分发配方、完整默认分发与最终软件／适用系统门禁须分别覆盖，不以图片回归代表完整 F-069 产品交付。


### Node 解析资源底座与托管接入实验

`TestWebAccessParserResourceActualHTMLPDFMatchOriginalDependencyBytes` 使用真实 Node 与当前安装依赖，把插件方法及依赖打包后放入资源库和 OS 隔离进程；实际 HTML／PDF 的标题／正文与原脚本逐字对照。`TestWebAccessParserResourceRetainsActualCommandAndModuleAcrossReplacement` 替换原命令／模块字节并引入新 revision，旧 revision 仍执行原方法；不同 revision 复用同一封存解释器，不依赖原可变安装路径。

`TestWebAccessParserResourceRejectsCorruptionPathsAndUnboundedCapture` 覆盖模块、解释器、元数据、symlink、路径伪造、取消和字节界限。`TestWebAccessParserResourceOSBoundaryDeniesAmbientFSNetworkForkAndEnvironment` 的真实候选尝试读取／写入外部私密文件、发本地 HTTP、fork 和读取 Host 环境，必须全部失败且 HTTP peer 无请求。`TestWebAccessParserResourceOriginalEightMiBInputLargeOutputAndCancellation` 消费完整原输入并返回超过普通纯服务上限的输出，验证取消与物理输出上限。

新 package 纳入 `make test-web-access` 的递归内容范围及必需 CI 根检查，打包脚本／插件方法纳入语法检查；共享隔离规则还须跑完整 RSI 回归。这些资源根只证明封存字节与纯计算资源边界；原 Tool／Run／Child、Guardian 和恢复须由下述实际业务根单独证明，默认分发由下述二十三包场景验证。Linux 必须使用真实解释器与 HTML／PDF 验证原 2 GiB 地址空间界限及生产 bubblewrap／seccomp，不能用交叉编译或裸解释器成功代替。


`TestWebPluginExtractActualHTMLPDFUpgradeRetainedResourcesRollbackAndDisable` 组合真实两插件 graph，独立把 PDF 标题 Page→Sheet，覆盖旧／排队 Run、当前原始命令与脚本被替换、旧包退休仍有在途引用、回退、停用和原完整 Artifact；不在换版后修复原路径。`TestWebPluginExtractGuardianRejectsActualModuleAndForgedScopeOrProof` 由真实候选模块参与验证，拒绝源 URL 修改、伪造 computed 证明及错误算法。

`TestWebPluginExtractLateForgetAndGuardianBeforePublication` 在真实提取方法后撤权／隔离，不能提交私密正文或引用。`TestWebPluginExtractChildRetainsOriginalGraphAfterInstallationReplacement` 在父执行建立后换版、退休父包并替换当前安装，真实 Child 仍使用原 PDF 方法和父图，且无父级私密消息。`TestWebPluginExtractRealReleaseRecoveryDoesNotReplayOriginalNeverTool` 在实际解析返回时释放 claim，经真实恢复扫描／新 claim 后，首个调用保留 unknown，不再次读取来源。

`TestWebPluginExtractCustomAbsoluteCommandAndScriptKeepFullInputAndLargeResult` 使用所有者独立的原绝对命令与自定义脚本，在当前安装被替换后消费完整 8 MiB 输入、通过 computed descriptor 保存超过 3 MiB 正文；不能用默认脚本替代自定义来源，也不能以普通结果上限截断。默认组合、Linux 生产沙箱业务链路及适用完整产品门禁必须另行通过；迁移验收不代表完整 Linux 产品或真实 Guest 平台验收。


`TestWebPluginExtractMissingCatalogAndChangedConfigCannotUseRetainedAuthority` 区分“安装文件换版仍用原封存资源”与“所有者配置授权改变”：缺 catalog 时不可回 native，配置来源变化时不可复用旧授权，二者都在读取来源前拒绝。`TestPluginParserResourceControlReturnsExactSourceBinding` 检查操作员捕获返回当前配置的确切 binding，输出不含原命令／脚本路径。Web 与提取两个实际组件分别进入 `make test-web-access` 的两次 race，CI 验证所有现存 WebPlugin 根都至少命中一个必需分区，不延长超时或删去旧根。


`TestWebPluginRouteActualNodeChoiceUpgradeRetainedRollbackAndDisable` 用实际 HTML／Node 两种受管方法证明分派独立换版；覆盖排队旧 Run、原完整 Artifact 与来源、退休包的在途执行、回退和停用。`TestWebPluginRouteHistoricalSevenServiceRawRunKeepsOriginalBytes` 使用真实七服务旧包，激活八服务新包后旧 Run 的 raw 字节与原服务集不变。

`TestWebPluginRouteAcceptedInvalidRawPlanCannotFallbackOrReplay` 让独立验证通过的候选在原 raw 输入返回不兼容方法，Host 必须拒绝，原失败回执重读不重新获取来源。`TestWebPluginRouteLateForgetAndGuardianBeforeDecode` 在实际分派返回处撤权／隔离，解码器不被执行，私密正文、Artifact 和成功回执均不提交；Forget 根保持在必需 memory race。

`TestWebPluginRouteChildRetainsOriginalRouteAndNodeAfterUpgrade` 在父执行冻结后更换分派政策、退休原 Web 包并替换安装中的脚本／命令，真实 Child 仍采用父图的原分派和 Node，且不继承父级私密消息。`TestWebPluginRouteRealReleaseRecoveryDoesNotReplayNeverTool` 在分派返回处释放 claim，真实恢复扫描／新 claim 后立即检查原图和 unknown，不能通过额外 select 修复夹具或补发原来源请求。

`TestWebAccessManagedRoute*` 验证同一生产后传输解码器：raw 原字节及来源保真、完整 8 MiB、原 2／8 MiB 超限、编码和空正文失败、不兼容 codec 与缺失受管 decoder 均不回 legacy。`make test-web-access` 保留三个必需 race 分区：原 Web 核心、实际 Extract／Route，以及真实首启与自定义资源冷重启；CI 动态枚举全部 WebPlugin 根确认可达，不删减原场景或延长单分区超时。


`TestWebAccessParserBundleDoesNotExecuteSourceAndRetainsCompleteNotices` 确认封存不执行源代码且原许可证全文保留。`TestWebAccessParserBundleRetainsActualOwnerImportsAfterSourceReplacement` 从真实所有者脚本与依赖得到原行为，再替换两者，隔离目录内的封存模块仍产出原结果。`TestWebAccessParserBundleRejectsEscapesUnboundedInputAndNetworkResolution` 覆盖取消、非绝对路径、单文件超限、范围外导入及远程导入无网络请求。`TestWebAccessParserBundleRejectsSpecialFilesWithoutBlocking` 覆盖已复现的命名管道输入阻塞，要求明确失败而非等待管道写端。`TestWebAccessParserBundleKeepsCanvasAbsentAndRejectsEscapingNotice` 证明安装中的可选 canvas 不成为封存的额外资源，许可证符号链接也不能带入范围外原文。

`TestPluginParserBundleIsOfflineAndPreservesOutputOnFailure` 经真实 CLI 语法及执行分发，在配置缺失时构建可运行模块，失败保留原输出，且不创建用户状态库。原 parserresource HTML／PDF 与独立 Web 提取夹具也必须使用同一 Go 封存入口，不能仅用新单测掩盖旧生产器仍被调用；Go 模块校验与原生许可证收集纳入封存器变更验收。


### 默认分发与冷重启验收

`TestWebBootstrapActualMethodsUnavailableResourceAndOwnerSelection` 使用真实封存模块和独立包，分别验证原解释器与未配置资源下的 HTML／PDF 方法、初次组合、原选择保留和停用不复活。`TestWebDefaultCustomSourceColdReopenRetainsOriginalModuleAndInterpreter` 从原自定义模块／依赖捕获资源，关闭真实 Store 后替换安装文件与解释器并移除依赖；重开后的第一次提取必须仍消费完整 8 MiB 并得到原方法的大于 3 MiB 结果，不得补 select／capture 修复。`TestWebDefaultInvalidOptionalSourceKeepsOtherManagedMethodsAvailable` 保留失败诊断，同时验证其他受管方法可用。以上进入必需 `make test-web-access` race。

`TestWebPluginExtractMissingCatalogAndChangedConfigCannotUseRetainedAuthority` 同时覆盖旧完整 scope 与新解析来源 scope：改变端点后旧 Run 在读取正文前被拒绝。默认分发的实际系统场景通过 `verify_web_plugins.py` 验证授权端点改变后的新 Run 可以使用原解析图，不将解析来源身份误当完整网络权限。

required `web-plugins` 系统 lane 通过正常 provider bridge、HTTP 模型 peer、真实 Code Mode、ToolExecutor、完整标准30包（包括四个 Apps 包）及独立 SearXNG HTTP peer 执行业务。场景包含无配置解析器的首启／冷重启、实际搜索的首轮与冷重启首次依赖行为、所有者端点变更的新 Run、原网络结果抵达模型续轮、原图不改写、无 Web 重复效果账本、显式停用后重启不复活。每条 CLI 都是新进程，不手工激活或中途修复 fixture。

这些软件场景不证明真实外部供应商质量、所有平台默认分发或完整产品门禁已经通过。旧冻结图／Run／Child、撤权／Forget、unknown 不重放与大文档仍运行前述真实业务回归；最终接纳保持源文件和二进制固定，另运行完整软件、prompts、memory 与相应系统门禁。

默认组合首启由系统夹具以有限60秒等待真实 runtime ready；首次账户登录含模型启用，同样需要等待这次冷准备。该窗口只属于系统验收的启动阶段，不改变原生产 OAuth、Run 租约、Tool 和逐例插件资源期限。独立验收用例有界并行但仍各用独立进程，原生计算保持串行屏障；不得删减 owner 用例、跳过环境验证或增加恢复后的手动 select／update 来获得通过。


Apps 公共编辑私有实验：`TestAppsEditorialActualOwnerHTTPVersionsSnapshotRollbackAndDisable` 构建真实隔离包和所有者载体，经原身份／持久渠道绑定读取实际离线目录、主页及详情；独立版本改变名称、用途、精选顺序及特定查询的匹配规则，并经实际主页／完整分组／详情验证，同次 HTTP 只读一次。原生期望来自迁移前真实发现页与原数据的独立捕获，包含 65 项完整分组、首页预览和十组空查询、大小写／空白、分类、交集及无匹配查询，覆盖换版／冷重启、停止后冷启动、回退／退役／隔离／停用后冷启动、独立账户管理与无项目／账户副作用。`TestEditorialManagedFailureNeverRestoresLegacyData` 覆盖读取错误、重复 slug、非法 ID／文本、缺失发现方案、未知条目／集合及重复集合 ID、缺失／悬空／重复筛选引用与非法／重复分类、独立新目录、合法空列表和显式 pre-service 原名单。默认分发、完整目录工作流／视图／资源及真实服务验收另行完成，不能以这些用例替代；相关包与根纳入 `make test-connected-apps` race。

公共目录的真实浏览器分支进入必需 `make test-browser`：使用真实 App／SQLite／隔离插件／原身份与渠道授权，读取换版并冷启动后的新精选顺序、名称与用途，随后验证回退。检查完整分组排序、全部 65 项和全部品牌图标、明暗主题桌面／手机无溢出与列数，并保留截图；不使用伪造目录输出，也不访问真实账户。`test_editorial_browser_branch_remains_in_required_software_gate` 保证该分支不能因可选环境开关而在必需软件门禁中静默跳过。

`TestEditorialQuerySnapshotCannotBeReusedForAnotherSearch` 验证原查询精确复用已接受结果且不重新执行候选，另一查询被拒绝；详情元数据仍保持原快照。实际隔离包在独立版本中为 `uniquehint` 增加匹配，而回退后恢复为空；原生查询夹具在移动算法之前捕获，不能由候选计算期望。浏览器同时验证该新规则和原大小写／空白语义。


目录列表来源：`TestAppsDirectoryActualSourcePolicyPagesVersionAndColdLifecycle` 通过实际 App、原身份／渠道、持久项目引用、真实隔离版本与 HTTP peer 验证无配置不发请求、原查询／分类／来源／密钥、40→20 首屏、升级／回退／冷启动后原游标完整续页、显式离线优先及不同失败处理、真实页面、隔离／停用后不复活实时来源及独立项目验证不受影响。另一个候选故意放宽自身 schema 并隐藏 legacy／1001 分支；Host 必须在外部 IO 前拒绝。`TestClientCatalogPageKeepsOriginalQueryAndOpaqueContinuation` 验证实际 1000 上限、原 UTF-8 查询、不透明游标、续页无 limit、固定范围与密钥遮盖，错误参数不得到达网络。`TestEditorialRouteCannotInventLiveAuthorityOrRewriteCursorSize` 保护独立来源／数量和旧解释器边界。以上根均进入必需 connected-apps race；未接真实供应商，不以协议 peer 证明真实分页或 OAuth 质量，详情来源／实时分类／视图资源与完整默认分发仍需独立完成。


详情取舍：`TestAppsDirectoryActualDetailMatchesOriginalNativeFixtures` 对迁移前捕获的 11 组实际旧 Service／SQLite／HTTP 结果，使用真实 App、持久 fixture 输入、隔离包与固定 Toolkit driver 逐项比较完整 Toolkit／错误；这些是既存事实输入，不冒充账户创建或恢复证明。`TestAppsDirectoryActualSourcePolicyPagesVersionAndColdLifecycle` 另走真实显式 Connect 形成公共绑定，验证独立版本转为本地 connection-first、页面一致且不读上游，回退／冷重启恢复实时配方；停用后已存账户事实可见、无关应用不恢复 native 查询。`TestEditorialDetailSnapshotCannotChangeSourceAuthority` 保证来源事实不能换到另一个快照；`TestEditorialLookupCannotInventAuthorityOrAnInterpreter` 保护原实时授权／封闭解释器边界。故意放宽 lookup schema 的潜伏分支也必须被 Host 拒绝。上述根由必需 connected-apps race 与普通 Go 回归保护，完整产品分发、真实供应商及其余视图／账户业务仍分别验收。


实时分类：`TestAppsDirectoryActualDetailMatchesOriginalNativeFixtures` 另验证迁移前独立捕获的六组分类结果；`TestAppsDirectoryActualSourcePolicyPagesVersionAndColdLifecycle` 经实际上游 HTTP → 当前原始 graph 的第二纯方法 → 原引用投影，验证首次／末次选择的独立版本、已选原值补充、7 万余条跨 256 KiB 输入分块完整到达以及 3 MiB 标签不截断。将上游阻塞后换版，原请求仍须用旧分类方法；上游返回前隔离，不能释放 Catalog／部分分类或选择 native／新图。潜伏坏位图须被 Host 拒绝，不追加 HTTP。`TestEditorialCategoryPlanUsesOnlyOriginalReferences` 检查匿名相等事实及原标签引用，`TestEditorialCategoryPlanRejectsInvalidMasksAndDuplicateReferences` 检查长度、尾位、重复 ID 和伪造追加。这些根在必需 connected-apps race 和普通回归中可达；真实供应商、Linux／默认整包与剩余视图／账户业务仍分别验收。


卡片摘要：`TestAppsDirectoryActualDetailMatchesOriginalNativeFixtures` 另比较迁移前独立捕获的八组原 Web 输出。`TestAppsEditorialActualOwnerHTTPVersionsSnapshotRollbackAndDisable` 通过真实隔离包验证 129 项完整分批、每条 3 MiB 原文仅发送字数、150／147→120／117 的独立版本、冷启动／回退、冻结请求不追随 active 以及隔离不回退。`TestAppsDirectoryActualSourcePolicyPagesVersionAndColdLifecycle` 经实际来源 HTTP 与所有者页面验证默认／升级／冷回退摘要；潜伏越界计划不释放原文或重试来源。`TestEditorialCaptionsProjectPrivateOriginalSourcesAcrossBatches` 验证仅传原字数和 Unicode 原前缀；`TestEditorialCaptionsRejectInvalidReferencesWithoutPartialOrFallback` 覆盖错误来源、负数／越界、文字非法／过大、缺方法、数量错误与第二批失败的原子性。这些根进入既有必需 connected-apps race；页面仍须检查桌面／手机及深浅主题。名称／账户展示、排序、视图／资源、默认整包及 Linux／真实服务仍分别验收。


名称与首字母：`TestAppsDirectoryActualDetailMatchesOriginalNativeFixtures` 对真实隔离默认贡献验证九组迁移前旧 Web 标签；`TestAppsLabelsProjectBeforeCatalogueOrder` 保护三组原排序及标签必须先投影的依赖。`TestAppsEditorialActualOwnerHTTPVersionsSnapshotRollbackAndDisable` 验证原名称／glyph 与独立 slug／原大小写版本、3 MiB 原名不进入小帧、冻结旧方案、冷启动／回退及原事实不改写；实际页面和浏览器检查可见文本，不能以 URL／属性里的 slug 匹配冒充标题验证。`TestAppsDirectoryActualSourcePolicyPagesVersionAndColdLifecycle` 使用原排序 fixture 经实际上游 HTTP 到页面验证默认／新标签／冷回退的顺序和 glyph，潜伏非法标签来源须在 IO 前拒绝。`TestEditorialLabelsProjectOriginalFieldsWithPresencePlans` 和 `TestEditorialLabelsNeverInferNativeFromInvalidSelection` 验证原引用、Unicode 与空／非法方案不恢复旧默认；相关根进入必需 connected-apps race。浏览器对发现集合的精确原 fixture 顺序、两个版本实际标签和查询结果独立断言，默认仍检查名称排序，并评审深浅／桌面／手机截图。账户工作流、视图／资源及默认整包／Linux 仍需后续验收。


当前页排序：`TestAppsDirectoryActualOrderCompleteSourceVersionColdAndInvalidWindows` 经真实 App／原客户端／隔离服务验证 50,003 行的原完整排列 hash 和四个输出窗口、输入大于 256 KiB、两个原生捕获的 10 MiB／9 MiB 名称 hash 与顺序、反序版本／冷重启／回退、旧冻结图、潜伏重复／越界／短页与第二窗重放、第一窗后实际隔离不发布部分结果，以及停用后冷启动不恢复 native。状态密钥引用是受控原源 fixture 输入，不冒充账户创建；独立账户流程仍由原 Source 生命周期回归证明。`TestAppsDirectoryActualSourcePolicyPagesVersionAndColdLifecycle` 通过实际页面的顺序／glyph 验证真实托管分派，坏排列不能释放原行或多读源。`TestEditorialOrderCompleteWindowsFreezeOriginalFactsAndLabels` 与 `TestEditorialOrderInvalidOrPartialPermutationNeverFallsBack` 检查精确窗口、标签／输入冻结和完整原索引，不把缺方法或空选择解释成原生模式。

`TestCatalogueOrderFactsUseExactPrivateWireAndRejectCorruption` 对独立固定字节检查私有编码／损坏边界；`TestCatalogueOrderFactsPreserveCapturedNativeNormalizationBeyondInputSize` 对原客户端独立捕获的 hash 验证扩张后完整内容及原源字节预算，不能只做镜像往返。普通原生排序、50,003 行和规范化输入在候选前捕获；65 项默认顺序由不改动的固定原源码独立执行后捕获，未执行到目标 root 的首次尝试排除。上述根和 codec 包在必需 connected-apps race 可达。管理浏览器按独立原 65 项默认顺序及明确反序版本检查全部项，并为首页和全部页评审明暗／桌面／手机截图；供应商分页和内部结果窗口分开验证。账户工作流、视图资源、默认24／旧实验图／Linux 与真实供应商仍单独验收。


卡片状态：`TestAppsDirectoryActualCardStatesOriginalRowsVersionAndRevisionFence` 经实际 App、持久受控原行和隔离候选比较十四组迁移前事实，验证 65,539 行的完整窗口、重复卡片／无匹配行、第一／最后 active 独立版本、冻结图、冷启动／回退、实际页面 Add account 可见性依赖原 Public 行。原状态 fixture 输入不冒充真实账户创建。旧修订调用在移走测试载体后仍返回原冲突，证明私有读前拒绝；第一真实窗口后修改状态必须丢弃全部结果。潜伏越界／非 active／非 pending 引用在 Domain 及实际页面拒绝，隔离不恢复 native。

`TestAppsOriginalInventoryAndStateProjectionRemainExact` 保护十四组兼容清单资格／去重与两种原排序；真实受管分派另由下述实际 App 用例验证。`TestEditorialCardStatesUseOnlyScopedFactsAndOriginalPublicValue` 检查匿名精确 facts、修订参数和原 Public 投影；`TestEditorialCardStatesRejectForgedReferencesAndPartialGroups` 检查错范围、伪造状态、无匹配原行、部分 Toolkit 失败及缺方法／空选择不恢复原生。这些根进入必需 connected-apps race。旧窗口、JSON／原生账户控制、页面与目录浏览器保持原行为；账户工作流、视图资源、默认整包／旧实验图／Linux 仍分别验收。


账户清单：`TestAppsDirectoryActualInventoryOriginalReferencesVersionAndCold` 从原迁移前十四组 fixture 经实际 App／持久状态／隔离候选与认证 HTTP 验证原引用、首页侧栏和已连接页的不同同名顺序；65,539 行默认选首行，独立版本选末行，同时旧请求保持首行。实际可见名称、冷启动、真实回退及重启、潜伏越界和隔离均需验证。原行 fixture 不冒充账户创建，账户动作继续由现有真实流程回归覆盖。`TestEditorialCardStatesUseOnlyScopedFactsAndOriginalPublicValue` 同时检查原源位置投影与所有状态重复读取不再执行候选；部分组失败不得返回局部引用或状态。该 App 根通过 `Apps` 前缀进入必需 connected-apps race，并由 CI 清单回归保证可达。账户工作流／资源和完整分发仍单独验收。


私有排序：`TestAppsDirectoryActualPrivateOrderLargeOriginalColdAndFences` 使用实际 App／持久原状态／隔离候选比较迁移前两个原生 hash，覆盖 13 MiB 完整名称、单个 3 MiB 名称及 Unicode／不稳定同名排序；另验证 16,390 行完整排列的两窗输出、原名称投影 hash、实际认证页面顺序、仅改变私有比较器且其余政策不变的独立反序版本、冻结请求／冷重启／真实回退。普通纯读对同一大输入仍拒绝；原 Host Fence、流读取途中撤权、移走载体后的启动前状态冲突、第一真实窗口后变更状态、潜伏重复／越界／短页／跨窗重放及隔离均不得释放私有／局部结果。停用后冷启动不得恢复受管元数据；仍原生的独立账户控制单独列为余项，不冒充迁移或账户创建证明。

`TestEditorialInventoryOrderCompletePrivateWindowsAndOriginalRevision` 保护同一原数据／修订／页面语义在两窗不变；`TestEditorialInventoryOrderInvalidPermutationNeverReleasesPartialOrNative` 保护原索引覆盖、后窗失败、缺方法与空选择；`TestInventoryOrderFactsExactOriginalBytesAndRejectMalformedInput` 独立固定 framing 验证 NUL／无效 UTF-8／反斜线、长度、数量和尾随数据。SDK 的 `TestInputStreamPreservesLargeIdentityAndDoesNotWidenOrdinaryRead` 验证大输入完整 digest、短写／取消／未授权身份拒绝和常规上限不变。这些根及 SDK 包进入必需 connected-apps race，CI 清单回归保护可达；完整软件检查继续覆盖既有 DOCX／OCR／资源／隐私路径。


账户连接意图：`TestAppsAccountIntentActualNativeEffectsVersionColdAndPrivateFences` 对迁移前捕获的十六组原 Service／原 HTTP 客户端／记录 CAS 的 fixture，在实际 App／SQLite／受管隔离包中比较原错误、来源请求顺序／别名、保存次数、原行 hash、授权链接及新增原行。原 fixture 的 CAS 不是实际 SQLite，不能混称；真正的持久流程由此回归单独证明。覆盖首末等待复用、显式别名、公共／私有 active、未知／断开状态、原随机 suffix、缓存缺失不创建、来源 session 失败不重试、公开免认证及超过 8 MiB 的历史别名；UUID／墙钟仅作确定性规范化。命名空间刷新读取与原连接主流程分别观察，不把它计作托管的新来源权限。

同一回归验证读流中撤权、移走载体后的旧状态启动前拒绝、实际来源 IO 中换版仍沿用旧已接纳别名、新版／冷启动认证 HTTP 连接、真实回退、目录组件移除后账户仍可连接、潜伏越界／显式参数改写／重复别名拒绝且来源调用为零、隔离／冷停用不恢复 native，以及所有者仍能关闭原授权。输入以原行 fixture 提供，不冒充真实外部账户创建；后续仍原生的认证与效果工作流必须独立迁移和验收。

`TestAppsConnectIntentScopedFactsAndOriginalPendingIdentity` 保护原组范围、最小事实与空原 ID 的缓存语义；`TestAppsConnectIntentRejectsForgedAliasReferenceAndManagedFallback` 保护原引用、显式意图、唯一性、空／错误选择。`TestAppsReconnectUnavailableIntentDoesNotCloseOriginalConnection` 证明缺策略时不先关闭原连接，以及原重连两阶段不重选图而使用撤权后的修订。`TestAppsAccountIntentFactsPreserveExactPrivateAliasBytes` 对固定独立 framing 检查 NUL／无效 UTF-8／原长度和事实 bits，拒绝伪造／多余输入。上述根与 `appsaccounts` 包进入必需 connected-apps race；CI 清单回归保护可达。旧 Directory 私有围栏与原生账户浏览器继续运行；完整工作流、默认分发／保留图／Linux 仍待后续验收。


认证配置：`TestAppsAuthConfigActualOriginalSourceChoiceVersionAndFences` 对迁移前原 Client／真实 HTTP peer 捕获的 23 组 fixture，在实际 App／SQLite／隔离包中比较来源 URI／次序、错误、实际 session 提交的原配置 ID 与连接状态是否落盘；分别验证明确无 hosted 和能力未知。覆盖显式／保存／自动来源、managed 显式配置、原资格与归属、created／ID tie、opaque 游标、后页权限错误、重复游标、20 页完成／耗尽及 10 MiB 原解码字符串的完整 hash，credentials 字段不能进入 metadata 或候选。

同一回归在第一页真实 IO 中换版，后续比较与第二页请求仍使用旧政策；新动作采用最早配置／100 条页大小，冷启动和真实回退保持实际 session 选择。设置保存前校验同样受管。来源返回后改变原状态、读取途中隔离、潜伏越界／外部行／跨阶段重取，以及改写显式来源都不能创建 session、重放来源或改变连接状态；停用冷启不恢复 native。旧连接意图的真实授权缓存与前置重连围栏继续回归。

`TestAppsAuthConfigHostRejectsChangedIntentAndForgedCommands` 独立拒绝显式来源改写、超限、错归属／越界引用、重取、伪造权限回退和结束后继续；`TestAppsAuthConfigHostPinsSourceCursorAndOriginalPrior` 验证原 opaque 游标、原 prior metadata 和实际候选选择不能被 Host 自行排序替换；`TestAppsAuthConfigFactsExactBytesAndRejectInvalidMetadata` 对固定独立字节验证完整字符串／NUL／无效 UTF-8、长度、bits 与数量／尾随数据拒绝。上述根进入必需 connected-apps race，CI 清单保护可达。普通 Connector 的具名 legacy 回归只证明确切原解释，不能代替实际 managed App；后续会话／检查／撤权、默认整包与 Linux 继续单独验收。


`TestAppsAuthConfigExactPreServiceColdInflightAndOptInBoundary` 使用带源文件 hash 的原 pre-auth handler／manifest fixture，在未改动的当前协议／SDK 下重建并实际运行旧单服务形态。验证旧图冷启动、来源读取中首次 auth opt-in 仍完成原生 in-flight 解释、新操作使用新政策，以及 opt-in 后切回旧图的新请求不得恢复 native。它不声称覆盖旧完整发布二进制／编译器组合。该根进入必需 connected-apps race；仅在所有保留旧图与在途旧请求不再需要此边界后删除 fixture 和原解释器。


### 创建连接与授权会话的受管边界

- `TestAppsSessionFlowActualOriginalEffectsVersionColdAndFences` 用迁移前原生 Service／Client 与真实 HTTP peer 捕获的 21 个样本，经实际 App、SQLite、封存候选进程核对来源 URI／方法／完整 session 和 link body、错误、CAS、回调、链接与连接字段。原 UUID／时间仅在对照时规范化，不重写生产身份。覆盖公开应用、两种允许地址、非法 ID／URL、协议错误、确定和不确定失败、写入冲突。
- 同一测试在实际会话 POST 期间换版，原动作保留原期限，新动作使用独立缩短期限，认证及意图仍保持原政策；冷重启保留图与 BindingHash，缓存缺失只提示过期；验证后回退恢复原期限。候选针对真实调用伪造公开绑定、跳过认证、重复 session、扩大租期均不能绕过 Host。效果后的私有状态变更／隔离不得产生第二 POST、后续链接或本地绑定；新调用与冷停用拒绝，所有者本地撤权保持可用。
- `TestAppsSessionFlowExactPreServiceColdInflightAndOptInBoundary` 校验保留的原 handler／manifest hash，以当前未扩大的协议／SDK 重建确切 pre-session 服务形状，验证冷读取、旧动作跨首次 opt-in 保持解释，以及切回旧图仍不能取消持久边界；新重连在关闭旧 pending 前拒绝，已接纳授权缓存仍可读取。它不证明历史完整发行版／编译器。
- `TestAppsSessionFlowHostRejectsForgedEffectsAndPreservesAcceptedLease` 验证来源调用上界、各阶段跳跃／重复、无 no_auth 证明的公开提权、提前 pending、零／负／超长／整数溢出租期、已接纳短租期和零字段旧缓存的原十分钟解释。

上述根测试保持在 `make test-connected-apps` 及普通 Go 必需检查中。最终验收同时运行应用／领域与原 Web 回归、race、现有桌面／移动与亮暗浏览器检查、`make test lint`；源码在同次验收中固定。状态检查按下述独立边界验证；完整重连按独立边界验证；资源／页面、默认／保留图完整分发和 Linux 仍需各自真实迁移与验收，不能用本组结果替代。


### 账户状态检查的受管边界

- `TestAppsAccountCheckActualOriginalSourceVersionColdAndFences` 用迁移前 24 个原生 Check／HTTP peer 样本，经真实 App、SQLite 与封存候选核对原来源读取、状态、修订差、写入次数、刷新及错误；覆盖 ACTIVE、INITIALIZING／INITIATED、禁用／未知／空状态、三种原身份不匹配、公开来源证明、确定来源错误、CAS／刷新失败。10 MiB 已解码原状态保留原 hash，资格位投影不缩小来源能力。pending 的已接纳链接保持原身份；实际再次 Check 恢复 pending 时不能重建先前已删除的链接。
- 同一测试验证来源 IO 期间换版保留旧解释，新动作使用独立过期政策；冷检查与验证后回退保留原 BindingHash。移走载体后制造原状态变化，仍须在候选启动前以冲突拒绝；真实候选按调用阶段伪造 disabled→active、未知→pending、私有→refresh 或 public→active 都不能越过原证明。来源期间隔离不写库／刷新，新检查与冷停用无原生旁路，所有者本地断开独立可用。
- `TestAppsAccountCheckManagedMatchesOriginalCacheAndReceipts` 在 owning-domain 使用实际候选 handler 与上述迁移前固定样本，核对全部 24 组原缓存保留／删除、CAS、回调和原状态结果。它补足 App 外部行为无法直接观察的缓存细节，不代替真实进程与私有围栏测试。
- `TestAppsAccountCheckHostRejectsInventedStatusAuthority` 覆盖不匹配／禁用／未激活来源的状态伪造、公开与私有结果混用、未知状态命令及零政策不能推导原生。
- `TestAppsAccountCheckExactPreServiceColdInflightAndOptInBoundary` 校验原三方法 handler／manifest hash，以当前协议／SDK 重建确切 pre-check 形状；冷检查、旧动作跨首次启用保持原解释，持久 opt-in 后切回旧图仍在来源 IO 前拒绝 Check，同时其原意图／认证／会话仍可创建另一个显式标签，已接纳链接可读，冷缓存丢失不重放来源。不声称历史整个发行版／编译器已验证。

这些根测试由 `make test-connected-apps` 与普通 Go 必需检查覆盖，并运行 owning App／领域／Web、race、既有亮暗／桌面移动浏览器和完整 `test/lint`。结果仅证明账户检查政策边界；完整重连按独立边界验证；资源／页面、默认／保留图整包及 Linux 仍独立验收。


### 远端清理与独立所有者关闭

- `TestAppsAccountCloseActualOriginalEffectsVersionColdAndFences` 用迁移前原生 Service／Client 与真实 HTTP peer 捕获的 22 组样本，经真实 App／SQLite／隔离候选进程核对来源方法／URI、状态、撤权确认、修订差、两次 CAS、回调、错误及再次点击不重放。覆盖公开／私有、禁用但仍可由所有者撤权、三种身份不匹配、非法历史 ID、来源错误、撤权 401／408／502、错误／缺失证明、删除失败、首末写入冲突及回调失败。
- 同一测试验证原动作跨来源 IO 换版保持旧删除配方，新动作独立选择确切撤权后保留 broker 记录；不确定撤权仍保持原删除／不确认语义。冷启动与验证后回退保留 BindingHash／原身份；来源状态变化不能越过私有围栏继续撤权。真实候选跳过／重复账户证明、撤权或删除不能扩权，也不能阻止已经接纳的本地关闭。撤权后原修订变化不保存证明且不重复 POST；撤权后仅候选失效仍保留已知源证明，但不得继续 DELETE。隔离／冷停用只关闭本地，不可回 native。
- `TestAppsAccountCloseExactPreServiceColdInflightAndIndependentOwnerBoundary` 校验原四方法 handler／manifest hash，以当前协议／SDK 重建 pre-close 服务形状。验证冷原生清理、与不相关业务失败独立的所有者路径、首次 opt-in 跨 IO 保留旧解释、重连冻结同图 cleanup＋创建政策及原身份、切回旧四方法图后新重连在关闭旧 pending 前拒绝、显式断开及冷断开仍本地关闭但不调用 native。它不声明旧整个发行版／编译器已验证。
- `TestAppsAccountCloseManagedMatchesOriginalCacheAndReceipts` 在 owning-domain 通过实际候选 handler 核对全部 22 组固定样本的原缓存、CAS、回调和不重放细节，补足 App 外部观察；不代替真实进程证明。
- `TestAppsAccountCloseHostRejectsForgedOrderAndKeepsSourceProof` 覆盖无身份证明的撤权／删除、重复效果、读取失败／外部账户、删除前没有撤权尝试、候选不能发明确认，以及选择器报错时不得抑制本地关闭或静默使用原生。

这些测试根保持在 `make test-connected-apps`、普通 Go 必需检查和 owning race 中。验收继续包含实际 App／领域／原 Web、既有亮暗／桌面移动浏览器及完整 `test/lint`。失败测试的 HTTP 释放在 defer 中使用单次释放，激活失败不能被悬挂 peer Close 掩盖。剩余完整重连按下述独立边界验证；资源／页面、默认／保留图及 Linux 按各自范围独立验收。


### 完整重连编排的受管边界

- `TestAppsReconnectActualOriginalEffectsVersionColdAndFences` 使用改动前原生 Reconnect wrapper、实际五项受管子方法、App／SQLite／HTTP peer 捕获的 24 个固定样本，对照完整 URI／方法／session＋link body、原／新连接字段、BindingHash、Link／缓存期限、CAS／回调／修订差、失败与再次调用不重放。覆盖 pending／expired／历史未知状态、原禁止状态、缺政策、缺凭据、旧账户来源／撤权不确定、公开应用、新 Toolkit／auth／session／link 失败、每次写入冲突、刷新失败及别名冲突。UUID／时间只在对照时规范化，不改变生产身份。
- 同一测试在旧关闭来源 IO 期间换版，原 expired 动作完成原配方，新 pending-only 版本在效果前拒绝 expired；冷启动与验证后回退保持选择。关闭后原身份变化或原私有围栏拒绝不得创建新连接。真实候选提前 connect、重复 close、注入 alias 或关闭后 deny 都不能越过原范围／单次效果限制，部分结果不回滚／重放。新 pending 已持久化后隔离图仍直接返回确切已接纳 Link／缓存；隔离后的新动作及冷停用在效果前拒绝。
- `TestAppsReconnectExactPreServiceColdInflightAndOptInBoundary` 校验原五方法 handler／manifest hash，以当前协议／SDK 重建 pre-reconnect 服务形状；验证原完整重连、冷缓存缺失不重建、冷原配方、首次 opt-in 跨 IO 保留旧解释、切回旧图后的持久拒绝且原绑定未关闭；独立 Connect／Continue／Disconnect 仍按原方法运行。不声明旧整个发行版／编译器已经验证。

这些根测试保持在 `make test-connected-apps`、普通 Go 必需检查及 owning race 中；最终同时运行 owning App／领域／原 Web、亮暗／桌面移动浏览器及完整 `test/lint`。这仅证明重连及已有子方法的实际组合；资源／页面、默认／保留图整包及 Linux 不能据此宣称完成。


### 设置行／草稿投影的受管边界

- `TestAppsSettingsActualOriginalHTTPRowsDraftAndSave` 以改动前原 Web＋实际 Service／SQLite／HTTP 捕获的 26 个 GET／POST 固定样本，经真实 App、隔离候选与原字段路线核对 HTML 行／key_ref／revision、未保存／已保存反馈、422、实际 ConfigureProject 调用、来源请求、配置与修订差。覆盖空／历史 129 行 GET、原排序、非法草稿、add／remove／numeric／正号／前导零、128 行 add 保存分支、过限、空行、重复／不完整字段、stale revision 与原 serialized JSON 的空／null／损坏／超限输入。
- 同一测试独立换版倒序／首位空行，原请求保留捕获图；冷启动与验证后回退保持选择。原 Web 源码 overlay 捕获的 16387 行／3 MiB 和单个 9 MiB 名称呈现 hash 必须一致，普通纯读限额仍拒绝大输入。移走载体后原修订变化不得释放私有行；真实候选重复／越界／丢行／恢复已移除行／晚窗口重放不得显示半份页面或保存。来源期间真实切图与禁止额外 selector 读取，仍使用原认证政策和响应投影；写入后隔离仍返回确切接纳状态，后续新 GET 与冷停用拒绝原生回退。
- `TestAppsSettingsExactPreServiceColdInflightAndIndependentBoundary` 校验原六方法 handler／manifest hash，以当前协议／SDK 重建 pre-settings 形状；验证冷原投影、首次启用跨请求保留旧解释、切回旧图后的持久拒绝且不保存，独立所有者关闭仍可用。不证明历史整个发行版／编译器。
- `TestAppsSettingsHostPreservesOriginalRowsAndExplicitDraftAuthority` 检查原 App facts 不夹带配置值、编辑不发送私有保存 facts、重复／越界／丢行／空行伪造及删错引用、正确组合保持原配置值、零政策不能推导原生。`TestAppsSettingsFactsExactBytesAndRejectMalformedInput` 覆盖大字段／Unicode／原字节和 count／截断／尾部错误。

这些根测试由普通 Go 与 `make test-connected-apps` 必需检查覆盖。`make test-browser` 同时运行目录与 settings owning App；`apps_settings_plugins_browser.mjs` 用非空原行验证两个版本的亮暗／1400px／390px 截图、原输入值、共享控件计算尺寸／字体、无页面横向溢出和真实 add 草稿不写库。最终继续跑 owning App／领域／原 Web、race 和完整 `test/lint`。其余模板、Apps 前端生命周期、logo 业务／资源、默认／保留图与 Linux 不由本组替代。


### Logo 字节、来源与缓存的受管边界

- `TestAppsLogoManagedOriginalHTTPBytesSourcesAndCache` 使用改动前原 Web／cache／匿名来源 HTTP 捕获的 82 样本，运行实际封存包和隔离协议进程，再经原 HTTP 验证 65 个随包图标、元数据替换／空保留、未登记、原 24 小时时效、PNG／SVG／主动内容、重定向／上游失败、字节与像素上限。所有终态的 body hash／长度、ETag、媒体类型、CSP／缓存头和缓存数保持；81 个非取消案例的完整来源轨迹保持，取消案例明确改为来源零派发，原 404／不缓存保持。此层证明实际候选＋Web＋来源，不冒充 App 控制面。
- `TestAppsLogoActualOwnerHTTPOriginal65FrozenReferencesColdAndDisable` 使用真实 App／SQL／HTTP、隔离候选和 Guardian 激活验证全部 65 个原资源及完整 HTTP 头；实际页面发出原图引用；独立图案与短缓存版本、旧引用、冷原包、验证回退、无效候选、原图隔离、缺 logo 的旧七方法账户图及冷停用不得恢复 native。首次启用前捕获的原解释保持；独立设置与目录可继续使用；GET 不改账户修订或创建 `rsi_call`。不声称整个历史发行版／编译器。
- `TestAppsLogoActualAccountFragmentsKeepOriginalVersionAndAcceptedAction` 经真实 App／SQL／HTTP，在公开账户两次原关闭保存之间真实激活新图；JSON 与原生表单都只在效果前选择一次原图，保留原响应图标引用、动态原来源登记与已接受关闭。中途隔离原图时，关闭仍成功而资源读取拒绝；新图不借用旧图登记，原匿名来源只读取一次且不携带凭据。
- `TestAppsLogoHostRejectsInventedCacheSourceBytesAndRevocation` 检查伪造缓存／未登记 provider、超限／非法字节与时效、过期缓存冒充、来源读回前撤权不得写缓存／释放字节。

普通 Go 与必需 `make test-connected-apps` 覆盖这些根测试及 owning 包；`make test-browser` 包含实际 Logo App。`apps_logos_plugins_browser.mjs` 对原版本／新图案和时效版本／回退各运行亮暗、1400px／390px 截图，验证全部 65 张真实加载、原图 URL、非空图片计算尺寸、无横向溢出及原／当前 ETag。最后执行 owning App／Web／插件、race 与完整 test/lint。真实在线质量、通用资源贡献、前端／模板及默认／Linux 验收不由这些软件检查替代。


### 冻结界面规则与真实浏览器

- `TestAppsInterfaceManagedOriginal512TemplatesAnd32Errors` 用改动前实际原模板和 appError 捕获的 512 个中英文账户／详情／设置场景及 32 个原／wrapped 错误对照；运行实际隔离包，再经真实模板分别核对明确原生兼容与托管路线。托管模式仅规范化逐项验证的新内部 revision／事实属性，其他完整 HTML 字节 hash／长度保持；未知／空状态、公开／确认、匹配范围、链接匹配、卡片和原错误隐私均覆盖。此层不是 App 控制面证明。
- `TestAppsInterfaceActualProfileFrozenEffectsColdAndUnavailable` 经真实 App／SQL／Guardian／HTTP：原请求解释、首次启用、控件倒序与轮询换版、旧引用、冷重建、验证回退、源保存间真实切图、JSON 与原生已接受回执、旧七方法图缺界面及冷停用不得恢复原生；效果前只有一次界面读取。无效运行时表不得越过原范围，停止后的自动请求不得执行账户检查。
- `TestAppsInterfaceRejectsUnsafePlanWithoutNativeFallback` 检查跨范围、隐藏关闭／不确定、无来源链接、伪造 active、超频／缺表、重复／任意动作、缺错误标签，以及候选标签和原字段的 HTML escaping。
- `TestAppsInterfaceActualManagedBrowserLifecycle` 使用真实 App／SQLite／隔离界面／Guardian、受控真实 HTTP broker 和维护中的原浏览器脚本。两个实际版本覆盖原目录、设置保存、弹窗与阻拦、复用链接、自动状态、多账户、确认／关闭、重连、迟到回复围栏、原生无脚本入口、亮暗／中英文／三宽截图、共享尺寸及无浏览器凭据存储。隔离后自动循环停止并清理原投影；已证明链接在 JSON／宿主原生回执中保持，创建数不增加。账户源服务仍是明确未启用对应私有服务的原消费者，不冒充所有 F-071 包或默认组合验收。

这些根测试进入普通 Go 和必需 `make test-connected-apps`；完整维护脚本仍在 `make test-browser` 验证原生组合，owning App 的两个托管版本也进入同一必跑分支。构建／控件归属和 Native／托管截图继续检查。所有命名原生规则有确切 pre-interface/default23/direct/旧请求消费者；模板布局和资源组合仍未完成，不将本组列作 F-071／F-070 全量验收。

真实 App 回归还将界面包与原七项账户服务组成同一图：JSON 关闭、原生关闭及连接服务保存分别在真实保存期间激活另一界面版本，断言 selector 只读一次、原版本控件／隐藏引用和已接受结果保留。单界面图缺已 opt-in 的账户设置会明确拒绝，不能靠测试手工修补或恢复 native；维护浏览器的双版本界面用例仍只证明其声明的 pre-opt-in 后端边界，不扩大为全部 F-071 联合交付证明。

## Apps 区块组合验证

- `TestAppsCompositionManagedOriginal224PagesAndSidebar` 使用组合候选之前真实原模板产出的 224 个中英文场景，覆盖发现／全部／已连接／分组／详情／连接服务／侧栏及目录不可用、分类、分页、原始字符。原生与真实隔离包的默认组合分别逐字节对照；只规范化已精确验证的内部版本引用与原事实属性，不重写原期望。
- `TestAppsCompositionRejectsMissingDuplicateAndUnknownSections` 验证无法删除关闭区块、复制身份代替动作、隐藏主体、清空标题或选择任意模板。`TestAppsCompositionPreservesCopyEscapingAndOriginalFields` 验证候选展示文案保持转义，原凭据引用与草稿字段不被改写。
- `TestAppsCompositionActualVersionsColdOriginalProfileOnlyAndUnavailable` 使用真实 App／SQLite／Guardian，验证 profile-only 原图、首次独立组合、真实顺序与文案变化、旧引用、已捕获请求、冷启动、回退、运行时坏组合、当前旧图缺服务、隔离／停用及独立已接受关闭。真实连接服务保存期间激活另一图，仍只捕获一次原解释并保留原组合与接受反馈。
- `make test-browser` 同时运行真实托管界面完整账户流程与两个组合版本的连接服务浏览器检查：桌面／手机、深浅主题、控件实际尺寸、原字段／版本、区块真实 DOM 顺序和无 JavaScript 原生表单。浏览器查看不写配置。

旧 profile-only fixture 来自确切原私有源码和 manifest，使用当前 SDK 编译；不宣称完整旧工具链兼容。原 512 模板／32 错误、账户效果与停止恢复、Shared controls 门禁继续保留。固定原生片段内部与通用资源／默认分发未完成的部分保持在迁移清单中。

## Apps 包内语言与最终文本验证

- `TestAppsLocalizationActualV2MatchesOriginalHostDisplayText` 对照 `/2` 候选之前由原 Host translator 真实生成的中英文规则／组合期望；只变展示字段，不翻译动作词汇、身份、事实和期限。
- 原 512 个模板／32 个错误及 224 个完整页面／侧栏场景，对原生与真实 `/2` 的两种语言分别对照原输出。仅精确验证后规范化版本／最终文本标记，不更新旧期望。
- `TestAppsLocalizationActualOwnedTextV1RefsColdFrozenEffectsAndNoFallback` 验证真实旧 `/1` 图、新默认／独立文案、原引用、冷启动、原生接纳期间换版仍保留原语言与反馈、JSON 错误及标签页标题。新版运行坏输出与同图健康旧服务共存，必须拒绝而不能向旧版重试；明确选回实际旧图仍按其旧契约解释。隔离／停用及语言闭集继续检查。
- `TestAppsLocalizationCompositionOnlyNeverRestoresNative` 用真实独占组合包、Guardian 激活及 HTTP 页面检查组合已 opt-in／规则缺失时明确拒绝；不能仅依赖规则服务的单一 opt-in 标志。
- `TestAppsLocalizationActualBrowserOwnedFeedbackAndNative` 使用真实 App／SQLite／Guardian、独占 `/2` 图和既有 HTTP peer；真实 Connect／自动检查使用新中文反馈，中文页面／回调／标题原样保留与全局词典冲突的 `Workspace`。检查中英文、深浅主题、桌面／手机、原字段／版本、真实控件尺寸和无 JavaScript 表单。

完整托管账户浏览器流程与保存／布局回归继续为必需 `test-browser` 消费者；正常与目标 race 回归保留 CI 可达性。旧 `/1` 测试源码和 manifest 来自确切前版本，使用当前 SDK；不扩大成完整旧发行版声明。语言采集助手在迁移当次删除，完整资源／默认分发继续独立验收。

Apps 卡片资源回归覆盖候选前原生 512 场景逐字节 oracle、实际封存文件摘要与有限语法、真实 App／SQL／Guardian 的独立资源顺序换版／回退／冷启动、pre-card v2 原源码及捕获引用、当前缺失／非法作用域／错误摘要、卡片单独激活不恢复原生、包隔离及停用。可选卡片错误须保留账户详情、服务设置和原接纳关闭。浏览器以有内容的卡片验证中英／桌面移动／明暗、DOM 与几何顺序、共享控件计算尺寸、原参数／来源引用、详情及无脚本表单；同批清理原内联拼装。卡片原子内部、CSS 与通用资源回收不在此证明中。

侧栏资源验证候选前原始 34 场景完整输出、实际资源摘要和五步组件完整性，原 512 模板／32 错误和 224 页面／侧栏用实际四项 UI 服务继续对照。真实 App／SQL／Guardian 验证三服务旧源码、资源顺序换版／回退／冷启动、旧引用、当前缺失／非法／错摘要、仅侧栏激活、包隔离及停用。原生及 JSON 关闭在保存时切换图，必须保持原侧栏顺序、一次 UI 读取与原两次持久保存；坏侧栏不能阻断已接纳关闭。浏览器验证实际 pending 和自动 active、两语言／明暗／桌面与打开移动导航后的可见侧栏、原链接／Logo 来源、计算几何／字体及无脚本导航；坏侧栏的已接纳授权链接在浏览器／JSON／原生回执中必须保持同一身份且只创建一次。原子内部 markup／pending 词条及通用回收不计入此证明。

标记资源须对照原始 512 卡片、34 侧栏、512 模板／32 错误和 224 页面；同时执行真实五项 UI 服务。验证真实文件 Source 摘要、闭合受限 AST、本地探针 DOM 校验、实际 span／div 结构与版本标记换版、原 pending 文案绑定不被二次翻译，以及真实 App／SQL／Guardian 的旧四服务源码、冷启动／回退／缺失／停用／隔离。负例覆盖反射到 State／凭据、方法链、函数／变量／管道／循环／include、脚本／事件／CSS／外部 URL／DOM hooks、隐藏必需内容、错误 action／toolkit／graph 参数及 missing pending；验证 wire 身份 fixture 可通过但渲染 guard 必须拒绝的封存坏资源，及 owner-read-only 源码摘要伪造。原生／JSON 关闭在效果中换图仍沿用原资源，坏标记不抹去已接纳关闭与原授权链接；浏览器证明真实 DOM 结构／样式／移动可见导航／双语明暗及无脚本。坏标记导致初始目录拒绝时，恢复链接测试只声明真实授权 HTTP 回执，不虚构可启动的界面或修复 fixture。共享原语、其他片段／CSS 与全量默认分发不计入此证明。

图形 `/2` 须执行候选前原始 28 输出及其余原始卡片／侧栏／模板／页面对照，验证真实资源几何换版、详情与侧栏实际消费、Models 共享旧字节独立可用。负例涵盖活动 SVG／外链／图片／paint URL／CSS／ID／DOM hooks、反射、错 Logo／首字母、NaN／无穷坐标；同图健康 `/1` 不能掩盖坏 `/2`。真实 App 验证最高历史版本持久化、旧 `/1` 当前回退、明确 pre-markup 引用、冷启动／隔离／停用、效果中切版及原回执／链接。浏览器验证实际几何／标记、原 Logo URL、双语明暗／移动可见布局与原生无脚本；坏图形初始拒绝时只声明授权 HTTP 恢复回执。

required Apps 竞态使用[同一动态分批机制](engineering.md#apps-竞态分批)核对全部用例；默认组合系统入口仍要求四种场景实际通过。


账户行展示迁移的 `TestAppAccountMarkupKeepsPrivateObjectsOutAndEscapesDisplayValues` 覆盖内部对象／方法／函数不可达、真实文本和属性转义，以及脚本、事件、额外动作、外部提交、缺失／复制身份、隐藏控件和丢失授权／撤权提示的拒绝。`TestAppAccountMarkupActualTemplateRetainsOriginalControlsAndNeverFallsBack` 对照八种状态组合和中英文的原 DOM、身份输入、原动作与必要提示，托管源缺失必须失败而非回退。

`TestAppsAccountMarkupActualVersionsOriginalEffectAndColdReferences` 使用真实 App、封存包和独立 Owner 预期，改变 owning 名称／别名展示次序，验证旧引用、回退、纯读取不修改账户，旧页面原版本的实际关闭效果及响应，停用后首次冷读取与原持久结果；不在恢复后重选或补写。旧图形／markup 生命周期回归保留，当前 `/2` 历史包从固定迁移前源码构建。`TestAppsAccountMarkupActualBrowserAndRecoveryLink` 通过真实浏览器检查桌面／手机、明暗主题、真实原控件和已接纳授权链接，并覆盖非法贡献下效果已接纳后的恢复链接；它沿用既有后端 fixture，不替代标准32及历史组合的实际 App 分发验收，也不以模板单测代替完整验收。


`TestAppsAccountMarkupActualBrowserAndRecoveryLink` 接入原 `make test-browser` 的 Apps 场景，使用同一真实 App／SQLite／来源 peer，分别运行默认账户模板、可见变体和非法控件模板。覆盖真实连接及状态检查、原连接 ID／原图提交、无脚本页面、桌面／窄屏与明暗主题的非空计算样式，显示失败仍保留已接纳 OAuth 链接和一次创建结果。该专项保留明确的 pre-opt-in 后端夹具边界，不能替代默认完整包的实际分发证明。


账户文案归属由 `TestAppsAccountCopyActualVersionsOriginalEffectAndColdReferences` 复用原 App 生命周期，覆盖原 `/2` 图、1.11／独立1.12 的实际按钮文字差异、原引用、回退、旧页面关闭后的原版本响应及停用首次冷读取，不在恢复后补选版本。`TestAppsAccountCopyActualSourcePreservesOriginalLocales` 使用实际候选进程对照原中英文；`TestAppsAccountCopyFinalTextEscapesAndRetainsOriginalAuthority` 检查最终文字、转义、原动作／身份／链接及必要确认／授权字段拒绝。原账户浏览器根测试新增1.11／1.12，覆盖实际按钮标签、共享确认的取消／Escape 不提交、原身份和修订号的真实关闭效果，继续保留所有历史模板变体。当前声明组合 `/3` 不可因故障回退 `/2`；独立 Owner 中英文预期源于原 Host，不能由候选运行结果生成。


`TestAppsAccountCopyCannotRewriteRequiredRevocationFacts` 注入“全部授权已撤销”的候选字段，校验拒绝未经定义的撤权文案，不能依赖候选的非空字符串或仅在 Owner 验证时返回正确文字。复现使用原已关闭／撤权未确认的 Host 事实与经验证的原 profile；原英文／中文警告由 Host 固定绑定，普通六项文案仍可独立改变。真实 App 生命周期同时保留一条已关闭且上游撤权未确认的原账户，逐个原图／换版／回退／停用冷读取检查必需披露及原账户事实。`validateAppInterface` 只校验原 action 词汇及约束，不再调用旧文案 helper 来判断动作有效性；该 helper 仅随实际旧模板保留。


账户行与六项普通文案子项已通过上述实际 App／旧引用／独立版本与停用冷恢复、不可改写的必需披露、原始快照／样式、标准32及六种保留组合、八项明确非跳过 race 根用例和固定源码 `make test lint` 验收。该范围不替代其余连接／设置模板、CSS／客户端和资源回收的各自迁移与验收，也不表示整个 F-071 完成。


连接表单迁移的 `TestAppConnectMarkupRejectsLostAuthorityAndChangedAliasControls` 覆盖丢失／复制身份、丢失原帮助、外部／GET 提交、别名 name／pattern／maxlength／required 改写、额外凭据输入、错误隐藏、禁用提交、脚本／事件和内部 State 不可达。`TestAppConnectMarkupOriginal32ScenesBothLocalesAndNoCurrentFallback` 对照32种实际显示组合和中英文原始表单字节，检查当前贡献不可用不恢复旧默认。`TestAppsConnectViewActualVersionsOriginalEffectAndColdReferences` 使用真实 App、封存包、独立 Owner 原文案／资源预期，检查1.13／独立1.14的实际 Account label／Account name变化、原图、回退、原提交身份、停用后首次冷读取以及原关闭／撤权未确认披露，不在恢复后重选或补写。实际浏览器与完整分发仍须覆盖原首次连接、新增账户提交及别名校验、旧图引用与可选视图故障下已接纳链接的保留。

`TestAppsConnectViewActualBrowserAndRecoveryLink` 已通过实际1.13／独立1.14／坏控制4.5封存包的首次创建、状态检查、新增别名账户、空别名不提交、原身份／版本和无脚本原控件；坏贡献下实际 HTTP 拒绝页面且保留已接纳授权链接，不重放创建。该用例独立纳入必需 `test-browser` 命令，避免挤占原有浏览器组时间预算。原别名 pattern 在当前 Chromium 对含空格输入不产生 patternMismatch，属于继承问题，本迁移保留原字节；浏览器拦截证据仅证明 required，不宣称该 pattern 的浏览器有效性。

连接表单子项已完成原字节、真实换版／回退／停用冷引用、必需四项非跳过 race、实际首次／新增提交及标准32与六种保留组合验收。固定源码全量流程的 Go、Node、Web浏览器、Apps原浏览器组及新增连接组均通过；infra 最初因“只有一条 App 浏览器命令”的旧断言失败，仅修正 CI harness 的分组可达性断言后，完整134项 infra 和 lint 通过。运行时代码、分发二进制及浏览器源未改变，保留此前有效检查结果；不将原失败命令描述为一次性全绿。其余设置视图、CSS、客户端和资源回收仍未迁完。

设置视图的 `TestAppSettingsMarkupOriginalFormBytes` 对照中英文、0／1／3／128行、折叠／展开及非空转义值的16种原模板输出；`TestAppSettingsMarkupRejectsChangedAuthorityAndControls` 拒绝20种身份／行／帮助丢失或重复、错误提交／字段／动作、隐藏控件、脚本／事件与内部 State／候选循环。`TestAppSettingsMarkupActualDefaultSourceAndNoFallback` 检查实际默认插件贡献渲染与当前不可用不恢复历史模板。以上不替代实际 App／SQL／Guardian 的新版本草稿／保存、回退／停用冷引用、真实浏览器及默认分发验收。

`TestAppsSettingsViewActualVersionsDraftSaveAndColdReferences` 使用真实 App／SQLite／Guardian 和原 HTTP peer，独立1.16改变保存文字；检查原4引用、1.15／1.16换版／回退、激活后首次冷读取、旧1.15表单在1.16当前图下增删草稿不持久化、原验证后显式保存只调用一次，以及停用后首次冷读取、版本边界5与已保存事实。坏4.7贡献只改 key_ref 字段名，必须由 Host 拒绝页面，不能恢复旧模板。真实浏览器与完整分发验收已另行完成。

`TestAppsSettingsViewActualBrowserDraftSave` 已复用原设置浏览器脚本并纳入独立必需命令，验证实际1.15／1.16插件贡献、桌面／手机和明暗8个有内容场景的原控件尺寸／字体／无溢出、真实增删草稿、一次显式保存及原图引用；无脚本上下文复用原会话验证原表单可用。独立版本变更保存文字，原真实生命周期同时验证保存事实与冷恢复。CI 可达性检查分别验证原设置组、连接组和设置视图组，不再假定 App 只允许一条命令。

连接服务设置视图子项已通过上述原字节、异常控件、真实版本／旧引用／回退／停用首次冷读取、六项非跳过 race、8种浏览器布局与真实草稿／保存，以及当前标准32和六种保留组合验收。全量 Go 初次仅因一个旧连接表单测试仍使用 `/4` 解码当前 `/5` 资源失败；修正该测试构造器后，完整受影响 Web 包及其余 Go 分区、Node、全部必需浏览器、134项 infra 与 lint 通过。运行时代码及已验收分发二进制未变，保留有效的其他 Go 和分发证据，不将初次失败命令描述为一次全绿。其余目录／页面片段、CSS、客户端及资源回收仍待迁移。

`TestAppDirectoryMarkupOriginalNavigationFilterBytes` 对照中英文、4种原视图状态、有无返回／分类／分页和特殊字符查询的96种组合、四类原输出；`TestAppDirectoryMarkupRejectsChangedNavigationAndFilterControls` 拒绝23种错误导航／当前视图、POST筛选、字段／分类／选中项改写、丢失或复制原选项、禁用或污染无脚本按钮、游标丢失／参数增删重放、脚本／事件／隐藏／State／循环。与原连接和设置控件一起执行，避免扩大被动解析字段时改变旧契约。以上不替代实际 App／SQL／Guardian 生命周期和完整分发验收。

`TestAppsDirectoryControlsActualVersionsQueriesAndColdReferences` 已通过真实 App／SQLite／Guardian 的原5、当前1.17和独立1.18导航顺序变化、原引用、回退、激活及停用后的首次冷读取，并验证真实下一页 API 接收原搜索／分类／不透明游标、纯展示不改账户／设置事实。坏4.9把 GET 改成 POST 时必须503，不能恢复旧模板。`TestAppsDirectoryControlsActualBrowserNavigationFilterAndPagination` 已纳入独立必需浏览器命令，实际检查旧5／当前6／独立顺序版的桌面手机、明暗12种有内容布局、共享下拉实际筛选、分组返回、原查询分页与结束、无脚本筛选／分页和零账户创建；导航及下拉实际计算尺寸须满足36／44px及原字体，无水平溢出。标准分发另以真实所有者页验证原筛选和精确分页结束，不仅检查 manifest。完整当前分发与最终验收仍待完成。

#### Apps 整页视图资源

- `TestAppPagesOriginalDisplayBytes` 对照全部剩余页面／详情／服务片段及外层容器，中英文、空与非空分组／账户、选中详情、不可用状态和原查询值均须保留原字节；同时使用 native 与实际托管规则，不能仅验证空页面。
- `TestAppPagesRejectChangedProtocolAndControls` 拒绝改变搜索 GET 目标／参数、账户身份／状态、协议／图／语言事实、丢失或重复子视图、隐藏控件、脚本／事件及外部图形。`TestAppPagesManagedResourceDispatchAndNoFallback` 验证完整父子视图实际消费资源及当前资源不可用时不恢复旧默认。
- `TestAppsPagesActualVersionsAndColdReferences` 复用真实 App／SQLite／Guardian 与 HTTP 来源，验证默认1.19、独立1.20的实际详情顺序差异、旧1.17引用、回退、首次冷读取及非法贡献和停用后的不可用；恢复后直接检查原图与原事实，不通过重新选择或修复夹具掩盖问题。

- `TestAppsPagesActualBrowserOriginalControlsAndViews` 为 `make test-browser` 必跑根：四个实际版本复用既有目录与账户浏览器脚本，覆盖桌面／手机、明暗主题、完整详情与设置页、无脚本搜索／筛选／分页，以及非法整页贡献下 JSON 和原生接纳回执／授权链接保留；创建必须只发生一次。CI 可达性检查须保留独立入口。


### Apps 客户端程序接入

`web/apps-browser-worker.test.mjs` 使用独立保存的原 profile，覆盖混合账户状态与首个 active 的详情归约、无账户／别名要求投影、轮询到期边界与已调度／在途／停止状态、授权弹窗结果、断开提示及独立决策变化。它是程序专项，不替代真实页面、隔离、生命周期或全量验收。

接通真实页面后须覆盖：候选程序不能直接访问已登录页面／网络／存储或提交账户动作；原表单和共享确认仍可用；原授权弹窗与页面内存链接、坏贡献下已接纳回执、停用与未知效果不重放；桌面／手机、亮暗主题、非空账户行与共享控件计算样式；实际独立版本、旧引用、回退与首次冷读取。默认原生业务分支必须在同批移除，历史消费者另行明确列举后才可标记迁移完成。


客户端接入专项另包含 `TestAppsBrowserActualOpaqueWorkerTransport`：真实受保护 HTTP、浏览器 CSP、不透明来源、Worker内执行、网络／存储拒绝、错误序号／死循环终止和销毁；该根加入常规浏览器 recipe。`TestAppsBrowserActualSealedProducerRouteRollbackAndColdReference` 用真实封存候选与插件进程验证原 profile 的同图 producer 读取、受保护路由、旧包不伪造新程序、回退、首次冷恢复和停用后的保留引用读取，加入 Apps race 清单。这些检查仍不替代主页面控件及完整客户端迁移验收。


投影消费者专项另以原 SSR 账户和控件运行实际 `enhanceAppConnections`：原包保留原顺序，独立 Worker 改变既有行顺序；漏掉原账户的候选须终止展示。Continue 操作必须提交原控件的确切连接与界面版本，仅接纳一次；正式响应后候选投影失败仍保留授权弹窗和原链接。该效果端为明确合成 fixture，不替代真实 App／Store 的账户效果验收。`web/apps-projection.test.mjs` 覆盖完整原作用域、重复／缺失／越界／跨应用索引和非布尔槽位；原账户业务浏览器回归与桌面／手机亮暗截图继续作为共享控件和原行为的证据。


同一实际浏览器根进一步覆盖包内决策的真正分发：候选改变动作提示、暂停轮询后越过原5秒间隔仍没有 check、关闭分类自动提交后原 select 变化不导航、已知别名错误的展开／聚焦与候选不展开。坏轮询计划必须保留原 pending 的有效授权链接，不能恢复过期链接；坏动作提示不重复导航授权地址，成功效果仍只提交一次。所有候选消息另加输入探针，拒绝原连接 ID、私有账户来源 ID 和页面授权 URL 穿过 Worker 边界。Node 专项覆盖96项同批计划、旧 deadline 不延长、在途／停止／过期禁止调度、提示类型与长度、未知错误不伪造别名原因。这些仍是明确合成效果端与原控件的组件验证，完整默认包／真实 Store 的业务与生命周期验收继续单独执行。


客户端版本边界通过同一真实 App／Store 根覆盖：已有 `/2` 与首次安装 `/2` 两种回退，当前 `/3` 在没有旧 profile 激活历史时可直接读取，原 `/3` 引用在回退和冷恢复后仍可读；声明损坏 `/3` 同时提供健康 `/2` 的候选也必须不可用，不能换分支。删除 profile 声明后，即使包自称旧版本、使用明确引用也不能回到原生。停用后关闭／重开 Store 的首次当前读取仍须不可用，原合法引用保持可读，不重选或补写 fixture。原 `/2` 中英文显示回归同时保留；旧 producer 数据不由新 `/3` 的输出建立预期。


### Apps 同包样式与原控件

`TestAppsStylesOriginalAndIndependentLayout` 覆盖原59槽样式和独立24px间距，`TestAppsStylesRejectForeignAuthorityAndHiddenDisclosures` 覆盖外部资源／导入、私有选择器、隐藏／裁剪账户、透明文字、负值、重要性覆写与不支持的主动语法，均保留在必需 Apps race 清单。样式 GET 验证冻结原图、准确内容、CSS MIME、重复／未知参数拒绝及原认证边界。

实际封存版本专项同时验证1.21／独立1.23样式读取、回退、双版本原引用、关闭／重开后的首次读取及停用；不为 fixture 重选图。真实浏览器根在账户动作后的异步重绘完成后，从仍连接页面且非空可见的当前账户行读取计算样式，验证16px／24px、1400／390宽度、亮／暗主题、Inter、原36／44px按钮和无横向溢出；关闭 JavaScript 后仍须取得同图样式和确切原表单版本。原账户与整页／详情／目录／服务页、侧栏及无脚本浏览器根继续复验原行为和外观。默认32包及六种保留组合已完成实际分发验证；客户端与样式子项已完成全量测试、清理后完整 lint 和最终视觉核对。

### 独立账户清单托管回归

以 `apps-account-inventory-original-v1.json` 中实现候选前执行捕获的原生结果为对照，覆盖空清单、重复 toolkit、已确认撤权的首行、未知状态、多个活动行的公开事实、名称大小写／slug 同名排序、中英文及 NUL。原始字节输入另覆盖非 UTF-8、截断、尾部数据、非法状态位和计数。

纯算法检查不替代真实派发：必须验证目录停用而账户管理仍走 owning 包、同一状态／版本围栏、完整窗口无遗漏与重放、升级／回退／停用与首次冷读取、原动作已接纳响应及 unknown 不重放。删除普通原生分支之前，明确旧图／引用消费者与删除条件；候选失败不得调用旧算法。

独立账户清单的实际 App 根已覆盖目录移除后的页面、状态修订围栏、首次冷恢复和隔离拒绝；独立版本根覆盖1.8／1.9、冻结选择、精确回退、停用和保留托管图冷读取。真实进程大清单根验证2051行跨三页及首个响应后状态变化时原子拒绝，原有10003行、13191939显示字节（单名称3MiB）的捕获案例已复验，保留原输入及两种排序哈希。上述根由常规 Go 检查与 Apps race 分组覆盖；这些专项不替代 pre-inventory 旧图兼容或完整分发验收。

账户清单失效还须覆盖实际动作回执：在真实供应商协议 peer 的 session／link 各执行一次、原 pending 数据提交之后隔离 owning 图，JSON 与无脚本成功响应仍须保留同一原链接和期限；再次 Continue 不新增 session／link。分别令上游 link 结果不确定，JSON 保持原422、原生表单保持原409，不能生成授权链接、成功账户或成功展示回执；原始私有错误不能进入回复。该根包含于 Apps race 分组和常规 Go 检查。

历史清单解释使用真实原始账户1.0 producer／manifest：分别覆盖原图升级后回退和升级后首次安装原包、回退后的真实账户页面、首次冷读取、原状态修订变化以及保留旧图撤权。另以诚实声明相同1.0版本但配置 schema 不同的七服务 producer 验证版本标签不能授予历史解释；其他省略 Inventory 的七服务版本也不可用，健康保留托管图不受影响。历史计算完成后的状态围栏变化不能释放部分事实。

大名称必须复用候选前捕获的原始10,003行案例：13,191,939显示文本字节及单个3MiB名称保持原输入哈希；实际插件进程的侧栏／已连接两种排序保持原始哈希，不能重算原预期。

已保留的 pre-auth／pre-session／pre-check／pre-close／pre-reconnect／pre-settings 原始 producer 也必须分别在独立原 Store 中验证 Inventory 已启用后的安装／选择与首次冷读取；不能只验证最新七服务包，也不能重选／修复 fixture。

完整分发 fixture 的清单读取器须和其他账户读取器一样绑定真实 Steward 载体，并实际投影 Check 后的当前状态；不能只靠启动 Owner 用例证明业务调用。标准组合还须以真实目录查询拒绝验证独立账户页面仍显示原 active 账户；不替换读取器、原图或事实来制造成功。

当前标准分发为40包。Connected Apps SQLite race 通过原执行器按同一命名选择集分批，每根恰好一次，CPU 并发、15分钟单批报警和完整性核对保留。实际 App 系统验收的 managed33／managed32／prior30／prior29／prior28／prior24／prior23／prior22 八个独立组合分别运行，各保留20分钟报警；每个原子组合必须恰好一次 pass，不能因累计前序时间跳过后续版本、审批、unknown 或冷恢复。

MCP 插件回归复用现有普通 CLI 的 `-test-mcp-server` 入口，仍运行同一个 Steward-owned fake 协议服务；Host 保留 race 检测，peer 避免 race 测试进程的退出等待干扰16页／4096条目录与原30秒 CodeMode边界。GitHub SQLite 回归复用具完整性核对的8根分批执行器，各根测试仍只运行一次。

精确 PR 的一类记录恰好 2,000 项时，第 21 页为空后继续检查其他类别并提交完整批次；有第 2,001 项或探测失败时不接纳部分结果，重启后保留此前进度。
