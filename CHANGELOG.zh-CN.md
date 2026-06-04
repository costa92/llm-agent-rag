# Changelog

`github.com/costa92/llm-agent-rag` 的所有重要变更都将记录在
本文件中。

<!-- Keep a Changelog format: https://keepachangelog.com/en/1.1.0/ -->
<!-- Semver: https://semver.org/ -->

## [1.9.0] - 2026-05-24

**最后一个增量的 v1.x minor**。`AskOptions.MaxTotalTokens` 强制执行扩展到 `AskGlobal` 和 `AskDrift`（C-BudgetExpand）。v1.x 现已特性冻结 —— 重塑轨道见 `docs/v2-rfc.md`。

### Added

- `rag.GlobalOptions.MaxTotalTokens int` —— 在一次 AskGlobal 调用的 map 和 reduce 子阶段之间为总 Generate token 设上限。零表示无限制（逐字节保留 v1.8.0 行为）。
- `rag.DriftOptions.MaxTotalTokens int` —— 在一次 AskDrift 调用的 primer、局部循环和合成子阶段之间为总 Generate token 设上限。零表示无限制。
- `BudgetExceededError.PartialDiagnostics.Global` 现在在 AskGlobal 中止时被填充（截至触发点收集的 CommunityIDs/MapScores/MapCalls/ConsultedReports）。
- `BudgetExceededError.PartialDiagnostics.Drift` 现在在 AskDrift 中止时被填充（截至触发点收集的 PrimerCommunityIDs/Rounds/RoundEntityIDs/ConsultedReports）。

### Changed

- `wrapBudgetError` 内部辅助函数重构为接受一个 `partialFn func() Diagnostics` 闭包。对 Ask 调用点保持行为不变；使 AskGlobal/AskDrift 能提供它们自己的子诊断构建器。
- `driftPrimer` 内部函数使用具名返回以在出错时传播部分 `driftPrimerResult`（之前被丢弃）。使 primer 中途中止时能填充 `PartialDiagnostics.Drift.PrimerCommunityIDs`。

### Compatibility

- `MaxTotalTokens=0` 在 AskGlobal 和 AskDrift 上都是无限制 —— 现有 v1.8.0 调用方逐字节不受影响。
- 预算强制执行在每个成功的子阶段 Generate（`global_map`、`global_reduce`、`drift_primer`、`drift_local`、`drift_synth`）之后运行，复用 v1.7.0 的 countingModel post-Append 检查。`BudgetExceededError.Stage` 携带触发的子阶段标签。
- **v1.9.0 是最后一个增量的 v1.x minor。** 下一个发布是 v2.0，它将重塑 `wrapBudgetError`、`BudgetExceededError` 和按阶段的仪表接缝。迁移清单见 `docs/v2-rfc.md`。
- 维持仅标准库不变式。
- API 快照差异：新增 2 行（两个 `MaxTotalTokens int` 字段），删除 0 行，重命名 0 行。
- 所有新测试 race-detector 干净。

## [1.8.0] - 2026-05-24

minor 发布，捆绑两个紧耦合的增量特性：一个 Grader/Judge LRU 缓存层（C-Cache）和基准测试记分板的 Markdown 渲染（C-MarkdownExport）。完全增量 —— v1.7.0 调用方看到逐字节的行为保留。

### Added

- `rag.GraderCacheModeRelevance` = `"relevance"` 和 `rag.GraderCacheModeSupport` = `"support"` —— `GraderCacheKey` 的导出模式常量。
- `rag.GraderCache` 接口、`rag.CacheStats{Hits, Misses, Evictions, Size}`、`rag.MemoryGraderCache`（线程安全的 LRU；cap <= 0 时默认为 1024）、`rag.NewMemoryGraderCache(cap int) *MemoryGraderCache`。
- `rag.WrapGrader(inner Grader, cache GraderCache) Grader` —— 将任意 Grader 与任意缓存组合；nil 缓存原样返回 inner。来自 inner 的失败 **不** 被缓存。
- `rag.NewCachingGrader(inner Grader, cap int) Grader` —— 组合 MemoryGraderCache + WrapGrader 的便利构造函数。
- `rag.GraderCacheKey(query, hitID, answer, mode string) string` —— 对 query/hitID/answer 应用 `normalize()`（小写 + 空白折叠）后的规范 SHA-256 十六进制缓存键；mode 是 `GraderCacheModeRelevance` 或 `GraderCacheModeSupport`。格式由 v1.x 缓存键契约锚定。
- `eval.JudgeCache` 接口、`eval.MemoryJudgeCache`、`eval.NewMemoryJudgeCache(cap int) *MemoryJudgeCache`、`eval.WrapJudge(inner Judge, cache JudgeCache) Judge`、`eval.NewCachingJudge(inner Judge, cap int) Judge`、`eval.JudgeCacheKey(query, answer string, context []string) string`。`Stats()` 复用 `rag.CacheStats`。
- `eval.BenchmarkResult.Markdown() string` —— 每指标的管道表记分板外加可选的 AdoptedRound 分布段。NaN 渲染为 `n/a`；Examples 总是发出。结构（段、列顺序、指标顺序）是稳定的；空白和小数精度可能在 minor 发布中演进。

### Changed

- （无 —— 完全增量）

### Compatibility

- `WrapGrader` / `WrapJudge` 是调用方侧的包装器；`rag.System` 和 `eval.AnswerBenchmark` 不变。现有 v1.7.0 调用方逐字节不受影响。
- **v1.8.0 中无 TTL**。纯 LRU 基于 cap 的淘汰。文本块版本变化时的缓存失效是调用方的责任 —— 通过构造一个全新缓存实例来失效。
- **缓存命中绕过 `OnGenerateUsage` 钩子和 `AskOptions.MaxTotalTokens` 消耗** —— 缓存命中时不触发 Generate，所以 observer 钩子和预算计数器都不更新。记录在 `WrapGrader` 和 `WrapJudge` 的 godoc 中。这是缓存的预期行为。
- **缓存键使用 `normalize()`（小写 + 空白折叠）** —— 与 ExactMatch 和 F1Token 相同的规范化。记录为 v1.x 缓存键契约；该格式不可逆，且缓存文件 **不** 跨主版本可移植。
- `CacheStats` 快照在并发负载下，跨计数器与 Size 不是事务一致的。
- `MemoryGraderCache` / `MemoryJudgeCache` 并发使用安全。`WrapGrader(inner, cache)` 当且仅当 `inner` 安全时对并发调用安全。
- `BenchmarkResult.Markdown()` 是一个仅结构的契约。每示例渲染是一个未来 v1.9+ 扩展。
- 维持仅标准库不变式。
- API 快照差异：新增 32 行，删除 0 行，重命名 0 行。
- 所有新测试 race-detector 干净。

## [1.7.0] - 2026-05-24

minor 发布，捆绑四个紧耦合的增量特性：一个每示例的基准测试进度回调（C-BenchProgress）、一个带类型化中止错误的 Ask 级累积 token 预算（C-CostBudget）、drift 报告的 Markdown 渲染（C-DriftDiff），以及 AdoptedRoundCounts 的直方图差异（C-HistogramDrift）。完全增量 —— v1.6.0 调用方看到逐字节的行为保留。

### Added

