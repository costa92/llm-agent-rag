# Self-RAG 反思实施计划

> **致 agentic worker：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实施本计划。各步骤使用复选框（`- [ ]`）语法以便跟踪。

**目标：** 为 `rag.System.Ask` 添加推理时的 Self-RAG 反思，带参数可选的 `rule`、`model` 和 `hybrid` 模式，同时保留现有的 `Ask` 入口点和最终答案语义。

**架构：** 把当前的单趟 `Ask` 流水线重构为一个可复用的私有单轮执行器，外加 `rag/ask.go` 中一个有界的反思循环。用反思配置和轮级诊断增量地扩展导出的 `rag` 类型，保持 `Observer.OnAsk` 为单个顶层回调，并让 `Observer.OnRetrieve` 每个内部检索轮次触发一次。

**技术栈：** Go、标准库 testing、现有的 `rag` / `retrieve` / `generate` / `obs` 包、`internal/apisnapshot` 中已提交的 API 快照门禁。

---

## 文件结构

- 修改：`rag/options.go`
  - 添加导出的反思配置类型并把它们挂入 `AskOptions`。
- 修改：`rag/system.go`
  - 向 `Diagnostics` 和 `Trace` 添加导出的反思诊断/链路结构。
- 修改：`rag/ask.go`
  - 把当前的单轮 `Ask` 主体抽取进一个私有辅助函数。
  - 添加有界的反思循环、决策辅助函数和指标聚合。
- 创建：`rag/reflection.go`
  - 持有私有决策类型、策略辅助函数、重写提示词辅助函数和聚合辅助函数，使 `ask.go` 不会变得无边界。
- 修改：`rag/system_test.go`
  - 添加 `off`、`rule`、`model` 和 `hybrid` 的路径测试。
- 修改：`rag/observer_test.go`
  - 为多轮 `OnRetrieve` 行为和顶层 `OnAsk` 语义添加显式断言。
- 修改：`rag/instrument_test.go`
  - 断言多轮指标和 token 聚合。
- 修改：`README.md`
  - 记录反思用法和 `OnRetrieve` 多轮行为。
- 修改：`docs/production-deployment.md`
  - 记录 fail-open 行为、与 MQE/HyDE 的重写叠加，以及可观测性语义。
- 修改：`api/v1.snapshot.txt`
  - 刷新已提交的导出 API 快照。
- 仅供参考：`agentic/correct.go`、`agentic/correct_test.go`
  - 用作行为灵感，而非实现依赖。

### Task 1：添加导出的反思类型

**文件：**
- 修改：`rag/options.go`
- 修改：`rag/system.go`
- 测试：`rag/system_test.go`

- [ ] **Step 1：编写失败的 API 形态测试**

在 `rag/system_test.go` 中 `Ask` 选项测试附近添加一个新测试：

```go
func TestAskOptionsExposeReflectionConfig(t *testing.T) {
	opts := AskOptions{
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        2,
			MinHits:          1,
			MinScore:         0.4,
			MinUniqueDocs:    1,
			RequireCitations: true,
			AllowRewrite:     true,
			FailOpen:         true,
		},
	}
	if opts.Reflection == nil {
		t.Fatal("Reflection = nil, want config attached")
	}
	if opts.Reflection.Mode != ReflectionModeRule {
		t.Fatalf("Mode = %q, want %q", opts.Reflection.Mode, ReflectionModeRule)
	}
}
```

- [ ] **Step 2：运行测试以确认导出类型尚不存在**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run TestAskOptionsExposeReflectionConfig -count=1
```

预期：因 `ReflectionOptions` / `ReflectionModeRule` 未定义而 FAIL。

- [ ] **Step 3：添加导出的选项类型和增量的诊断类型**

用以下内容更新 `rag/options.go`：

```go
type ReflectionMode string

const (
	ReflectionModeOff    ReflectionMode = "off"
	ReflectionModeRule   ReflectionMode = "rule"
	ReflectionModeModel  ReflectionMode = "model"
	ReflectionModeHybrid ReflectionMode = "hybrid"
)

