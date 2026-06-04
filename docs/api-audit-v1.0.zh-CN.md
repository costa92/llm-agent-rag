[English](./api-audit-v1.0.md) | [简体中文](./api-audit-v1.0.zh-CN.md)

# `llm-agent-rag` v1.0 导出面审计

**目的。** 这是 `github.com/costa92/llm-agent-rag` 的
`v1.0.0` 发布的冻结期导出面清单。它枚举每个可导入包以及带构建标签的
`adapter/llmagent` 的每个导出符号，把每个符号分类为
**keep / rename / unexport**，以书面形式确认不存在意外导出，并记录
已批准的冻结前命名决策。在 `v1.0.0` 之后，每次破坏性重命名
都需要一个 `/v2`；本文档是最后一次评审记录。

**审计的 tag。** `v0.6.0-1-g1d6e206`（`git describe --tags`）。
**日期。** 2026-05-19。
**状态。** 时间点记录。这 *不是* 活的兼容性
策略 —— `docs/compatibility.md`（Phase 29）是活文档；本
文件是一次性的冻结期审计，在 v1.0 之后不再更新。

**方法。** 包列表来自 `go list ./...`（默认构建标签）—— 22
个包 —— 加上 `adapter/llmagent`（在 `-tags llmagent` 之后）。每个包的
导出面通过 `go doc <import-path>` 捕获；在 keep/rename 判断需要时，
结构体字段和接口方法被展开；`adapter/llmagent` 从源码（`adapter/llmagent/*.go`）检视，因为
`go doc` 不接受 `-tags` 标志。

**处置图例。**

- **keep** —— 为 v1.0 原样冻结。
- **rename→X** —— 一次已批准的破坏性重命名，*在 slice 28-02 中应用*。
- **unexport** —— 将被设为包私有。（没有符号携带此
  处置：见下文「意外导出确认」。）

---

## 逐包清单

### root —— 包 `ragkit`（`github.com/costa92/llm-agent-rag`）

仅 `doc.go` —— 一条包注释，**无导出符号**。

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| *(无)* | — | — | `doc.go` 声明 `package ragkit`，带一条包注释，零导出符号。`ragkit` ≠ module 路径（`llm-agent-rag`）名称是一个刻意的文档锚点 —— 包注释在 28-02（KS-3）中被重写以记录这一点，无符号变更。 |

### `advanced`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `ErrModelRequired` | var | keep | 带包前缀的哨兵错误。 |
| `ExpandQuery` | func | keep | 多查询扩展辅助函数。 |
| `GenerateHypothetical` | func | keep | HyDE 辅助函数。 |

### `agentic`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `ErrAskerRequired` | var | keep | 带包前缀的哨兵错误。 |
| `Attempt` | type (struct) | keep | 一次纠正循环的尝试记录。 |
| `CorrectiveAsker` | type (struct) | keep | 自纠正检索循环。 |
| `LLMReformulator` | type (struct) | keep | LLM 支撑的 `QueryReformulator`。 |
| `QueryReformulator` | interface | keep | 刻意的插入点接缝 —— 调用方可提供自定义的 reformulator。 |
| `Result` | type (struct) | keep | 纠正循环结果。包局部；不是正被重命名的 `eval.Result` —— `agentic.Result` 无关，保持不变。 |

### `contract`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| *(无)* | — | — | `contract/contract_test.go` 是一个仅测试的跨仓编译锚定（`go doc` 报告「no source-code package」）。无一等导出 API 需要冻结。见下文「契约门禁交叉核对」。 |

### `embed`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `CosineSimilarity` | func | keep | 向量相似度辅助函数。 |
| `Embedder` | interface | keep | 刻意的插入点接缝 —— 嵌入后端抽象。 |
| `HashEmbedder` | type (struct) | keep | 确定性的测试/默认嵌入器。 |
| `NewHashEmbedder` | func | keep | `HashEmbedder` 的构造函数。 |
| `Vector` | type (`[]float32`) | keep | 嵌入向量类型。 |