- `eval.AnswerBenchmark.Progress func(ctx, idx, total int, result AnswerExampleResult, err error)` —— 在顺序和并行模式中每个示例之后触发。Nil 安全。当 Parallelism>=2 时可能从 worker goroutine 并发触发；调用方必须线程安全。
- `eval.DriftReport.Markdown() string` —— 每指标的管道表记分板外加可选的 New/Dropped/Histograms 段。结构（列、指标顺序、方向标签）是稳定的；空白和小数精度可能在 minor 发布中演进。
- `eval.HistogramDelta{Name, Prev, Curr, Delta, L1Distance}` 和 `eval.DriftReport.Histograms []HistogramDelta` —— `AdoptedRoundCounts` 直方图的差异（按较长一侧补零）。`L1Distance` 是 float64，为加权变体留出空间。
- `rag.AskOptions.MaxTotalTokens int` —— 在一次 Ask 调用的所有阶段（ask + reflection + grader + planner + 子阶段）之间为总 Generate token 设上限。零表示无限制（保留 v1.6.0 行为）。
- `rag.ErrTokenBudgetExceeded` 哨兵和 `rag.BudgetExceededError{Stage, Used, Budget, PartialDiagnostics}` —— 当任何成功的 Generate 调用之后累积 StageTokenUsage 超过 MaxTotalTokens 时返回。返回零 Answer；错误结构体上的 PartialDiagnostics 携带截至中止点收集的 Metrics、StageTokenUsage 和部分 Reflection 轮次。Unwrap 到 `ErrTokenBudgetExceeded`；用 `errors.As` 访问 PartialDiagnostics。
- `obs.StageUsageAccumulator.TotalSoFar() int` —— TotalTokens 的运行总和；受互斥锁保护。
- `obs.WithTokenBudget(ctx, max int) context.Context` / `obs.TokenBudgetFrom(ctx) int` —— 上下文附加的累积预算，由 countingModel 在每次 Generate 之后查询。

### Changed

- （无 —— 完全增量）

### Compatibility

- `MaxTotalTokens=0`（零值）是无限制 —— v1.6.0 调用方逐字节不受影响。
- 预算强制执行在每个成功的 Generate 之后（post-Append 到 StageUsageAccumulator）运行。无 Generate 中途的 context 取消；在途 HTTP 自然完成，且超额调用的 StageTokenUsage 条目 *被* 记录。沿调用栈往上的下一个返回携带 `BudgetExceededError`。
- `BudgetExceededError` 在错误结构体上（而非在 Answer 上）携带 `PartialDiagnostics`。调用方应使用 `errors.As(err, &budgetErr)`，然后访问 `budgetErr.PartialDiagnostics`。预算中止时 Answer 是零值。
- **v1.7.0 中 MaxTotalTokens 强制执行仅限 Ask**。`AskGlobal` 和 `AskDrift` 忽略预算并正常完成 —— 跟踪到 v1.8.0。
- `DriftReport.Markdown()` 是一个仅结构的契约。列集合（Metric、Prev、Curr、Δ、Direction）、指标排序（匹配 Deltas 切片）、方向标签（improved/regressed/unchanged/undefined），以及段排序（表 → New/Dropped → Histograms）是稳定的。空白、小数精度和项目符号列表格式可能在 minor 发布中改变。
- `HistogramDelta` 按较短一侧补零；`L1Distance == sum |Delta[i]|`。
- `Progress` 在 `Parallelism>=2` 下可能并发触发 —— 与现有 `OnGenerateUsage` 钩子相同的线程安全契约。
- 维持仅标准库不变式。
- API 快照差异：新增约 22 行，删除 0 行，重命名 0 行。
- 所有新测试 race-detector 干净。

## [1.6.0] - 2026-05-24

minor 发布，捆绑两个紧耦合的增量特性：用于基准测试结果和 rag 诊断的 NaN 安全规范 JSON/JSONL 编解码器（C-DiagnosticsExport），以及一个带每指标极性表的指标 drift 比较器（C-Drift）。

### Added

- `eval.MarshalBenchmark(r BenchmarkResult) ([]byte, error)` 和 `eval.UnmarshalBenchmark(b []byte) (BenchmarkResult, error)` —— 对完整基准测试运行的规范 JSON 往返，包括通过 JSON null 的 NaN 哨兵保留。
- `eval.WriteBenchmarkJSONL(w io.Writer, r BenchmarkResult) error` 和 `eval.ReadBenchmarkJSONL(r io.Reader) (BenchmarkResult, error)` —— 流式 JSONL 制品格式（一行头部 + 每个 AnswerExampleResult 一行）。严格 —— 不跳过 `//` / `#` 注释（与 `LoadAnswerJSONL` 不同）。
- `eval.ErrEmptyBenchmark` —— 当 reader 不产出行时由 `ReadBenchmarkJSONL` 返回。
- `rag.MarshalDiagnostics(d Diagnostics) ([]byte, error)` 和 `rag.UnmarshalDiagnostics(b []byte) (Diagnostics, error)` —— 每 Answer Diagnostics 的编解码器，带结构化的 Subgraph 投影（实体 ID、边 ID、最大跳数）。
- `eval.Direction` 枚举（`DirectionImproved`/`DirectionRegressed`/`DirectionUnchanged`/`DirectionUndefined`）、`eval.MetricDelta{Name, Prev, Curr, Delta, Direction}`、`eval.DriftReport{Dataset, Deltas, NewExamples, DroppedExamples}`、`eval.CompareBenchmarks(prev, curr BenchmarkResult) DriftReport` —— 带锚定的每指标极性的 drift 比较器。
- `DriftReport.Summary() string` —— 人类可读的记分板。

### Changed

- （无 —— 完全增量；锁定的设计禁止在现有结构体上加 `json:` 标签）

### Compatibility

- wire 类型保持未导出；只有编解码器函数是稳定 API。未来的 wire 重排不会破坏调用方。
- `BenchmarkMetrics` 中的 NaN/Inf 编码为 JSON `null`；null 解码回 `math.NaN()`。按字段记录。
- `Direction` 语义：任何触及 NaN 的转换（finite→NaN、NaN→finite、NaN→NaN）产出 `DirectionUndefined`。极性含糊的指标（`ReflectionRoundsMean`、`GraderAdoptionRate`、`ActiveRetrievalFireRate`）无论 delta 如何都总是产出 `DirectionUndefined`。`Unchanged` 容差硬编码为绝对值 `1e-9`。
- Drift 示例匹配按精确的 `Example.Query` 字符串相等。想要在重命名查询间稳定匹配的调用方必须确保查询在一个数据集内唯一。
- `AdoptedRoundCounts` 直方图差异在范围之外（v1.7.0 候选）。
- `GraphTrace.EvidenceSubgraph` 序列化为结构化投影（实体 ID、边 ID、最大跳数），而非完整 Subgraph。记录为一个可扩展的 v1.6.0 契约。
- Diagnostics 和 Benchmark JSON 解码时严格未知键：未知顶层键报错。向前兼容：未来 minor 版本可能添加字段；reader 已更新。
- 维持仅标准库不变式。
- API 快照差异：新增约 25 行，删除 0 行，重命名 0 行。

## [1.5.1] - 2026-05-24

patch 发布，闭合 v1.5.0 的明确范围外项：AskGlobal/AskDrift 的子阶段标签、RetryPolicy 可观测性钩子，以及预构建的错误分类器。

### Added

