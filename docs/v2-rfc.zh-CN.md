[English](./v2-rfc.md) | [简体中文](./v2-rfc.zh-CN.md)

# llm-agent-rag v2.0 RFC

**日期：** 2026-05-24
**状态：** DRAFT —— 尚未接受
**作者：** 由 Plan agent 在 v1.x 复盘期间生成
**Module：** `github.com/costa92/llm-agent-rag`
**分支目标：** `v2/main`（在 v1.9.x 浸泡后切出）

---

## 1. 执行摘要

`llm-agent-rag` 的 v1.x 线于 2026-05-21 随 v1.0.0 冻结，并在四天内通过 v1.8.0 累积了十三个 tag。每个 minor 和 patch 都是增量的 —— `api/v1.snapshot.txt` 从冻结基线增长到 **1,177 行**，无一处重命名或删除。这一承诺的代价在公共面上清晰可见：`ReflectionOptions` 现在携带 20 个字段，`Observer` 携带 6 个回调，`rag` 包导出 12 个不同的错误符号，且 `wrapBudgetError` 在 `rag/ask.go` 中出现在 6 个返回点 —— 当 v1.9.0（C-BudgetExpand）把预算强制执行扩展到 `AskGlobal` 和 `AskDrift` 时，这个数量将大致翻倍。

v1.9.0 将作为最后一个增量的 v1.x minor 发布。在两到三个浸泡 patch 发布之后，`v2/main` 切出，v2.0 开始。

v2.0 将保留每一项现有能力，并重塑 v1.x 已经长大超出的五个面：ReflectionOptions 变为由函数式选项和具名预设驱动的 `ReflectionConfig`；`Ask` 返回一个类型化的 `AskResult`，其 `State` 锚定结果并替换 `wrapBudgetError` 模式；标量旋钮 `MaxTotalTokens` 变为带分阶段上限和一个策略枚举的 `BudgetSpec`；一个 `generate.StreamModel` 能力接口在不破坏 `Model` 的前提下解锁流式生成；错误分类法被重新归组为一个有文档记载的三层层级，并把 v1.x 名称保留为弃用别名。

v2.0 不是一个一刀切的切换日。v2.0 保留的每个 v1.x 导出符号都保留其名称。v2.0 重塑的符号至少在一个 v2.x minor 期间以 `// Deprecated:` 别名形式发布。v1.9.x 在 v2.0 GA 后继续接收 bug 修复六个月。流式 Provider 实现、Tree-of-Segments 和 Strategy 预设明确不在 v2.0 范围内，并被跟踪到 v2.1+。

---

## 2. v1.x 状态评估

这种累积是可度量的。

| 公共面 | v1.0.0 冻结 | v1.8.0 |
|---|---|---|
| `api/v1.snapshot.txt` 总行数 | （基线） | **1,177** |
| `rag.ReflectionOptions` 字段 | 8 | **20** |
| `rag.Observer` 回调字段 | 3（`OnImport`、`OnRetrieve`、`OnAsk`） | **6** |
| `rag` 包错误符号 | 4 个哨兵 | **6 个哨兵 + 1 个类型化结构体** = 7 |
| `rag/ask.go` 中的 `wrapBudgetError` 返回点 | 0 | **6**（v1.7.0+） |
| 导出的 `Stage*` 常量 | 0 | **10**（`StageAsk`、`StageReflectionDecision`、`StageGrader`、`StagePlanner`、`StageJudgeEval`、`StageAskGlobalMap`、`StageAskGlobalReduce`、`StageAskDriftPrimer`、`StageAskDriftLocal`、`StageAskDriftSynth`） |
| 缓存面（`rag` + `eval`） | 无 | **2 个接口、2 个存储、6 个辅助工具**（v1.8.0） |

`ReflectionOptions` 累积序列：v1.1.0 添加 6 个字段（文本块打分 + 选择 + 自适应检索），v1.2.0 添加 4 个字段（主动检索），v1.2.1 添加 2 个字段（并行后续查询）。每个 minor 都正确地是增量的；累积的公共面如今对使用位置式字面量的调用方很不友好（`go vet` 已经建议使用具名复合字面量；`Compatibility note: this exported struct may grow additively over time, so keyed composite literals are recommended.` 这条文档注释现在出现在 `rag/options.go` 和 `rag/system.go` 中的七个结构体之前）。

