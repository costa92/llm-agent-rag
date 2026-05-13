//go:build llmagent

package llmagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	agents "github.com/costa92/llm-agent"
	"github.com/costa92/llm-agent-rag/ingest"
	ragcore "github.com/costa92/llm-agent-rag/rag"
)

func AsTool(r *ragcore.System) agents.Tool {
	return agents.NewFuncTool(
		"rag",
		"Retrieval-Augmented Generation. Actions: add_text, search, ask, remove, stats.",
		ragToolSchema(),
		ragToolHandler(r),
	)
}

func ragToolSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"action":{"type":"string","enum":["add_text","search","ask","remove","stats"]},
			"text":{"type":"string"},
			"query":{"type":"string"},
			"question":{"type":"string"},
			"id":{"type":"string"},
			"top_k":{"type":"integer"},
			"namespace":{"type":"string"},
			"metadata":{"type":"object"}
		},
		"required":["action"]
	}`)
}

type ragToolArgs struct {
	Action    string         `json:"action"`
	Text      string         `json:"text"`
	Query     string         `json:"query"`
	Question  string         `json:"question"`
	ID        string         `json:"id"`
	TopK      int            `json:"top_k"`
	Namespace string         `json:"namespace"`
	Metadata  map[string]any `json:"metadata"`
}

func ragToolHandler(r *ragcore.System) agents.ExecuteFunc {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var p ragToolArgs
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", fmt.Errorf("rag: bad args: %w", err)
		}
		switch p.Action {
		case "add_text":
			if p.Text == "" {
				return "", errors.New("rag: text required for add_text")
			}
			res, err := r.Import(ctx, []ingest.Document{{
				ID:       p.ID,
				Content:  p.Text,
				Metadata: p.Metadata,
			}}, ingest.ImportOptions{Namespace: p.Namespace})
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(map[string]any{"ids": res.ChunkIDs, "count": len(res.ChunkIDs)})
			return string(b), nil
		case "search":
			if p.Query == "" {
				return "", ragcore.ErrEmptyQuery
			}
			hits, err := r.Retrieve(ctx, p.Query, ragcore.SearchOptions{
				TopK:      p.TopK,
				Namespace: p.Namespace,
			})
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(hits)
			return string(b), nil
		case "ask":
			if p.Question == "" {
				return "", errors.New("rag: question required for ask")
			}
			ans, err := r.Ask(ctx, p.Question, ragcore.AskOptions{
				Search: ragcore.SearchOptions{
					TopK:      p.TopK,
					Namespace: p.Namespace,
				},
				Metadata: p.Metadata,
			})
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(map[string]string{"answer": ans.Text})
			return string(b), nil
		case "remove":
			if p.ID == "" {
				return "", errors.New("rag: id required for remove")
			}
			if err := r.Remove(ctx, p.ID); err != nil {
				return "", err
			}
			return `{"removed":true}`, nil
		case "stats":
			stats, err := r.Stats(ctx, p.Namespace)
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(stats)
			return string(b), nil
		case "":
			return "", errors.New("rag: action is required")
		default:
			return "", fmt.Errorf("rag: unknown action %q", p.Action)
		}
	}
}