### `eval`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `ErrJudgeModelRequired` | var | keep | 带包前缀的哨兵错误。 |
| `WriteJSONL` | func | keep | 将一个 `TriadResult` 写为 JSONL。 |
| `Asker` | interface | keep | 刻意的插入点接缝。 |
| `Dataset` | type (struct) | keep | 带标注的评估数据集。 |
| `LoadJSONL` | func | keep | 从 JSONL 加载一个 `Dataset`。 |
| `DriftAsker` | interface | keep | 刻意的插入点接缝。 |
| `DriftEvalResult` | type (struct) | keep | DRIFT 路径评估器结果（已带名称前缀）。 |
| `DriftEvaluator` | type (struct) | keep | DRIFT 路径评估器（已带名称前缀）。 |
| `DriftExampleResult` | type (struct) | keep | 单个示例的 DRIFT 结果。 |
| `Evaluator` | type (struct) | **rename→`RetrievalEvaluator`** | 基础检索评估器。唯一未带前缀的评估器；为与 `GlobalEvaluator`/`DriftEvaluator`/`TriadEvaluator` 对称而重命名。在 28-02 中应用。 |
| `Example` | type (struct) | keep | 一个带标注的数据集示例。 |
| `ExampleResult` | type (struct) | keep | 单个示例的检索结果。 |
| `GenerationMetrics` | type (struct) | keep | 生成侧指标。 |
| `GlobalAsker` | interface | keep | 刻意的插入点接缝。 |
| `GlobalEvalResult` | type (struct) | keep | 全局路径评估器结果（已带前缀）。 |
| `GlobalEvaluator` | type (struct) | keep | 全局路径评估器（已带前缀）。 |
| `GlobalExampleResult` | type (struct) | keep | 单个示例的全局结果。 |
| `GraphABResult` | type (struct) | keep | A/B 比较结果。 |
| `RunGraphAB` | func | keep | A/B 测试套件。内部构造 `Evaluator{...}` —— 一个重命名引用点，在 28-02 中更新。 |
| `Judge` | interface | keep | 刻意的插入点接缝。 |
| `JudgeRequest` | type (struct) | keep | LLM-judge 请求。 |
| `Judgement` | type (struct) | keep | LLM-judge 裁定。 |
| `LLMJudge` | type (struct) | keep | LLM 支撑的 `Judge`。 |
| `Metrics` | type (struct) | keep | 四个头条检索指标。 |
| `Result` | type (struct) | **rename→`RetrievalResult`** | 基础检索评估器结果。`(Evaluator).Run` 返回它；为对称随 `Evaluator` 一起重命名。在 28-02 中应用。 |
| `Retriever` | interface | keep | 刻意的插入点接缝。 |
| `TriadEvaluator` | type (struct) | keep | RAG-triad 评估器（已带前缀）。 |
| `TriadExampleResult` | type (struct) | keep | 单个示例的 triad 结果。 |
| `TriadResult` | type (struct) | keep | RAG-triad 结果（已带前缀）。 |

### `examples`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| *(无)* | — | — | 仅测试的实例演示包（仅 `*_test.go` 文件；`go doc` 报告「no source-code package」）。无一等导出 API 需要冻结。 |

### `feedback`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `BuildExample` | func | keep | 将一个 `rag.Trace` 转换为一个 `eval.Example`。 |
| `Recorder` | type (struct) | keep | 将被标记的 Ask 捕获为 JSONL。 |
| `NewRecorder` | func | keep | 从一个 `io.Writer` 构造。 |
| `OpenFile` | func | keep | 打开一个文件的构造函数。 |

### `generate`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `Message` | type (struct) | keep | Chat 消息。 |
| `Model` | interface | keep | 核心生成接缝 —— 刻意的插入点。 |
| `Request` | type (struct) | keep | 生成请求。 |
| `Response` | type (struct) | keep | 生成响应。 |
| `Usage` | type (struct) | keep | Token 用量。 |

