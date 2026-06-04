[English](./compatibility.md) | [简体中文](./compatibility.zh-CN.md)

# 兼容性策略

本文档是 `llm-agent-rag` 在 `v1.0.0` 处承诺的 Go module
兼容性保证。打 `v1.0.0` tag 即在本 module 自身的公共
API 上启用 Go 的 [导入兼容规则][go-compat]。下面所有内容都把规则
讲清楚，让每位贡献者和用户都准确理解 `v1.x` 保证什么 ——
以及不保证什么。

> **范围。** 本文档涵盖 `llm-agent-rag` *自身* 的 API。关于
> 本仓库如何与核心 `github.com/costa92/llm-agent` 仓库关联 ——
> 双仓拆分、哪些特性跨越边界，以及
> 跨仓 CI 门禁 —— 见 [`core-compatibility.md`](./core-compatibility.md)。

[go-compat]: https://go.dev/blog/v2-go-modules

## 导入兼容

在 `v1.x` 系列内，公共 API 是 **仅增量** 的。从
`v1.0.0` 起，没有任何 `v1.MINOR.PATCH` 发布会：

- 删除或重命名一个导出符号（类型、函数、方法、
  变量、常量）；
- 改变一个导出函数或方法的签名；
- 删除一个导出的结构体字段；
- 改变调用方依赖的一个导出常量的值。

`v1.x` 发布 **可以** 做的：

- 添加新的导出函数、类型、方法、变量和常量；
- 向现有结构体添加新的导出 *字段*（使用
  位置式结构体字面量的调用方应改用具名字面量 —— 具名字面量
  不受新增字段影响）；
- 添加全新的包。

判定标准是 Go 导入兼容规则：针对 `v1.N` 构建的代码
必须能在无源码改动的情况下继续针对此后每个 `v1.M`
（`M ≥ N`）构建。

### 接口方法陷阱

**向导出接口添加方法是一个破坏性变更** —— 即便
「添加」听起来像是增量。每个实现该接口的外部类型
在新方法被要求的那一刻就不再满足它，
且那段代码无法编译。所以：

- `llm-agent-rag` 中的导出接口（例如 `store.Store`、
  `embed.Embedder`、`generate.Model`、`retrieve.Retriever`、
  `rerank.Reranker`、`ingest.Splitter`、`ingest.Source`、
  `prompt.Template`）在 `v1.x` 内 **冻结**：不可向它们
  添加任何方法。
- 一项本来需要新接口方法的新能力，转而
  以一个 *独立、可选* 的接口引入，类型可以
  额外实现它（即 `store.LexicalSearcher` 所用的模式）。调用方对该可选
  接口做类型断言；不实现它的类型不受影响。
- 扩展一个核心接缝接口是仅 `/v2` 的变更（见下文）。

## 语义化版本

发布遵循 Go module 系统所强制执行的 [语义化版本][semver]。版本号为 `v1.MINOR.PATCH`：

- **PATCH**（`v1.0.0 → v1.0.1`）—— 仅 bug 修复；无 API 变更。
- **MINOR**（`v1.0.0 → v1.1.0`）—— 向后兼容的新增
  （新函数、类型、包、结构体字段）。
- **MAJOR**（`v1.x → v2.0.0`）—— 破坏性变更。见下文 `/v2`。

Go 的 module 工具链和 module proxy 强制执行主版本
契约：`v2+` module 必须使用一个独立的导入路径，因此破坏性
变更不会意外地波及现有调用方。

[semver]: https://semver.org/

## 破坏性变更与 `/v2`

对 `llm-agent-rag` 进行破坏性变更只有恰好一种机制：
在新导入路径下发布一个新的主版本，
`github.com/costa92/llm-agent-rag/v2`。没有其他机制 —— 没有
「破坏性 patch」，没有选择开启的开关，没有悄悄改变
公共面的构建标签。

`/v2` 规则的后果：

- 一个 `v1.x` 消费者永远不会被 `v2` 发布破坏：导入路径
  不同，所以 `go get` 一个 `v1` 仍然解析到 `v1`。
- 迁移到 `/v2` 是一个显式、刻意的行为：消费者修改
  导入路径。`v1` 和 `v2` 甚至能在同一个构建中共存。
- 因为 `/v2` 是 *唯一* 的逃生舱，破坏性变更在设计上
  代价高且少见。只要 `v1.x` 内存在增量方案，
  就优先选它。

## `contract` 子契约

`contract` 包（`contract/contract_test.go`）是一个仅测试
包，它锚定当前核心集成所消费的精确 `llm-agent-rag`
面。它是完整 `v1.x` 承诺的一个 *子* 集：核心仓库
所依赖的、窄而显式枚举的一个面。