- `rag.StageAsk`、`rag.StageReflectionDecision`、`rag.StageGrader`、`rag.StagePlanner`、`rag.StageJudgeEval` —— 作为 v1.5.0 中引入的现有阶段标签的具名常量导出。
- `rag.StageAskGlobalMap`、`rag.StageAskGlobalReduce` —— `AskGlobal` 期间 map/reduce 内部 Generate 调用上现已标记的子阶段。
- `rag.StageAskDriftPrimer`、`rag.StageAskDriftLocal`、`rag.StageAskDriftSynth` —— `AskDrift` 期间 3 个内部 Generate 调用上现已标记的子阶段。
- `eval.RetryPolicy.OnRetry func(ctx, attempt int, err error)` —— 在失败尝试和下一次睡眠之间触发。在终止失败或不可重试（Classify=false）错误上 **不** 触发。Nil 安全。
- `eval.ClassifyTransientHTTP(err error) bool` —— 尽力而为的分类器，匹配网络超时和 HTTP 408/429/500-504。
- `eval.ClassifyRateLimited(err error) bool` —— 尽力而为的分类器，匹配 HTTP 429 和常见的限流子串。

### Changed

- （无 —— 完全增量）

### Compatibility

- 顶层 Ask 调用仍发出单个 `"ask"` 阶段标签（不变）。只有 AskGlobal/AskDrift 中的 **内部** Generate 调用获得子阶段标签。
- 现有 OnGenerateUsage 消费者从 AskGlobal/AskDrift 路径看到额外的不同阶段值；如果之前在 `stage="ask"` 下求和，他们现在会在更具体的标签下看到那些条目。已记录。
- ClassifyTransientHTTP 和 ClassifyRateLimited 是尽力而为的启发式。具有已知 SDK 错误形态的生产用户应编写他们自己的分类器 —— 内置项面向未知/混合 SDK 环境。
- API 快照差异：新增约 13 行，删除 0 行。

## [1.5.0] - 2026-05-24

minor 发布，捆绑两个紧耦合的成本与韧性特性：用于按阶段 token 归因的 CostObserver 钩子，以及用于在瞬时错误下韧性化 Asker/Judge 的 RetryWrap 适配器。两者都完全增量。

### Added

- `rag.Observer.OnGenerateUsage func(ctx, stage string, usage obs.TokenUsage)` —— 每个成功的 `generate.Model.Generate` 调用触发；标准阶段：`ask`、`reflection_decision`、`grader`、`planner`。Nil 安全。在 ParallelFollowups / AnswerBenchmark.Parallelism>=2 下可能并发触发 —— 实现必须线程安全。
- `obs.StageTokenUsage{Stage, Usage}` + `obs.Metrics.StageTokenUsage []StageTokenUsage` —— 顶层 Ask/AskGlobal/AskDrift 期间每次 Generate 的仅追加审计。
- `obs.StageUsageAccumulator`、`obs.WithStageUsage(ctx, *StageUsageAccumulator)`、`obs.StageUsageFrom(ctx)` —— 上下文附加的聚合器；通过 sync.Mutex 线程安全。
- `eval.RetryPolicy{MaxAttempts, BaseDelay, MaxDelay, Jitter, Classify}` 和 `eval.JitterMode`（`JitterNone`、`JitterEqual`）。
- `eval.NewRetryAsker(inner Asker, policy RetryPolicy) Asker` 和 `eval.NewRetryJudge(inner Judge, policy RetryPolicy) Judge` —— 以 2 为底的指数退避，在 MaxDelay 处封顶，可选的 equal jitter，睡眠期间 ctx-cancel 短路。Classify==nil 重试所有错误。
- `eval.NewCostObservingJudge(inner LLMJudge, hook GenerateUsageHook) Judge` —— 触发 stage="judge_eval" 的 eval 侧对应物。位于 `eval` 中以避免 `eval → rag.countingModel` 导入循环。

### Changed

- （无 —— 完全增量）

### Compatibility

- `Metrics.Tokens` 逐字节不变。`StageTokenUsage` 是增量的；跨条目的 SUM **不** 等于 `Tokens`（不同的聚合语义 —— Tokens 是答案段的 `deriveTokenUsage(req, resp)`；StageTokenUsage 是每 Generate 审计）。
- 用户自定义的 `Grader` / `QueryPlanner` 实现 **不** 被自动包装 —— 只有发货的 `PromptGrader` / `PromptQueryPlanner` 在 `New(opts)` 期间重建其 `.Model`。自定义实现必须包装它们自己的模型以触发 OnGenerateUsage。
- 单个 `"ask"` 阶段标签覆盖 Ask/AskGlobal/AskDrift 内部 Generate 调用。子阶段（`global_map`、`drift_primer` 等）是未来增量的。
- 维持仅标准库不变式（math/rand/v2 是标准库 Go 1.22+）。
- API 快照差异：新增约 14 行，删除 0 行，重命名 0 行。
- 并行重试测试和并行 observer 测试 race-detector 干净。

## [1.4.0] - 2026-05-24

minor 发布，捆绑对 v1.3.0 AnswerBenchmark 测试套件的两个紧耦合新增：一个可选的 LLM-as-judge 过程（C-BenchJudge）和有界并行示例分发（C-BenchPar）。两者都完全增量且对 v1.3.0 调用方零影响。

### Added

- `eval.AnswerBenchmark.Judge eval.Judge` —— 可选的 LLM-as-judge。非 nil 时，每个示例生成的答案以与 `TriadEvaluator` 所用相同的 `JudgeRequest{Query, Answer, Context}` 形态针对其检索到的上下文打分。nil 时，judge 侧指标为 NaN。
- `eval.AnswerBenchmark.Parallelism int` —— 在数据集示例上的有界 worker 池。`0` 或 `1` 逐字节运行 v1.3.0 顺序路径。`>=2` 通过 `sync.WaitGroup` + 带缓冲 channel 信号量扇出，镜像 v1.2.1 主动检索模式。负值强制为顺序；大值原样通过（无 GOMAXPROCS 上限 —— Asker 工作是 I/O 密集型）。
- `eval.BenchmarkMetrics.MeanGroundedness` 和 `eval.BenchmarkMetrics.MeanAnswerRelevance` —— 数据集级别的均值。当 `Judge == nil` 或数据集为空时为 `math.NaN()`。
- `eval.AnswerExampleResult.Judgement eval.Judgement` 和 `eval.AnswerExampleResult.JudgeApplied bool` —— 每示例的 judge 链路；该 bool 区分「未配置 judge」与「judge 返回 0.0/0.0」。

### Changed

- （无 —— 完全增量）

### Compatibility

- `eval.Asker` 和 `eval.Judge` 接口不变。
- `*rag.System` 结构上满足 `eval.Asker`（不变）。
- 成本说明：在 `Judge != nil` 下，每个示例产生一次 judge 调用（无内部限流，无每示例跳过）。需要采样的调用方通过包装他们的 Judge 在外部叠加。
- 错误语义：在顺序和并行模式下，Asker 和 Judge 错误都是首遇即中止。在并行下，第一个错误通过一个粘性的 `firstErr` 胜出；在途的同辈自然完成（context 不被取消）。记录在字段 godoc 中。
- 确定性：无论 `Parallelism` 如何，`PerExample` 顺序匹配 `dataset.Examples` 顺序（按索引为键写入一个预分配切片，与 `unionHits` 相同的技巧）。
- 线程安全：当 `Parallelism >= 2` 时 `Asker` 和 `Judge` 实现必须并发使用安全。`*rag.System` 是安全的。
- 维持仅标准库不变式（无新第三方导入）。
- API 快照差异：新增 6 行，删除 0 行，重命名 0 行。
- 所有新并行测试 race detector 干净。

