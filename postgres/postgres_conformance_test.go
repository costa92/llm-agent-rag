package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-rag/postgres"
	"github.com/costa92/llm-agent-rag/store"
	"github.com/costa92/llm-agent-rag/store/storetest"
)

const liveEnvVar = "LLM_AGENT_RAG_PG_URL"

// TestPostgresStoreConformance runs the shared conformance suite against
// a live Postgres + pgvector. Gated on LLM_AGENT_RAG_PG_URL so CI without
// Postgres still passes.
//
// Each subtest gets its own fresh table (named from t.Name() + a random
// suffix) so subtests cannot pollute each other; t.Cleanup drops the
// table when the subtest finishes.
// openTestPool dials the live Postgres named by LLM_AGENT_RAG_PG_URL, skips
// the test when the env var is unset, and registers pool cleanup.
func openTestPool(t *testing.T) *pgxpool.Pool {
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
		return postgres.RegisterTypes(ctx, conn)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pgxpool.NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newTableStore returns a Factory that builds a postgres.Store on a fresh,
// uniquely named table and drops it on subtest cleanup.
func newTableStore(ctx context.Context, pool *pgxpool.Pool) storetest.Factory {
	return func(t *testing.T) store.Store {
		table := sanitizeTableName(t.Name())
		s, err := postgres.New(pool, postgres.Config{Table: table, Dimension: 2})
		if err != nil {
			t.Fatalf("postgres.New: %v", err)
		}
		if err := s.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		t.Cleanup(func() {
			if _, err := pool.Exec(context.Background(), fmt.Sprintf(`DROP TABLE IF EXISTS %s`, table)); err != nil {
				t.Logf("cleanup drop table %s: %v", table, err)
			}
		})
		return s
	}
}

func TestPostgresStoreConformance(t *testing.T) {
	pool := openTestPool(t)
	storetest.RunConformance(t, newTableStore(context.Background(), pool), storetest.WithDimensionStrict())
}

// TestPostgresLexicalConformance runs the lexical-search conformance suite
// against a live Postgres, exercising the tsvector / ts_rank_cd path.
func TestPostgresLexicalConformance(t *testing.T) {
	pool := openTestPool(t)
	storetest.RunLexicalConformance(t, newTableStore(context.Background(), pool))
}

// sanitizeTableName builds a safe ASCII identifier from t.Name(). t.Name()
// looks like "TestPostgresStoreConformance/Upsert_and_Get_round_trip"; we
// lowercase, replace '/' with '_', and append 4 random hex chars to keep
// every subtest table unique across reruns.
func sanitizeTableName(name string) string {
	lower := strings.ToLower(name)
	var b strings.Builder
	b.Grow(len(lower) + 8)
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_':
			b.WriteByte('_')
		default:
			b.WriteByte('_')
		}
	}
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	return "conf_" + b.String() + "_" + hex.EncodeToString(rnd[:])
}