`rag/ask.go` 中的 `wrapBudgetError` 模式是最易读的累积：今天有六个返回点，每一个都是一个手工调用点，必须把 `askStart`、`counter`、`stageUsage`、`reflection.Mode`、`rounds` 和 `decisionModelCalls` 一路接线传过去。v1.9.0 把同样的接线添加到 `AskGlobal`（map 和 reduce 段）和 `AskDrift`（primer、local、synth 段）—— 在 v2.0 切出之前把点数大致翻倍到十二个。

自冻结以来，Observer 增长了三个新回调：`OnPlanFollowups`（v1.2.1）、`OnFollowupRetrieve`（v1.2.1）、`OnGenerateUsage`（v1.5.0）。三者都正确地是增量的。在 v2.x 中添加第七个回调将以一个类型化接口接缝而非第七个字段的形式完成。

`generate.Model` 是单方法的：`Generate(ctx, req) (Response, error)`。流式需要一个该接口无法表达的返回类型。v1.x 无法在不破坏每个现有 provider 的前提下添加第二个方法；v2.0 的 `generate.StreamModel` 兄弟能力接口是唯一尊重 v1.x 调用方的路径。

---

## 3. v2.0 设计支柱

### 支柱 1 —— 带函数式选项 + 预设的 `ReflectionConfig`

**动机。** `ReflectionOptions` 是一个 20 字段的结构体，其中大多数字段有跨字段有效性约束（`AdaptiveRetrieval` 要求 `EnableChunkGrading`；`MaxFollowupConcurrency` 仅在配合 `ParallelFollowups` 时有意义；`GraderRelevanceWeight` 仅在 `SelectionModeBestByScore` 下被查询）。今天这些被记录在字段 godoc 中，并在 `normalizeReflectionOptions` 中被静默强制执行。有了 20 个字段，调用方无法通过阅读一个复合字面量区分良构的形态和畸形的形态。

**提议的 API。** `ReflectionConfig` 是一个由函数式选项构造的不透明结构体：

```go
// v2.0
type ReflectionConfig struct { /* unexported fields */ }

type ReflectionOption func(*reflectionConfigBuilder)

func NewReflectionConfig(opts ...ReflectionOption) ReflectionConfig

// Knobs (1:1 with v1.x fields, but each is now an option):
func WithMode(m ReflectionMode) ReflectionOption
func WithMaxRounds(n int) ReflectionOption
func WithMinHits(n int) ReflectionOption
func WithMinScore(s float64) ReflectionOption
func WithChunkGrading(weights GraderWeights) ReflectionOption
func WithSelectionMode(m SelectionMode) ReflectionOption
func WithAdaptiveRetrieval(threshold float64) ReflectionOption // implies grading
func WithActiveRetrieval(opts ActiveRetrievalOptions) ReflectionOption
func WithParallelFollowups(maxConcurrency int) ReflectionOption // implies active
// ... etc

// Named presets — high-traffic shapes the v1.x docs already endorse:
func PresetSelfRAG() ReflectionConfig      // chunk grading + adaptive retrieval + best-by-score
func PresetActiveRetrieval() ReflectionConfig // active retrieval + parallel followups
func PresetBudgeted(budget BudgetSpec) ReflectionConfig // rule mode + budget
```

约束强制执行移入 builder：`WithAdaptiveRetrieval` 记录启用打分的意图；config 终定时的一个内部校验过程返回一个类型化的 `ConfigError`，一次性列出每一处跨字段违规，替换 v1.x 中的静默归一化。

**迁移。**

```go
// v1.x
opts := rag.AskOptions{
    Reflection: &rag.ReflectionOptions{
        Mode:                          rag.ReflectionModeHybrid,
        MaxRounds:                     3,
        EnableChunkGrading:            true,
        SelectionMode:                 rag.SelectionModeBestByScore,
        GraderRelevanceWeight:         0.6,
        GraderSupportWeight:           0.4,
        EnableActiveRetrieval:         true,
        MaxFollowupQueries:            3,
        ActiveRetrievalRelevanceFloor: 0.4,
    },
}

// v2.0
opts := rag.AskOptions{
    Reflection: rag.NewReflectionConfig(
        rag.WithMode(rag.ReflectionModeHybrid),
        rag.WithMaxRounds(3),
        rag.WithChunkGrading(rag.GraderWeights{Relevance: 0.6, Support: 0.4}),
        rag.WithSelectionMode(rag.SelectionModeBestByScore),
        rag.WithActiveRetrieval(rag.ActiveRetrievalOptions{
            MaxQueries:      3,
            RelevanceFloor:  0.4,
        }),
    ),
}
```

