package examples

import (
	"context"
	"fmt"
	"strings"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/rag"
	"github.com/costa92/llm-agent-rag/store"
)

// globalExampleModel is a fully deterministic generate.Model for the
// global-search worked example. AskGlobal drives three kinds of generation
// through one model — the community summarizer, the per-community map step,
// and the reduce step — and they are told apart by the request's
// SystemPrompt. Each branch returns fixed text so the example's Output is
// stable with no live model.
type globalExampleModel struct{}

func (globalExampleModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	switch {
	case strings.HasPrefix(req.SystemPrompt, "You summarize one community"):
		// Community summarizer: a title line plus a one-paragraph summary.
		return generate.Response{Text: "Title: Computing Pioneers\n" +
			"A community of early-computing figures and the machines they built."}, nil
	case strings.HasPrefix(req.SystemPrompt, "You are answering a whole-corpus question using ONE community summary"):
		// Map step: a self-rated helpfulness score and a partial answer.
		return generate.Response{Text: "Score: 90\n" +
			"This community covers the people and machines central to the question."}, nil
	case strings.HasPrefix(req.SystemPrompt, "You are answering a whole-corpus question.\nYou are given"):
		// Reduce step: the synthesized final answer.
		return generate.Response{Text: "The corpus is about the pioneers of computing " +
			"and the early machines they designed."}, nil
	default:
		return generate.Response{Text: ""}, nil
	}
}

// Example_graphRAGGlobal shows the full v0.8 GraphRAG global-search path end
// to end: a deterministic entity extractor builds a knowledge graph at
// ingest, LouvainDetector partitions it into a community hierarchy, and
// AskGlobal answers a whole-corpus question by map-reducing over the
// community reports. A single scripted model serves the summarizer and the
// map/reduce steps, so the example is fully deterministic — no live model,
// the project's example discipline.
func Example_graphRAGGlobal() {
	st := store.NewInMemoryStore(32)
	emb := embed.NewHashEmbedder(32)
	model := globalExampleModel{}

	sys := rag.New(rag.Options{
		Model:    model,
		Store:    st,
		Embedder: emb,
		// A deterministic, zero-LLM extractor — a gazetteer of the salient
		// entities across the fixed corpus.
		EntityExtractor: graph.DictionaryEntityExtractor{Terms: map[string]string{
			"Ada Lovelace":      "person",
			"Charles Babbage":   "person",
			"Analytical Engine": "machine",
			"Alan Turing":       "person",
			"Turing Machine":    "machine",
			"Bombe":             "machine",
		}},
		// Detect a community hierarchy over the namespace graph at ingest.
		CommunityDetector: graph.LouvainDetector{},
		// The summarizer AskGlobal uses to write community reports.
		CommunitySummarizer: graph.LLMCommunitySummarizer{Model: model},
	})

	// A small fixed corpus: two clusters of early-computing history.
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "Ada Lovelace worked with Charles Babbage."},
		{ID: "d2", Content: "Charles Babbage designed the Analytical Engine."},
		{ID: "d3", Content: "Alan Turing described the Turing Machine."},
		{ID: "d4", Content: "Alan Turing built the Bombe."},
	}, ingest.ImportOptions{Namespace: "history"})
	if err != nil {
		panic(err)
	}

	// Eagerly prewarm every community report so the first global query is
	// all cache hits — the thin opt-in eager mode.
	warmed, err := sys.PrewarmCommunityReports(context.Background(), "history")
	if err != nil {
		panic(err)
	}
	fmt.Println("reports prewarmed:", warmed > 0)

	// Answer a whole-corpus question via map-reduce over the communities.
	ans, err := sys.AskGlobal(context.Background(), "what is this corpus about",
		rag.GlobalOptions{Namespace: "history"})
	if err != nil {
		panic(err)
	}
	fmt.Println("communities consulted:", len(ans.Diagnostics.Global.CommunityIDs) > 0)
	fmt.Println("answer:", ans.Text)

	// Output:
	// reports prewarmed: true
	// communities consulted: true
	// answer: The corpus is about the pioneers of computing and the early machines they designed.
}