## [1.3.0] - 2026-05-24

minor 发布，向 `eval` 包添加一个 C-Eval 答案质量基准测试套件 —— 一个针对 Ask 路径的生成侧记分板，基于文本匹配（ExactMatch、token F1、必需短语召回）外加 v1.2.x 反思/主动检索信号聚合打分。纯 C-Eval：无 LLM judge，不捆绑外部数据集。

### Added

- `eval.AnswerExample` —— 嵌入 `eval.Example`，添加 `GoldAnswers []string`（任一匹配计为 ExactMatch）和 `RequiredPhrases []string`（应出现在答案中的逐字子串）。
- `eval.AnswerDataset` —— `Dataset` 的同类，携带 `[]AnswerExample`。
- `eval.LoadAnswerJSONL(path) (AnswerDataset, error)` —— 镜像 `LoadJSONL` 语义的 JSONL 加载器（跳过注释/空白行，遵循第一个 `top_k`，4 MiB 行缓冲）。
- `eval.AnswerBenchmark` —— `{ Asker, Options }`。顺序运行器；复用现有的 `eval.Asker` 接口（不添加新接缝）。
- `eval.BenchmarkMetrics` —— 固定结构体，带 `ExactMatch`、`F1Token`、`RequiredPhraseRecall`、`ReflectionRoundsMean`、`AdoptedRoundCounts []int`、`GraderAdoptionRate`、`FollowupQueriesUsedMean`、`ActiveRetrievalFireRate`。当一个指标的底层特性在整个数据集上都关闭时为 NaN 哨兵。
- `eval.BenchmarkResult` 和 `eval.AnswerExampleResult` —— 完整的每示例链路，使离线指标重算成为可能。
- `eval/testdata/answer_bench_minimal.jsonl` —— 3 示例的合成 fixture，封闭自洽。

### Changed

- （无 —— 完全增量）

### Compatibility

- 现有 `eval.Asker` 接口不变且复用。
- 无新第三方导入（保留仅标准库不变式）。
- API 快照差异：新增 40 行，删除 0 行，重命名 0 行。
- `*rag.System` 结构上满足 `eval.Asker`（与 v1.2.x 相比不变）。
- F1Token 分词器是空白 + ToLower；记录为「尽力而为，非论文级」。必需短语匹配是逐字的大小写敏感子串。
- `BenchmarkMetrics` 对底层特性在所有示例上都关闭的指标携带 `math.NaN()`；该结构体刻意没有 `json:` 标签 —— 调用方在序列化时用他们自己的 wire 类型包装它。

## [1.2.1] - 2026-05-24

patch 发布，闭合 v1.2.0 的明确范围外项：并行
后续分发、主动检索 Observer 钩子，以及一个每 Ask 的
QueryPlanner 覆盖。

### Added

- `rag.ReflectionOptions.ParallelFollowups bool`（默认 `false`）。为
  true 时，主动检索的后续检索通过 `sync.WaitGroup` + 一个带缓冲 channel 信号量以
  有界并行度并发触发。
- `rag.ReflectionOptions.MaxFollowupConcurrency int`（默认 `0`，意味着
  `MaxFollowupQueries`）。为并行扇出设上限。
- `rag.AskOptions.QueryPlanner QueryPlanner` —— 每 Ask 的 planner 覆盖，
  优先于 `Options.QueryPlanner`。Nil 回退到
  系统 planner，然后到 `NoopQueryPlanner`。
- `rag.Observer.OnPlanFollowups func(ctx, question, scores, planned)` ——
  在 planner 发出非空计划且预算上限被应用之后、任何后续分发之前，每个反思轮次触发一次。
- `rag.Observer.OnFollowupRetrieve func(ctx, query, hits, err)` —— 无论分发模式（顺序或并行）、也无论成功/出错，每个被执行的后续检索触发一次。

### Changed

- （无 —— 所有变更都是增量的；默认保留 v1.2.0 行为）

### Compatibility

- 完全增量。新字段保持零/nil 时现有 v1.2.0 调用方
  不受影响。
- 确定性不变式保留：无论 `ParallelFollowups` 设置如何，`RoundDiagnostics.FollowupQueries` 都按 **planner 输出顺序** 而非完成顺序排列。合并后的命中保持确定性（按分数稳定排序），因为 `hitSets` 按 planner 位置索引。
- Observer 回调（尤其是 `OnFollowupRetrieve`）在并行模式下可能并发触发 —— 调用方实现 **必须** 线程安全。记录在字段 godoc 中。
- 维持仅标准库不变式（无新第三方导入）。
- API 快照差异：新增 5 行，删除 0 行，重命名 0 行。
- 所有新并行测试 race detector 干净。

## [1.2.0] - 2026-05-24

minor 发布，向反思循环添加 Active Retrieval 和一个可插拔的 QueryPlanner 接缝。增量、无破坏性变更 —— 受 v1.x 仅增量承诺覆盖。

### Added

- `rag.QueryPlanner` 接口 —— `PlanFollowups(ctx, question, prevAnswer, scores) ([]string, error)`。两个实现在包内发货：
  - `rag.NoopQueryPlanner` —— 返回 `(nil, nil)`。安全默认。
  - `rag.PromptQueryPlanner{Model generate.Model}` —— LLM 驱动的 planner，每行发出一个后续查询。防御性地剥离项目符号/编号前缀。在模型错误或空回复上 fail open（返回 `nil, nil`，绝不出错）；仅当 `Model` 为 nil 时返回非 nil 错误。
- `rag.Options.QueryPlanner` —— 系统级接线槽。nil planner 默认为 `NoopQueryPlanner`。
- `rag.ReflectionOptions` 主动检索字段：
  - `EnableActiveRetrieval bool`（默认 `false`）
  - `MaxFollowupQueries int`（默认 `2`，每轮上限）
  - `MaxFollowupQueriesPerAsk int`（默认 `4`，每 Ask 的全局上限）
  - `ActiveRetrievalRelevanceFloor float64`（默认 `0.4`）
- `rag.ReflectionRoundDiagnostics.FollowupQueries []string` 和 `rag.ReflectionRoundTrace.FollowupQueries []string` —— 该轮 planner 发出的后续查询。
- `rag.ReflectionDiagnostics.FollowupQueriesUsed int` —— 一次 Ask 调用中跨轮消耗的全局计数器。

### Changed

- （无 —— 行为由 `EnableActiveRetrieval` 门控）

### Compatibility

- 完全增量。`EnableActiveRetrieval` 保持零值时现有 v1.1.x 调用方不受影响。
- 组合：主动检索和 `AllowRewrite` 是正交的 —— 主动检索在一轮 **内** 触发（在打分/打包之前把后续命中与种子检索取并集）；rewrite 驱动下一轮的输入查询。两者可同时开启。
- 保留仅标准库不变式（无新第三方导入）。
- API 快照差异：新增 16 行，删除 0 行，重命名 0 行。

## [1.1.1] - 2026-05-24

patch 发布，在反思链路之上添加一个数据集抽取接缝。增量、无破坏性变更 —— 受 v1.x
仅增量承诺覆盖。

### Added

