[English](./production-deployment.md) | [简体中文](./production-deployment.zh-CN.md)

# 生产部署

本指南带你走一遍如何针对真实的
PostgreSQL + pgvector 后端并带上可观测性钩子来部署 `llm-agent-rag`。它假设
你已经选定 `postgres.Store` 作为你的存储 —— 如果你仍在
权衡选项，见 [`backend-selection.md`](./backend-selection.zh-CN.md)。

## 前置条件

- PostgreSQL >= 14，且可用 `vector` 扩展
- 一个拥有 `CREATE EXTENSION` 权限的用户，或一位能
  在目标数据库上预先创建该扩展的管理员
- 为本次部署固定嵌入器模型及其维度
  （postgres 存储列被创建为 `vector(N)`）

## 连接池接线

`postgres.Store` 接收一个由调用方拥有的 `*pgxpool.Pool`。该
连接池的 `AfterConnect` 钩子必须在每个新连接上注册 pgvector
类型，以便 vector 编解码器可用：

```go
import (
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/costa92/llm-agent-rag/postgres"
)

cfg, err := pgxpool.ParseConfig(os.Getenv("PG_URL"))
if err != nil { /* ... */ }
cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
    return postgres.RegisterTypes(ctx, conn)
}
pool, err := pgxpool.NewWithConfig(ctx, cfg)
if err != nil { /* ... */ }
defer pool.Close()
```

忘记 `AfterConnect` 会在 `Upsert` / `Search` 上导致
`unknown type vector` 错误。参考测试
`postgres/postgres_test.go::TestPostgresStore_LiveSmoke` 展示了
完整的形态。

## 构造与迁移

```go
s, err := postgres.New(pool, postgres.Config{
    Table:     "rag_chunks",  // optional; defaults to "chunks"
    Dimension: 1536,          // required; must match your embedder
})
if err != nil { /* ... */ }

if err := s.Migrate(ctx); err != nil { /* ... */ }
```

`Migrate(ctx)` 是幂等的。它创建 `vector` 扩展
（若已安装则为空操作）、带 `vector(N)`
列的 chunks 表，以及一个命名空间索引。在每次启动时运行它，而不只是
第一次。

`Table` 字段被校验为严格的 ASCII 标识符；如果名称
包含 `[a-zA-Z_][a-zA-Z0-9_]*` 之外的任何字符，构造函数会在任何 SQL 运行之前
返回一个错误。

## 接入 rag.System

`postgres.Store` 满足 `store.Store`，所以 rag 门面通过
`Options.Store` 选用它：

```go
import "github.com/costa92/llm-agent-rag/rag"

sys := rag.New(rag.Options{
    Store:    s,
    Embedder: yourEmbedder,
    Model:    yourLLM,
})
```

无需改动其他 rag 门面代码。

## 安全过滤

postgres 存储通过 JSONB 子集匹配
（`metadata @> $N`）实现安全过滤。调用方过滤和安全过滤被
**取交集**（AND），而非覆盖 —— 一致性子测试
`Security_filter_intersects_with_caller_filter` 记录了这条
契约。

示例：一个带 `tenant` 元数据键的多租户部署：

```go
hits, err := sys.Retrieve(ctx, query, rag.SearchOptions{
    Namespace:       "docs",
    Filters:         rag.Filter{"lang": "en"},
    SecurityFilters: rag.Filter{"tenant": userTenantID},
})
```

一个试图通过设置 `Filters: {"tenant":
otherTenant}` 来扩大作用域的请求，在 `SecurityFilters` 锚定了
允许的租户时返回零命中 —— 设计如此。

## Observer 接线

rag 门面在成功时发出三个 observer 事件：`OnImport`、
`OnRetrieve`、`OnAsk`。在 `Options` 中传入一个 `Observer`，这些
回调会在每个顶层操作完成后触发：

```go
sys := rag.New(rag.Options{
    Store: s,
    Model: yourLLM,
    Observer: rag.Observer{
        OnImport: func(ctx context.Context, trace rag.ImportTrace) {
            // record metrics, emit a span, log structured event
        },
        OnRetrieve: func(ctx context.Context, trace retrieve.Trace) {
            // every internal retrieval round, including reflection retries
        },
        OnAsk: func(ctx context.Context, trace rag.Trace) {
            // end-to-end answer trace, fires after OnRetrieve
        },
    },
})
```

