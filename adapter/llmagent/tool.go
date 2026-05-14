//go:build llmagent

package llmagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	agents "github.com/costa92/llm-agent"
	"github.com/costa92/llm-agent-rag/advanced"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/prompt"
	ragcore "github.com/costa92/llm-agent-rag/rag"
	"github.com/costa92/llm-agent-rag/store"
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
			"enable_mqe":{"type":"boolean"},
			"enable_hyde":{"type":"boolean"},
			"mqe_count":{"type":"integer"},
			"metadata":{"type":"object"}
		},
		"required":["action"]
	}`)
}

type ragToolArgs struct {
	Action     string         `json:"action"`
	Text       string         `json:"text"`
	Query      string         `json:"query"`
	Question   string         `json:"question"`
	ID         string         `json:"id"`
	TopK       int            `json:"top_k"`
	Namespace  string         `json:"namespace"`
	EnableMQE  bool           `json:"enable_mqe"`
	EnableHyDE bool           `json:"enable_hyde"`
	MQECount   int            `json:"mqe_count"`
	Metadata   map[string]any `json:"metadata"`
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
			hits, err := search(ctx, r, p)
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(hits)
			return string(b), nil
		case "ask":
			if p.Question == "" {
				return "", errors.New("rag: question required for ask")
			}
			text, err := ask(ctx, r, p)
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(map[string]string{"answer": text})
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

func search(ctx context.Context, r *ragcore.System, p ragToolArgs) ([]store.Hit, error) {
	topK := p.TopK
	if topK <= 0 {
		topK = 5
	}
	pool := topK
	queries := []string{p.Query}

	model, err := modelFromSystem(r)
	if err != nil {
		return nil, err
	}
	if p.EnableMQE {
		count := p.MQECount
		if count <= 0 {
			count = 3
		}
		expansions, err := advanced.ExpandQuery(ctx, model, p.Query, count)
		if err != nil {
			return nil, fmt.Errorf("rag: MQE: %w", err)
		}
		queries = append(queries, expansions...)
	}
	if p.EnableHyDE {
		hypo, err := advanced.GenerateHypothetical(ctx, model, p.Query)
		if err != nil {
			return nil, fmt.Errorf("rag: HyDE: %w", err)
		}
		queries = append(queries, hypo)
	}

	merged := make(map[string]store.Hit, pool)
	for _, q := range queries {
		hits, err := r.Retrieve(ctx, q, ragcore.SearchOptions{
			TopK:      pool,
			Namespace: p.Namespace,
		})
		if err != nil {
			return nil, err
		}
		for _, hit := range hits {
			if prev, ok := merged[hit.Chunk.ID]; !ok || hit.Score > prev.Score {
				merged[hit.Chunk.ID] = hit
			}
		}
	}

	out := make([]store.Hit, 0, len(merged))
	for _, hit := range merged {
		out = append(out, hit)
	}
	sortHitsDesc(out)
	if len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

func ask(ctx context.Context, r *ragcore.System, p ragToolArgs) (string, error) {
	hits, err := search(ctx, r, ragToolArgs{
		Query:      p.Question,
		TopK:       max(p.TopK, 5),
		Namespace:  p.Namespace,
		EnableMQE:  p.EnableMQE,
		EnableHyDE: p.EnableHyDE,
		MQECount:   p.MQECount,
	})
	if err != nil {
		return "", err
	}
	model, err := modelFromSystem(r)
	if err != nil {
		return "", err
	}
	req, err := prompt.DefaultQATemplate{}.Render(ctx, prompt.RenderContext{
		Question:  p.Question,
		Namespace: p.Namespace,
		Hits:      hits,
		Metadata:  p.Metadata,
	})
	if err != nil {
		return "", err
	}
	resp, err := model.Generate(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

func modelFromSystem(r *ragcore.System) (ModelAdapter, error) {
	model, ok := r.Model().(ModelAdapter)
	if !ok || model.Inner == nil {
		return ModelAdapter{}, ragcore.ErrModelRequired
	}
	return model, nil
}

func sortHitsDesc(hits []store.Hit) {
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].Score > hits[j-1].Score; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
