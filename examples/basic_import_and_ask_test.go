package examples

import (
	"context"
	"fmt"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/rag"
)

type echoModel struct{}

func (echoModel) Generate(_ context.Context, req generate.Request) (generate.Response, error) {
	return generate.Response{Text: req.Messages[0].Content}, nil
}

func Example_basicImportAndAsk() {
	sys := rag.New(rag.Options{Model: echoModel{}})

	_, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "paris", Content: "Paris is the capital of France."},
		{ID: "berlin", Content: "Berlin is the capital of Germany."},
	}, ingest.ImportOptions{Namespace: "cities"})
	if err != nil {
		panic(err)
	}

	hits, err := sys.Retrieve(context.Background(), "France capital", rag.SearchOptions{
		Namespace: "cities",
		TopK:      1,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(hits[0].Chunk.DocID)

	ans, err := sys.Ask(context.Background(), "What is the capital of France?", rag.AskOptions{
		Search: rag.SearchOptions{Namespace: "cities", TopK: 1},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(ans.Text != "")

	// Output:
	// paris
	// true
}