### `graph`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `ErrCommunitySummarizerModelRequired` | var | keep | 带包前缀的哨兵错误。 |
| `ErrEntityExtractorModelRequired` | var | keep | 带包前缀的哨兵错误。 |
| `ErrEntityResolverEmbedderRequired` | var | keep | 带包前缀的哨兵错误。 |
| `CommunityContentHash` | func | keep | 一个 `Community` 的稳定内容哈希。 |
| `NormalizeName` | func | keep | 实体名归一化器。 |
| `Community` | type (struct) | keep | 一个被检测到的社区。 |
| `CommunityDetector` | interface | keep | 刻意的插入点接缝。 |
| `CommunityReport` | type (struct) | keep | 一份社区摘要报告。 |
| `CommunitySummarizer` | interface | keep | 刻意的插入点接缝。 |
| `DictionaryEntityExtractor` | type (struct) | keep | 确定性的基于字典的抽取器。 |
| `EmbeddingEntityResolver` | type (struct) | keep | 嵌入相似度消解器。 |
| `Entity` | type (struct) | keep | 一个图实体。 |
| `EntityExtractor` | interface | keep | 刻意的插入点接缝。 |
| `EntityResolver` | interface | keep | 刻意的插入点接缝。 |
| `Graph` | type (struct) | keep | 知识图谱。 |
| `Canonicalize` | func | keep | 构建一个规范化的 `Graph`。 |
| `LLMCommunitySummarizer` | type (struct) | keep | LLM 支撑的摘要器。 |
| `LLMEntityExtractor` | type (struct) | keep | LLM 支撑的抽取器。 |
| `LabelPropagationDetector` | type (struct) | keep | 标签传播社区检测器。 |
| `LouvainDetector` | type (struct) | keep | Louvain 社区检测器。 |
| `NoopEntityResolver` | type (struct) | keep | 空操作消解器。 |
| `PathRanker` | interface | keep | 刻意的插入点接缝 —— 路径排序抽象。 |
| `RankedPath` | type (struct) | keep | 一条带分数的图路径。 |
| `Relation` | type (struct) | keep | 一个类型化关系。 |
| `Subgraph` | type (struct) | keep | 一个图邻域。 |
| `WeightedPathRanker` | type (struct) | keep | 带权 `PathRanker`。 |

### `guard`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `NeutralizeText` | func | keep | 提示词注入中和器。 |
| `InjectionPattern` | type (struct) | keep | 一个注入检测模式。 |
| `InjectionScanner` | interface | keep | 刻意的插入点接缝。 |
| `InjectionVerdict` | type (struct) | keep | 注入扫描裁定。 |
| `PIIRedactor` | type (struct) | keep | PII 脱敏器。 |
| `NewPIIRedactor` | func | keep | 构造函数。 |
| `PatternScanner` | type (struct) | keep | 基于模式的注入扫描器。 |
| `NewPatternScanner` | func | keep | 构造函数。 |
| `RedactResult` | type (struct) | keep | 脱敏结果。 |
| `Redaction` | type (struct) | keep | 一条脱敏记录。 |
| `Redactor` | interface | keep | 刻意的插入点接缝。 |
| `Rule` | type (struct) | keep | 一条脱敏规则。 |
| `SanitizeMode` | type (int) | keep | 净化模式枚举。 |
| `Neutralize` | const | keep | `SanitizeMode` 枚举值（及其同类）。 |

### `ingest`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `MetadataSourceIDKey` | const | keep | 元数据键常量（及其同类）。 |
| `ErrNilSource` | var | keep | 带包前缀的哨兵错误。 |
| `ErrNilSplitter` | var | keep | 带包前缀的哨兵错误。 |
| `ImportFrom` | func | keep | 流式导入入口点。 |
| `CharSplitter` | type (struct) | keep | 基于字符的切分器。 |
| `NewCharSplitter` | func | keep | 构造函数。 |
| `Chunk` | type (struct) | keep | 一个被摄入的文本块。 |
| `Document` | type (struct) | keep | 一个来源文档。 |
| `Collect` | func | keep | 排空一个 `StreamingSource`。 |
| `ImportOptions` | type (struct) | keep | 导入配置。 |
| `ImportResult` | type (struct) | keep | 导入结果。 |
| `Importer` | type (struct) | keep | importer。 |
| `NewImporter` | func | keep | 构造函数。 |
| `MarkdownSplitter` | type (struct) | keep | markdown 感知的切分器。 |
| `NewMarkdownSplitter` | func | keep | 构造函数。 |
| `Source` | interface | keep | 刻意的插入点接缝。 |
| `StaticSource` | func | keep | 内存版 `Source` 构造函数。 |
| `SourceFunc` | type (func) | keep | `Source` 的函数适配器。 |
| `Splitter` | interface | keep | 刻意的插入点接缝。 |
| `StreamingSource` | interface | keep | 刻意的插入点接缝。 |

