[English](./backend-selection.md) | [简体中文](./backend-selection.zh-CN.md)

# 后端选型

`llm-agent-rag` 目前提供两个 `store.Store` 实现，且为更多
实现做了设计。本指南说明何时选择哪一个，以及
如何新增后端。

## 已提供的两个后端

### `store.InMemoryStore`

纯 Go 的、基于 map 的存储。无外部依赖。

**适用于：**

- 单元测试和集成测试
- 演示、原型、notebook
- 语料较小（约数千）的单进程应用

**不适用于：**

- 多进程部署（状态按进程隔离）
- 无法装入内存的语料
- 任何必须在重启后仍然存活的场景

### `postgres.Store`

PostgreSQL + pgvector 后端。位于 `postgres/` 子包中。
引入 `github.com/jackc/pgx/v5` 和 `github.com/pgvector/pgvector-go`
作为本 SDK 中最早的非标准库依赖。

**适用于：**

- 生产部署
- 多进程负载
- 从数千到数百万文本块的语料
- 你已经在运维 PostgreSQL 的环境

**权衡：**

- 要求目标数据库上有 `vector` 扩展
- 向量维度在建表时固定
  （`postgres.Config.Dimension`）
- 实时 Postgres 集成测试由
  `LLM_AGENT_RAG_PG_URL` 做环境门控，未设置时干净跳过

完整的配置步骤见
[`production-deployment.md`](./production-deployment.zh-CN.md)。

## 一致性契约

每个后端都必须通过 `store/storetest.RunConformance`。该
辅助工具生成 12 个具名子测试，覆盖契约：

- `Upsert_and_Get_round_trip` —— 每个 `StoredChunk` 字段都能往返
- `Search_returns_nearest_first` —— 余弦排序
- `Search_respects_namespace` —— 无跨命名空间渗漏
- `Filter_narrows_results` —— 元数据相等
- `Security_filter_intersects_with_caller_filter` —— AND 语义
- `List_returns_namespace_chunks` —— 带过滤与不带过滤
- `Get_on_missing_returns_ErrNotFound`
- `Remove_on_missing_returns_ErrNotFound`
- `Remove_deletes`
- `RemoveByFilter_returns_count_and_removes`
- `Stats_reports_count_and_dim`
- `Dimension_mismatch_returns_error`（通过
  `WithDimensionStrict()` 选择开启 —— 面向强制固定
  嵌入维度的后端）

用一次调用把你的后端接入套件：

```go
import "github.com/costa92/llm-agent-rag/store/storetest"

func TestMyBackendConformance(t *testing.T) {
    storetest.RunConformance(t, func(t *testing.T) store.Store {
        // build a fresh, isolated store for this subtest
        return newMyBackend(t)
    }, storetest.WithDimensionStrict())
}
```

该工厂在每个子测试中被调用一次。对于共享
schema 的后端（如 postgres），工厂在每次调用时创建一个全新的
命名空间/表/集合，并注册 `t.Cleanup` 以
删除它。

## 新增后端

五步清单：

1. **实现 `store.Store`。** 全部七个方法。将缺失的
   行映射到 `store.ErrNotFound`；将向量维度不匹配映射到
   `store.ErrDimensionMismatch`。编译期检查：
   `var _ store.Store = (*MyStore)(nil)`。
2. **运行一致性测试。** 添加一个调用
   `storetest.RunConformance` 的 `*_conformance_test.go`。把任何外部服务测试
   用环境变量门控，使默认的 `go test` 仍然通过。
3. **记录运维指引。** 在
   [`production-deployment.md`](./production-deployment.zh-CN.md) 中添加一节，涵盖
   连接池/连接配置、索引选择和维度语义。
4. **更新下方矩阵**，填入你后端的能力
   概况。
5. **提交 PR。** 一致性通过是合并门禁。

## 能力矩阵（当前）

| 后端                | 持久化      | 向量索引                | 元数据过滤      | 安全过滤（AND）       | 实时测试门控          |
| ------------------- | ----------- | ----------------------- | --------------- | --------------------- | --------------------- |
| `InMemoryStore`     | 否          | 线性扫描                | 是              | 是                    | （始终开启）          |
| `postgres.Store`    | 是          | 通过 `Config.VectorIndex` 的 ivfflat / hnsw | JSONB `@>`      | 是                    | `LLM_AGENT_RAG_PG_URL` |

## 前瞻性后端

这些是可能的未来贡献。今天都尚未提供 ——
每一个在通过一致性套件后都能嵌入上面的
矩阵：

- **Qdrant** —— 专为向量打造的数据库，gRPC + HTTP，有维护中的
  Go 客户端。今天混合检索特性集最强。
- **SQLite + sqlite-vec** —— 可嵌入的单文件存储；给
  SDK 提供一条零服务端的演示路径。今天用 cgo，纯 Go 变体存在。
- **DuckDB** —— 进程内分析型数据库，向量支持日渐增强。
  适合离线评估流水线。
- **带 HNSW 的 pgvector** —— 同一后端，不同索引。可以
  暴露为一个 `postgres.Config.Index` 旋钮，而非一个新
  包。

按运维成本而非特性清单来选择。SDK
契约足够小，以至于后端选择是可逆的 —— 从
运维最便宜处起步，在规模需要时再切换。
