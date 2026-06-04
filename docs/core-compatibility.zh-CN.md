[English](./core-compatibility.md) | [简体中文](./core-compatibility.zh-CN.md)

# 核心兼容性

本文档说明 `llm-agent-rag`（本仓库）如何与
`github.com/costa92/llm-agent`（核心 agents 仓库）关联、何时
会发生变更，以及跨仓契约门禁强制执行什么。

## 双仓拆分

伞形项目有两个与 RAG 相关的仓库：

- **`github.com/costa92/llm-agent`** —— agents 框架。保持
  **仅标准库**：`go.mod` 中无非标准库依赖，发布 tag 之前无
  `go.sum`。需要嵌入或 RAG 数据的核心包
  直接依赖本仓库 API 的一小撮、被锚定的子集。
- **`github.com/costa92/llm-agent-rag`** —— 本仓库。独立的
  RAG SDK。可以引入依赖（自 v0.2 起也确实引入了 —— postgres 后端用的
  `pgx/v5` + `pgvector-go`）。

这一拆分之所以存在，是因为 `llm-agent` 的用户应当能够
`go get` 核心并读懂每一行。引入一个向量数据库
驱动会破坏这条「读源码」承诺。把
生产级的 RAG 实现放在一个有自己
依赖预算的兄弟仓里，让我们在不损害核心
可审计性的前提下拥有一个可部署的系统。

## 各找各处

| 想找什么                                    | 去哪里                                           |
| ------------------------------------------- | ------------------------------------------------ |
| `ChatModel`、`Agent`、工具执行              | `github.com/costa92/llm-agent`                   |
| 核心辅助集成（`context`、`memory`）         | `github.com/costa92/llm-agent`                   |
| RAG 编排、检索、持久化                      | `github.com/costa92/llm-agent-rag`               |
| 提供方适配器（OpenAI、Anthropic、……）       | `github.com/costa92/llm-agent-providers`         |
| OTel 可观测性包装器                         | `github.com/costa92/llm-agent-otel`              |
| 参考客服服务                                | `github.com/costa92/llm-agent-customer-support`  |

## 可选适配器包

本仓库在一个构建标签之后提供 `adapter/llmagent/`：

```go
//go:build llmagent
```

该包把核心的 `llm.ChatModel` 接口适配为
独立的 `generate.Model` 接口，使 rag 系统能把
核心的 chat 模型当作其答案生成器使用。

默认构建 **不会** 编译这个包。默认的 `go test
./...` 不会运行它。这是刻意的 —— 没有该构建标签时，
本仓库对 `github.com/costa92/llm-agent` 零依赖。

如果你想把一个核心 chat 模型接入独立的 RAG SDK，
带标签构建：

```bash
go build -tags llmagent ./...
go test  -tags llmagent ./adapter/llmagent
```

本地开发时你可能需要一个临时的 `replace` 指令
指向你的 `llm-agent` 检出。不要提交那条 replace ——
独立仓库的发布制品绝不能依赖一个本地的
核心检出。

## 版本预期

独立仓库独立于核心演进。两者之间的
契约面很小（例如 `embed.Embedder`、
`store.Hit`、`store.InMemoryStore`、`ingest.Document` 和
`rag.System`），且刻意保持稳定。

- **独立仓的 minor 提升** 增加功能，但保留
  当前核心集成所消费的 API 子集。
- **独立仓的 major 提升** 是核心
  集成必须被显式更新的时刻。它们很少发生。
- **核心发布** 锚定一个特定的独立仓版本。该锚定
  只在计划好的核心 minor/major 提升时改变。

契约门禁在以下情况失败：

- 独立 module 导出了一个新的公共类型或方法，而
  某个被锚定的核心集成在未更新锚定的情况下读取了它
- 独立 module 改变了核心
  集成所调用的某个方法的签名
- 编译期契约测试不再匹配被锚定的
  独立仓版本

## 什么 *不* 跨越边界

独立仓的新能力不会自动
通过核心辅助包暴露。下列项先落在
独立仓，且 **默认不会通过核心包接线打通**：

- `postgres` 包（核心保持仅标准库）
- `rag.Observer` 钩子面
- `eval` 框架
- `store/storetest` 一致性套件
- 按路由的 `SearchTrajectory` 数据

想要这些特性的消费者应当直接导入
`github.com/costa92/llm-agent-rag`。核心仓库不会
试图镜像完整的独立仓面。

## 何时提升什么

粗略的启发式：

- **独立仓 `rag.System` 上新增公共方法？** 独立仓
  minor 提升。核心集成不受影响，除非它们显式
  转发该方法。
- **`store.Store` 的破坏性变更？** 独立仓 major 提升。
  每个后端（内存版、postgres、第三方）都必须更新。
  任何被锚定的核心集成都必须更新。
- **独立仓中的新后端（Qdrant、SQLite-vec、……）？**
  独立仓 minor 提升。核心包不受影响；消费者通过
  导入新包来选择开启。
- **核心 `llm.ChatModel` 的变更？** 核心仓库的事；
  影响这里的 `adapter/llmagent` 包，但仅在
  `-tags llmagent` 下。

拿不准时，优先选独立仓 minor 而非 major。契约
面足够小，破坏性变更应当很少见。
