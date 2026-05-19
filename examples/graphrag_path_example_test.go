package examples

import (
	"context"
	"fmt"
	"strings"

	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/rag"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

// Example_graphRAGPaths shows the v0.9 path-ranked subgraph-as-evidence
// feature end to end: a deterministic entity extractor builds a knowledge
// graph at ingest, a GraphRetriever is wired with a graph.WeightedPathRanker
// (path mode), and a query that links to two ends of a multi-hop chain has
// its connecting path ranked and surfaced on Diagnostics.GraphTrace.
//
// Path mode is opt-in and additive: the only thing the PathRanker adds is
// extra trace output (GraphTrace.Paths + GraphTrace.EvidenceSubgraph) — the
// chunk hits and their scores are byte-identical to path mode off. The
// example uses no live model — the project's example discipline.
func Example_graphRAGPaths() {
	st := store.NewInMemoryStore(32)

	sys := rag.New(rag.Options{
		Model: echoModel{},
		Store: st,
		// A deterministic, zero-LLM extractor — single-word entity names so
		// the default LexicalEntityLinker resolves them from query tokens.
		EntityExtractor: graph.DictionaryEntityExtractor{Terms: map[string]string{
			"Lovelace": "person",
			"Babbage":  "person",
			"Engine":   "machine",
		}},
		// A bare GraphRetriever with a WeightedPathRanker — path mode on.
		// With PathRanker nil this retriever is byte-identical to v0.7/v0.8.
		Retriever: retrieve.GraphRetriever{
			Store:      st,
			MaxDepth:   2,
			PathRanker: graph.WeightedPathRanker{},
		},
	})

	// A small fixed corpus whose entities form a known two-hop chain:
	// Lovelace — co-occurs — Babbage — co-occurs — Engine.
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "Lovelace worked with Babbage."},
		{ID: "d2", Content: "Babbage designed the Engine."},
	}, ingest.ImportOptions{Namespace: "history"}); err != nil {
		panic(err)
	}

	// "Lovelace Engine" links to the two ends of the chain, so the retriever
	// ranks the simple path connecting them through Babbage.
	ans, err := sys.Ask(context.Background(), "Lovelace Engine", rag.AskOptions{
		Search: rag.SearchOptions{Namespace: "history", TopK: 10},
	})
	if err != nil {
		panic(err)
	}

	gt := ans.Diagnostics.GraphTrace
	top := gt.Paths[0]
	fmt.Println("top ranked path:", strings.Join(top.EntityIDs, " -> "))
	fmt.Println("path edges:", len(top.RelationIDs))
	fmt.Println("evidence subgraph entities:", len(gt.EvidenceSubgraph.Entities))
	fmt.Println("evidence subgraph relations:", len(gt.EvidenceSubgraph.Relations))

	// Output:
	// top ranked path: machine:engine -> person:babbage -> person:lovelace
	// path edges: 2
	// evidence subgraph entities: 3
	// evidence subgraph relations: 2
}