- `rag.GraderExample` —— 一个从完成的 Ask 链路中抽取的、捕获一个
  `(query, answer, chunk, grader-scores, round, adopted-flag)` 元组的结构体。字段：`Query`、`Answer`、
  `ChunkID`、`ChunkContent`、`Relevance`、`Support`、`Reason`、
  `Round`（从 1 开始，匹配 `ReflectionRoundDiagnostics.Round`）、
  `Adopted`（当且仅当 `Round == AdoptedRound` 且 `ChunkID` 在
  被采纳轮次的 `PromptChunkIDs` 中时为 true）。
- `rag.ExportGraderDataset(Answer) []GraderExample` —— 在
  `Answer.Diagnostics.Reflection.RoundDetails` 之上的纯只读抽取器。
  当反思关闭（无 `RoundDetails`）或打分
  关闭（任何轮次中都无 `ChunkScores`）时返回 nil。按文本块 ID 从
  `Answer.Hits` 拼接文本块文本；当没有 `Hit` 携带该 ID 时（例如对于在非采纳轮次打分或被打包器丢弃的文本块），`ChunkContent` 回退为 `""`。对任何 goroutine 安全；无 I/O、无变更。

### Changed

- （无）

### Compatibility

- 完全增量。现有 v1.x 用户不受影响。
- API 快照差异：新增 11 行，删除 0 行，重命名 0 行。
- 保留仅标准库不变式（无新第三方导入）。
- 命名说明：`GraderExample` 的分数来自被接线的任何 `Grader`
  （通常是 `PromptGrader`）。它们 **不是** 论文级的
  `[ISREL]` / `[ISSUP]` 评论模型标签 —— 作为弱
  监督对待。

## [1.1.0] - 2026-05-23

minor 发布，闭合 Self-RAG 反思里程碑的 Track B。
增量、无破坏性变更 —— 受 v1.x 仅增量
承诺覆盖。

### Added

- `rag.Grader` 接口（每文本块的相关性/支持度打分），
  受 Self-RAG / RAGLab 的 `[ISREL]` / `[ISSUP]` 反思
  token 启发。两个实现在包内发货：
  - `rag.NoopGrader` —— 确定性的中性 0.5 分数，未配置真实 grader 时的安全
    默认。
  - `rag.PromptGrader{Model generate.Model}` —— LLM 驱动的打分器，
    要求模型发出 `score=<float>` 行。把
    超范围值钳制到 `[0,1]`，并在无法解析的回复上 fail open（0.5 + 原始文本作为
    原因）。传播模型级传输
    错误，使反思的 `FailOpen` 逻辑能区分一个
    确定性中性与一次真实中断。
- `rag.ChunkScore`（`HitID` / `Relevance` / `Support` / `Reason`）——
  携带在 `ReflectionRoundDiagnostics.ChunkScores` 和
  `ReflectionRoundTrace.ChunkScores` 上的每文本块打分记录。仅当
  `ReflectionOptions.EnableChunkGrading` 为 true 且配置了一个 `Grader` 时填充；否则为空。
- `rag.Options.Grader` —— 系统级接线槽。一个 nil Grader
  且启用了打分时回退到 `NoopGrader`，使接线
  始终可用。
- `rag.SelectionMode` 枚举：
  - `SelectionModeLastRound`（= 0，默认）—— 保持 v1.0.x 的
    最后一轮胜出采纳。
  - `SelectionModeBestByScore` —— 采纳加权聚合 `ChunkScores` 最高的
    那一轮。循环语义（何时停止、
    何时重写）不变 —— 该标志只影响最后返回哪一轮的答案/引用。平局由
    最早轮次索引打破以保确定性。
- `ReflectionOptions` 获得六个增量字段，所有零默认值
  都保留 v1.0.x 行为：
  - `EnableChunkGrading`（默认 `false`）
  - `GraderRelevanceWeight`（默认 `0` → 激活时 `0.5`）
  - `GraderSupportWeight`（默认 `0` → 激活时 `0.5`）
  - `SelectionMode`（默认 `SelectionModeLastRound`）
  - `AdaptiveRetrieval`（默认 `false`）—— 当最新一轮的最大文本块相关性低于
    `AdaptiveRetrievalThreshold` 时强制一个额外轮次，受 `MaxRounds` 约束。
  - `AdaptiveRetrievalThreshold`（默认 `0` → 激活时 `0.6`）

### Changed

- 当 `EnableChunkGrading=true` 时反思循环现在在每一轮记录每文本块的 `ChunkScores`，并在最后应用配置的
  `SelectionMode` 来挑选被采纳的轮次。默认值
  （全部关闭）使行为与 v1.0.6 逐字节相同 —— 22+
  个现有反思测试保持原样绿色。

### Compatibility

- 纯增量：每个现有测试都无需
  修改地保持绿色。
- API 快照差异：新增 22 行，删除 0 行，重命名 0 行。
- 保留仅标准库不变式（无新第三方导入）。

## [v1.0.6] - 2026-05-23

增量、无破坏性变更 —— 受 v1.x 仅增量
承诺覆盖。

### Added

- `ReflectionRoundDiagnostics` 和 `ReflectionRoundTrace` 现在捕获
  每轮的路由情报：`RoutePath`、`AutoRoutePath`、
  `AutoRouteCandidates`、`SearchTrajectory`、`GraphTrace`（在
  Diagnostics 上）；`AutoRoutePath`（在 Trace 上）。之前只有最后
  一轮的路由在 `Answer.Trace` 中可见；多轮
  反思现在分别暴露每轮的路由决策，
  使「为何第 N 轮选择了与第 N-1 轮不同的路由」式
  调试成为可能。（D4 闭合）
- `ReflectionRoundDiagnostics.RawDecisionText` 和 `DecisionPrompt`
  保留模型的原始反思决策回复和发送给模型的提示词，
  用于事后调试决策漂移。在规则模式中和规则路径先停止的混合轮次中为空。（D5 闭合）
- `ReflectionRoundTrace.RawDecisionText` 为面向 observer 的链路
  消费者镜像诊断侧的等价物。

### Changed

- `parseReflectionDecision` 现在对决策值
  接受任何大小写（`Stop`、`STOP`、`Continue`、`Rewrite_and_continue` 等）。
  `reflectionDecisionPrompt` 中记录的
  协议仍然要求小写，但真实世界的模型输出会漂移；我们在枚举匹配之前用 `strings.ToLower`
  归一化该值。（D3 闭合）

### Compatibility

- 纯增量：现有反思测试保持原样绿色。
- API 快照增量仅为增量（7 个新字段，0 删除，
  0 重命名）。
- 保留仅标准库不变式（无新第三方导入）。

## [v1.0.5] - 2026-05-23

增量、无破坏性变更 —— 受 v1.x 仅增量
承诺覆盖。

### Added

- `postgres.VectorIndex` 枚举（`VectorIndexNone` / `VectorIndexIVFFlat`
  / `VectorIndexHNSW`）外加 `Config.VectorIndex`、`Config.IVFFlatLists`
  和 `Config.HNSWConstructionM` 字段。设置时，`Migrate` 为嵌入列发出一个
  幂等的 `CREATE INDEX IF NOT EXISTS` ——
  `USING ivfflat (embedding vector_cosine_ops) WITH (lists = N)` 或
  `USING hnsw (embedding vector_cosine_ops) WITH (m = M)`。默认
  零值保留 v1.0.4 行为（无向量索引 —— 现有
  数据库不受影响）。在约 100K 文本块的表上 IVFFlat lists=100 把
  最近邻查询延迟从约 1.5s 降到约 80ms（约 19 倍加速，按
  路线图模型 —— 实际增益取决于环境）。HNSW
  要求 pgvector >= 0.5。（P1-1）