`ReflectionOptions` 保留为一个 `// Deprecated:` 别名类型，其零值通过一个内部适配器转换为 `ReflectionConfig`；现有调用方在一个 v2.x minor 期间继续针对 `*ReflectionOptions` 编译。

### 支柱 2 —— 类型化的 `AskState` 状态机（替换 `wrapBudgetError`）

**动机。** `rag/ask.go` 今天有 6 个点调用 `wrapBudgetError(err, askStart, counter, stageUsage, reflection.Mode, rounds, decisionModelCalls)`。这个模式之所以存在，是因为 `Ask` 返回 `(Answer, error)` —— 部分完成在成功通道上无处安放，所以 v1.7.0 把部分链路挂在错误结构体上（`BudgetExceededError.PartialDiagnostics`）。v1.9.0 把该模式扩展到 `AskGlobal` 和 `AskDrift`，使点数翻倍。

**提议的 API。** v2.0 返回一个类型化结果，其 `State` 字段锚定结果：

```go
// v2.0
type AskState int

const (
    AskStateUnknown AskState = iota
    AskStateFinal              // Answer fully assembled, no abort.
    AskStatePartial            // Aborted before final round; Answer is best-effort partial.
    AskStateAborted            // Aborted by policy (budget, validation, etc.); Answer is zero value.
    AskStateCancelled          // ctx.Done(). Answer is zero value.
)

type AskResult struct {
    State       AskState
    Answer      Answer        // Zero value when State == AskStateAborted | AskStateCancelled.
    Diagnostics Diagnostics   // Always populated, even on Partial/Aborted.
    AbortReason error         // Non-nil when State != AskStateFinal. Sentinel-matchable.
}

func (s *System) Ask(ctx context.Context, question string, opts AskOptions) (*AskResult, error)
```

`error` 保留给真正的系统故障（nil store、nil model、被父级取消的 ctx）。预算中止、校验中止和部分轮次中止都通过 `AskResult.State` + `AbortReason` 流动。调用方停止对 `errors.As(err, &budgetErr)` 做模式匹配，转而在 `result.State` 上做 switch。

六个（很快是十二个）`wrapBudgetError` 点坍缩为一个单一的内部辅助函数，它填充 `AskResult.Diagnostics` 并设置 `State` + `AbortReason` —— 该模式从「把一个错误包装六次」移动到「计算一次结果」。

**迁移。**

```go
// v1.x
ans, err := sys.Ask(ctx, q, opts)
var be *rag.BudgetExceededError
if errors.As(err, &be) {
    // recover partial via be.PartialDiagnostics
    log.Printf("budget exceeded at %s; partial rounds=%d", be.Stage, len(be.PartialDiagnostics.Reflection.RoundDetails))
    return be.PartialDiagnostics, nil
}
if err != nil { return nil, err }

// v2.0
res, err := sys.Ask(ctx, q, opts)
if err != nil { return nil, err } // genuine system failure only
switch res.State {
case rag.AskStateFinal:
    return res.Answer, nil
case rag.AskStatePartial, rag.AskStateAborted:
    log.Printf("ask aborted (%v): %v", res.State, res.AbortReason)
    return res.Answer, nil // Answer is partial or zero-value
case rag.AskStateCancelled:
    return zero, ctx.Err()
}
```

`Ask`（v1.x 签名）以一个 `// Deprecated:` 垫片形式发布，它调用新的 `Ask`，并在一个 v2.x minor 期间把带预算原因的 `AskStateAborted` 转换回 v1.7.0 的 `*BudgetExceededError` 形式。

### 支柱 3 —— `BudgetSpec`（替换标量 `MaxTotalTokens`）

**动机。** `AskOptions.MaxTotalTokens int` 是一个带单一策略（超额时中止）的单一累积上限。v1.7.0 记录了两个缺口：预算强制执行仅限 Ask；唯一的策略是中止。v1.9.0 闭合第一个缺口。第二个缺口 —— 想要仅警告模式的运维者，或想要分阶段上限以便反思不能消耗规划器的 token —— 在 v1.x 内没有干净的增量公共面。

**提议的 API。**

