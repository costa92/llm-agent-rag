[English](./README.md) | [简体中文](./README.zh-CN.md)

# llm-agent-rag

独立的 Go RAG SDK，提供抽象导入、检索、自定义 LLM 生成，
以及自定义提示词模板接缝。生产就绪，配备 PostgreSQL +
pgvector 后端、面向 OTel 的观察钩子，以及作为 `go test` 回归门禁的
评估框架。

## 文档

- [生产部署](./docs/production-deployment.zh-CN.md) —— pgvector
  配置、连接池配置、observer 接线、运维说明
- [后端选型](./docs/backend-selection.zh-CN.md) —— 内存版 vs
  postgres、一致性契约、新增后端
- [核心兼容性](./docs/core-compatibility.zh-CN.md) —— 与
  `github.com/costa92/llm-agent` 的关系，以及可选适配器

## 范围

本 SDK 围绕三个主要工作流设计：

- 从抽象来源导入文档
- 为查询检索排序后的文本块
- 使用调用方提供的模型和提示词模板生成答案

默认包 **不含任何非标准库依赖**。`postgres`
子包和带构建标签的 `adapter/llmagent`
包是仅有的两处引入外部依赖的地方。

## 包布局

下面列出的每个包都属于 **冻结的 v1 公共面**，
除非另有明确标注 —— v1.x 的仅增量承诺覆盖了它们全部。
带构建标签的 `adapter/llmagent` 和
`internal/` 子树是仅有的两处例外。

**流水线核心：**

- `ingest`：文档、来源、切分器、导入辅助工具
- `embed`：嵌入器接缝和默认的哈希嵌入器
- `store`：向量存储接缝和内存版参考存储
- `store/storetest`：每个后端都要接线对接的共享一致性套件
- `postgres`：PostgreSQL + pgvector 后端（选择开启的依赖：`pgx/v5`、`pgvector-go`）
- `retrieve`：混合检索、结构感知的 route policy、搜索轨迹
- `pack`：token 预算感知的上下文打包
- `rerank`：启发式 + 模型打分的重排器
- `generate`：文本生成接缝
- `prompt`：提示词模板接缝和默认的 QA 模板
- `rag`：导入、检索、ask 的编排层 + observer 钩子
- `tree`：面向结构化 markdown 语料的文档树原语

**GraphRAG：**

- `graph`：进程内 Louvain + LabelPropagation 社区检测、
  社区摘要、带权多跳路径排序、子图
  证据（由 `rag.System.AskGlobal` / `AskDrift` 使用）

**答案路径附加项（同样冻结于 v1）：**

- `advanced`：无状态的查询扩展辅助工具 —— MQE、HyDE
- `agentic`：`CorrectiveAsker` —— 包装 `rag.Ask` 的有界重试循环
- `feedback`：用于被标记 Ask 的并发安全 JSONL 写入器
  （在线到离线的回归反馈环）
- `guard`：内容安全层 —— 导入时 PII 脱敏、
  检索时提示词注入筛查（叶子包，仅标准库）

**质量 / 横切关注点：**

- `eval`：检索指标（precision / recall / MRR / grounding@k）
  加上 RAG-Triad 答案评估、JSONL 加载器，用作 `go test`
  回归门禁
- `obs`：进程内指标 + `rag.Observer` 钩子（由
  `llm-agent-otel` 消费）
- `contract`：对 `github.com/costa92/llm-agent` 的 `rag` 门面
  所用门面子集做跨仓编译期锚定
- `api`：已提交的 `v1.snapshot.txt` 导出符号基线，
  由 `internal/apisnapshot` 做差异比对（测试制品，无导出符号）

**例外项：**

- `adapter/llmagent`：带构建标签（`llmagent`）的
  `github.com/costa92/llm-agent` 互操作层 ——
  让默认构建保持仅标准库
- `internal/`：不可导入，包含 `internal/apisnapshot`（
  v1 面差异测试）