### `obs`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `WithCounter` | func | keep | 将一个 `Counter` 附加到一个 context。 |
| `CallCounts` | type (struct) | keep | 分阶段调用计数。 |
| `Counter` | type (struct) | keep | 成本/延迟计数器。 |
| `CounterFrom` | func | keep | 从一个 context 读取一个 `Counter`。 |
| `NewCounter` | func | keep | 构造函数。 |
| `Metrics` | type (struct) | keep | 成本与延迟指标；嵌入在整条流水线中。 |
| `StageTiming` | type (struct) | keep | 分阶段计时。 |
| `TokenUsage` | type (struct) | keep | Token 用量记录。 |

### `pack`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `GreedyTokenPacker` | type (struct) | keep | 贪心上下文打包器。 |
| `Packer` | interface | keep | 刻意的插入点接缝。 |
| `Request` | type (struct) | keep | Pack 请求。 |
| `Result` | type (struct) | keep | Pack 结果。包局部；与 `eval.Result` 无关。 |
| `SimpleCounter` | type (struct) | keep | 刻意的接缝 —— 默认的空白字符 token 计数器；`rag/ask.go` 使用 `SimpleCounter{}`，调用方可换入一个真实分词器。不是意外导出。 |
| `TokenCounter` | interface | keep | 刻意的插入点接缝 —— 分词器抽象。 |
| `Trace` | type (struct) | keep | Packer 链路。 |

### `postgres`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `RegisterTypes` | func | keep | 在一个连接上注册 pgvector 类型。 |
| `Config` | type (struct) | keep | Postgres 存储配置。 |
| `Store` | type (struct) | keep | 针对 PostgreSQL/pgvector 的 `store.Store`。 |
| `New` | func | keep | 构造函数。 |

### `prompt`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `DefaultQATemplate` | type (struct) | keep | 默认 QA 提示词模板。 |
| `RenderContext` | type (struct) | keep | 模板渲染上下文。 |
| `Template` | interface | keep | 刻意的插入点接缝 —— 提示词模板抽象。 |

### `rag`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `ErrCommunitySummarizerRequired` | var | keep | 带包前缀的哨兵错误。 |
| `ErrEmptyQuery` | var | keep | 带包前缀的哨兵错误。 |
| `ErrImporterRequired` | var | keep | 带包前缀的哨兵错误。 |
| `ErrModelRequired` | var | keep | 带包前缀的哨兵错误。 |
| `ErrRetrieverRequired` | var | keep | 带包前缀的哨兵错误。 |
| `ErrSourceRequired` | var | keep | 带包前缀的哨兵错误。 |
| `Answer` | type (struct) | keep | 一个答案路径结果。 |
| `AskOptions` | type (struct) | keep | `System.Ask` 的配置。命名原样批准 —— 见「已批准的命名决策」。 |
| `Citation` | type (struct) | keep | 一个答案引用。 |
| `Diagnostics` | type (struct) | keep | 每次运行的诊断信息。 |
| `DriftDiagnostics` | type (struct) | keep | DRIFT 路径诊断信息。 |
| `DriftOptions` | type (struct) | keep | `System.AskDrift` 的配置。命名原样批准。 |
| `GlobalDiagnostics` | type (struct) | keep | 全局路径诊断信息。 |
| `GlobalOptions` | type (struct) | keep | `System.AskGlobal` 的配置。命名原样批准。 |
| `ImportTrace` | type (struct) | keep | 导入阶段链路。 |
| `InjectionFinding` | type (struct) | keep | 一项提示词注入发现。 |
| `Observer` | type (struct) | keep | 流水线回调钩子。 |
| `Options` | type (struct) | keep | `System` 构造配置。 |
| `SearchOptions` | type (struct) | keep | `System.Search` 的配置。 |
| `System` | type (struct) | keep | 顶层 RAG 系统。 |
| `New` | func | keep | `System` 的构造函数。 |
| `Trace` | type (struct) | keep | 传递给 `Observer` 的每次运行链路。 |

