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
func TestPostgresStoreConformance(t *testing.T) {
	dsn := os.Getenv(liveEnvVar)
	if dsn == "" {
		t.Skipf("set %s to run this test (e.g. postgres://localhost/llm_agent_rag_test?sslmode=disable)", liveEnvVar)
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return postgres.RegisterTypes(ctx, conn)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgxpool.NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)

	storetest.RunConformance(t, func(t *testing.T) store.Store {
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
	}, storetest.WithDimensionStrict())
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
