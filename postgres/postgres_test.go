package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/store"
)

const liveEnvVar = "LLM_AGENT_RAG_PG_URL"

func openTestPool(t *testing.T, ctx context.Context, table string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(liveEnvVar)
	if dsn == "" {
		t.Skipf("set %s to run this test (e.g. postgres://localhost/llm_agent_rag_test?sslmode=disable)", liveEnvVar)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return RegisterTypes(ctx, conn)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgxpool.NewWithConfig: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), fmt.Sprintf(`DROP TABLE IF EXISTS %s`, table)); err != nil {
			t.Logf("cleanup drop table: %v", err)
		}
		pool.Close()
	})
	return pool
}

func TestPostgresStore_LiveSmoke(t *testing.T) {
	ctx := context.Background()
	table := "chunks_smoke"
	pool := openTestPool(t, ctx, table)

	s, err := New(pool, Config{Table: table, Dimension: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	err = s.Upsert(ctx, []store.StoredChunk{
		{
			ID:        "smoke:0",
			Namespace: "test",
			DocID:     "smoke",
			Content:   "near to 1,0",
			Vector:    embed.Vector{1, 0},
			Metadata:  map[string]any{"lang": "en"},
		},
		{
			ID:        "smoke:1",
			Namespace: "test",
			DocID:     "smoke",
			Content:   "near to 0,1",
			Vector:    embed.Vector{0, 1},
			Metadata:  map[string]any{"lang": "zh"},
		},
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	hits, err := s.Search(ctx, store.Query{
		Namespace: "test",
		Vector:    embed.Vector{1, 0},
		TopK:      2,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	if hits[0].Chunk.ID != "smoke:0" {
		t.Fatalf("top hit id = %q, want smoke:0 (closer to query vector)", hits[0].Chunk.ID)
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("hits scores not descending: %v", hits)
	}
	if hits[0].Chunk.Metadata["lang"] != "en" {
		t.Fatalf("metadata round-trip failed: got %v", hits[0].Chunk.Metadata)
	}

	got, err := s.Get(ctx, "smoke:0")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != "near to 1,0" {
		t.Fatalf("Get content = %q, want round-tripped value", got.Content)
	}

	if err := s.Remove(ctx, "smoke:0"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := s.Get(ctx, "smoke:0"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get after Remove err = %v, want store.ErrNotFound", err)
	}

	stats, err := s.Stats(ctx, "test")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Count != 1 || stats.Dim != 2 {
		t.Fatalf("stats = %+v, want Count=1 Dim=2", stats)
	}
}

func TestPostgresStore_LiveMetadataFilter(t *testing.T) {
	ctx := context.Background()
	table := "chunks_filter"
	pool := openTestPool(t, ctx, table)

	s, err := New(pool, Config{Table: table, Dimension: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	err = s.Upsert(ctx, []store.StoredChunk{
		{ID: "a", Namespace: "test", DocID: "x", Content: "english", Vector: embed.Vector{1, 0}, Metadata: map[string]any{"lang": "en"}},
		{ID: "b", Namespace: "test", DocID: "x", Content: "chinese", Vector: embed.Vector{1, 0}, Metadata: map[string]any{"lang": "zh"}},
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	hits, err := s.Search(ctx, store.Query{
		Namespace: "test",
		Vector:    embed.Vector{1, 0},
		TopK:      5,
		Filters:   store.Filter{"lang": "en"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk.ID != "a" {
		t.Fatalf("filtered hits = %+v, want only id=a", hits)
	}
}

func TestStoreNewRejectsBadInputs(t *testing.T) {
	if _, err := New(nil, Config{Dimension: 2}); err == nil {
		t.Fatalf("expected error for nil pool")
	}
	if _, err := New(&pgxpool.Pool{}, Config{}); err == nil {
		t.Fatalf("expected error for zero dimension")
	}
	if _, err := New(&pgxpool.Pool{}, Config{Dimension: 2, Table: "1bad"}); err == nil {
		t.Fatalf("expected error for invalid table identifier")
	}
}
