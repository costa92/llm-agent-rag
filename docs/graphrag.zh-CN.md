[English](./graphrag.md) | [简体中文](./graphrag.zh-CN.md)

# GraphRAG —— 关系遍历检索

`llm-agent-rag` v0.7 添加了 **Tier-1 轻量级 GraphRAG**：从被摄入的文档中抽取一个
知识图谱（实体 + 类型化关系），并通过
遍历它来检索，作为第四个信号与稠密、
词法和结构检索融合。

v0.8 在该基础之上添加 **Tier-3 GraphRAG**：层级化的
**社区检测**、惰性的 LLM 撰写的 **社区摘要**、
针对全语料「意义构建」问题的 map-reduce **全局检索**，以及
选择开启的 **模糊实体消解**。

v0.9 用两项 **GraphRAG 精化** 完善整幅图景：选择开启的
**路径排序证据**（`GraphRetriever.PathRanker`）和 **DRIFT 检索**
（`System.AskDrift`）—— 这条混合答案路径以一次全局引导
过程开场，随后在图上运行一个有界的局部后续循环。

GraphRAG 的每一块都是 **选择开启且增量的** —— 任何一块都不接线时，
SDK 的行为与之前完全相同。Tier-3 位于默认为
空操作的接缝之后；一个只接线 Tier-1（或什么都不接线）的 SDK 与 v0.7 逐字节相同。

---

## Tier-1 —— 三块

Tier-1 GraphRAG 是三个可组合的接缝。接线你需要的那些。

### 1. 实体抽取（`graph.EntityExtractor`）

一个抽取器在摄入时把每个文本块的文本转化为实体和关系。
把它设置到 `rag.Options.EntityExtractor` 上：

- `graph.LLMEntityExtractor{Model: m}` —— 提示一个 `generate.Model`；
  生产路径。
- `graph.DictionaryEntityExtractor{Terms: gazetteer}` —— 确定性、
  零 LLM；可复现测试和无 LLM 默认的基础。

`Import` 在切分后运行抽取器，规范化图
（按精确匹配 `(name, type)` 合并，并带来源文本块溯源），并
在 `ImportResult.Graph` 上浮现它。

### 2. 图存储（`store.GraphStore`）

`GraphStore` 是一个 `store.Store` 可以实现的 **可选能力**
（与 `store.LexicalSearcher` 相同的模式）：

- 内存版 `store.InMemoryStore` 用一个标准库
  邻接图实现它；
- `postgres.Store` 用 `entities`/`relations` 表和
  递归 CTE 遍历实现它 —— **无图数据库**。

当存储是一个 `GraphStore` 时，`Import` 持久化抽取的图，并
在一次 `ReplaceSource` 重新摄入时与其调和（移除陈旧的贡献，
重新抽取的子图做并集合并）。遍历是硬性有界的：深度
≤ 2，并带每跳扇出上限。

### 3. 图检索（`retrieve.GraphRetriever`）

`GraphRetriever` 把一个查询链接到种子实体（`EntityLinker` ——
`LexicalEntityLinker` 是默认），扩展它们的有界邻域，
并按图邻近度给实体的溯源文本块打分。把它接线为
`HybridRetriever` 的 `Graph` 字段：

```go
retrieve.HybridRetriever{
    Dense:     retrieve.DenseRetriever{Embedder: emb, Store: st},
    Lexical:   retrieve.LexicalRetriever{Store: st},
    Structure: retrieve.StructureRetriever{Store: st},
    Graph:     retrieve.GraphRetriever{Store: st},
}
```

它作为第四个倒数排名融合信号融合 —— 绝不替换稠密
或词法。按查询用 `SearchOptions.EnableGraph` 启用它。
图的贡献在 `FusionAttribution.GraphRank` 和
`Diagnostics.GraphTrace`（种子实体、到达的实体、最大跳数）中归因。当
存储还携带被检测到的社区（下方 Tier-3）时，链路添加
`GraphTrace.CommunityIDs` —— 到达的实体所属的社区。

