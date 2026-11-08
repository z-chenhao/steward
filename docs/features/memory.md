# Developer Memory 记忆浏览

[需求主索引](../feature-map.md) · [技术方案](../designs/memory.md) · [测试方案](../test-plans/memory.md)

<a id="f-033"></a>

## F-033 · Web Memory 列表与来源检查

**需求**：所有者能查看当前保存的记忆正文、形成方式与来源，区分已生效记忆和待处理的管理请求。

**当前实现**：Developer → Memory 替代原 Database 入口。按 Transcripts、Memory notes、Memory representations、Injected context 展示原始记录与派生内容。旧 Core／Recall 保留为笔记元数据；形成方式区分自动提取、主动写入、后台整理和显式记住。搜索是在本机解密后的正文和 ID 中进行的字面包含匹配，不调用模型或 embedding，也不把搜索称为语义相关性排名。

## 使用

四个层级使用同一个后台列表和详情侧栏；列表显示标题或正文摘要、层级、形成方式、状态、可见来源和创建时间，详情补充修订号与关联文档。原始消息可沿来源阅读，并打开对应 Trace。默认每页 20 条有效记录；可以筛选层级、形成方式、有效／被替代状态与 UTC 创建时间，按创建时间和 ID 降序翻页。日期结束边界不包含该时刻。搜索和过滤结果共用分页边界，改变条件需要从第一页开始。

点击正文在右侧非模态详情栏查看全文；左侧列出已保存且当前可访问的来源，可打开原始消息／Task 正文或进入实际 memory_manage 调用对应的 Trace。每页最多展示 20 个可见来源，使用“更多来源”续看；消息和 Task 正文按 UTF-8 边界每段最多 4096 字节，使用“继续阅读”查看下一段。详情与来源分栏可拖拽或用方向键调宽，也可打开独立页面。手机采用上下布局，无 JavaScript 时保留表单搜索、分页和来源导航。

维护记录页展示 remember／forget 的接受时间、pending／applied／rejected、尝试次数、错误代码和存在时的来源 Trace。已接受请求不等于已形成记忆，observe 后台事件不混入此页。

被替代记忆仅显示元数据与仍可见的替代关系，不显示旧正文。已遗忘、越权及隐私屏障之前的记忆不可访问。密钥不可用时记录显示正文不可用，不显示密文；包含不可解密候选的正文搜索返回暂时不可用，避免把不完整结果显示成搜索成功。

本页只读；记住、纠正和忘记继续通过对话或既有 memory manage 操作完成。浏览不产生推理、embedding、记忆写入或新的执行。

## 接口

```text
GET /api/developer/memory
GET /api/developer/memory?search=tea&tier=core&origin=explicit&status=active
GET /api/developer/memory?ref=memory:OPAQUE_REF
GET /api/developer/memory?ref=memory:OPAQUE_REF&source=conversation:SOURCE_REF
GET /api/developer/memory?view=maintenance
```

同工作台所有者认证与授权。接口不接受 SQL、存储路径、Agent 或用户作用域。原 /developer/database 与 /api/developer/database 已移除，返回 404；数据库文件和既有记忆格式不变。

**边界**：不展示未持久化的召回次数、最近使用时间、置信度或语义归并质量；来源只沿已保存引用解析，不从正文或时间推断。记忆形成与检索自身的限制见[记忆与上下文](memory-context.md)。

显式记忆搜索对新 Run 返回有来源与偏移的原文摘录，排除当前请求及其派生证据；使用现有 memory_manage 精读文档或授权 transcript。Dream 新文档逐条绑定精确引文与原始来源；引文元数据加密保存，旧请求和回执不改写。相似事件的检索匹配不等于身份、用途或时间相同。

新冻结的 search v4 保留原始证据入口，完整可见结果不重复占搜索名额，支持引文按查询选择；新 L4 私有策略冻结相关文档的可操作引用并限制注入预算，复用既有精读与隐私屏障。旧工具和已接纳请求保持精确回放。


维护 Inspection JSON 的 metrics 区分模型成本、文档变化、未变化提案与后续曝光；未知用量／价格单独保留，不推算为零费用。页面继续沿用四层列表与抽屉，不添加任意 SQL 查询或独立管理工具。


维护记录包含 Dream，列表展示状态、调用／token 和参考费用，详情沿用共享只读抽屉与原生页面。后台整理可核对 Worker 的实际工具反馈；来源关联原 Trace，精读使用已有 memory_manage。事实改写为笔记后仍保留纠正身份，长期编辑可裁剪当前支持而不丢失历史审计和 Forget 能力。