## [v1.0.4] - 2026-05-23

### Changed

- `HybridRetriever.Retrieve` 现在并发扇出 Dense/Lexical/Structure/Graph
  检索器。混合查询的墙钟延迟从
  四者之和降到四者最大（典型 2-4 倍降低）。行为
  逐字节相同：融合（RRF）从构造上确定性，且
  Dense > Lexical > Structure > Graph 的错误优先级
  通过等待所有 goroutine 并按原始顺序选择得以保留。（P1-15）

## [v1.0.3] - 2026-05-23

增量、无破坏性变更 —— 受 v1.x 仅增量
承诺覆盖。

### Added

- `embed.BatchEmbedder` —— 面向原生支持多文本批次的嵌入器的可选兄弟能力接口。`rag.System`
  importer（`Import` / `ImportFrom`）针对 `BatchEmbedder` 对配置的
  嵌入器做类型断言，且满足时把每篇文档的每个
  待处理文本块坍缩进一次 `EmbedBatch` 调用 ——
  用一次往返替换 N 次顺序的每文本块 `Embed` 调用。
  纯 `Embedder` 调用方看不到变化：每文本块循环
  与 v1.0.2 行为逐字节相同。计数仪表
  包装器（`countingEmbedder`）获得一个 `countingBatchEmbedder` 兄弟，
  使该能力在仪表层中存续，且类型
  断言对调用方提供的 `BatchEmbedder` 仍然成功。（P1-16）

## [v1.0.1] - 2026-05-20

维护发布。无公共 API 变更 —— 受 v1.x
仅增量承诺覆盖。

### Changed

- 把带构建标签的 `adapter/llmagent/` 反向边版本提升回
  `github.com/costa92/llm-agent v0.5.0`，拾取
  生态对齐的核心。默认（未带标签）构建在 `postgres` 子包之外
  保持仅标准库。（KE-2 —— 反向边
  版本提升在 v1.x 下被允许，因为它们被门控在
  `llmagent` 构建标签之后，且在默认构建上触及不到任何导出符号。）

## [v1.0.0] - 2026-05-21

v1.0 API 冻结。**不是特性发布** —— 本发布中任何地方都没有新特性、无
行为变更、无新依赖。v1.0.0
冻结 `github.com/costa92/llm-agent-rag` 公共 API，并采纳
Go module 导入兼容承诺：在 `v1.x` 系列内
导出 API 是 **仅增量** 的 —— 导出符号不被重命名、
删除或重新签名，且任何破坏性变更都需要一个新的主版本（`/v2`）。完整策略写在
[`docs/compatibility.md`](docs/compatibility.md) 中。

`postgres` 子包仍然是唯一的非标准库孤岛；其他
一切都保持仅标准库。

### Changed

冻结前的 **最后** 破坏性变更 —— API 在 `v1.x` 线中将做的
最后一批重命名：

- `eval.Evaluator` → `eval.RetrievalEvaluator` 和 `eval.Result` →
  `eval.RetrievalResult`。检索评估类型和结果现在加了
  前缀，以与已加前缀的答案路径评估器
  `eval.GlobalEvaluator`、`eval.DriftEvaluator` 和 `eval.TriadEvaluator` 对称。
  调用方更新类型名称；方法集和行为
  不变。
- `ragkit` 根包注释（`doc.go`）被重写以记录
  根作为一个刻意的文档锚点 —— 它不导出符号；
  调用方直接导入子包（`rag`、`retrieve`、`store`、`embed`、
  `ingest`、`generate`、`eval` 以及其余）。无符号改变；
  在此说明是因为该包记录的角色现在是显式的。

### Added

增量、非破坏性的 v1.0 工作 —— 文档和一个稳定性门禁，无
运行时变更：

- [`docs/compatibility.md`](docs/compatibility.md) —— 书面的 Go module
  兼容承诺：`v1.x` 仅增量保证覆盖什么、
  明确在其之外的是什么，以及未来的 `/v2` 将如何处理。
- `docs/api-audit-v1.0.md` —— 冻结期导出面审计：每个
  可导入包（加上带构建标签的
  `adapter/llmagent`）的每个导出符号被清点并分类为 keep / rename / unexport。
- 整个 module 的完整的包级和导出符号级文档注释覆盖
  —— 每个可导入包和每个导出符号现在都
  携带文档。
- `api/v1.snapshot.txt` 导出面快照门禁 —— 一个已提交的
  冻结 v1 API 基线，由一个普通的标准库 `go test`
  （`internal/apisnapshot`）重新生成并做差异。它
  对任何意外的导出 API 变更失败，补充了更窄的跨仓 `contract`
  编译锚定：`contract` 跨仓锚定核心门面子集，
  快照对整个仓库内的面做差异。
- 仓库现在 `gofmt` 干净。

## [v0.6.0] - 2026-05-20

minor 发布，闭合 v0.9 GraphRAG 精化里程碑（Phases
26-27）。在 v0.7 Tier-1 和 v0.8 Tier-3 GraphRAG 之上构建，带路径排序
子图证据和 DRIFT 混合检索。增量且选择开启 —— 默认
行为不变。无新依赖、无图数据库；
`postgres` 子包仍然是唯一的非标准库孤岛。

### Added

- 路径排序和子图作为证据（Phase 26）：
  - 新 `graph.RankedPath` 类型和 `graph.PathRanker` 接缝
  - `graph.WeightedPathRanker` —— 一个 `Subgraph` 内多跳简单路径的确定性纯标准库排序器（有界 DFS 枚举；
    在路径长度、`Relation.Weight` 和溯源
    重叠上的复合分数；全序的实体 ID 序列平局打破）
  - `retrieve.GraphRetriever` 上的一个选择开启 `PathRanker` 字段；
    `retrieve.GraphTrace` 获得增量的 `Paths` 和 `EvidenceSubgraph`
    字段，通过 `rag.Diagnostics` 浮现。路径模式关闭时，
    `GraphRetriever.Retrieve` 与 v0.7/v0.8 逐字节相同。
- DRIFT 检索（Phase 27）：
  - `rag.System.AskDrift` —— 一条混合答案路径：一次全局「primer」过程
    用于宽泛定向、一个硬性有界的局部后续循环
    （轮次上限 3，在无新后续实体时终止），以及一个
    合成步骤。一条分离的答案路径 —— 它不是一个 `Retriever`，
    也不是 `Ask`/`AskGlobal` 上的一个模式标志。
  - `rag.DriftOptions` 和一个 `Diagnostics.Drift` 块（primer 社区、
    运行的轮次、每轮实体 ID、咨询过的报告）
  - `eval.DriftEvaluator` —— 在
    RAG-Triad / `LLMJudge` 路径之上的 DRIFT 答案评估测试套件（依据性、答案相关性）
  - `docs/graphrag.md` 为完整的 GraphRAG 谱系定稿

### Notes

- 增量社区维护仍推迟到 v1.0+ —— v0.8 在重新摄入时的
  全量重新检测在本 SDK 的规模上正确且快速；
  只在性能剖析显示社区检测主导重新摄入时才重新审视。
- 推迟到 v1.0+：增量社区维护、声明/协变量
  抽取、一个专用图数据库。

## [v0.5.0] - 2026-05-20