## 状态

当前状态：稳定 —— v1.0。公共 API 在 `v1.x` 系列下
冻结于仅增量的兼容承诺；关于导入兼容规则、semver 策略，
以及破坏性变更的 `/v2` 流程，见
[docs/compatibility.md](docs/compatibility.zh-CN.md)。

已实现：

- 通过 `ingest.Source` 和 `ingest.Importer` 进行抽象导入
- 确定性的默认 `CharSplitter` 和 markdown 切分器
- 默认 `HashEmbedder`
- 默认 `InMemoryStore` + `postgres.Store`（pgvector）
- 共享的 `store/storetest.RunConformance` 契约套件
- 通过 `generate.Model` 进行抽象生成
- 通过 `prompt.Template` 自定义提示词
- 带 `Import`、`ImportFrom`、`Retrieve` 和 `Ask` 的 `rag.System`
- 用于外部追踪的 `rag.Observer{OnImport, OnRetrieve, OnAsk}` 钩子
  （由 `llm-agent-otel` 消费）
- 检索策略接缝，用于：
  - 查询预处理
  - 词法检索
  - 混合检索
  - MQE / HyDE 查询扩展
  - 启发式重排
  - token 预算感知的上下文打包
  - 结构感知的章节/路径检索
  - 子树约束的 route-path 检索
  - 层级语料的自动章节路由选择
  - 置信度差距自适应扇出（在强 top-1 时收敛，
    在前两条路由接近时扇出）
  - 按路由的 `SearchTrajectory` 归因
  - 可插拔的 `SectionPlanner` 接口（默认
    `GapAwareSectionPlanner`）
- 面向结构化 markdown 语料的文档树原语
- 评估框架（`eval`），带 precision / recall / MRR /
  grounding@k 指标、一个 JSONL 加载器，以及一个在 `go test`
  时为检索质量把关的种子回归测试

## 快速开始

```go
package main

import (
	"context"
	"fmt"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/rag"
)

type echoModel struct{}

func (echoModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	return generate.Response{Text: req.Messages[0].Content}, nil
}

func main() {
	sys := rag.New(rag.Options{Model: echoModel{}})

	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "paris", Content: "Paris is the capital of France."},
		{ID: "berlin", Content: "Berlin is the capital of Germany."},
	}, ingest.ImportOptions{Namespace: "cities"})
	if err != nil {
		panic(err)
	}

	hits, err := sys.Retrieve(context.Background(), "France capital", rag.SearchOptions{
		Namespace: "cities",
		TopK:      1,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(hits[0].Chunk.ID)

	ans, err := sys.Ask(context.Background(), "What is the capital of France?", rag.AskOptions{
		Search: rag.SearchOptions{Namespace: "cities", TopK: 1},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(ans.Text)
}
```

## 使用说明

- `Import` 用于显式的内存版文档批次。
- `ImportFrom` 用于已实现来源接缝的文档来源。
- `Retrieve` 不依赖 LLM，仅依赖嵌入器和存储。
- `Ask` 叠加了检索、可选重排、上下文打包、提示词渲染，
  以及答案生成。

### Self-RAG 反思

`Ask` 可以在采纳最终一轮之前运行一个有界的自我反思循环：

```go
ans, err := sys.Ask(ctx, "What changed in the refund policy?", rag.AskOptions{
	Search: rag.SearchOptions{
		Namespace:  "docs",
		TopK:       6,
		EnableMQE:  true,
		EnableHyDE: true,
	},
	Reflection: &rag.ReflectionOptions{
		Mode:             rag.ReflectionModeHybrid,
		MaxRounds:        3,
		MinHits:          3,
		MinScore:         0.75,
		MinUniqueDocs:    2,
		RequireCitations: true,
		AllowRewrite:     true,
		FailOpen:         true,
	},
})
if err != nil {
	panic(err)
}

fmt.Println(ans.Text)
fmt.Println(ans.Diagnostics.Reflection.AdoptedRound)
fmt.Println(ans.Diagnostics.Reflection.StopReason)
```