```go
// v2.0
type BudgetSpec struct {
    // Total caps cumulative TotalTokens across all stages. Zero is unlimited.
    Total int
    // PerStage caps cumulative TotalTokens per named stage. Missing stage = inherit Total.
    // Keys match the rag.Stage* constants: "ask", "reflection_decision", "grader", etc.
    PerStage map[string]int
    // OnExceeded selects the policy when any (Total or PerStage) cap is exceeded.
    OnExceeded BudgetPolicy
}

type BudgetPolicy int

const (
    PolicyAbort    BudgetPolicy = iota // Default: terminate Ask, State=AskStateAborted.
    PolicyWarn                         // Fire Observer.OnBudgetWarning, continue.
    PolicyTruncate                     // Skip non-critical stages (grader, planner) once exceeded; continue answer leg.
)
```

`AskOptions.Budget BudgetSpec` 替换 `AskOptions.MaxTotalTokens int`。零值 `BudgetSpec` 是无限制 / 中止 —— 与 v1.7.0 的零值语义逐字节相同。

`PolicyTruncate` 是 v1.x 无法表达的新能力。它启用一个「优雅降级」模式，让一个长时间运行的 Ask 在反思中途耗尽其打分器和规划器预算，但仍然用它已经拥有的轮次产出一个最终答案。

**迁移。**

```go
// v1.x
opts := rag.AskOptions{ MaxTotalTokens: 1000 }

// v2.0
opts := rag.AskOptions{
    Budget: rag.BudgetSpec{
        Total:      1000,
        OnExceeded: rag.PolicyAbort,
    },
}

// v2.0 new capability — per-stage caps:
opts := rag.AskOptions{
    Budget: rag.BudgetSpec{
        Total: 1000,
        PerStage: map[string]int{
            rag.StageGrader:  200,
            rag.StagePlanner: 100,
        },
        OnExceeded: rag.PolicyTruncate,
    },
}
```

`AskOptions.MaxTotalTokens int` 在一个 v2.x minor 期间保留为一个 `// Deprecated:` 字段；当 `Budget.Total == 0` 且 `MaxTotalTokens != 0` 时，后者被一个内部适配器提升为 `BudgetSpec{Total: MaxTotalTokens, OnExceeded: PolicyAbort}`。

### 支柱 4 —— 流式生成接缝（`generate.StreamModel`）

**动机。** `generate.Model` 是单方法的 `Generate(ctx, req) (Response, error)`。流式生成要么需要增量产出 token（channel/迭代器），要么需要接受一个回调。两种形态都不契合 `(Response, error)` 签名，且 v1.x 的仅增量承诺禁止向 `Model` 添加方法 —— 每个实现的 provider 都会破坏。

FLARE 式的前瞻生成和 Tree-of-Segments 检索（按设计笔记两者都是 v2.1+ 候选）都需要在生成期间访问部分输出。没有流式接缝，这扇门就保持关闭。

**提议的 API。** 兄弟能力接口 —— 类型断言模式，形态上与 v1.0.3 的 `embed.BatchEmbedder` 和 v1.0.6 的 `store.LexicalSearcher` 相同：

```go
// v2.0 — in generate/model.go
type Token struct {
    Text         string
    LogProb      float64    // 0 when provider does not report.
    UsageDelta   Usage      // Per-token usage delta (provider-reported, may be zero).
    Done         bool       // True on the terminal sentinel token.
}

// StreamModel is an optional sibling-capability over Model. A Model
// implementation that also satisfies StreamModel can be consumed by
// streaming-aware callers; plain Model implementations are unchanged.
//
// The receive channel closes naturally when the stream ends. On error,
// the error is delivered on a separate channel returned alongside;
// implementations must close both channels before returning.
type StreamModel interface {
    Model // every StreamModel is also a Model (non-streaming fallback)
    GenerateStream(ctx context.Context, req Request) (<-chan Token, <-chan error)
}
```

`Model` 超级接口被逐字保留。每个 v1.x provider 继续编译。新 provider 通过添加第二个方法选择开启流式。`rag.System` 答案路径使用一个类型断言检查：

```go
// in rag/ask.go (v2.0)
if sm, ok := s.model.(generate.StreamModel); ok && s.streamingEnabled {
    // streaming code path; falls back to sm.Generate when streaming inappropriate
}
// existing non-streaming path otherwise — unchanged.
```

**迁移。** 现有调用方无需迁移 —— `generate.Model` 未改变。新的流式感知代码通过实现或类型断言 `StreamModel` 来选择开启。

