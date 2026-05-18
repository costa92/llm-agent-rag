package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPScoringModelMapsResultsToDocumentOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
		}
		// Results deliberately out of index order — Score must reorder.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"index": 2, "relevance_score": 0.1},
				{"index": 0, "relevance_score": 0.9},
				{"index": 1, "relevance_score": 0.5},
			},
		})
	}))
	defer srv.Close()

	m := HTTPScoringModel{Endpoint: srv.URL, Token: "tok", Model: "rerank-1"}
	scores, err := m.Score(context.Background(), "q", []string{"d0", "d1", "d2"})
	if err != nil {
		t.Fatalf("Score(): %v", err)
	}
	want := []float64{0.9, 0.5, 0.1}
	if len(scores) != len(want) {
		t.Fatalf("scores len = %d, want %d", len(scores), len(want))
	}
	for i := range want {
		if scores[i] != want[i] {
			t.Fatalf("scores = %v, want %v", scores, want)
		}
	}
}

func TestHTTPScoringModelRequiresEndpoint(t *testing.T) {
	if _, err := (HTTPScoringModel{}).Score(context.Background(), "q", []string{"d"}); err == nil {
		t.Fatalf("Score() with empty endpoint: want error")
	}
}

func TestHTTPScoringModelErrorsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := (HTTPScoringModel{Endpoint: srv.URL}).Score(context.Background(), "q", []string{"d"}); err == nil {
		t.Fatalf("Score() against a 500 response: want error")
	}
}