`System` 携带导出的答案路径方法 `Ask`、`AskGlobal`、
`AskDrift`、`Search`、`Import` —— 全部 keep；它们的选项结构体命名在
「已批准的命名决策」中处理。

### `rerank`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `ErrScoringModelRequired` | var | keep | 带包前缀的哨兵错误。 |
| `HTTPScoringModel` | type (struct) | keep | HTTP 支撑的 `ScoringModel`。 |
| `HeuristicReranker` | type (struct) | keep | 启发式重排器。 |
| `ModelReranker` | type (struct) | keep | 模型支撑的重排器。 |
| `NoopReranker` | type (struct) | keep | 空操作重排器。 |
| `Request` | type (struct) | keep | 重排请求。 |
| `RerankScore` | type (struct) | keep | 一个重排分数。 |
| `Reranker` | interface | keep | 刻意的插入点接缝。 |
| `ScoringModel` | interface | keep | 刻意的插入点接缝。 |
| `Trace` | type (struct) | keep | 重排链路。 |

### `retrieve`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `ErrBaseRetrieverRequired` | var | keep | 带包前缀的哨兵错误。 |
| `BM25Params` | type (struct) | keep | BM25 调优参数。 |
| `DenseRetriever` | type (struct) | keep | 稠密向量检索器 —— 冻结的具体检索器。 |
| `EntityLinker` | interface | keep | 刻意的插入点接缝。 |
| `FusionAttribution` | type (struct) | keep | 混合融合归因。 |
| `GapAwareSectionPlanner` | type (struct) | keep | 差距感知的 `SectionPlanner`。 |
| `GraphRetriever` | type (struct) | keep | 图检索器 —— 冻结的具体检索器。 |
| `GraphTrace` | type (struct) | keep | 图检索链路。 |
| `HeuristicDecomposer` | type (struct) | keep | 启发式 `QueryDecomposer`。 |
| `HopAttribution` | type (struct) | keep | 多跳归因。 |
| `HybridRetriever` | type (struct) | keep | 混合检索器 —— 冻结的具体检索器。 |
| `LLMDecomposer` | type (struct) | keep | LLM 支撑的 `QueryDecomposer`。 |
| `LLMExpansionPreprocessor` | type (struct) | keep | LLM 扩展预处理器。 |
| `LexicalEntityLinker` | type (struct) | keep | 词法 `EntityLinker`。 |
| `LexicalRetriever` | type (struct) | keep | 词法检索器 —— 冻结的具体检索器。 |
| `MultiHopRetriever` | type (struct) | keep | 多跳检索器 —— 冻结的具体检索器。 |
| `NoopPreprocessor` | type (struct) | keep | 空操作预处理器。 |
| `PreprocessResult` | type (struct) | keep | 预处理结果。 |
| `QueryDecomposer` | interface | keep | 刻意的插入点接缝。 |
| `QueryEmbedder` | interface | keep | 刻意的插入点接缝。 |
| `QueryPreprocessor` | interface | keep | 刻意的插入点接缝。 |
| `Request` | type (struct) | keep | 检索请求。 |
| `Retriever` | interface | keep | 刻意的插入点接缝 —— 检索器抽象。 |
| `RouteCandidate` | type (struct) | keep | 一个 route-policy 候选。 |
| `RoutePolicyTrace` | type (struct) | keep | route-policy 链路。 |
| `SectionPlanner` | interface | keep | 刻意的插入点接缝。 |
| `SectionPlannerDecision` | type (struct) | keep | 一个 section-planner 决策。 |
| `StructureRetriever` | type (struct) | keep | 结构检索器 —— 冻结的具体检索器。 |
| `Trace` | type (struct) | keep | 检索链路。 |
| `TrajectoryStep` | type (struct) | keep | 一个多跳轨迹步骤。 |
| `VariantRetriever` | type (struct) | keep | 变体检索器 —— 冻结的具体检索器。 |