v2.0 **只发布接口**。流式 Provider 实现（OpenAI、Anthropic、llmagent）在后续的 Provider 发布中发货 —— 见第 6 节（范围之外）。

### 支柱 5 —— 统一错误分类法

**动机。** `rag` 包今天导出 6 个哨兵错误和 1 个类型化结构体，按特性累积而非按类别组织：

- `ErrEmptyQuery`（v0.1.x —— 校验）
- `ErrModelRequired`（v0.1.x —— 接线）
- `ErrImporterRequired`（v0.1.x —— 接线）
- `ErrRetrieverRequired`（v0.1.x —— 接线）
- `ErrSourceRequired`（v0.1.x —— 校验）
- `ErrCommunitySummarizerRequired`（v0.5.0 —— 接线）
- `ErrTokenBudgetExceeded` + `BudgetExceededError`（v1.7.0 —— 策略）

没有有文档记载的类型层级，也没有调用方可以 switch 的共享接口。`eval` 和 `obs` 包各有自己的。给每个新错误分类变成临时性的。

**提议的 API。** 三个有文档记载的层级，把 v1.x 名称保留为别名：

```go
// v2.0 — Tier 1: sentinel errors (compare with errors.Is)
var (
    ErrInvalidArgument = errors.New("rag: invalid argument")        // umbrella
    ErrConfigRequired  = errors.New("rag: required configuration missing") // umbrella
    ErrAborted         = errors.New("rag: operation aborted by policy")    // umbrella

    // Concrete sentinels (wrap an umbrella sentinel via Unwrap):
    ErrEmptyQuery                  // wraps ErrInvalidArgument
    ErrSourceRequired              // wraps ErrInvalidArgument
    ErrModelRequired               // wraps ErrConfigRequired
    ErrImporterRequired            // wraps ErrConfigRequired
    ErrRetrieverRequired           // wraps ErrConfigRequired
    ErrCommunitySummarizerRequired // wraps ErrConfigRequired
    ErrTokenBudgetExceeded         // wraps ErrAborted
)

// Tier 2: typed wrappers (compare with errors.As, carry context)
type BudgetError struct {
    Stage  string
    Used   int
    Budget int
    Spec   BudgetSpec        // The full spec, not just the scalar.
    // PartialDiagnostics moves to AskResult.Diagnostics — see Pillar 2.
}
func (e *BudgetError) Error() string
func (e *BudgetError) Unwrap() error // returns ErrTokenBudgetExceeded

type ValidationError struct {
    Field   string
    Message string
}
func (e *ValidationError) Error() string
func (e *ValidationError) Unwrap() error // returns ErrInvalidArgument

type ConfigError struct {
    Violations []ConfigViolation // From ReflectionConfig builder (Pillar 1).
}
func (e *ConfigError) Error() string
func (e *ConfigError) Unwrap() error // returns ErrConfigRequired

// Tier 3: external-cause wrappers (network, model, store) — out of scope for v2.0.
```

调用方获得一个有文档记载的 switch：

```go
// v2.0
res, err := sys.Ask(ctx, q, opts)
switch {
case errors.Is(err, rag.ErrInvalidArgument): // user input
case errors.Is(err, rag.ErrConfigRequired):  // wiring
case errors.Is(err, rag.ErrAborted):         // policy
case err != nil:                              // unexpected
}
```

**迁移。** 每个 v1.x 错误名称被逐字保留。`BudgetExceededError` 被重命名为 `BudgetError`，并把 v1.x 名称保留为一个 `// Deprecated:` 类型别名。`PartialDiagnostics` 从错误移动到 `AskResult.Diagnostics` —— 当错误由垫片产出时，该别名提供一个从父级 `AskResult` 拉取的 `Compatibility() Diagnostics` 辅助函数。

---

## 4. 符号迁移清单

这是 v2.0 的契约 —— 每个 v1.x 导出符号及其 v2.0 处置。