完整、确定性的接线见 `examples/graphrag_example_test.go`，
衡量图信号对检索召回影响的方式见 `eval.RunGraphAB`。

#### 路径排序证据（`GraphRetriever.PathRanker`）

默认情况下 `GraphRetriever` 按图邻近度给 **溯源文本块**
打分 —— 它回答「哪些文本块靠近查询的实体？」但
对那些实体 *如何* 连接只字不提。v0.9 添加了一个选择开启的
**路径排序** 模式，把连接结构本身浮现为一个
排序后的证据制品。

把 `GraphRetriever.PathRanker` 设置为一个 `graph.PathRanker`：

- `graph.WeightedPathRanker{LengthDecay: d}` —— 默认的确定性、
  纯标准库排序器。对每一对无序的、被链接的种子实体，它
  枚举它们之间的简单路径（一次有界 DFS，≤ 2 条边，
  关系按无向处理），并按图中已有的三个信号的复合给每条路径打分：一个 **长度** 衰减
  （`LengthDecay^(edges-1)` —— 更短的路径分数更高；`LengthDecay ≤ 0`
  按 `0.5` 处理）、**边权之积**，以及在连续关系引用了一个共享
  `SourceChunkID` 时的一个小的 **溯源重叠** 奖励（共同佐证的跳排在分散的之上）。
  返回的 `[]graph.RankedPath` 按 `Score` 降序排序，平局
  由拼接的实体 ID 序列打破 —— 一个全序、可复现的顺序
  （基石 KG4-4、KG4-6）。

当设置了一个 `PathRanker` 时，`Retrieve` 在链路上记录两个额外
字段：

- `GraphTrace.Paths` —— 连接查询种子
  实体的 `[]graph.RankedPath`，按确定性的降序分数顺序。每个 `graph.RankedPath`
  携带 `EntityIDs`（有序遍历）、`RelationIDs`（连续实体之间的
  边），以及一个复合 `Score`。
- `GraphTrace.EvidenceSubgraph` —— 检索器遍历过的 `*graph.Subgraph`
  （到达的实体、它们之间的关系，以及每个实体的
  跳数 `Depth`），作为结构化证据对象浮现。

两者都免费搭乘 `Answer.Diagnostics.GraphTrace` —— 与
`GraphTrace.CommunityIDs` 相同的方式。

**路径模式是选择开启且增量的。** 当 `PathRanker` 为 nil（
默认）时，`Paths` 和 `EvidenceSubgraph` 保持 nil，且图检索
**与 v0.7/v0.8 逐字节相同** —— 文本块命中、它们的分数，以及每个其他
链路字段都不动。路径模式只 *添加* 链路输出；它绝不
改变 `Retrieve` 作为命中返回的内容。

```go
ret := retrieve.GraphRetriever{
    Store:      st,
    MaxDepth:   2,
    PathRanker: graph.WeightedPathRanker{}, // path mode on; omit for off
}
// ... wire ret (directly, or as HybridRetriever.Graph), run a query ...
gt := ans.Diagnostics.GraphTrace
if len(gt.Paths) > 0 {
    top := gt.Paths[0] // highest-scored connecting path
    fmt.Println("path:", top.EntityIDs, "score:", top.Score)
    fmt.Println("evidence entities:", len(gt.EvidenceSubgraph.Entities))
}
```

完整、完全确定性的端到端接线（一个 `DictionaryEntityExtractor`
gazetteer、一个内存版存储和一个 `WeightedPathRanker` —— 无实时模型）见
`examples/graphrag_path_example_test.go`。

---

## Tier-3 —— 社区与全局检索

