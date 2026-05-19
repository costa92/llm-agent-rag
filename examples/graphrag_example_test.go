package examples

import (
	"context"
	"fmt"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/rag"
	"github.com/costa92/llm-agent-rag/retrieve"
	"github.com/costa92/llm-agent-rag/store"
)

// Example_graphRAG shows the full v0.7 GraphRAG path: a deterministic
// entity extractor builds a knowledge graph at ingest, a GraphRetriever is
// wired as a fourth signal in the hybrid retriever, and Ask runs with the
// graph signal enabled. It uses no live model — the project's example
// discipline.
func Example_graphRAG() {
	st := store.NewInMemoryStore(32)
	emb := embed.NewHashEmbedder(32)

	sys := rag.New(rag.Options{
		Model:    echoModel{},
		Store:    st,
		Embedder: emb,
		// A deterministic, zero-LLM extractor — a gazetteer of the salient
		// entities. LLMEntityExtractor is the production path.
		EntityExtractor: graph.DictionaryEntityExtractor{Terms: map[string]string{
			"Ada Lovelace":      "person",
			"Charles Babbage":   "person",
			"Analytical Engine": "machine",
		}},
		// Wire GraphRetriever as the fourth signal in HybridRetriever.
		Retriever: retrieve.VariantRetriever{Base: retrieve.HybridRetriever{
			Dense:     retrieve.DenseRetriever{Embedder: emb, Store: st},
			Lexical:   retrieve.LexicalRetriever{Store: st},
			Structure: retrieve.StructureRetriever{Store: st},
			Graph:     retrieve.GraphRetriever{Store: st},
		}},
	})

	res, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "Ada Lovelace worked with Charles Babbage."},
		{ID: "d2", Content: "Charles Babbage designed the Analytical Engine."},
	}, ingest.ImportOptions{Namespace: "history"})
	if err != nil {
		panic(err)
	}
	fmt.Println("graph extracted:", res.Graph != nil && len(res.Graph.Entities) > 0)

	// Ask with the graph signal enabled — the query links to Ada Lovelace
	// and the traversal reaches Charles Babbage and the Analytical Engine.
	ans, err := sys.Ask(context.Background(), "Ada Lovelace", rag.AskOptions{
		Search: rag.SearchOptions{Namespace: "history", TopK: 5, EnableGraph: true},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("graph seeds linked:", len(ans.Diagnostics.GraphTrace.SeedEntityIDs) > 0)

	// Output:
	// graph extracted: true
	// graph seeds linked: true
}