| v1.x 符号 | v2.0 处置 | 备注 |
|---|---|---|
| `rag.ReflectionOptions`（结构体，20 字段） | 重命名 → `ReflectionConfig`；v1.x 名称保留为弃用类型别名 | 逐字段的函数式选项映射；一个 v2.x minor 的重叠期。 |
| `rag.AskOptions.MaxTotalTokens int` | 由 `AskOptions.Budget BudgetSpec` 替换；字段保留为弃用 | 一个 v2.x minor 的自动提升适配器。 |
| `rag.BudgetExceededError`（结构体） | 重命名 → `BudgetError`；v1.x 名称保留为弃用别名 | `PartialDiagnostics` 移动到 `AskResult`；`.Compatibility()` 辅助函数桥接。 |
| `rag.ErrTokenBudgetExceeded`（哨兵） | 逐字保留；现在包装 `ErrAborted` | 现有的 `errors.Is` 检查继续匹配。 |
| `rag.ErrEmptyQuery`、`ErrSourceRequired` | 逐字保留；现在包装 `ErrInvalidArgument` | |
| `rag.ErrModelRequired`、`ErrImporterRequired`、`ErrRetrieverRequired`、`ErrCommunitySummarizerRequired` | 逐字保留；现在包装 `ErrConfigRequired` | |
| `rag.Observer`（结构体，6 字段） | **保留为结构体，不转换为接口** | 决策：结构体跨 v2.x minor 比接口更可靠地保持增量；增加字段不会破坏实现者（因为没有实现者）。 |
| `rag.Observer.OnGenerateUsage`、`OnPlanFollowups`、`OnFollowupRetrieve` | 逐字保留 | |
| `rag.Observer.OnBudgetWarning` | **v2.0 新增** —— 在 `BudgetSpec.OnExceeded = PolicyWarn` 下触发 | 对 `Observer` 增量。 |
| `rag.System.Ask` 签名 `(Answer, error)` | 由 `(*AskResult, error)` 替换；v1.x 名称保留为弃用垫片 | 垫片把 `AskResult.State == AskStateAborted` 转换回 `BudgetExceededError`。 |
| `rag.Grader`（接口） | 逐字保留 | |
| `rag.QueryPlanner`（接口） | 逐字保留 | |
| `rag.NoopGrader`、`PromptGrader` | 逐字保留 | |
| `rag.NoopQueryPlanner`、`PromptQueryPlanner` | 逐字保留 | |
| `rag.ChunkScore`（结构体） | 逐字保留 | |
| `rag.SelectionMode` 枚举 | 逐字保留 | |
| `rag.ReflectionMode` 枚举 | 逐字保留 | |
| `rag.GraderCache`（接口）、`MemoryGraderCache`、`NewMemoryGraderCache`、`WrapGrader`、`NewCachingGrader`、`GraderCacheKey`、`GraderCacheModeRelevance`、`GraderCacheModeSupport`、`CacheStats` | 逐字保留 | 所有 v1.8.0 缓存面不动。 |
| `rag.Stage*` 常量（10 个） | 逐字保留 | |
| `rag.Diagnostics`（结构体，约 14 字段） | **v2.0 中逐字保留**；重塑推迟到 v2.x | 范围之外。 |
| `obs.Metrics`、`obs.TokenUsage`、`obs.StageTokenUsage`、`obs.StageUsageAccumulator` | 逐字保留 | |
| `obs.WithTokenBudget`、`obs.TokenBudgetFrom` | 逐字保留，但预算传播现在读取 `BudgetSpec` 而非标量 | 实现细节。 |
| `generate.Model` 接口 | **逐字保留 —— 明确不变** | 添加兄弟 `StreamModel`。 |
| `generate.StreamModel` 接口、`generate.Token` 结构体 | **v2.0 新增** | 增量接缝。 |
| `eval.JudgeCache`、`MemoryJudgeCache`、`WrapJudge` 等 | 逐字保留 | |
| `eval.BenchmarkResult`、`BenchmarkMetrics`、`Direction`、`MetricDelta`、`DriftReport`、`HistogramDelta`、`Markdown()` | 逐字保留 | |
| `eval.RetryPolicy`、`JitterMode`、`NewRetryAsker`、`NewRetryJudge` | 逐字保留 | |
| `eval.RetrievalEvaluator`、`RetrievalResult`、`GlobalEvaluator`、`DriftEvaluator`、`TriadEvaluator` | 逐字保留 | |
| `postgres.*` 子包 | 逐字保留 | |
| `graph.*` 子包 | 逐字保留 | |
| `store.*` 子包 | 逐字保留 | |
| `retrieve.*` 子包 | 逐字保留 | |
| `pack.*`、`rerank.*`、`prompt.*`、`embed.*`、`ingest.*`、`guard.*`、`agentic.*`、`advanced.*`、`feedback.*` | 逐字保留 | |
| `contract` 包 | 逐字保留；编译锚定针对 v2.0 面重新基准化 | |
| `adapter/llmagent`（带构建标签） | 逐字保留 | |