Tier-1 回答 **局部** 问题：「实体 X 是什么、它与什么
相连？」—— 锚定到一个查询，通过邻域遍历检索。
它无法回答 **全局** 的全语料问题 ——「被摄入的所有内容中的
主要主题是什么？」—— 因为没有可供遍历起点的、锚定到查询的
实体。

Tier-3 添加了那条路径。它把知识图谱归组为一个 **社区
层级**，让模型为每个社区撰写一份 **摘要报告**，并
通过 **对那些报告做 map-reduce** 来回答全局问题。它是一条
与 Tier-1 `Ask` 分离的答案路径（`System.AskGlobal`）—— 它绝不
运行 retrieve、rerank 或 pack。

### 1. 社区检测（`graph.CommunityDetector`）

一个 `CommunityDetector` 把一个 `graph.Graph` 划分成一个
`graph.Community` 聚类的层级。提供两个实现，两者都 **确定性
且纯标准库** —— 同一个图总是产出同一个 `[]Community`，
逐字节一致（基石 KG3-6）：

- `graph.LouvainDetector{Resolution: r}` —— 层级化默认。运行
  标准的两阶段 Louvain 方法（局部模块度增益贪心
  移动，然后粗化为超级节点）并重复。每个粗化
  过程产出一个层级层级：`Level` 0 是最细的划分；每个
  更高层级把它下面的层级归组，由 `ParentID` 链接。
  可选的 `Resolution` 旋钮缩放模块度零模型项 ——
  `> 1` 的值偏好更小的社区，`< 1` 的值偏好更大的；`<= 0` 按
  经典模块度（`1.0`）处理。
- `graph.LabelPropagationDetector{}` —— 更简单的单层
  替代方案。每个实体从自己的社区开始，然后采纳
  其邻居中最大入射边权所携带的标签，
  反复扫描直至收敛。它产出一个层级（`Level` 0）—— 无
  层级。

一个 `graph.Community` 携带其 `ID`、`Level`、`ParentID`（顶层
时为 `""`），以及其成员的已排序 `EntityIDs` / `RelationIDs`。社区
ID 是层级和聚类成员的确定性函数，所以
该层级是可黄金测试的输出。

把一个检测器接线到 `rag.Options.CommunityDetector`。当它被设置 **且**
存储实现了 `store.CommunityStore` 时，`Import` 在图被持久化后
检测命名空间图上的社区层级，且
`UpsertCommunities` 存储它。重新检测是 **每命名空间全量** 的：
每次重新摄入都重新检测整个命名空间并替换已存储的集合
（`UpsertCommunities` 是全量替换）。一个 nil 检测器，或一个不是
`CommunityStore` 的存储，会让社区保持未检测 —— `Import` 的行为
与之前完全相同。

`store.CommunityStore` 是一个可选能力，是
`GraphStore` 的同类：消费者对它做类型断言，并在一个
存储不实现它时优雅降级。`store.InMemoryStore` 实现它；`postgres.Store` 也实现它。社区集合是每命名空间的。

### 2. 社区摘要（`graph.CommunitySummarizer`）

一个社区本身只是一组实体 ID。为了对它做 map-reduce，
模型为每个社区撰写一份简短的 **`graph.CommunityReport`** —— 一个标题和一个
段落摘要。该接缝是
`graph.CommunitySummarizer`：

- `graph.LLMCommunitySummarizer{Model: m}` —— 用社区的成员实体和关系
  提示一个 `generate.Model`，宽容地
  解析一个标题和一个段落（一个畸形的响应绝不致命，镜像
  `LLMEntityExtractor`）。一个 nil `Model` 返回
  `graph.ErrCommunitySummarizerModelRequired`。

把它接线到 `rag.Options.CommunitySummarizer`。

#### 默认惰性，选择开启则及早

生成一份报告每个社区花费一次模型调用。v0.8 的设计
选择（基石 KG3-2）是让报告 **默认惰性**：

