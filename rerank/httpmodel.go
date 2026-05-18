package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// HTTPScoringModel is a ScoringModel backed by an HTTP rerank API. It POSTs
// the query and documents as JSON and reads a Cohere/Jina/TEI-style response
// of {index, relevance_score} results. It uses only the standard library.
type HTTPScoringModel struct {
	// Endpoint is the full URL of the rerank endpoint. Required.
	Endpoint string
	// Model is an optional model name sent in the request body.
	Model string
	// Token, when set, is sent as an Authorization: Bearer header.
	Token string
	// Client is the HTTP client to use; nil uses http.DefaultClient.
	Client *http.Client
}

var _ ScoringModel = HTTPScoringModel{}

type httpRerankRequest struct {
	Model     string   `json:"model,omitempty"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type httpRerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

type httpRerankResponse struct {
	Results []httpRerankResult `json:"results"`
}

// Score implements ScoringModel by calling the configured HTTP rerank
// endpoint. Results are mapped back to document order by their index field.
func (m HTTPScoringModel) Score(ctx context.Context, query string, documents []string) ([]float64, error) {
	if m.Endpoint == "" {
		return nil, fmt.Errorf("rerank: HTTPScoringModel endpoint required")
	}
	body, err := json.Marshal(httpRerankRequest{
		Model:     m.Model,
		Query:     query,
		Documents: documents,
	})
	if err != nil {
		return nil, fmt.Errorf("rerank: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rerank: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if m.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.Token)
	}
	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("rerank: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("rerank: endpoint returned status %d", resp.StatusCode)
	}
	var parsed httpRerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("rerank: decode response: %w", err)
	}
	scores := make([]float64, len(documents))
	for _, result := range parsed.Results {
		if result.Index < 0 || result.Index >= len(documents) {
			return nil, fmt.Errorf("rerank: result index %d out of range for %d documents", result.Index, len(documents))
		}
		scores[result.Index] = result.RelevanceScore
	}
	return scores, nil
}