minor 发布，闭合 v0.8 GraphRAG Tier-3 里程碑（Phases 23-25）。
在 v0.7 Tier-1 GraphRAG 之上构建，带层级化社区检测、
惰性社区摘要、一条 map-reduce 全局检索答案路径，以及
嵌入相似度模糊实体消解。增量且选择开启 —— 默认
行为不变。无新依赖、无图数据库：社区
检测是纯标准库，`postgres` 子包仍然是唯一的
非标准库孤岛。

### Added

- 社区检测（Phase 23）：
  - 新 `graph.Community` 类型和 `graph.CommunityDetector` 接缝
  - `graph.LouvainDetector` —— 一个确定性纯标准库 Louvain 检测器，
    通过粗化过程产出社区层级
  - `graph.LabelPropagationDetector` —— 一个更快的单层替代方案
  - `store.CommunityStore` 可选能力接口（`store.GraphStore` 的同类）—— `GraphSnapshot`、`UpsertCommunities`、
    `Communities` —— 带一个纯标准库的内存实现和一个
    `postgres` 实现（`_communities` 表）
  - 社区检测接线为一个规范化后的 `Import` 阶段
    （`rag.Options.CommunityDetector`），在 `ReplaceSource`
    重新摄入时重新检测
- 社区摘要（Phase 24）：
  - `graph.CommunityReport` 类型、`graph.CommunitySummarizer` 接缝，以及
    在 `generate.Model` 之上的 `graph.LLMCommunitySummarizer`
  - `graph.CommunityContentHash` —— 一个用作报告缓存键的确定性社区成员
    哈希
  - 惰性报告生成（LazyGraphRAG 模型）：报告在
    查询时生成并缓存，通过 `CommunityStore` 持久化
    （`PutCommunityReport` / `CommunityReport`；`postgres`
    `_community_reports` 表）
- 全局检索（Phase 24）：
  - `rag.System.AskGlobal` —— 一条在社区报告之上的 map-reduce 全局检索答案路径
    （社区选择、惰性报告、每社区
    map、按分数排序的 reduce）；一条与 `Ask` 分离的路径 —— 无 retrieve、
    rerank 或 pack
  - `rag.System.PrewarmCommunityReports` —— 选择开启的及早报告生成
  - `Diagnostics.Global` 全局检索归因；`GraphTrace.CommunityIDs`
    局部检索社区归因
- 模糊实体消解（Phase 25）：
  - `graph.EntityResolver` 接缝、`graph.NoopEntityResolver`（默认），
    以及 `graph.EmbeddingEntityResolver` —— 对
    近似重复实体的嵌入相似度合并，仅同类型，作为 `Canonicalize`
    之前的一个选择开启前置过程运行
- 评估（Phase 25）：
  - `eval.GlobalEvaluator` —— 在
    RAG-Triad / `LLMJudge` 路径之上的全局检索评估测试套件（依据性、答案相关性）
  - `docs/graphrag.md` 为 Tier-3 更新

### Notes

- `postgres` 的 `_communities` / `_community_reports` 路径由环境门控；
  与 v0.5 的 `tsvector` 和 v0.7 的图路径一样，它们在 CI 中
  尚未针对一个实时数据库演练。
- `EmbeddingEntityResolver` 有记录的假阳性风险；它发货时
  保守（高阈值、仅同类型）且选择开启。
- 推迟到 v0.9：DRIFT 检索、增量社区维护，以及
  路径排序 / 子图作为证据。

## [v0.4.0] - 2026-05-19

minor 发布，闭合 v0.7 GraphRAG 里程碑（Phases 20-22）。Tier-1
轻量级 GraphRAG：一个从被摄入的文档中抽取并通过遍历检索的
知识图谱，作为第四个信号与稠密、
词法和结构检索融合。增量且选择开启 —— 默认行为
不变。无新依赖、无图数据库：`postgres`
子包仍然是唯一的非标准库孤岛。

### Added

- 知识图谱构建（Phase 20）：
  - 新 `graph` 包 —— `Entity` / `Relation` / `Graph` 和一个
    `EntityExtractor` 接缝
  - 在 `generate.Model` 之上的 `graph.LLMEntityExtractor` —— 管道分隔的
    抽取提示词，对畸形模型输出宽容解析
  - `graph.DictionaryEntityExtractor` —— 一个确定性零 LLM 抽取器
    （gazetteer 术语 + 共现关系）
  - `graph.Canonicalize` —— 带源文本块溯源和关系端点解析的精确匹配 `(name, type)` 实体合并
  - 图抽取接线为一个切分后的 `Import` 阶段，在
    `ingest.ImportResult.Graph` 上浮现
- 图存储（Phase 21）：
  - `store.GraphStore` 可选能力接口（镜像
    `store.LexicalSearcher`）—— `UpsertGraph`、`RemoveGraphBySource`、
    `Neighborhood`、`FindEntities`
  - 一个纯标准库的内存邻接实现和一个 `postgres`
    实现，在 `entities` / `relations` 表之上带
    递归 CTE 遍历 —— 无图数据库
  - 硬性有界遍历：深度上限 2 加一个每跳扇出上限，
    在两个实现中都强制执行
  - 在 `ReplaceSource` 重新摄入时的增量调和 ——
    基于溯源的移除、并集合并和垃圾回收
  - `storetest.RunGraphConformance` 共享一致性套件
- 图遍历检索（Phase 22）：
  - `retrieve.EntityLinker` 接缝和 `LexicalEntityLinker`
  - `retrieve.GraphRetriever` —— 查询实体链接、有界邻域
    扩展和邻近度衰减打分
  - 图作为第四个 RRF 信号融合进 `HybridRetriever`：一个 `Graph`
    字段、`FusionAttribution.GraphRank`、一个 `EnableGraph` 开关，以及
    `retrieve.Trace` / `rag.Diagnostics.GraphTrace` 中的图归因
  - `eval.RunGraphAB` —— 在评估测试套件之上的一个图开/关 A/B
  - 一个确定性 GraphRAG 实例和 `docs/graphrag.md`

### Notes

- `postgres` 图路径由环境门控；与 v0.5 的 `tsvector` 路径一样它在 CI 中
  尚未针对一个实时数据库演练。
- 推迟到 v0.8：MS-GraphRAG 社区检测 / 摘要、
  全局/DRIFT 检索，以及模糊/嵌入实体消解。

## [v0.3.0] - 2026-05-18

minor 发布，闭合 v0.6 生产级检索里程碑
（Phases 14-19）。无新依赖 —— 完全是标准库加上
现有接缝；`postgres` 子包仍然是唯一的非标准库
孤岛。

### Added

- BM25 词法检索（Phase 14）：
  - 内存版词法路径中的 Okapi BM25 排序，替换
    token 重叠打分；可配置的 `retrieve.BM25Params`
  - 可选的 `store.LexicalSearcher` 能力接口，由
    `postgres` 存储通过 `tsvector`/`ts_rank_cd` 路径实现
  - 可配置的 RRF 融合常数加上检索链路中的每信号 `FusionAttribution`
- 基于模型的重排（Phase 15）：
  - `rerank.ScoringModel` 接缝、`ModelReranker` 和 `HTTPScoringModel`
    （一个 `net/http` 重排 API 客户端）
  - 重排可解释性：`rerank.RerankScore` / `Trace.Scores` 通过
    `rag.Diagnostics.RerankScores` 浮现
- 生成侧评估 —— RAG Triad（Phase 16）：
  - `eval.Judge` 接缝和 `LLMJudge`（LLM-as-judge 用于依据性和
    答案相关性）
  - `eval.TriadEvaluator` 把检索 + 生成指标组装进一个
    `TriadResult`，带一个 JSONL 报告和一个 RAG-Triad CI 门禁