- **惰性** —— `System.AskGlobal` 只为一个给定查询实际选中的社区
  生成报告，且只在 **缓存未命中** 时。每份报告
  在 `CommunityStore` 上以 `graph.CommunityContentHash` 为键缓存 —— 一个
  对社区已排序成员的确定性 SHA-256。一个
  成员未变的重新检测社区复用其缓存报告；
  任何成员变化都会翻转哈希并强制一次新的摘要。摘要的成本
  增量地支付，只为被查询的社区支付，且
  对一个稳定的社区绝不支付两次。

- **及早** —— `System.PrewarmCommunityReports(ctx, namespace)` 走遍一个
  命名空间中的每个社区，并生成 + 持久化任何缺失
  或陈旧的报告，返回生成的数量。它使用 *同一个*
  摘要器和 *同一个* `CommunityStore` 支撑的缓存，与惰性路径一致
  —— 它只是提前支付成本，以便第一个全局查询全部
  缓存命中地运行。一份已经新鲜的报告保持不动。

**这个权衡：** 惰性把摘要成本分摊到各查询，且绝不
摘要无人问及的社区，但第一个触及一个冷
社区的查询内联支付其摘要延迟。及早把 *每个*
社区的成本前置 —— 包括可能永远不被查询的社区 ——
以换取此后均匀的快速全局查询。当
全局查询延迟必须可预测时（且语料足够稳定
以致前置成本能摊销）做预热；否则保持惰性。两条路径共享
一个缓存，所以一次预热后跟惰性查询，或反过来，组合
时不产生冗余生成。

一次缓存未命中且无配置的摘要器返回
`rag.ErrCommunitySummarizerRequired` —— 惰性路径上来自 `AskGlobal`，
及早路径上来自 `PrewarmCommunityReports`。

### 3. 全局检索（`System.AskGlobal`）

`System.AskGlobal(ctx, question, GlobalOptions)` 通过 **对社区报告做
map-reduce** 来回答一个全语料问题。它是一条与 `Ask` **分离的
答案路径**：它绝不调用 retrieve、重排器或
打包器。流程是 *select → lazy report → map → reduce*：

1. **Select** —— 选取最粗的社区层级（最宽泛的主题）。如果
   该层级有超过 `GlobalOptions.MaxCommunities` 个社区，
   按查询 token 与成员实体名称的重叠给它们排序，
   并截断到前 N（平局由社区 ID 打破 —— 完全确定性）。
2. **Report** —— 为每个选中的社区解析一份 `CommunityReport`：当且仅当其 `ContentHash` 仍与活社区匹配时，`CommunityStore` 上的缓存
   命中被复用，否则该报告被惰性摘要
   并缓存。
3. **Map** —— 每份报告一次模型调用：判断该社区对答案贡献多少，并撰写一个部分答案外加一个自评的
   有用度分数。
4. **Reduce** —— 丢弃分数为 0 的部分答案，按分数给幸存者排序，并
   做一次模型调用合成最终答案。

`GlobalOptions` 刻意很小 —— v0.8 把全局检索固定在
最粗层级，所以唯一的旋钮是 `Namespace` 和 `MaxCommunities`（一个
`<= 0` 的值选择一个合理的默认）。

`Answer.Diagnostics.Global`（一个 `rag.GlobalDiagnostics`）归因该次运行：
咨询过的 `CommunityIDs`、每社区的 `MapScores`、`MapCalls` /
`ReduceCalls` 计数，以及 `ConsultedReports` —— map 步骤
实际运行过的报告（评估器从 `Answer` 上读取的
依据上下文）。

优雅降级匹配 Tier-1 图信号：一个不
实现 `store.CommunityStore` 的存储，或一个无被检测到的
社区的命名空间，产出一个空 `Answer` 且无错误。一个 nil 模型返回
`rag.ErrModelRequired`。

#### 接线全局检索