`retrieve` 的具体检索器面（6+ 个结构体）及其 5+ 个接缝
接口是刻意的、冻结的 `retrieve` 面 —— 全部 keep。

### `store`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `ErrDimensionMismatch` | var | keep | 带包前缀的哨兵错误。 |
| `ErrNotFound` | var | keep | 带包前缀的哨兵错误。 |
| `CommunityStore` | interface | keep | 刻意的能力接口接缝。 |
| `Filter` | type (`map[string]any`) | keep | 元数据过滤器。 |
| `GraphStore` | interface | keep | 刻意的能力接口接缝。 |
| `Hit` | type (struct) | keep | 一个检索命中。 |
| `InMemoryStore` | type (struct) | keep | 内存版 `store.Store`。 |
| `NewInMemoryStore` | func | keep | 构造函数。 |
| `LexicalSearcher` | interface | keep | 刻意的能力接口接缝。 |
| `Query` | type (struct) | keep | 一个存储查询。 |
| `Stats` | type (struct) | keep | 存储统计。 |
| `Store` | interface | keep | 核心存储接缝 —— 刻意的插入点。 |
| `StoredChunk` | type (struct) | keep | 一个已存储的文本块。 |

`store` 的能力接口（`CommunityStore`、`GraphStore`、
`LexicalSearcher`）是刻意的能力接缝 —— 后端通过实现一项
能力来选择开启它；调用方做类型断言。全部 keep。

### `store/storetest`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `RunCommunityConformance` | func | keep | 社区能力一致性套件。 |
| `RunConformance` | func | keep | 核心 `store.Store` 一致性套件。 |
| `RunGraphConformance` | func | keep | 图能力一致性套件。 |
| `RunLexicalConformance` | func | keep | 词法能力一致性套件。 |
| `Factory` | type (func) | keep | 每个子测试的存储工厂。 |
| `Option` | type (func) | keep | 一致性套件选项。 |
| `WithDimensionStrict` | func | keep | 选项构造函数。 |

一个测试支持包，但是一个刻意的一等导出 —— 兄弟仓中的后端
导入它以证明一致性。全部 keep。

### `tree`

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `DocumentTree` | type (struct) | keep | 一个文档的层级树。 |
| `Build` | func | keep | 从一个 `Document` + 文本块构建一棵树。 |
| `BuildStored` | func | keep | 从已存储的文本块构建一棵树。 |
| `Node` | type (struct) | keep | 一个树节点。 |

### `adapter/llmagent`（构建标签：`llmagent`）

在 `//go:build llmagent` 之后。从源码（`adapter/llmagent/model.go`、
`adapter/llmagent/tool.go`）检视 —— `go doc` 无 `-tags` 标志。

| 符号 | 种类 | 处置 | 备注 |
|--------|------|-------------|------|
| `ModelAdapter` | type (struct) | keep | 把核心 `llm-agent` 的 `corellm.ChatModel` 适配到 `generate.Model` 接缝。 |
| `ModelAdapter.Inner` | field | keep | 被包装的核心 `corellm.ChatModel`。导出以便调用方直接构造适配器。 |
| `ModelAdapter.Generate` | method | keep | 满足 `generate.Model`。 |
| `AsTool` | func | keep | 把一个 `rag.System` 暴露为核心 agent 框架的一个 `agents.Tool`。 |

带构建标签的适配器是刻意的、选择开启的核心仓集成
接缝；它的三个导出符号（`ModelAdapter`、`ModelAdapter.Generate`、
`AsTool`）全部 keep。（`tool.go` 中的 `ragToolArgs`、`ragToolSchema`、`ragToolHandler`、
`search`、`ask`、`modelFromSystem`、`max` 正确地是
小写的包私有 —— 这里没有意外导出。）

---

## 意外导出确认

上面的导出面经过 **逐符号** 评审。**不存在
意外导出** —— 每个可导入包（以及 `adapter/llmagent`）的每个导出符号都是 v1.0 API 的刻意组成部分，并
携带处置 **keep**，除两处已批准的 `eval` 重命名外。
没有符号携带 **unexport** 处置。

特别地，许多小的接缝接口被确认为刻意的
插入点，而非意外：