反思诊断信息在 `ans.Diagnostics.Reflection.RoundDetails` 和
`trace.Reflection.Rounds` 中保留完整的轮次历史。
最终答案字段只反映被采纳的那一轮：文本块 ID、分数，
以及其他面向答案的属性都不会合并被拒绝轮次的信号。

Observer 行为保持增量：`Observer.OnAsk` 仍然在每个顶层
`Ask` 中触发一次，而 `Observer.OnRetrieve` 在反思运行时
可能在一次 `Ask` 中触发多次，因为每个内部检索轮次都会发出
自己的检索链路。

## 最小示例工作流

1. 构建一个 `rag.System`
2. 通过 `Import` 或 `ImportFrom` 导入文档
3. 调用 `Retrieve` 获取原始排序后的文本块
4. 当你想要合成答案时调用 `Ask`

尚未实现：

- HTTP 服务层
- CLI

## 可选适配器

`adapter/llmagent` 包刻意放在构建标签之后：

- 构建标签：`llmagent`

这让核心 SDK 在不要求 `github.com/costa92/llm-agent`
的情况下也能发布和测试。

核心验证：

```bash
cd /tmp/llm-agent-rag
GOWORK=off GOCACHE=/tmp/go-build go test ./...
```

如果你想在本地开发 `llm-agent` 适配器，添加一个临时的
开发依赖并运行：

```bash
GOWORK=off GOCACHE=/tmp/go-build go test -tags llmagent ./adapter/llmagent
```

因为该适配器导入了 `github.com/costa92/llm-agent`，独立
module 不会在其可发布的核心 `go.mod` 中保留该依赖。
为了本地开发适配器，添加一个临时的 `require` 和 `replace`
指向你本地的 `llm-agent` 检出，然后运行上面带标签的测试。

## 验证

```bash
cd /tmp/llm-agent-rag
GOWORK=off GOCACHE=/tmp/go-build go test ./...
```

## PR 自动化

本仓库现在期望 `.github/workflows/pr-governance.yml` 强制执行一条简单策略：

- 由 `costa92` 编写的 PR 应自动通过治理，并在必需的检查通过后启用自动合并。
- 同仓库的 owner 分支应由该工作流在 PR 确认合并后显式删除。
- 由其他任何人编写的 PR 应向 `costa92` 请求评审，并保持阻塞直到 `costa92` 批准当前 PR 头。

这条策略设计为与要求 `go` 和 `governance` 状态检查的分支保护配合工作，而非 GitHub 内置的必需批准门禁。

仓库级的 `deleteBranchOnMerge` 设置作为安全网保持启用，但当前经过测试的主路径已经位于 `pr-governance.yml` 内部：启用自动合并、等到 PR 可见地合并、然后用 GitHub API 删除同仓库的 head ref。独立的下游清理工作流在推出期间经过测试，但不再是文档记载的主要机制。

完整的多仓治理设计，包括 `llm-agent`、`llm-agent-rag`、`llm-agent-flow`、`llm-agent-providers`、`llm-agent-otel` 和 `llm-agent-customer-support` 之间的关系，位于核心仓库文档中：

- [`PR-GOVERNANCE-OVERVIEW.md`](https://github.com/costa92/llm-agent/blob/main/docs/PR-GOVERNANCE-OVERVIEW.md)
- [`PR-GOVERNANCE-PROJECTS.md`](https://github.com/costa92/llm-agent/blob/main/docs/PR-GOVERNANCE-PROJECTS.md)
- [`PR-GOVERNANCE-RULES.md`](https://github.com/costa92/llm-agent/blob/main/docs/PR-GOVERNANCE-RULES.md)
- [`PR-GOVERNANCE-OPERATIONS.md`](https://github.com/costa92/llm-agent/blob/main/docs/PR-GOVERNANCE-OPERATIONS.md)