**v2.0 净破坏性变更数：4 处重塑**（`ReflectionOptions`、`AskOptions.MaxTotalTokens`、`BudgetExceededError`、`Ask` 返回类型）。每处重塑都在整整一个 v2.x minor 期间发布一个 `// Deprecated:` 桥接。

---

## 5. 时间线 + 迁移策略

```
2026-05-24    v1.8.0 (current — C-Cache + C-MarkdownExport)
2026-MM-DD    v1.9.0  (C-BudgetExpand — final additive minor)
2026-MM-DD    v1.9.1  (soak patch)
2026-MM-DD    v1.9.2  (soak patch, optional)
2026-MM-DD    v2/main branches from v1.9.x
2026-MM-DD    v2.0-RC1  (Pillars 1-5 wired; deprecation shims live)
2026-MM-DD    v2.0-RC2  (feedback-driven; timing flexible)
2026-MM-DD    v2.0 GA
              v1.9.x continues bug-fix-only for 6 months after v2.0 GA
2026-MM-DD    v2.1+  (streaming Providers, Tree-of-Segments, Strategy presets)
```

**对 v1.x 调用方的迁移承诺：**

1. v2.0 逐字保留的每个 v1.x 导出符号都保留其名称和签名。
2. v2.0 重塑的每个 v1.x 符号都至少在一个 v2.x minor 期间发布一个 `// Deprecated:` 桥接（即贯穿 v2.1）。
3. `go vet` 和 `deprecation` 分析器浮现每一处弃用点，使调用方能按自己的节奏逐步降级。
4. `api/v1.snapshot.txt` 测试在一个 v2.x minor 期间保持绿色 —— v1 快照继续断言 v1 命名的面能编译，即便底层类型是别名。
5. v1.9.x 在 v2.0 GA 后仅接收 bug 修复 6 个月。新特性落在 `v2/main`。

**对 SDK 作者：**

- `v2/main` 从 `main@<v1.9.x soak tag>` 切出；`v2/main` 上的第一个提交是 module 路径提升（`module github.com/costa92/llm-agent-rag/v2`）。
- `contract` 包的被锚定符号列表在 v2.0 GA 时针对 v2.0 面重新基准化；跨仓消费者按自己的节奏更新其 `require`。

---

## 6. v2.0 范围之外

v2.0 是公共面重塑发布。以下明确 **不是** v2.0 交付物：

- **流式 Provider 实现。** v2.0 发布 `generate.StreamModel` 接口和 `rag.System` 类型断言检查，但没有 Provider 实现。OpenAI、Anthropic 和 `llmagent` 适配器的流式支持在后续 provider 发布中按各自节奏落地。
- **Tree-of-Segments 检索。** 依赖流式生成来检测答案中途的信息缺口。v2.1+ 候选。
- **FLARE / 前瞻生成。** 同样的依赖。v2.1+ 候选。
- **Strategy 预设**（`SelfRAGStrategy`、`ActiveStrategy`、`BudgetedStrategy` 作为完整流水线，而非仅反思预设）。在 v1.5+ 规划中讨论过；完整实现是 v2.1+。v2.0 只发布 `PresetSelfRAG()` / `PresetActiveRetrieval()` / `PresetBudgeted()` 作为 `ReflectionConfig` builder。
- **`Diagnostics` 重塑。** 深度嵌套的 `Diagnostics` 结构体（Reflection/Global/Drift/GraphTrace）累积被承认，但重塑它会触及下游每个可观测性消费者。推迟到一个单独的 v2.x 考量。
- **`Observer` → 接口转换。** 已调研并为 v2.0 否决（见清单）。可能在 v2.x 重新审视。
- **错误层级 3**（网络/模型/存储故障的外因包装器）。v2.0 只发布层级 1 + 2。
- **`eval` 包重塑。** eval 包自 v1.3.0 以来增长了 21 个源文件，但其公共面组织良好；v2.0 不动它。
- **`postgres` 驱动修订。** pgx/pgvector 依赖线在 v2.0 中不变。

---

## 7. 待解问题

这些在 v2.0 GA 之前解决。每一个都是面向用户的 discuss-phase 候选。