```go
sys := rag.New(rag.Options{
    Store:               st, // an InMemoryStore / postgres.Store — a CommunityStore
    Model:               model,
    EntityExtractor:     graph.DictionaryEntityExtractor{Terms: gazetteer},
    CommunityDetector:   graph.LouvainDetector{},          // detect at Import
    CommunitySummarizer: graph.LLMCommunitySummarizer{Model: model}, // write reports
})

// Import builds the graph and detects the community hierarchy.
if _, err := sys.Import(ctx, docs, ingest.ImportOptions{Namespace: "kb"}); err != nil {
    return err
}

// Optional: pay summarization cost up front so the first query is all-cache-hits.
if _, err := sys.PrewarmCommunityReports(ctx, "kb"); err != nil {
    return err
}

// Global search — a whole-corpus "sense-making" question.
answer, err := sys.AskGlobal(ctx, "What are the main themes across the corpus?",
    rag.GlobalOptions{Namespace: "kb", MaxCommunities: 8})
if err != nil {
    return err
}
fmt.Println(answer.Text)
```

完整、完全确定性的端到端接线（一个 `DictionaryEntityExtractor`
gazetteer、一个 `LouvainDetector`，以及一个服务于
summarize / map / reduce 步骤的单一脚本化 `generate.Model`）见
`examples/graphrag_global_example_test.go`。

### 4. 模糊实体消解（`graph.EntityResolver`）

Tier-1 规范化是 **仅精确匹配** 的：`graph.Canonicalize`
合并共享一个 `(NormalizeName, type)` 键的实体，所以「Acme」和
「Acme Corp」保持两个分离的节点。模糊消解闭合该缺口。

`graph.EntityResolver` 是一个 **在 `Canonicalize` 之前的选择开启前置过程**
（基石 KG3-8）—— `Canonicalize` 及其测试不动：

- `graph.NoopEntityResolver{}` —— **默认**。原样返回其
  输入；有了它，`Import` 与模糊消解之前的行为
  逐字节相同。
- `graph.EmbeddingEntityResolver{Embedder: e, Threshold: t}` —— 通过一个 `embed.Embedder`
  嵌入每个实体的 `Name`，聚类余弦相似度至少为 `Threshold` 的 **同 `Type`**
  实体，并把每个成员实体名称 —— **以及每个匹配的关系端点** ——
  重写为每个聚类一个的规范表面形式（最长的成员名称，平局
  由字典序最低打破）。

重写关系端点不是可选的：`Canonicalize` 按名称解析
关系端点，并 **丢弃一个端点不匹配任何实体的关系**。一个在实体上把「Acme」→「Acme Corp」重写但
留下一个仍命名「Acme」的关系的消解器会静默地使该关系成为孤儿 ——
所以 `EntityResolver` 接缝一致地重写两者。

该消解器 **从构造上是确定性的**（基石 KG3-6）：
实体按名称排序顺序处理，聚类是在一个固定排序对扫描上的单链接，规范名称由一个固定
规则选择，且无随机性 —— 同一个输入总是产出同一个
输出。它针对一个返回固定向量的脚本化嵌入器做单元测试。

把它接线到 `rag.Options.EntityResolver`。`graph` 获得一个 `embed` 导入以
支持 `EmbeddingEntityResolver` —— `embed` 是一个仅标准库的叶子包，
所以无依赖循环、无新 module 依赖。

#### 假阳性警告 —— 保守发货

嵌入相似度合并可能 **错误**：公司「Apple」和
水果「Apple」嵌入得很接近，但它们是不同实体；两个
名称嵌入相似的无关人物不应坍缩为一个
节点。一次错误合并是 **破坏性的** —— 它混淆两个真实实体，且
在下游无法撤销。

因此 v0.8 把 `EmbeddingEntityResolver` 刻意发货得保守：

- **选择开启。** 默认 `NoopEntityResolver` 什么都不做；除非显式
  接线，模糊消解绝不开启。
