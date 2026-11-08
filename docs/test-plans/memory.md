# Developer Memory：测试方案

[功能与验收目标](../features/memory.md) · [技术方案](../designs/memory.md) · [主索引](../feature-map.md) · [统一测试入口](product.md)

## 验收

- 真实自动提取、memory_manage 和本机控制记忆进入同一列表；正确展示加密正文、Core／Recall 和可验证来源。
- Unicode 字面搜索、层级／来源／状态／时间过滤、完整无重复分页；游标不能跨条件、所有者或隐私边界复用。
- 原始消息／Task 正文只沿该记忆的当前可见引用读取；来源工具只能关联真实的当前 Agent memory_manage 调用。
- 外部／未知／遗忘记录一致不可访问；被替代正文不可见。缺失密钥不泄露密文，搜索不把解密失败伪装成无结果。
- 输出前发生 Forget、替代或表示变动时拒绝旧快照；只读查询不持有 WAL 写锁。
- remember／forget 的 pending／applied／rejected 保持维护回执语义，不与当前有效记忆混合。
- Web 认证、作用域、无效参数、只读写请求拒绝、HTML 转义、中文、延迟脚本、无脚本和移动端行为；旧 Database 路由及导航已移除。
- desktop/mobile、浅色/深色的真实截图验证；Developer 一级／二级导航为 176px／168px，过滤栏为 176px；半屏详情、分栏调宽、来源导航、返回、前后条目、独立页保持可用。

## 入口与证据

```sh
go test ./internal/agent/sqlite ./internal/interaction/web -run 'MemoryInspection|MemoryRoutes|MemoryFailures|Chinese' -count=1
python3 tests/system/verify_channels.py --binary bin/steward --memory-only --evidence evidence/CHOOSE_FRESH_MEMORY_RUN/report.json
make test lint
```

Memory 独立场景也属于既有 channels 门禁。Harness 使用临时数据根、实际记忆管理命令与工具、当前加密机制和真实 Chromium，检查原始持久记录指纹，验证浏览前后只读、重启和遗忘。原始报告和截图保留在忽略的 evidence/ 中。已验证的确定性 UI／数据边界不代表真实模型的语义记忆质量或大规模查询性能。

共享列表几何断言覆盖 320／390／760／761／1024／1400px，检查导航留白、compact 密度、不同模块图标、窄屏溢出和折叠筛选与视图标签不重叠。组件状态 UT 与所属 Go 回归分别由既有 frontend／go 通道运行，真实 Memory 场景由 channels-web 通道运行；统一设计依据见 [design.md](../../design.md#数据列表页契约)。

增强抽屉场景等待 Memory 自身的初始化标记，再断言非模态详情；延迟 app.js 时验证标记不会提前出现，原生链接始终保留。不能把脚本加载前合法的完整详情回退误判成抽屉故障。

## 四层列表与详情

channels 的 memory 场景通过现有 memory manage 控制路径创建笔记、Wiki、注入文档，分别验证四层 API 与原生详情，真实 Chromium 打开每层的共享侧栏并保存桌面明暗/手机截图。继续验证筛选、分页、来源精读、关联 Trace、键盘调宽、中文、无脚本回退与只读指纹；新增层级不允许 SQL/任意表入口。底层权限与并发复查由 Go 所有者检视回归验证。

检索精度回归：recall/excerpt_test.go 验证连续摘录、UTF-8、偏移、Latin 词边界和完整引用；agent/sqlite/recall_explicit_test.go 验证 v2 排除工作集、输出预算与 v1 行为；memory/dream_evidence_test.go 验证逐文档支持、伪造／缺失引文拒绝和旧私有输入回放；memory/sqlite/documents_test.go 验证证据密封、回执一致和 Forget；agent/sqlite/memory_precision_test.go 验证 transcript 跨作用域拒绝、分页、密封、撤销及 CodeMode 传输展开的稳定性。所有新增测试进入 test-memory；涉及上下文传输的测试也进入 test-prompts。真实模型对比使用冻结 case／评分标准，并保留初始失败及未完成试验，独立于确定性系统 gate。

列表回归通过共享 `assertDataList` 检查 Refresh 仅位于全局顶栏右侧、Filters 在搜索下方左侧且展开收起不移位；覆盖桌面／手机和明暗主题，保留原生筛选与分页。

渐进读取回归覆盖：完整 Wiki 不能遮住原始会话；同相关度原文优先，L4 不作为搜索回声；相关引文保留作者与原句；只排除当前工作集实际展示且有同 Run 成功回执的完整结果，失败 CodeMode 父帧／伪造内容／截断／压缩后的证据仍可读取；导航引用授权隔离、预算与冻结续接；原有 Forget 和历史协议不变。真实模型使用同题、同快照、同16K窗口和4K输出比较优化前四层、优化后四层与同代码L1/L2，不择优采样。


维护 JSON 指标须区分没有价格与零费用，重放不增加文档变化／访问计数；计数处在同一授权作用域。检视仍使用统一列表与详情抽屉，不因指标新增查询控制台。


审查缺陷回归由 agent/sqlite/memory_review_fixes_test.go、memory/sqlite/document_fixes_test.go、memory/manage_contract_test.go 覆盖混合业务回执、超过最近窗口的旧笔记与 L4、事实纠正身份及旧数据补齐、256 历史来源裁剪后的写入与 Forget、工具必填字段、Dream 维护检视、Worker 真实反馈与权限／覆盖重放、微秒分页及精确详情。均进入必需的 test-memory；压缩回归也进入 test-prompts。

Web 的 memory_test.go 验证有价／未知及原生维护详情，memory_browser_test.go 经现有 test-browser opt-in 使用真实 Chromium 与共享 channels_memory.mjs，覆盖桌面明暗、手机、列表内部滚动和抽屉宽度，使用只读领域 fixture 提供已计价／未知值。channels 的真实领域 Memory 场景同时验证维护列表／详情和 GET 不改业务数据。模型效果复测继续冻结原 case、快照和评分，并另计维护成本。

Worker 来源分页回归通过真实接纳的 63／64／65 条回执，验证任务原文与工具页长分离、整页完成不留假游标、超页续读不丢失或重复。该用例随 `test-memory` 进入必需 CI；harness 的 `test_ci.py` 同时断言维护详情浏览器用例仍由 `test-browser` 执行，防止仅保留测试文件却移出门禁。

形成方式回归 `TestMemoryInspectionFormationUsesDocumentMetadata` 经既有 `test-memory` 全包 race 检查运行：覆盖 L2／L3／L4 的 written／dream、有来源但不误分为 automatic／explicit／unknown、退役后形成方式保留且内容隐藏，以及元数据密钥不可用不能伪装为零结果。
