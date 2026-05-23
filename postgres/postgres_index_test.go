package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/store"
)

// listIndexNamesForTable queries pg_indexes for the supplied table.
func listIndexNamesForTable(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT indexname FROM pg_indexes WHERE tablename = $1`, table)
	if err != nil {
		t.Fatalf("pg_indexes query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("pg_indexes scan: %v", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("pg_indexes rows err: %v", err)
	}
	return out
}

// indexDefFor returns the indexdef SQL for indexName.
func indexDefFor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, indexName string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname = $1`, indexName).Scan(&s); err != nil {
		t.Fatalf("indexdef for %s: %v", indexName, err)
	}
	return s
}

// pgvectorExtVersion returns the installed pgvector extension version, or
// the empty string if the extension is not present.
func pgvectorExtVersion(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var v string
	if err := pool.QueryRow(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'vector'`).Scan(&v); err != nil {
		return ""
	}
	return v
}

// versionAtLeast compares dot-separated numeric semver strings (e.g.
// "0.5.0" vs "0.4.4"). An empty got is always less than want.
func versionAtLeast(got, want string) bool {
	if got == "" {
		return false
	}
	gs := strings.Split(got, ".")
	ws := strings.Split(want, ".")
	for i := 0; i < len(ws); i++ {
		gv := 0
		if i < len(gs) {
			fmt.Sscanf(gs[i], "%d", &gv)
		}
		wv := 0
		fmt.Sscanf(ws[i], "%d", &wv)
		if gv > wv {
			return true
		}
		if gv < wv {
			return false
		}
	}
	return true
}

// hasIndexWithSuffix returns true when any indexname in list ends with the
// supplied suffix (e.g. "_embedding_ivfflat").
func hasIndexWithSuffix(list []string, suffix string) bool {
	for _, n := range list {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	return false
}

// countIndexesWithSuffix counts entries whose name ends with suffix.
func countIndexesWithSuffix(list []string, suffix string) int {
	n := 0
	for _, name := range list {
		if strings.HasSuffix(name, suffix) {
			n++
		}
	}
	return n
}

// ----------------------------------------------------------------------
// Tests — env-gated under LLM_AGENT_RAG_PG_URL via openTestPool.
// ----------------------------------------------------------------------

func TestMigrate_VectorIndexNone_NoIndexCreated(t *testing.T) {
	ctx := context.Background()
	table := "chunks_idx_none"
	pool := openTestPool(t, ctx, table)

	s, err := New(pool, Config{Table: table, Dimension: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	names := listIndexNamesForTable(t, ctx, pool, table)
	if hasIndexWithSuffix(names, "_embedding_ivfflat") {
		t.Fatalf("expected no ivfflat index, got %v", names)
	}
	if hasIndexWithSuffix(names, "_embedding_hnsw") {
		t.Fatalf("expected no hnsw index, got %v", names)
	}
	if !hasIndexWithSuffix(names, "_namespace_idx") {
		t.Fatalf("expected namespace_idx to remain, got %v", names)
	}
}

func TestMigrate_VectorIndexIVFFlat_CreatesIndex(t *testing.T) {
	ctx := context.Background()
	table := "chunks_idx_ivf_default"
	pool := openTestPool(t, ctx, table)

	s, err := New(pool, Config{Table: table, Dimension: 2, VectorIndex: VectorIndexIVFFlat})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	indexName := table + "_embedding_ivfflat"
	names := listIndexNamesForTable(t, ctx, pool, table)
	if !hasIndexWithSuffix(names, "_embedding_ivfflat") {
		t.Fatalf("expected ivfflat index %s, got %v", indexName, names)
	}
	def := indexDefFor(t, ctx, pool, indexName)
	if !strings.Contains(def, "USING ivfflat") {
		t.Fatalf("indexdef missing USING ivfflat: %s", def)
	}
	if !strings.Contains(def, "lists='100'") && !strings.Contains(def, "lists=100") {
		t.Fatalf("indexdef missing default lists=100: %s", def)
	}
}

func TestMigrate_VectorIndexIVFFlat_RespectsCustomLists(t *testing.T) {
	ctx := context.Background()
	table := "chunks_idx_ivf_custom"
	pool := openTestPool(t, ctx, table)

	s, err := New(pool, Config{Table: table, Dimension: 2, VectorIndex: VectorIndexIVFFlat, IVFFlatLists: 256})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	indexName := table + "_embedding_ivfflat"
	def := indexDefFor(t, ctx, pool, indexName)
	if !strings.Contains(def, "lists='256'") && !strings.Contains(def, "lists=256") {
		t.Fatalf("indexdef missing custom lists=256: %s", def)
	}
}

func TestMigrate_VectorIndexHNSW_CreatesIndex(t *testing.T) {
	ctx := context.Background()
	table := "chunks_idx_hnsw"
	pool := openTestPool(t, ctx, table)

	// pgvector added HNSW in 0.5.0; older servers reject the DDL outright.
	v := pgvectorExtVersion(t, ctx, pool)
	if !versionAtLeast(v, "0.5.0") {
		t.Skipf("HNSW requires pgvector >= 0.5.0, got %q", v)
	}

	s, err := New(pool, Config{Table: table, Dimension: 2, VectorIndex: VectorIndexHNSW})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	indexName := table + "_embedding_hnsw"
	names := listIndexNamesForTable(t, ctx, pool, table)
	if !hasIndexWithSuffix(names, "_embedding_hnsw") {
		t.Fatalf("expected hnsw index %s, got %v", indexName, names)
	}
	def := indexDefFor(t, ctx, pool, indexName)
	if !strings.Contains(def, "USING hnsw") {
		t.Fatalf("indexdef missing USING hnsw: %s", def)
	}
	if !strings.Contains(def, "m='16'") && !strings.Contains(def, "m=16") {
		t.Fatalf("indexdef missing default m=16: %s", def)
	}
}

func TestMigrate_VectorIndex_Idempotent(t *testing.T) {
	ctx := context.Background()
	table := "chunks_idx_idempotent"
	pool := openTestPool(t, ctx, table)

	s, err := New(pool, Config{Table: table, Dimension: 2, VectorIndex: VectorIndexIVFFlat})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (1): %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (2): %v", err)
	}
	names := listIndexNamesForTable(t, ctx, pool, table)
	if got := countIndexesWithSuffix(names, "_embedding_ivfflat"); got != 1 {
		t.Fatalf("expected exactly 1 ivfflat index, got %d (%v)", got, names)
	}
}

func TestMigrate_VectorIndex_Search_StillRanksNearestFirst(t *testing.T) {
	ctx := context.Background()
	table := "chunks_idx_search"
	pool := openTestPool(t, ctx, table)

	s, err := New(pool, Config{Table: table, Dimension: 2, VectorIndex: VectorIndexIVFFlat})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if err := s.Upsert(ctx, []store.StoredChunk{
		{ID: "near", Namespace: "test", DocID: "d", Content: "near", Vector: embed.Vector{1, 0}},
		{ID: "far", Namespace: "test", DocID: "d", Content: "far", Vector: embed.Vector{0, 1}},
	}); err != nil {
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
	if len(hits) == 0 {
		t.Fatalf("expected at least one hit")
	}
	if hits[0].Chunk.ID != "near" {
		t.Fatalf("top hit = %q, want near", hits[0].Chunk.ID)
	}
}