- **高默认阈值。** 当 `Threshold <= 0` 时消解器使用
  `0.92` —— 一个险些匹配的保持两个节点，而非冒一次假合并的风险。
- **仅同类型。** 不同 `Type` 的实体绝不合并 —— 一个人物
  绝不被折叠进一个组织。

诚实的措辞：模糊消解用召回（把「Acme」/
「Acme Corp」作为一个实体捕获）换取一次假合并的精确率风险。
v0.8 选择精确率。如果出现假阳性就提高 `Threshold`；
谨慎地降低它，且只在有一个代表性语料可对照检查时。
消解质量改进是一个 v0.9 项（下方）。

### 5. 评估全局检索（`eval.GlobalEvaluator`）

`eval.RunGraphAB` 衡量 **文本块 recall@k** —— Tier-1 局部检索的
正确指标，对全局检索无意义，后者用 **无黄金文本块集合** 合成一个
答案。v0.8 为全局路径添加了一个独立的测试套件：

- `eval.GlobalAsker` —— `*rag.System` 通过 `AskGlobal` 满足的接缝；
  `eval.Asker` 的全局检索对应物。
- `eval.GlobalEvaluator{Asker, Judge, MaxCommunities}` —— 通过 `AskGlobal` 运行一个全语料问题的 `Dataset`，并用
  RAG-Triad `Judge` 给每个答案打分。judge 的依据上下文是答案实际咨询过的社区
  报告（`Answer.Diagnostics.Global.ConsultedReports`），所以全局检索的
  依据性读作「答案是否有依据于它读过的社区报告」；
  答案相关性是问题对答案。
- `eval.GlobalEvalResult` —— 携带 `MeanGroundedness`、
  `MeanAnswerRelevance` 和每示例明细。它刻意 **不** 携带
  文本块 recall@k / precision@k：全局检索没有文本块召回
  概念。`RunGraphAB` / `Evaluator` 仍然是局部信号的文本块召回路径。

该测试套件由一个脚本化模型 + 脚本化 judge 的 CI 门禁演练，即
项目标准的确定性评估纪律。

---

## DRIFT 检索 —— 混合答案路径（`System.AskDrift`）

`Ask`（+ `GraphRetriever`）回答 **局部** 问题 —— 锚定到一个
查询，通过邻域遍历检索。`AskGlobal` 回答 **全局**
全语料问题 —— 对社区报告做 map-reduce。许多真实
问题位于两者之间：它们既需要对语料的宽泛感知，*又* 需要
只有图遍历才能浮现的具体细节。v0.9 为此恰好添加了第三条答案
路径 —— **DRIFT 检索**（Dynamic Reasoning and Inference
with Flexible Traversal）。

`System.AskDrift(ctx, question, DriftOptions)` 是一条 **分离的答案路径**，
而非 `Ask` 或 `AskGlobal` 上的一个模式标志，也不是一个 `Retriever`。它绝不
调用 retrieve、重排器或打包器流水线；相反它
编排 `AskGlobal` 的引导部件和直接图遍历。
流程是 *primer → bounded local follow-up loop → synthesis*：

1. **Primer** —— 一次全局过程：选取最粗层级的社区，解析
   它们的报告（与 `AskGlobal` 相同的惰性 `CommunityStore` 缓存），并
   运行 map 步骤 —— 每份报告一次模型调用，得到一个带分数的部分答案。
   map 步骤打分高于零的社区的成员实体
   成为局部循环的 **第 0 轮种子实体**。（当存储不是
   一个 `CommunityStore`，或命名空间无社区时，primer 就是
   空的 —— DRIFT 降级为一个仅局部的答案，无错误。）
2. **局部后续循环** —— 硬性有界。对每一轮，DRIFT 遍历
   当前种子实体的 1 跳邻域，把它们的
   溯源文本块打包进上下文，向模型索要一个部分答案外加一个
   **后续实体名称** 的短列表，并把那些名称解析为
   下一轮的种子。该循环在以下之一首次发生时终止：达到轮次
   上限；模型不发出新的后续实体；没有新实体
   可达。它 **从构造上有界** —— 它不会失控。