- 如果某个被锚定的核心集成所读取的符号被
  删除、重命名或重新签名，契约测试会失败 —— 在发布前
  捕获跨仓漂移。
- 被锚定的面 **只通过与 `llm-agent` 仓库协调的 PR** 改变：核心集成锚定与本仓库的面一起
  移动。
- 完整的跨仓故事 —— 双仓拆分、哪些特性跨越
  边界、何时提升什么 —— 记录在
  [`core-compatibility.md`](./core-compatibility.md) 中。本节只
  声明 `contract` 包存在、属于 v1.0
  兼容保证的一部分，且不会被单方面改变。

## 外部依赖（`postgres`）

`llm-agent-rag` **不是** 仅标准库的 —— 且这是刻意的（这正是
它作为仅标准库核心 `llm-agent` 的兄弟仓存在的全部
理由）。它仅有的非标准库依赖是：

- `github.com/jackc/pgx/v5` —— PostgreSQL 驱动；
- `github.com/pgvector/pgvector-go` —— `pgvector` 类型支持。

这些依赖的策略：

- 它们 **隔离在 `postgres` 包中**。导入 `llm-agent-rag` 的任何其他
  包都不会拉入第三方代码；只有
  导入 `postgres` 的消费者才承担该依赖。
- `go.mod` 声明 `llm-agent-rag` 测试所针对的 **最小**
  版本。一个 `v1.x` 发布可以在依赖自身的
  `v5.x` / `v0.x` 线内提高这些最小版本（一次向后兼容的提升）。
- 一个外部依赖的 **major** 提升（例如 `pgx/v5 → pgx/v6`）是
  *那个依赖的* semver 事件，不会被静默吸收：它在
  本仓库的 `go.mod` 中以一个变更的导入要求形式浮现，并按
  API 影响所要求的那样作为 `llm-agent-rag` 的 minor 或 major 发布。外部 major 绝不会藏在一个 patch 发布里。

## `adapter/llmagent` 覆盖

`adapter/llmagent` 是一个带构建标签的包（`//go:build llmagent`），
它把核心 `llm-agent` 的 chat-model 接口适配为本仓库的
`generate.Model` 接缝。**构建标签不会让一个包豁免于
兼容承诺。** `adapter/llmagent` 的导出面受与每个默认构建
包相同的 `v1.x` 仅增量规则覆盖：一个 `v1.x` 发布不会删除、重命名或重新签名它的
导出符号。

构建标签控制的是 *包何时编译*，而非 *它的 API
是否稳定*。用 `-tags llmagent` 构建的消费者获得与其他人相同的 `v1.x`
保证。

## `go.sum`

`llm-agent-rag` **提交 `go.sum`**，且这对本 module 是
正确的：`go.sum` 记录了 `postgres` 孤岛
依赖（`pgx`、`pgvector-go`）及其传递闭包的校验和，使
构建可复现、可验证。

这刻意 **不同于仅标准库的核心 `llm-agent`**，
后者没有非标准库依赖，因此在发布 tag 之前没有
`go.sum`。这一差异是设计如此 —— 关于两仓依赖拆分背后的
理据，见 [`core-compatibility.md`](./core-compatibility.md)。

## 最低 Go 版本

`go 1.26` 是 `v1.0` 的下限 —— 在 `go.mod` 中声明为 `go 1.26.0`。
构建 `llm-agent-rag v1.x` 需要 Go 1.26 或更新版本。

在 `v1.x` 系列内，最低 Go 版本 **可以在一个 MINOR 发布中
上升到一个更新的 1.x Go 发布**（例如 `go 1.27`）；提升 Go
下限被视作一次向后兼容的新增，而非破坏性
变更，与 Go 项目自身的兼容性策略一致。Go
下限提升总是会在发布说明中点明。

## 弃用流程

因为一个符号无法在 `v1.x` 内被删除（那会破坏导入
兼容），删除是一个两步、跨主版本的过程：

1. **标记。** 在一个 `v1.x` MINOR 发布中，该符号的文档注释获得一行
   `// Deprecated: …`，点明替代项和原因。
   该符号保持不变地继续工作 —— `pkg.go.dev` 和 `go vet`（通过
   `staticcheck` 风格的工具）会把弃用提示给调用方，但
   什么都不会破坏。
2. **删除。** 该符号只在未来的某个 `/v2`
   （`github.com/costa92/llm-agent-rag/v2`）中删除，绝不在 `v1.x` 内。

所以 `v1.x` 中的弃用是一个 *信号*，而非一次删除。调用方拥有
整个剩余的 `v1.x` 生命周期来迁移，之后符号才可能
消失，而且即便那时也只能通过显式选择开启 `/v2`。