type ReflectionOptions struct {
	Mode             ReflectionMode
	MaxRounds        int
	MinHits          int
	MinScore         float64
	MinUniqueDocs    int
	RequireCitations bool
	AllowRewrite     bool
	FailOpen         bool
}
```

并用以下扩展 `AskOptions`：

```go
Reflection *ReflectionOptions
```

用增量的导出类型更新 `rag/system.go`：

```go
type ReflectionDiagnostics struct {
	Mode                 ReflectionMode
	Rounds               int
	AdoptedRound         int
	StopReason           string
	FailureFallback      bool
	FailureReason        string
	DecisionModelCalls   int
	RewriteModelCalls    int
	RoundDetails         []ReflectionRoundDiagnostics
}

type ReflectionRoundDiagnostics struct {
	Round            int
	InputQuery       string
	EffectiveQuery   string
	RewrittenQuery   string
	ReturnedChunkIDs []string
	PromptChunkIDs   []string
	UniqueDocCount   int
	TopScore         float64
	Decision         string
	DecisionMode     ReflectionMode
	DecisionReason   string
}

type ReflectionTrace struct {
	Mode         ReflectionMode
	AdoptedRound int
	StopReason   string
	Rounds       []ReflectionRoundTrace
}

type ReflectionRoundTrace struct {
	Round            int
	InputQuery       string
	EffectiveQuery   string
	RewrittenQuery   string
	ReturnedChunkIDs []string
	PromptChunkIDs   []string
	Decision         string
	DecisionReason   string
}
```

增量地附上它们：

```go
type Diagnostics struct {
	...
	Reflection ReflectionDiagnostics
}

type Trace struct {
	...
	Reflection ReflectionTrace
}
```

- [ ] **Step 4：运行聚焦测试和 API 快照门禁**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run TestAskOptionsExposeReflectionConfig -count=1
GOCACHE=/tmp/go-build go test ./internal/apisnapshot -run TestAPISnapshot -count=1
```

预期：第一个测试通过；API 快照测试失败，因为已提交的基线陈旧。