3. **Synthesis** —— 一次模型调用把引导部分答案和每个局部
   轮次的部分答案折叠进最终的 `Answer.Text` —— 结构上是
   `AskGlobal` 的 reduce 步骤。

### 预算与轮次上限

`DriftOptions` 很小，且每个旋钮都有界：

```go
type DriftOptions struct {
    Namespace      string // which namespace's communities + graph to search
    MaxCommunities int    // primer breadth; <= 0 -> default 8
    Rounds         int    // local follow-up rounds; <= 0 -> default 2, hard cap 3
    TopK           int    // provenance chunks packed per local round; <= 0 -> default 8
}
```

`Rounds` 在循环运行 *之前* 被钳制到 `[1, 3]` —— `0` 的值
变成默认 `2`，高于 `3` 的值被锚定到硬上限 `3`。
因此局部循环无论调用方（或模型的后续）要求什么都
绝不能超过三次迭代。这是刻意的：
DRIFT 的局部循环由模型驱动，而一个无界的模型驱动循环是一个
成本和延迟隐患。一次 `AskDrift` 的总 LLM 预算是
`MaxCommunities` 个引导 map 调用 + 至多 `Rounds` 个局部轮次调用 + 1 个
合成调用 —— 全部由该次运行的 `obs.Counter` 计数。

### 诊断 —— `Answer.Diagnostics.Drift`

`Answer.Diagnostics.Drift`（一个 `rag.DriftDiagnostics`）归因该次运行：

- `PrimerCommunityIDs` —— primer 映射过的社区；
- `Rounds` —— 实际运行的局部轮次数（≤ 钳制后的上限）；
- `RoundEntityIDs` —— 每轮从中遍历的种子实体 ID，按顺序
  （每个列表已排序并去重 —— 该编排是可黄金测试的）；
- `ConsultedReports` —— primer 的社区报告，评估器从 `Answer` 上读取的依据上下文（镜像
  `Diagnostics.Global.ConsultedReports`）。

### 接线 DRIFT 检索

```go
sys := rag.New(rag.Options{
    Store:               st, // an InMemoryStore / postgres.Store — a CommunityStore + GraphStore
    Model:               model,
    EntityExtractor:     graph.DictionaryEntityExtractor{Terms: gazetteer},
    CommunityDetector:   graph.LouvainDetector{},                     // detect at Import
    CommunitySummarizer: graph.LLMCommunitySummarizer{Model: model},  // primer reports
})

// Import builds the graph and detects the community hierarchy.
if _, err := sys.Import(ctx, docs, ingest.ImportOptions{Namespace: "kb"}); err != nil {
    return err
}

// DRIFT search — a primer pass, a bounded local loop, and a synthesis step.
answer, err := sys.AskDrift(ctx, "how did mechanical computing begin",
    rag.DriftOptions{Namespace: "kb", MaxCommunities: 8, Rounds: 2})
if err != nil {
    return err
}
fmt.Println(answer.Text)
fmt.Println("primer communities:", len(answer.Diagnostics.Drift.PrimerCommunityIDs))
fmt.Println("local rounds run:", answer.Diagnostics.Drift.Rounds)
```

一个 nil 模型返回 `rag.ErrModelRequired`；一次缓存未命中且无配置的
摘要器返回 `rag.ErrCommunitySummarizerRequired`。

完整、完全确定性的端到端接线（一个 `DictionaryEntityExtractor`
gazetteer、一个 `LouvainDetector`，以及一个服务于
摘要器、引导 map 步骤、每个局部轮次和合成的单一脚本化 `generate.Model`）见
`examples/graphrag_drift_example_test.go`。

### 评估 DRIFT 检索（`eval.DriftEvaluator`）