`llm-agent-otel` 兄弟仓中的 OTel 适配器接到这些
钩子上。每个回调收到 rag 门面返回给调用方的
同一份链路数据，所以 observer 代码永远不需要检视
内部实现。

当 `AskOptions.Reflection` 启用时，一次顶层 `Ask` 可以触发
多个成功的内部检索轮次。将 `OnRetrieve` 计数视作
按轮次，而非按公共 API 调用。`OnAsk` 仍然为被采纳的
答案轮次触发一次。

错误会在任何回调触发之前短路。如果你需要错误
span，直接在你的追踪层中包装 rag 方法。

## 运维说明

### 连接池规模

`pgxpool.Config.MaxConns` 默认为 `max(4, NumCPU)`。对于一个混合了
嵌入期突发与稳定检索的 RAG 负载，
按峰值并发 Retrieve 调用数加一个小的导入缓冲来设定规模。
一个合理的起点是 `2 * peak QPS / avg query latency in
seconds`，每池上限 `25`。

### 单表 vs 每租户一表

默认 schema 使用一张带 `namespace`
列的单一 chunks 表。这对大多数部署都是正确的选择 —— pgvector 的
ivfflat 索引并不会因为对小数据集分片而变快。

仅在以下情况选择每租户一表：

- 某个租户的热工作集主导了索引
- 合规要求物理隔离
- 你需要按租户的 ALTER TABLE 自由度（不同维度）

在那些情况下，为每个租户构造一个带
独立 `Config.Table` 的 `postgres.Store`。

### 索引选择

默认的 `Migrate` 不创建向量索引。对于小
数据集（< 10k 文本块），顺序扫描就够了。对于更大的
数据集，设置 `postgres.Config.VectorIndex` 并重新运行 `Migrate`：

```go
s, err := postgres.New(pool, postgres.Config{
    Table:        "rag_chunks",
    Dimension:    1536,
    VectorIndex:  postgres.VectorIndexIVFFlat, // or VectorIndexHNSW
    IVFFlatLists: 100,                          // default 100 when zero
})
// s.Migrate(ctx) now also issues:
//   CREATE INDEX IF NOT EXISTS rag_chunks_embedding_ivfflat
//     ON rag_chunks USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)
```

对于 HNSW，设置 `VectorIndex: postgres.VectorIndexHNSW` 并可选地设置
`HNSWConstructionM`（默认 16）。HNSW 要求 pgvector >= 0.5。

`Migrate` 使用 `CREATE INDEX IF NOT EXISTS`，所以用相同
`VectorIndex` 重新运行是空操作。事后从 IVFFlat 切换到 HNSW
需要在带外删除先前的索引 —— `Migrate`
不会替你删除它。

对于必须避免 DDL 锁的热生产表，改为带外运行
`CREATE INDEX CONCURRENTLY` —— `Migrate` 目前
不使用 `CONCURRENTLY`，因为它无法在一个
事务内运行：

```sql
-- ivfflat for cosine distance — faster build, lower memory
CREATE INDEX CONCURRENTLY ON rag_chunks USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

-- hnsw for higher recall — slower build, higher memory
CREATE INDEX CONCURRENTLY ON rag_chunks USING hnsw (embedding vector_cosine_ops);
```

对 ivfflat 来说，`lists = sqrt(rowcount)` 是一个合理的起点。

### 重新导入语义

将 `ImportOptions.ReplaceSource: true` 与 `Document.SourceID` 一起使用，以
干净地重新导入一个来源。存储在 upsert 新文本块之前，
删除任何 `metadata.source_id` 匹配的现有文本块。
删除的数量在 `ImportTrace.RemovedChunks` 中浮现。

### 清理

`Remove` 按 ID 删除单个文本块。`RemoveByFilter` 删除
每个匹配命名空间 + 过滤的文本块并返回计数。
无软删除 —— 删除是终态。

### 生产中的反思

对于生产推出，优先 `ReflectionOptions.FailOpen: true`，这样一次较晚的
反思或重写失败仍能返回已经可用的最佳轮次，
而不是让整个请求失败。

如果反思重写了查询，且 `Search.EnableMQE` 或 `Search.EnableHyDE`
也启用了，重写后的查询在下一轮仍然流经那些预处理器。因此较晚轮次的检索链路同时反映了反思
重写和任何已配置的 MQE 或 HyDE 扩展。