- [ ] **Step 5：提交导出面变更**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add rag/options.go rag/system.go rag/system_test.go
git commit -m "feat: add self-rag reflection api types"
```

### Task 2：抽取单轮 Ask 执行

**文件：**
- 修改：`rag/ask.go`
- 测试：`rag/system_test.go`

- [ ] **Step 1：编写失败的单轮回归测试**

把这个测试添加到 `rag/system_test.go`：

```go
func TestAskOffModeMatchesSingleRoundBehavior(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
		Reflection: &ReflectionOptions{
			Mode:      ReflectionModeOff,
			MaxRounds: 3,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Diagnostics.Reflection.Rounds != 0 {
		t.Fatalf("Reflection.Rounds = %d, want 0 for off mode", ans.Diagnostics.Reflection.Rounds)
	}
	if len(ans.Hits) != 1 {
		t.Fatalf("len(ans.Hits) = %d, want 1", len(ans.Hits))
	}
}
```

- [ ] **Step 2：运行测试以确认编排路径尚未实现**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run TestAskOffModeMatchesSingleRoundBehavior -count=1
```

预期：FAIL，因为反思诊断尚未被一致地填充。

- [ ] **Step 3：把 `Ask` 重构为一个可复用的单轮辅助函数**

在 `rag/ask.go` 中，把当前主体抽取进一个带私有结果形态的辅助函数，如：

```go
type askRoundResult struct {
	answer        Answer
	retrieveTrace retrievepolicy.Trace
	topScore      float64
	uniqueDocIDs  []string
}

func (s *System) askRound(ctx context.Context, originalQuestion, query string, opts AskOptions) (askRoundResult, error) {
	// Move the existing retrieve/rerank/pack/sanitize/render/generate path here.
	// The prompt should still render against originalQuestion.
	// Retrieval should execute against query.
}
```

然后让 `Ask` 这样做：

```go
func (s *System) Ask(ctx context.Context, question string, opts AskOptions) (Answer, error) {
	if reflectionDisabled(opts.Reflection) {
		res, err := s.askRound(obs.WithCounter(ctx, obs.NewCounter()), question, question, opts)
		if err != nil {
			return Answer{}, err
		}
		return res.answer, nil
	}
	// Reflection loop added in the next task.
}
```

使用辅助函数，使旧的无反思路径精确保留当前的 `Answer` 语义。

- [ ] **Step 4：运行聚焦的 `Ask` 测试**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run 'TestSystemImportRetrieveAsk|TestAskOffModeMatchesSingleRoundBehavior|TestAskCarriesTraceAndFilters' -count=1
```

预期：PASS。

- [ ] **Step 5：提交重构**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add rag/ask.go rag/system_test.go
git commit -m "refactor: extract single-round ask execution"
```

### Task 3：实现 Rule 模式反思循环

**文件：**
- 修改：`rag/ask.go`
- 创建：`rag/reflection.go`
- 测试：`rag/system_test.go`

- [ ] **Step 1：编写失败的 rule 模式测试**

添加这些测试：

```go
func TestAskRuleModeStopsAfterSatisfiedFirstRound(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        2,
			MinHits:          1,
			MinUniqueDocs:    1,
			RequireCitations: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Diagnostics.Reflection.Rounds != 1 {
		t.Fatalf("Reflection.Rounds = %d, want 1", ans.Diagnostics.Reflection.Rounds)
	}
	if ans.Diagnostics.Reflection.StopReason == "" {
		t.Fatal("StopReason empty, want explicit rule stop reason")
	}
}

func TestAskRuleModeStopsAtMaxRounds(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "generic travel text"},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        2,
			MinHits:          2,
			MinUniqueDocs:    2,
			RequireCitations: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Diagnostics.Reflection.Rounds != 2 {
		t.Fatalf("Reflection.Rounds = %d, want 2", ans.Diagnostics.Reflection.Rounds)
	}
	if ans.Diagnostics.Reflection.StopReason != "max_rounds" {
		t.Fatalf("StopReason = %q, want max_rounds", ans.Diagnostics.Reflection.StopReason)
	}
}
```

- [ ] **Step 2：运行 rule 模式测试以验证它们失败**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run 'TestAskRuleModeStopsAfterSatisfiedFirstRound|TestAskRuleModeStopsAtMaxRounds' -count=1
```

预期：FAIL，因为反思循环行为不存在。

- [ ] **Step 3：实现 rule 决策辅助函数和循环**

创建 `rag/reflection.go`，带如下私有辅助函数：

```go
type reflectionDecision struct {
	Action string
	Query  string
	Reason string
}

func reflectionDisabled(opts *ReflectionOptions) bool {
	return opts == nil || opts.Mode == "" || opts.Mode == ReflectionModeOff
}

func normalizeReflectionOptions(opts *ReflectionOptions) ReflectionOptions {
	out := ReflectionOptions{Mode: ReflectionModeOff, MaxRounds: 1, FailOpen: true}
	if opts != nil {
		out = *opts
	}
	if out.Mode == "" {
		out.Mode = ReflectionModeOff
	}
	if out.MaxRounds <= 0 {
		out.MaxRounds = 1
	}
	return out
}

func decideRule(next ReflectionOptions, round askRoundResult, prev *askRoundResult) reflectionDecision {
	// Evaluate MinHits / MinScore / MinUniqueDocs / RequireCitations.
	// Stop on unchanged evidence or satisfied thresholds.
	// Otherwise continue, keeping the same query for v1 when no rewrite is requested.
}
```

然后更新 `Ask` 以执行至多 `MaxRounds` 轮并填充：

```go
answer.Diagnostics.Reflection
answer.Trace.Reflection
```

每一轮都必须追加轮次明细，并保留被采纳轮次的最终答案语义。

- [ ] **Step 4：运行 rule 模式测试和核心 `Ask` 回归集**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run 'TestAskRuleModeStopsAfterSatisfiedFirstRound|TestAskRuleModeStopsAtMaxRounds|TestSystemImportRetrieveAsk|TestAskCarriesTraceAndFilters|TestAskReranksAndPacksContext' -count=1
```

预期：PASS。

- [ ] **Step 5：提交 rule 模式循环**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add rag/ask.go rag/reflection.go rag/system_test.go
git commit -m "feat: add rule-based self-rag reflection"
```

### Task 4：实现 Model 和 Hybrid 反思

**文件：**
- 修改：`rag/ask.go`
- 修改：`rag/reflection.go`
- 测试：`rag/system_test.go`

- [ ] **Step 1：编写失败的 model 和 hybrid 测试**

使用一个自定义模型桩添加脚本化模型测试：

```go
type sequenceModel struct {
	responses []string
	calls     int
}

func (m *sequenceModel) Generate(_ context.Context, _ generate.Request) (generate.Response, error) {
	if m.calls >= len(m.responses) {
		return generate.Response{}, nil
	}
	out := m.responses[m.calls]
	m.calls++
	return generate.Response{Text: out}, nil
}
```

然后添加测试：

```go
func TestAskModelModeRewritesAndContinues(t *testing.T) {
	// First answer weak, reflection says rewrite, second round completes.
}

func TestAskHybridModeSkipsReflectionModelWhenRulesAlreadyPass(t *testing.T) {
	// Reflection model call count should remain zero when first round is clearly sufficient.
}
```

第一个测试应断言：

- rounds == 2
- 第一轮决策是 `rewrite_and_continue`
- 第二轮被采纳
- 记录了重写后的查询

第二个测试应断言：

- rounds == 1
- 决策模式是 `hybrid`
- 除答案生成外无额外的反思模型调用

- [ ] **Step 2：运行测试以确认它们失败**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run 'TestAskModelModeRewritesAndContinues|TestAskHybridModeSkipsReflectionModelWhenRulesAlreadyPass' -count=1
```

预期：FAIL。

- [ ] **Step 3：实现内部模型驱动决策和重写流程**

在 `rag/reflection.go` 中添加私有辅助函数：

```go
func (s *System) reflectWithModel(ctx context.Context, originalQuestion string, round askRoundResult) (reflectionDecision, error) {
	// Prompt the existing generate.Model for a strict stop/continue/rewrite decision.
}

func (s *System) rewriteQuery(ctx context.Context, originalQuestion string, round askRoundResult) (string, error) {
	// Prompt the existing generate.Model for a rewritten retrieval query.
}

func (s *System) decideReflection(ctx context.Context, cfg ReflectionOptions, originalQuestion string, round askRoundResult, prev *askRoundResult) (reflectionDecision, error) {
	// Switch on rule/model/hybrid.
}
```

实现规则：

- 使用原始问题作为任务锚点
- 让 hybrid 在明确的规则成功时短路
- 如果重写返回相同的查询，带一个显式原因早停
- 如果较晚轮次的证据集未变，早停
- 不要跨轮改变 `SearchOptions`

- [ ] **Step 4：运行 model/hybrid 测试和更广的 `rag` 套件**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -count=1
```

预期：PASS。

- [ ] **Step 5：提交 model 和 hybrid 支持**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add rag/ask.go rag/reflection.go rag/system_test.go
git commit -m "feat: add model and hybrid self-rag reflection"
```

### Task 5：修复 Observer 和指标语义

**文件：**
- 修改：`rag/observer_test.go`
- 修改：`rag/instrument_test.go`
- 修改：`rag/ask.go`
- 修改：`rag/reflection.go`

- [ ] **Step 1：编写失败的 observer 和指标测试**

添加到 `rag/observer_test.go`：

```go
func TestAskReflectionTriggersRetrieveObserverPerRound(t *testing.T) {
	var retrieveCalls, askCalls int
	sys := New(Options{
		Model: fakeModel{},
		Observer: Observer{
			OnRetrieve: func(context.Context, retrieve.Trace) { retrieveCalls++ },
			OnAsk: func(context.Context, Trace) { askCalls++ },
		},
	})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "generic travel text"},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	_, err = sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        2,
			MinHits:          2,
			MinUniqueDocs:    2,
			RequireCitations: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if retrieveCalls != 2 {
		t.Fatalf("OnRetrieve calls = %d, want 2", retrieveCalls)
	}
	if askCalls != 1 {
		t.Fatalf("OnAsk calls = %d, want 1", askCalls)
	}
}
```

添加到 `rag/instrument_test.go`：

```go
func TestAskReflectionAggregatesMetricsAcrossRounds(t *testing.T) {
	sys := New(Options{Model: fakeModel{}})
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "generic travel text"},
	}, ingest.ImportOptions{Namespace: "geo"})
	if err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of france", AskOptions{
		Search: SearchOptions{Namespace: "geo", TopK: 1},
		Reflection: &ReflectionOptions{
			Mode:             ReflectionModeRule,
			MaxRounds:        2,
			MinHits:          2,
			MinUniqueDocs:    2,
			RequireCitations: true,
		},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if ans.Diagnostics.Reflection.Rounds != 2 {
		t.Fatalf("Reflection.Rounds = %d, want 2", ans.Diagnostics.Reflection.Rounds)
	}
	if ans.Diagnostics.Metrics.Calls.Generate < 2 {
		t.Fatalf("Calls.Generate = %d, want >= 2", ans.Diagnostics.Metrics.Calls.Generate)
	}
}
```

- [ ] **Step 2：运行 observer 和指标测试以确认失败**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run 'TestAskReflectionTriggersRetrieveObserverPerRound|TestAskReflectionAggregatesMetricsAcrossRounds' -count=1
```

预期：如果回调计数或聚合指标不完整则 FAIL。

- [ ] **Step 3：实现顶层聚合和 observer 语义**

更新 `rag/ask.go` 和 `rag/reflection.go`，使：

- `obs.Counter` 在顶层 `Ask` 处安装一次
- 每一轮复用同一个 context 计数器
- 轮指标被追加进顶层反思诊断
- 最终的 `Diagnostics.Metrics` 反映所有 generation/embed 调用
- 最终的 `Trace.Reflection` 镜像每轮决策

v1 中不要添加新的 observer 回调。保留：

- `OnAsk`：成功时一次
- `OnRetrieve`：每次内部检索调用一次

- [ ] **Step 4：运行聚焦测试和包套件**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -count=1
```

预期：PASS。

- [ ] **Step 5：提交可观测性修复**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add rag/ask.go rag/reflection.go rag/observer_test.go rag/instrument_test.go
git commit -m "feat: aggregate self-rag reflection metrics and trace"
```

### Task 6：添加 Fail-Open 行为和边界情形防护

**文件：**
- 修改：`rag/reflection.go`
- 修改：`rag/system_test.go`

- [ ] **Step 1：编写失败的边界情形测试**

为以下内容添加测试：

```go
func TestAskReflectionStopsWhenRewriteDoesNotChangeQuery(t *testing.T) {}

func TestAskReflectionStopsWhenEvidenceDoesNotImprove(t *testing.T) {}

func TestAskReflectionFailOpenReturnsBestAvailableAnswer(t *testing.T) {}
```

fail-open 测试应使用一个脚本化模型，它：

- 返回一个有效的第一轮答案
- 在较晚的反思或重写期间失败

然后断言：

- 当 `FailOpen` 为 true 时 `Ask` 返回无错误
- 返回第一个可用答案
- `FailureFallback` 为 true
- `FailureReason` 被填充

- [ ] **Step 2：运行边界情形测试以验证失败**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -run 'TestAskReflectionStopsWhenRewriteDoesNotChangeQuery|TestAskReflectionStopsWhenEvidenceDoesNotImprove|TestAskReflectionFailOpenReturnsBestAvailableAnswer' -count=1
```

预期：FAIL。

- [ ] **Step 3：实现护栏和 fail-open 回退**

更新 `rag/reflection.go`，使该循环：

- 当重写返回相同查询时停止
- 当返回的文本块 ID 跨轮未变时停止
- 保留可用的最佳 `askRoundResult`
- 当 `FailOpen` 为 true 且发生较晚的仅反思失败时返回那个最佳结果

使用一个如下的辅助形态：

```go
func sameChunkSet(a, b []string) bool {
	// compare deduped IDs
}
```

并把回退状态记录进 `ReflectionDiagnostics`。

- [ ] **Step 4：运行聚焦测试和完整的 `rag` 包**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag -count=1
```

预期：PASS。

- [ ] **Step 5：提交边界情形行为**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add rag/reflection.go rag/system_test.go
git commit -m "feat: harden self-rag reflection fallback behavior"
```

### Task 7：刷新快照并更新文档

**文件：**
- 修改：`README.md`
- 修改：`docs/production-deployment.md`
- 修改：`api/v1.snapshot.txt`

- [ ] **Step 1：编写文档更新**

用一个展示以下内容的新章节更新 `README.md`：

```go
ans, err := sys.Ask(ctx, question, rag.AskOptions{
	Search: rag.SearchOptions{Namespace: "docs", TopK: 4},
	Reflection: &rag.ReflectionOptions{
		Mode:             rag.ReflectionModeHybrid,
		MaxRounds:        2,
		MinHits:          2,
		MinUniqueDocs:    1,
		RequireCitations: true,
		AllowRewrite:     true,
		FailOpen:         true,
	},
})
```

并解释：

- 最终答案字段只反映被采纳的轮次
- 反思诊断包含所有轮次
- `OnRetrieve` 可能在一次 `Ask` 期间触发多次

用以下更新 `docs/production-deployment.md`：

- 针对生产的 fail-open 建议
- 说明如果启用了重写后的查询仍然流经 MQE/HyDE
- 说明 `OnRetrieve` 回调计数现在按内部检索轮次

- [ ] **Step 2：重新生成已提交的 API 快照**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./internal/apisnapshot -run TestAPISnapshot -update
```

预期：PASS，且磁盘上的 `api/v1.snapshot.txt` 被更新。

- [ ] **Step 3：运行最终验证套件**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
GOCACHE=/tmp/go-build go test ./rag ./eval ./internal/apisnapshot -count=1
```

预期：PASS。

- [ ] **Step 4：评审 API、文档和测试的 diff**

运行：

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git diff -- README.md docs/production-deployment.md rag/options.go rag/system.go rag/ask.go rag/reflection.go rag/system_test.go rag/observer_test.go rag/instrument_test.go api/v1.snapshot.txt
```

预期：反思配置、轮诊断、observer 语义和文档都出现在 diff 中，无无关变更。

- [ ] **Step 5：提交文档和快照刷新**

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-rag
git add README.md docs/production-deployment.md api/v1.snapshot.txt
git commit -m "docs: document self-rag reflection flow"
```

## 自我评审

规范覆盖检查：

- 反思模式选择在 Task 1、3 和 4 中覆盖
- 有界循环和最终答案语义在 Task 2 和 3 中覆盖
- 轮级诊断和链路在 Task 1、3 和 5 中覆盖
- observer 和 token 聚合语义在 Task 5 中覆盖
- fail-open 和早停防护在 Task 6 中覆盖
- 文档和导出 API 快照在 Task 7 中覆盖

占位符扫描：

- 不存在 `TODO`、`TBD` 或延后实现占位符
- 所有改动代码的任务都包含具体的代码片段或精确的辅助形态
- 所有验证步骤都包含精确的命令

类型一致性检查：

- `ReflectionOptions`、`ReflectionMode`、`ReflectionDiagnostics` 和 `ReflectionTrace` 的命名跨任务一致
- `FailOpen`、`AllowRewrite`、`RequireCitations` 和 `MaxRounds` 在 API、测试和文档间一致

## 执行交接

计划完成并保存到 `docs/superpowers/plans/2026-05-23-self-rag-reflection.md`。两个执行选项：

**1. 子代理驱动（推荐）** —— 我为每个任务派发一个全新子代理，在任务之间评审，快速迭代

**2. 内联执行** —— 在本会话中使用 executing-plans 执行任务，带检查点的批量执行