- 成本和延迟可观测性（Phase 17）：
  - `obs` 包 —— `Metrics` 带每阶段时长、embed/generate
    调用计数和 token 用量，记录在 `rag.Diagnostics`、
    `retrieve.Trace`、`ingest.ImportResult` 和 `rag.ImportTrace` 上
  - `generate.Response` 上的 `generate.Usage` token 计量字段
- 内容安全（Phase 18）：
  - `guard` 包 —— `PIIRedactor` 在切块之前从被摄入的内容中脱敏 PII，
    带一个可配置的实体规则集
  - `guard.PatternScanner` 提示词注入过滤器，带一个 `SanitizeMode`
    （neutralize/drop），在提示词组装之前应用于检索到的文本块
- agentic 检索（Phase 19）：
  - `retrieve.MultiHopRetriever` 把一个复合查询分解为
    子查询并合并子检索（`QueryDecomposer` 接缝）
  - `agentic` 包 —— `CorrectiveAsker` 自纠正检索循环，
    它检测到低依据性并在一个有界上限下用重新表述的查询
    重试

### Changed

- 词法检索现在使用 Okapi BM25 而非 token 重叠打分
- `generate.Response` 获得一个增量的 `Usage` 字段
- `rag.Diagnostics`、`retrieve.Trace`、`ingest.ImportResult` 和
  `rag.ImportTrace` 携带额外的可观测性和安全字段
  （`Metrics`、`Redactions`、`InjectionFindings`、`Hops`）—— 全部增量

## [v0.2.0] - 2026-05-15

minor 发布，闭合 v0.5 RAG 生产化里程碑
（Phases 11-13）。第一个带非标准库依赖的发布 —— 限定
在 `postgres` 子包内。

### Added

- 结构感知检索策略（Phase 11）：
  - 子树 route-path 约束和自动章节路由选择
  - 带置信度/证据元数据的多候选自动路由规划
  - 可执行的 route policy：置信度阈值 + top-N 扇出
  - route-policy 理据和被选路由链路标记
  - 置信度差距自适应扇出 —— 在强 top-1 时收敛，在前两条路由接近时
    扇出
  - 每路由的 `SearchTrajectory` 输出，把命中和章节归因到
    每条被执行的路由
  - 可插拔的 `retrieve.SectionPlanner` 接口，以
    `GapAwareSectionPlanner` 为默认
- `postgres` 包 —— `store.Store` 的 PostgreSQL + pgvector 实现，
  在本 module 的第一批非标准库依赖
  （`pgx/v5`、`pgvector-go`）之后
- `store/storetest.RunConformance` —— 每个 `store.Store` 实现都针对其运行的
  共享 12 子测试一致性套件
- `rag.Observer{OnImport, OnRetrieve, OnAsk}` 钩子面外加
  `rag.ImportTrace`，用于不触及内部实现的外部追踪
- `eval` 包 —— 带 precision@k / recall@k / MRR / grounding@k 指标
  和一个 JSONL 加载器的检索/依据性评估框架
- `feedback` 包 —— 并发安全的写入器，把被标记的 Ask 捕获为
  JSONL 评估示例（在线到离线的回归反馈环）
- `contract` 包 —— 编译期门禁，锚定当时核心 `llm-agent/rag` 门面所消费的
  跨仓面。历史说明：
  该门面此后已从当前核心树中移除。

### Fixed

- `adapter/llmagent` rag 工具：当调用方省略时，`add_text` 现在
  每次调用生成一个唯一的基础文档 ID，防止
  跨命名空间的静默文本块 ID 冲突

## [v0.1.4] - 2026-05-14

### Added

- 通过 `SearchOptions` 的检索层查询扩展编排：
  - `EnableMQE`
  - `EnableHyDE`
  - `MQECount`
- 用于策略层 MQE/HyDE 查询重写的 `retrieve.LLMExpansionPreprocessor`
- 用于在任何基础检索器之上做多查询合并/去重的 `retrieve.VariantRetriever`
- 重排和上下文打包接缝：
  - `rerank.Reranker`
  - `pack.Packer`
- 默认启发式重排和贪心 token 预算感知的上下文打包

### Changed

- 默认 `rag.System` 检索现在路由经过策略感知的预处理器
  加上变体合并检索，而非要求适配器侧的查询循环
- 可选的 `adapter/llmagent` search/ask 现在把 MQE/HyDE 处理委托给
  独立的检索层
- 默认 `rag.Ask(...)` 现在支持重排加上带可追踪文本块选择的
  提示词证据打包

### Fixed

- 移除适配器和核心检索路径之间重复的 MQE/HyDE 编排逻辑

## [v0.1.3] - 2026-05-14

Phase 9 来源感知摄入打基础的 patch 发布。

### Added

- `ingest.Document` 上增量的来源谱系字段：
  - `SourceID`
  - `Version`
  - `Checksum`
  - `EmbeddingVersion`
- 自动谱系元数据传播进文本块和已存储文本块元数据
- 带章节感知元数据的 `MarkdownSplitter`：
  - `heading`
  - `heading_level`
  - `section_path`
- 用于按来源替换摄入行为的 `ImportOptions.ReplaceSource`
- `Store.RemoveByFilter(...)` 外加默认 `InMemoryStore` 支持

### Changed

- 当启用 `ReplaceSource` 时，独立导入现在能在 upsert 新内容之前
  移除同一 `source_id` 的现有文本块

## [v0.1.2] - 2026-05-14

Phase 8 RAG 契约加固的 patch 发布。

### Added

- 默认 `InMemoryStore` 中真正的元数据过滤
- 独立检索查询中显式的 `SecurityFilters` 接线
- 机器可读的答案引用、诊断信息和检索链路字段

### Changed

- 独立检索现在区分正常的调用方过滤与强制的
  安全裁剪输入

## [v0.1.1] - 2026-05-14

CI 稳定性的 patch 发布。

### Fixed

- 用一个 module 边界检查替换 `go mod tidy` 漂移强制执行，使
  `adapter/llmagent` 带构建标签的导入不会强制对
  `github.com/costa92/llm-agent` 的硬依赖
- 保持独立核心包可发布而不修改 `go.mod`

## [v0.1.0] - 2026-05-14

初始的独立 RAG SDK 发布。

### Added

- 独立的 Go module：`github.com/costa92/llm-agent-rag`
- 通过 `ingest.Source`、`Import` 和 `ImportFrom` 的抽象导入
- 确定性的默认 `ingest.CharSplitter`
- 默认 `embed.HashEmbedder`
- 默认 `store.InMemoryStore`
- 通过 `generate.Model` 的抽象生成接缝
- 通过 `prompt.Template` 的提示词自定义
- 用于导入、检索、ask、remove 和 stats 的 `rag.System` 编排
- `advanced` 包，用于：
  - 多查询扩展（`MQE`）
  - HyDE 式的假设答案生成
- 在构建标签 `llmagent` 之后的可选 `adapter/llmagent` 桥接

### Notes

- 核心 module 刻意可发布而不对
  `github.com/costa92/llm-agent` 硬依赖。
- `adapter/llmagent` 是一个开发桥接，在隔离测试时需要一个临时本地的
  `require` / `replace`。
- 本发布意在作为一个可复用的 `v0.1` 基线，而非稳定性
  保证。