1. **`AskResult.State == AskStatePartial` 是否应该曾经带着一个可用的 `Answer` 被返回？**
   当前 v1.7.0 行为在预算中止时返回零值 `Answer`。`AskStatePartial` 打开了「你拿到了 3 轮反思中的 2 轮；这是迄今为止最好的一轮」这扇门。权衡：今天依赖 `Answer.Text == ""` 作为中止信号的调用方需要一个新检查。提议：是，但用一个新的 `AskOptions.ReturnPartialOnAbort bool` 门控（默认 false → 与 v1.7.0 中止语义逐字节相同）。

2. **`BudgetSpec.PerStage` 的键是否针对已知的 `Stage*` 常量校验，还是接受任意键？**
   任意键灵活（自定义 Grader 可能发出自定义阶段标签）但拼写错误不被检测。提议：在 builder 中针对 10 个已知常量校验，返回一个 `ConfigError`；调用方可通过 `BudgetSpec{PerStageStrict: false}` 选择关闭。

3. **`generate.StreamModel.GenerateStream` 返回签名：`<-chan Token, <-chan error)` vs 单个 `<-chan StreamEvent`，其中 `StreamEvent` 是一个 sum 类型？**
   两个 channel 匹配 Go 惯例（errgroup 风格），并让 provider 在 token 关闭后发出终止错误信号；单个 channel 更简单，但错误语义不那么明显。提议：两个 channel；如果迭代器模式（Go 1.23+ `range func`）变得惯用，则重新审视。

4. **`ReflectionConfig` builder 错误应该中止 `NewReflectionConfig`（panic 或零 config）还是浮现为 `(ReflectionConfig, error)`？**
   函数式选项库在这一点上有分歧。提议：`NewReflectionConfig(...) ReflectionConfig` 永不出错；终定时校验在 `Ask` 内首次使用时惰性运行，通过正常的错误路径返回一个 `*ConfigError`。避免构造时 panic；代价是坏 config 被更晚检测到。

5. **`v2.0-RC1` 内容范围：所有五个支柱都落在 RC1，还是它们跨 RC1/RC2/RC3 逐步推进？**
   支持一次性的论点：弃用垫片需要一起稳定下来，以便调用方能测试完整迁移。支持逐步推进的论点：更小的 RC 更易于评审和回退。提议：支柱 1、2、5 在 RC1；支柱 3、4 在 RC2；完整集成浸泡在 RC3。

---

## 8. 参考

- **复盘：** [`docs/retro-2026-05-23-rag-v1.0.5-to-v1.5.0.zh-CN.md`](../../docs/retro-2026-05-23-rag-v1.0.5-to-v1.5.0.zh-CN.md)（伞形仓库）—— 较早的内部复盘，涵盖 v1.0.5 → v1.5.0 区间；为支柱 1 和 5 提供信息。
- **CHANGELOG 条目：**
  - v1.0.0（2026-05-21）—— API 冻结和仅增量承诺。
  - v1.1.0（2026-05-23）—— Self-RAG 文本块打分 + `SelectionMode` + `AdaptiveRetrieval`；第一次 `ReflectionOptions` 扩展（6 个新字段）。支柱 1 的起源。
  - v1.2.0 / v1.2.1（2026-05-24）—— 主动检索 + 并行后续查询；`ReflectionOptions` 达到 16 个字段。强化支柱 1。
  - v1.5.0（2026-05-24）—— `Observer.OnGenerateUsage`；第一次 observer 字段累积。为清单中保留 `Observer` 为结构体的决策提供信息。
  - v1.5.1（2026-05-24）—— 导出 10 个 `Stage*` 常量。支柱 3 `BudgetSpec.PerStage` 键的基础。
  - v1.7.0（2026-05-24）—— `MaxTotalTokens` + `BudgetExceededError` + `wrapBudgetError`。支柱 2 和 3 的起源。
  - v1.8.0（2026-05-24）—— Grader/Judge 缓存。v2.0 不动（支柱 5 清单）。
- **Plan agent 设计历史：**
  - v1.5+ minor 提议（CostBudget 系列）首次标记了导致支柱 3 的标量旋钮局限。
  - v1.6+ minor 提议（Strategy 预设）首次标记了导致支柱 1 的 21 字段 `ReflectionOptions` 问题。
  - v1.7+ minor 提议（Streaming）首次标记了导致支柱 4 的 `generate.Model` 单方法局限。