- `retrieve.EntityLinker`、`retrieve.QueryDecomposer`、
  `retrieve.SectionPlanner`、`retrieve.QueryEmbedder`、
  `retrieve.QueryPreprocessor`、`retrieve.Retriever` —— 检索流水线
  接缝；调用方和兄弟仓提供自定义实现。
- `graph.PathRanker`、`graph.EntityExtractor`、`graph.EntityResolver`、
  `graph.CommunityDetector`、`graph.CommunitySummarizer` —— GraphRAG 接缝。
- `pack.TokenCounter` / `pack.SimpleCounter` —— 一个正当的接缝：
  `rag/ask.go` 使用 `SimpleCounter{}` 作为默认空白字符计数器，且
  调用方可以提供一个真实分词器。两者都是刻意的 —— keep。
- `store.Store` 加上 `store.CommunityStore` / `store.GraphStore` /
  `store.LexicalSearcher` 能力接口 —— 存储后端
  契约及其选择开启的能力接缝；兄弟仓后端实现
  它们并通过 `store/storetest` 证明一致性。
- `retrieve` 的具体检索器（`DenseRetriever`、`HybridRetriever`、
  `LexicalRetriever`、`GraphRetriever`、`MultiHopRetriever`、
  `StructureRetriever`、`VariantRetriever`）—— 冻结的具体检索器
  面，刻意导出以供直接构造 —— keep。
- `embed.Embedder`、`generate.Model`、`prompt.Template`、`ingest.Source` /
  `Splitter` / `StreamingSource`、`rerank.Reranker` / `ScoringModel`、
  `guard.Redactor` / `InjectionScanner`、`agentic.QueryReformulator`、
  `eval.Asker` / `GlobalAsker` / `DriftAsker` / `Judge` / `Retriever` ——
  每个包的抽象接缝；全部刻意，全部 keep。

依据 KS-2，v1.0 是一次冻结 —— 本审计 *记录 keep 决策*；它
不修剪或重新设计公共面。

---

## 已批准的命名决策

### 1. `Ask` / `AskGlobal` / `AskDrift` vs `AskOptions` / `GlobalOptions` / `DriftOptions` —— 原样批准

三个答案路径方法是 `System.Ask`、`System.AskGlobal`、
`System.AskDrift`；它们的选项结构体是 `AskOptions`、`GlobalOptions`、
`DriftOptions`。这种不对称（`AskGlobal`/`AskDrift` 携带 `Ask`
前缀，`GlobalOptions`/`DriftOptions` 不携带）在 v1.0 中 **原样批准**，不
重命名。

理据：

- 选项结构体命名的是 *答案模式*（`Global`、`Drift`），而非
  方法。`GlobalOptions` 读作「全局答案模式的选项」——
  这正是它的含义。
- 这一集合内部一致：每个答案模式 `X` 都有一个
  `XOptions` 结构体（`Ask`→`AskOptions`、`Global`→`GlobalOptions`、
  `Drift`→`DriftOptions`）。
- 重命名为 `AskGlobalOptions` / `AskDriftOptions` 将是纯粹的搅动 ——
  一次没有清晰度收益的破坏性变更，与 KS-2 的冻结意图相悖。

决策：所有六个符号 **keep**。无 28-02 动作。

### 2. `eval.Evaluator` → `eval.RetrievalEvaluator`、`eval.Result` → `eval.RetrievalResult`（KS-4）—— 已批准，在 28-02 中应用

基础检索评估器及其结果是 `eval` 包中唯一未带前缀的
一对；那三个较晚的评估器已带名称前缀
（`GlobalEvaluator`/`GlobalEvalResult`、`DriftEvaluator`/`DriftEvalResult`、
`TriadEvaluator`/`TriadResult`）。为对称，基础对被重命名为
`Evaluator`→`RetrievalEvaluator`、`Result`→`RetrievalResult`。方法
`(Evaluator).Run` 保留其名称 `Run`。

引用范围（grep 确认，仓库范围 —— 仅 `eval` 包）：

- `eval/eval.go` —— 声明（`type Result` 第 65 行、`type Evaluator`
  第 81 行）和所有内部返回（`(Evaluator).Run` 第 88 行）。
