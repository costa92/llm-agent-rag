# Self-RAG 反思设计

## 目标

为 `rag.System.Ask` 添加一条有界的 Self-RAG 路径，使调用方能通过参数选择 `rule`、`model` 或 `hybrid` 反思模式，同时保持现有的单轮 `Ask` API 形态，并保留当前的检索、rerank、pack、prompt 和生成接缝。

## 范围

本 v1 设计仅添加推理时的反思编排。

包含：

- `Ask` 上参数化的反思模式选择
- 有界的多轮检索/生成循环
- 规则驱动、模型驱动和混合的决策模式
- 轮级诊断和顶层聚合的链路/指标
- 测试和 API 快照更新

排除：

- token 级的 Self-RAG 控制 token 或微调特定行为
- 训练流水线或基准测试数据集导入
- 新的公共 `AskSelfRAG` API
- 对 `retrieve.Retriever`、`pack.Packer`、`prompt.Template` 或 `generate.Model` 的改动
- 从 `rag` 内部对 `eval` 包依赖的改动

## 现有约束

- `rag.System.Ask` 当前是一条在 `retrieve -> rerank -> pack -> sanitize -> render -> generate` 之上的单一直线编排。
- `retrieve.Trace` 已经捕获查询塑形和路由细节，但 `rag.Answer.Diagnostics` 只暴露单轮视图。
- `Observer.OnAsk` 和 `Observer.OnRetrieve` 已被外部消费，所以新行为必须保持增量且有文档记载。
- `eval` 导入 `rag`，所以 `rag` 不能在不制造循环的情况下导入 `eval`。

## 所选架构

### 公共 API

保持 `rag.System.Ask` 作为唯一的公共答案入口点。

用以下扩展 `rag.AskOptions`：

- `Reflection *ReflectionOptions`

在 `rag` 中添加新的公共类型：

- `type ReflectionMode string`
- `type ReflectionOptions struct { ... }`

`ReflectionMode` 值：

- `""` 或 `off`
- `rule`
- `model`
- `hybrid`

`ReflectionOptions` v1 字段：

- `Mode ReflectionMode`
- `MaxRounds int`
- `MinHits int`
- `MinScore float64`
- `MinUniqueDocs int`
- `RequireCitations bool`
- `AllowRewrite bool`
- `FailOpen bool`

V1 刻意把公共配置保持得小。更高级的旋钮可以在不改变编排边界的情况下稍后添加。

### 内部结构

重构 `rag/ask.go`，使当前的单趟逻辑移入一个私有辅助函数：

- `askRound(ctx, originalQuestion, query string, opts AskOptions) (roundResult, error)`

`roundResult` 是一个携带以下内容的私有类型：

- 该轮的最终 `Answer` 数据
- 原始 `retrieve.Trace`
- 轮局部指标
- 派生的规则信号，如命中数、最高分和唯一文档数

`System.Ask` 变成一个有界循环：

1. 初始化顶层 `obs.Counter`
2. 如果反思为 `off`，运行一次 `askRound` 并返回与今天相同的语义
3. 否则运行至多 `MaxRounds` 轮
4. 每轮之后，评估配置的反思策略
5. 在策略决策或硬上限时停止
6. 将最终被采纳的轮次作为 `Answer.Text/Hits/Citations/Prompt` 返回
7. 附上完整的反思诊断信息和聚合指标

### 反思决策模型

决策接缝在 `rag` 内部；它不是一个新的跨包依赖。

添加一个私有的反思策略抽象，带三个内置实现：

- 规则策略
- 模型策略
- 混合策略

该抽象决定：

- 是否停止
- 是否继续
- 是否重写下一次检索查询
- 为何做出该决策

V1 模型驱动的反思使用现有的 `generate.Model` 加一个内部提示词。它不复用 `eval.Judge`，因为那会制造一个导入循环。

## 决策语义

### Rule 模式

Rule 模式仅使用该轮的结构化信号：

- 检索到的命中数
- 最高检索分数
- 唯一支持文档
- 打包的引用数
- 当前轮次是否相对前一轮实质改善

如果阈值被满足，停止。

如果阈值未被满足：

- 当还允许另一轮时继续
- 如果启用了 `AllowRewrite` 则可选地重写

### Model 模式

Model 模式在答案轮之后执行第二次模型调用，以判断：

- 证据是否充分
- 是否需要另一个检索轮次
- 下一轮是否应使用一个重写后的查询

该决策始终锚定到原始用户问题，而非重写后的查询。这保留了相对于用户实际任务的相关性。

### Hybrid 模式

Hybrid 模式先应用规则检查。

- 如果规则检查明确满足停止条件，则在不做反思模型调用的情况下停止
- 否则调用模型策略来决定继续 vs 重写 vs 停止

这让简单情形保持廉价，并让含糊情形使用模型判断。

## 查询重写语义

当启用重写时，下一轮可以使用一个重写后的检索查询，但：

- 原始问题在 `Answer.Trace.Question` 中仍然是面向用户的问题
- 相同的 `SearchOptions` 向前传递
- 正常的查询预处理器仍然在重写后的查询上运行