DRIFT 和全局检索一样，用 **无黄金文本块集合** 合成一个答案，
所以文本块 recall@k 对它无意义。v0.9 添加了一个生成侧的测试套件，
镜像 `GlobalEvaluator`：

- `eval.DriftAsker` —— `*rag.System` 通过 `AskDrift` 满足的接缝；
  `eval.GlobalAsker` 和 `eval.Asker` 的 DRIFT 对应物。
- `eval.DriftEvaluator{Asker, Judge, MaxCommunities, Rounds}` —— 通过 `AskDrift` 运行一个全语料问题的
  `Dataset`，并用 RAG-Triad `Judge` 给每个
  答案打分。judge 的依据上下文是
  primer 咨询过的社区报告
  （`Answer.Diagnostics.Drift.ConsultedReports`），所以 DRIFT 依据性读作
  「答案是否有依据于 primer 读过的社区报告」；
  答案相关性是问题对答案。
- `eval.DriftEvalResult` —— 携带 `MeanGroundedness`、`MeanAnswerRelevance`
  和每示例明细。和 `GlobalEvalResult` 一样它刻意携带
  **无** 文本块 recall@k / precision@k。

该测试套件由一个脚本化模型 + 脚本化 judge 的 CI 门禁演练 ——
项目标准的确定性评估纪律。

---

## 推迟到 v1.0+

v0.9 收尾 GraphRAG 精化里程碑：**路径排序证据**
（`GraphRetriever.PathRanker`）和 **DRIFT 检索**（`System.AskDrift`）—— v0.8 明确推迟的
两个项 —— 均在上方发货。有了它们，全部三条
答案路径都存在：`Ask`（局部）、`AskGlobal`（全局）和 `AskDrift`（
混合）。其余部分明确 **不在** v0.9 中，并被带到 v1.0+：

- **增量社区维护** —— 每次重新摄入仍然做 **每命名空间
  全量重新检测**（`UpsertCommunities` 是全量替换），且
  `AskGlobal` 的 `ContentHash` 缓存随后重新摘要每个成员
  移动过的社区。只增量更新一次重新摄入实际触及的社区 ——
  并只选择性地使它们的报告失效 —— 被
  推迟。**性能剖析触发条件：** 只在社区检测
  （`CommunityDetector.Detect`）在一个真实语料上可测量地主导重新摄入成本时
  重新审视这一点。在那份剖析存在之前，全量重新检测是正确、简单
  且足够快的 —— 增量维护是没有已证明回报的
  附加复杂度。
- **声明 / 协变量抽取** —— v0.9 只抽取实体和类型化
  关系。Microsoft GraphRAG 还抽取 *声明*（协变量 ——
  关于一个实体的时间范围限定的事实陈述）。在 `EntityExtractor`
  之外添加一个声明抽取接缝，并在社区报告
  和 DRIFT primer 中浮现声明，是一个 v1.0+ 项。
- **一个专用图数据库** —— GraphRAG 仍然完全运行在
  现有存储上：测试和小语料用 `store.InMemoryStore`，
  生产用 `postgres.Store`（在 `entities`/`relations`
  表上的递归 CTE 遍历）。`GraphStore` 和 `CommunityStore` 是接口，
  所以一个 `neo4jgraph` 风格的子包 —— **Neo4j 是一个未来的 `GraphStore`
  实现** —— 可以在完全隔离的情况下稍后添加，不触及
  任何现有代码。Postgres 上的递归 CTE 遍历覆盖本 SDK 的
  规模；只在遍历深度或图大小超出它时才有理由用图数据库，
  而本里程碑的范围并未如此。
- **模糊消解质量改进** —— v0.8 的
  `EmbeddingEntityResolver` 刻意保守（高阈值、
  仅同类型、单链接聚类）。更好的聚类、类型感知的
  阈值、描述感知的嵌入，以及合并的审计轨迹
  仍被推迟。