- `eval/graph.go` —— `RunGraphAB` 构造 `Evaluator{...}`。
- `eval/eval_test.go` —— `eval.Evaluator{...}` 字面量和测试名称。
- `eval/drift.go`、`eval/global.go` —— 两处文档注释提及。
- **在 `examples/`、`contract/` 或任何其他包中无引用** ——
  由 `grep -rn "eval\." contract/*.go examples/*.go` 确认（无匹配）。

该重命名完全包含在 `eval` 包内。**在 slice
28-02 中应用。**

### 3. `ragkit` `doc.go` 包注释重写（KS-3）—— 已批准，在 28-02 中应用

根包是 `package ragkit`，而 module 路径是
`github.com/costa92/llm-agent-rag`。该名称被 **keep** —— `ragkit` 是
SDK 的简短品牌名，也是一个刻意的文档锚点。`doc.go`
包注释被重写以 *陈述* 这一意图：`ragkit` 是一个
文档锚点，调用方导入子包，而非根。
这把名称不匹配从一个无文档的意外转换为一项
有记录的决策。无符号变更 —— `doc.go` 保持无导出符号。
**在 slice 28-02 中应用。**

---

## 契约门禁交叉核对

`contract/contract_test.go` 是跨仓编译锚定：它在
编译期锚定本仓库为核心
`github.com/costa92/llm-agent` 的 `rag/` 门面所消费而导出的面。

由 `grep -rn "eval\." contract/*.go` 确认：契约测试
**完全不引用任何 `eval.` 符号** —— 特别是它不
引用 `eval.Evaluator` 或 `eval.Result`。因此决策 2 中两处已批准的
重命名触及 **零个契约锚定的符号**，且 **无需协调的核心仓 PR**
来落地它们。（slice 28-02 仍然
运行核心门面冒烟测试作为安全网。）

---

## 发布就绪度

在 v1.0 冻结（slice 28-03）时重新验证。代码库不携带任何
延后工作标记、无本地开发依赖逃生舱，也无
死代码桩 —— 被审计的面就是发货的面。

### 非测试代码中零延后工作标记

任何非测试 Go 文件中都不存在 `TODO` / `FIXME` / `XXX` / `HACK` / `Deprecated`
标记。证据：

```
$ grep -rn 'TODO\|FIXME\|XXX\|HACK\|Deprecated' --include='*.go' . | grep -v _test.go
(no output — exit status 1)
```

不存在藏在注释标记之后的「以后再做」遗留
工作；本审计中的每个导出符号都已完整实现。

### 无 `replace` 指令

`go.mod` 不含 `replace` 指令 —— 该 module 完全
通过打了 tag 的依赖解析，无会破坏全新 `go get` 的本地文件系统逃生舱。证据：

```
$ grep -n '^replace\|	replace' go.mod
(no output — exit status 1)
```

### 无死代码 / 无 HTTP 服务端、无 CLI

README「Not implemented yet」列表上的两个真正非目标
针对代码库重新验证：

- **HTTP 服务层** —— 不存在 HTTP 服务端。针对非测试代码 `grep`
  `http.ListenAndServe` / `http.Server` / `http.Handle` / `ServeMux` /
  `http.HandlerFunc` 返回为空。唯一的
  `net/http` 导入者 `rerank/httpmodel.go` 是一个 HTTP *客户端*
  （`HTTPScoringModel` 向一个外部重排 API 发 POST）—— 一个 `ScoringModel`
  接缝，而非 SDK 暴露的服务。
- **CLI** —— 不存在 `cmd/` 目录，仓库中任何地方也没有 `package main`
  （针对非测试代码 `grep -rln '^package main'` 返回
  为空）；不存在 `os.Args` 消费者。

### README「Not implemented yet」列表 —— 现在事实准确

截至 slice 28-03，README「Not implemented yet」列表只命名上面两个
真正延后的非目标（HTTP 服务层、CLI）。两个陈旧的
条目 —— `online-to-offline production-feedback workflow` 和
`cross-repo contract-drift CI gates` —— 已被移除：两者均 **已发货**（
`feedback` 包和 `contract/contract_test.go` 存在于本仓库中）。
该列表不再声称一个已发货的特性未实现。