这意味着重写可以与现有的 MQE 或 HyDE 行为叠加。这是可接受的，但必须记录，因为它改变了用户如何解读较晚轮次的检索链路。

## 诊断与链路

`Answer.Text`、`Answer.Hits`、`Answer.Citations` 和 `Answer.Prompt` 只代表被采纳的最终轮次。

向 `rag.Diagnostics` 和 `rag.Trace` 添加增量的反思字段。

建议的公共结构：

- `ReflectionDiagnostics`
- `ReflectionRoundDiagnostics`
- `ReflectionTrace`
- `ReflectionRoundTrace`

每一轮至少应记录：

- 轮次索引
- 输入查询
- 来自检索链路的有效查询
- 是否应用了重写
- 返回的文本块 ID
- 打包的文本块 ID
- 唯一文档数
- 最高分
- 决策：`stop`、`continue`、`rewrite_and_continue`
- 决策模式：`rule`、`model`、`hybrid`
- 决策原因
- 轮指标摘要

顶层反思诊断应记录：

- 配置的模式
- 运行的总轮数
- 被采纳的轮次索引
- 停止原因
- 是否发生了 fail-open 回退

## Observer 与仪表语义

`Observer.OnAsk` 保持为单个顶层成功回调。它接收最终 `Trace`，后者现在包括反思细节。

`Observer.OnRetrieve` 继续为每次内部检索调用触发。在反思模式下这意味着一次顶层 `Ask` 可能触发多个 `OnRetrieve` 回调。这是一个行为变更，必须记录。

## 指标与 token 计量

当前 `Ask` 的 token 计量只从最终生成请求/响应派生 token 用量。一旦存在多轮，这就不正确了。

V1 必须聚合：

- 跨所有轮次的生成调用
- 跨所有轮次的嵌入调用
- 总的顶层墙钟时间
- 跨答案生成和反思模型生成求和的 token 用量

如果无法从模型响应获得精确 token 用量，估算仍然可接受，但聚合必须包括每一轮而非只是最终一轮。

## 错误处理

第一轮失败仍是硬失败，与今天一样。

一个可用轮次之后的反思阶段失败，在 `FailOpen` 为 true 时应默认为 fail-open 行为：

- 返回可用的最佳轮次
- 用 `reflection_error_fallback` 标记诊断信息
- 在诊断信息中保留错误原因

当 `FailOpen` 为 false 时，应返回反思错误。

这让该特性对偏好降级可用性而非不必要硬失败的生产调用方保持安全。

## 测试策略

### 单元测试

添加涵盖以下内容的 `rag` 测试：

- `off` 模式保持当前单轮行为
- 当阈值被满足时 `rule` 模式在一轮内停止
- 当阈值失败时 `rule` 模式执行一个额外轮次
- `rule` 模式在 `MaxRounds` 处停止
- 当脚本化模型反思如此指示时 `model` 模式重写并继续
- 当脚本化反思指示停止时 `model` 模式立即停止
- 当规则阈值明确停止时 `hybrid` 模式跳过模型反思
- 当规则阈值不确定时 `hybrid` 模式调用模型反思
- 反思诊断记录每轮的查询、ID、决策和停止原因
- 在反思模式下 `OnRetrieve` 每轮触发一次
- 当多轮运行时聚合指标包括多于一次的生成调用

### Eval 复用

针对最终的 `Ask` 实现复用 `eval.TriadEvaluator`。v1 不需要新的 eval 包集成。

添加至少一个小的数据集驱动回归测试，其中：

- 基线单轮 `Ask` 表现欠佳
- 启用反思的 `Ask` 到达第二轮
- 最终依据性或 grounding-at-k 不回退

### API 面

更新 `api/v1.snapshot.txt`，因为导出的 `rag` 类型改变。

## 预期会改动的文件

- `rag/options.go`
- `rag/ask.go`
- `rag/system.go`
- `rag/observer_test.go`
- `rag/system_test.go`
- `rag/instrument_test.go`
- `api/v1.snapshot.txt`
- `README.md`
- `docs/production-deployment.md`

如果 `ask.go` 变得过大，可能添加一个聚焦的辅助文件：

- `rag/reflection.go`

## 风险与护栏

主要风险：

- 顶层指标与多轮执行变得不一致
- `OnRetrieve` 回调计数变化可能让 otel 消费者意外
- 重写加 MQE/HyDE 叠加在没有清晰文档时可能让用户困惑
- 反思决策可能在未变的证据上循环，除非去重/早停逻辑是显式的

护栏：

- 强制 `MaxRounds >= 1`
- 当重写后的查询未变时早停
- 当检索到的文本块集合无实质改善时早停
- 保持所有新公共字段增量
- 在新的反思诊断之外，保持最终答案语义与今天相同

## 评审说明

该设计刻意被限定为一个最小、可生产使用的推理时 Self-RAG。它避免新的包循环，避免拓宽底层接口，并把改动集中在 `rag.Ask` 中 —— 现有编排已经驻留于此。
