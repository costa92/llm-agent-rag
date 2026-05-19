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

// driftExampleModel is a fully deterministic generate.Model for the DRIFT
// worked example. AskDrift drives four kinds of generation through one model
// — the community summarizer, the global primer's map step, each local
// follow-up round, and the final synthesis — and they are told apart by the
// request's SystemPrompt (the same routing the global example uses). Two of
// the four also sub-route on the request content so the example exercises a
// real two-round local loop:
//
//   - the summarizer gives the two communities distinct titles;
//   - the map step scores only the Babbage community above zero, so just its
//     entities seed the local loop's round 0;
//   - the local step emits "Follow-up: Alan Turing" while it has not yet seen
//     Turing in its context, then "Follow-up: none" once it has — so the loop
//     runs exactly two rounds and then terminates by itself.
//
// Every branch returns fixed text, so the example's Output is stable with no
// live model.
type driftExampleModel struct{}

func (driftExampleModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	content := ""
	if len(req.Messages) > 0 {
		content = req.Messages[0].Content
	}
	switch {
	case strings.HasPrefix(req.SystemPrompt, "You summarize one community"):
		// Community summarizer: a title line plus a one-paragraph summary,
		// routed by which cluster's entities the prompt carries.
		if strings.Contains(content, "Charles Babbage") {
			return generate.Response{Text: "Title: Babbage and the Analytical Engine\n" +
				"Ada Lovelace and Charles Babbage and the first mechanical general-purpose computer."}, nil
		}
		return generate.Response{Text: "Title: Turing and Wartime Codebreaking\n" +
			"Alan Turing, the Turing Machine, and the Bombe codebreaking device."}, nil
	case strings.HasPrefix(req.SystemPrompt, "You are answering a whole-corpus question using ONE community summary"):
		// Primer map step: score each community's contribution. Only the
		// Babbage community scores above zero, so only its member entities
		// seed the local follow-up loop's round 0.
		if strings.Contains(content, "Babbage and the Analytical Engine") {
			return generate.Response{Text: "Score: 90\n" +
				"This community is central — it covers the origin of the mechanical computer."}, nil
		}
		return generate.Response{Text: "Score: 0\n" +
			"This community is about codebreaking and does not address the question."}, nil
	case strings.HasPrefix(req.SystemPrompt, "You are answering a question using a slice of a knowledge graph"):
		// Local follow-up round. Round 0's context has not yet seen Alan
		// Turing, so it names him as a follow-up; round 1's context has, so
		// it emits no follow-up and the loop terminates.
		if strings.Contains(content, "Alan Turing") {
			return generate.Response{Text: "The codebreaking work built on the earlier mechanical-computing ideas.\n" +
				"Follow-up: none"}, nil
		}
		return generate.Response{Text: "Babbage's Analytical Engine was the first general-purpose mechanical computer.\n" +
			"Follow-up: Alan Turing"}, nil
	case strings.HasPrefix(req.SystemPrompt, "You are answering a question using DRIFT search."):
		// Synthesis: fold the primer partials and every local round into one
		// final answer.
		return generate.Response{Text: "DRIFT traced the corpus from Babbage's Analytical Engine " +
			"through to Turing's wartime codebreaking machines."}, nil
	default:
		return generate.Response{Text: ""}, nil
	}
}

// Example_graphRAGDrift shows the full v0.9 DRIFT hybrid-search path end to
// end. DRIFT is the bridge between the two existing answer paths: it opens
// with a global primer pass over the community reports (like AskGlobal), then
// runs a hard-bounded local follow-up loop that traverses the entity graph
// from the primer's most relevant communities (like Ask + GraphRetriever),
// and finally synthesizes one answer. A deterministic entity extractor builds
// the knowledge graph at ingest, LouvainDetector partitions it into
// communities, and a single scripted model serves the summarizer, the primer
// map step, every local round, and the synthesis — so the example is fully
// deterministic, no live model, the project's example discipline.
func Example_graphRAGDrift() {
	st := store.NewInMemoryStore(32)
	emb := embed.NewHashEmbedder(32)
	model := driftExampleModel{}

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
		// Detect a community hierarchy over the namespace graph at ingest —
		// the primer pass maps over the coarsest level.
		CommunityDetector: graph.LouvainDetector{},
		// The summarizer the primer uses to write community reports.
		CommunitySummarizer: graph.LLMCommunitySummarizer{Model: model},
	})

	// A small fixed corpus: two clusters of early-computing history that the
	// graph connects only loosely — exactly the shape DRIFT is built for.
	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "d1", Content: "Ada Lovelace worked with Charles Babbage."},
		{ID: "d2", Content: "Charles Babbage designed the Analytical Engine."},
		{ID: "d3", Content: "Alan Turing described the Turing Machine."},
		{ID: "d4", Content: "Alan Turing built the Bombe."},
	}, ingest.ImportOptions{Namespace: "history"})
	if err != nil {
		panic(err)
	}

	// DRIFT search: a global primer pass, a bounded local follow-up loop, and
	// a synthesis step — a third answer path alongside Ask and AskGlobal.
	ans, err := sys.AskDrift(context.Background(), "how did mechanical computing begin",
		rag.DriftOptions{Namespace: "history"})
	if err != nil {
		panic(err)
	}

	drift := ans.Diagnostics.Drift
	fmt.Println("primer communities mapped:", len(drift.PrimerCommunityIDs))
	fmt.Println("local rounds run:", drift.Rounds)
	fmt.Println("answer:", ans.Text)

	// Output:
	// primer communities mapped: 2
	// local rounds run: 2
	// answer: DRIFT traced the corpus from Babbage's Analytical Engine through to Turing's wartime codebreaking machines.
}
