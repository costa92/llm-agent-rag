package eval

import (
	"context"

	"github.com/costa92/llm-agent-rag/rag"
)

// GraphABResult compares retrieval metrics with the GraphRAG signal off
// versus on, so the graph's effect on recall is measurable.
type GraphABResult struct {
	GraphOff    Metrics // GraphOff is the retrieval scoreboard with the graph signal off.
	GraphOn     Metrics // GraphOn is the retrieval scoreboard with the graph signal on.
	RecallDelta float64 // RecallDelta is GraphOn.RecallAtK - GraphOff.RecallAtK.
	MRRDelta    float64 // MRRDelta is GraphOn.MRR - GraphOff.MRR.
}

// RunGraphAB scores dataset twice through retriever — once with
// EnableGraph off, once on — and reports the metrics of each arm plus the
// recall/MRR delta. base supplies the shared SearchOptions; its
// EnableGraph field is overridden per arm. It answers the question "does
// the graph signal improve retrieval on this dataset?".
func RunGraphAB(ctx context.Context, retriever Retriever, base rag.SearchOptions, dataset Dataset) (GraphABResult, error) {
	off := base
	off.EnableGraph = false
	offRes, err := RetrievalEvaluator{Retriever: retriever, Options: off}.Run(ctx, dataset)
	if err != nil {
		return GraphABResult{}, err
	}
	on := base
	on.EnableGraph = true
	onRes, err := RetrievalEvaluator{Retriever: retriever, Options: on}.Run(ctx, dataset)
	if err != nil {
		return GraphABResult{}, err
	}
	return GraphABResult{
		GraphOff:    offRes.Metrics,
		GraphOn:     onRes.Metrics,
		RecallDelta: onRes.Metrics.RecallAtK - offRes.Metrics.RecallAtK,
		MRRDelta:    onRes.Metrics.MRR - offRes.Metrics.MRR,
	}, nil
}
