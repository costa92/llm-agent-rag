package eval_test

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/eval"
	"github.com/costa92/llm-agent-rag/rag"
	"github.com/costa92/llm-agent-rag/store"
)

// abStubRetriever is a deterministic eval.Retriever: with the graph signal
// off it returns one gold chunk, with it on it surfaces a second — the
// shape of a graph signal that improves recall. (Faithful end-to-end graph
// retrieval is covered by retrieve/graph_test.go; this gate verifies the
// RunGraphAB harness itself.)
type abStubRetriever struct{}

func (abStubRetriever) Retrieve(_ context.Context, _ string, opts rag.SearchOptions) ([]store.Hit, error) {
	hits := []store.Hit{{Chunk: store.StoredChunk{ID: "c1", DocID: "d1"}, Score: 1}}
	if opts.EnableGraph {
		hits = append(hits, store.Hit{Chunk: store.StoredChunk{ID: "c2", DocID: "d2"}, Score: 0.5})
	}
	return hits, nil
}

func TestRunGraphAB(t *testing.T) {
	ds := eval.Dataset{
		Name: "graph-ab",
		TopK: 5,
		Examples: []eval.Example{
			{Query: "q", Namespace: "ns", GoldDocIDs: []string{"d1", "d2"}, GoldChunkIDs: []string{"c1", "c2"}},
		},
	}
	res, err := eval.RunGraphAB(context.Background(), abStubRetriever{}, rag.SearchOptions{}, ds)
	if err != nil {
		t.Fatalf("RunGraphAB: %v", err)
	}
	if res.GraphOff.Examples != 1 || res.GraphOn.Examples != 1 {
		t.Fatalf("both arms should have run the dataset: off=%+v on=%+v", res.GraphOff, res.GraphOn)
	}
	if res.GraphOn.RecallAtK < res.GraphOff.RecallAtK {
		t.Fatalf("graph-on recall %v < graph-off %v — the graph signal regressed recall",
			res.GraphOn.RecallAtK, res.GraphOff.RecallAtK)
	}
	if res.RecallDelta <= 0 {
		t.Fatalf("RecallDelta = %v, want > 0 (the stub graph signal adds a gold doc)", res.RecallDelta)
	}
}

func TestRunGraphABRejectsNilRetriever(t *testing.T) {
	ds := eval.Dataset{Name: "d", TopK: 3, Examples: []eval.Example{{Query: "q"}}}
	if _, err := eval.RunGraphAB(context.Background(), nil, rag.SearchOptions{}, ds); err == nil {
		t.Fatalf("RunGraphAB with nil retriever: want an error")
	}
}
