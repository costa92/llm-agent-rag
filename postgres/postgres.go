// Package postgres implements store.Store against PostgreSQL with the
// pgvector extension.
//
// Callers own the *pgxpool.Pool. To make pgvector types available on every
// connection in the pool, register them in the pool config's AfterConnect
// hook using RegisterTypes(ctx, conn).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
	pgvector_pgx "github.com/pgvector/pgvector-go/pgx"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/store"
)

// Config configures a Store.
type Config struct {
	// Table is the table name backing the store. Defaults to "chunks".
	Table string
	// Dimension is the vector dimension. Required. Must match the embedding
	// model used by callers; mismatched upserts return store.ErrDimensionMismatch.
	Dimension int
	// TextSearchConfig is the PostgreSQL text-search configuration used for
	// the full-text lexical index and queries. Defaults to "english".
	TextSearchConfig string
}

// Store is a PostgreSQL + pgvector implementation of store.Store.
type Store struct {
	pool *pgxpool.Pool
	cfg  Config
}

// Compile-time check that *Store satisfies store.Store and store.LexicalSearcher.
var (
	_ store.Store           = (*Store)(nil)
	_ store.LexicalSearcher = (*Store)(nil)
)

// textSearchConfig returns the configured text-search configuration, or the
// "english" default when unset.
func (s *Store) textSearchConfig() string {
	if s.cfg.TextSearchConfig == "" {
		return "english"
	}
	return s.cfg.TextSearchConfig
}

// New constructs a Store using the provided pool. The caller owns the pool
// lifecycle. The pool's AfterConnect hook should call RegisterTypes so each
// connection knows the pgvector codec.
func New(pool *pgxpool.Pool, cfg Config) (*Store, error) {
	if pool == nil {
		return nil, errors.New("postgres: pool is required")
	}
	if cfg.Dimension <= 0 {
		return nil, errors.New("postgres: cfg.Dimension must be > 0")
	}
	if cfg.Table == "" {
		cfg.Table = "chunks"
	}
	if !isSafeIdent(cfg.Table) {
		return nil, fmt.Errorf("postgres: invalid table name %q", cfg.Table)
	}
	if cfg.TextSearchConfig != "" && !isSafeIdent(cfg.TextSearchConfig) {
		return nil, fmt.Errorf("postgres: invalid text search config %q", cfg.TextSearchConfig)
	}
	return &Store{pool: pool, cfg: cfg}, nil
}

// RegisterTypes registers the pgvector codec on a single pgx connection.
// Wire this into pgxpool.Config.AfterConnect so every pooled connection knows
// the vector type.
func RegisterTypes(ctx context.Context, conn *pgx.Conn) error {
	return pgvector_pgx.RegisterTypes(ctx, conn)
}

// Migrate creates the pgvector extension (idempotent), the chunks table, a
// default index on the namespace column, a generated tsvector column + GIN
// index backing full-text lexical search, the entities/relations tables
// backing GraphStore, and the communities + community-reports tables backing
// CommunityStore. Safe to call on every startup.
func (s *Store) Migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE EXTENSION IF NOT EXISTS vector`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id            TEXT PRIMARY KEY,
			namespace     TEXT NOT NULL,
			doc_id        TEXT NOT NULL,
			title         TEXT,
			section_id    TEXT,
			section_path  TEXT[],
			heading       TEXT,
			heading_level INT,
			content       TEXT NOT NULL,
			metadata      JSONB,
			embedding     vector(%d)
		)`, s.cfg.Table, s.cfg.Dimension),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_namespace_idx ON %s (namespace)`, s.cfg.Table, s.cfg.Table),
		fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS content_tsv tsvector
			GENERATED ALWAYS AS (to_tsvector('%s', content)) STORED`, s.cfg.Table, s.textSearchConfig()),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_content_tsv_idx ON %s USING GIN (content_tsv)`, s.cfg.Table, s.cfg.Table),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s_entities (
			namespace        TEXT NOT NULL,
			id               TEXT NOT NULL,
			name             TEXT NOT NULL,
			type             TEXT,
			description      TEXT,
			source_chunk_ids TEXT[],
			metadata         JSONB,
			PRIMARY KEY (namespace, id)
		)`, s.cfg.Table),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s_relations (
			namespace        TEXT NOT NULL,
			id               TEXT NOT NULL,
			source           TEXT NOT NULL,
			target           TEXT NOT NULL,
			relation         TEXT,
			description      TEXT,
			source_chunk_ids TEXT[],
			weight           DOUBLE PRECISION,
			PRIMARY KEY (namespace, id)
		)`, s.cfg.Table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_rel_source_idx ON %s_relations (namespace, source)`, s.cfg.Table, s.cfg.Table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_rel_target_idx ON %s_relations (namespace, target)`, s.cfg.Table, s.cfg.Table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_ent_name_idx ON %s_entities (namespace, lower(name))`, s.cfg.Table, s.cfg.Table),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s_communities (
			namespace    TEXT NOT NULL,
			community_id TEXT NOT NULL,
			level        INT NOT NULL,
			parent_id    TEXT,
			entity_ids   TEXT[],
			relation_ids TEXT[],
			PRIMARY KEY (namespace, community_id)
		)`, s.cfg.Table),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s_community_reports (
			namespace    TEXT NOT NULL,
			community_id TEXT NOT NULL,
			title        TEXT,
			summary      TEXT,
			content_hash TEXT,
			PRIMARY KEY (namespace, community_id)
		)`, s.cfg.Table),
	}
	for _, stmt := range stmts {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("postgres: migrate: %w", err)
		}
	}
	return nil
}

// Upsert inserts or updates chunks. Vector dimension must equal cfg.Dimension;
// rows with mismatched dims return store.ErrDimensionMismatch.
func (s *Store) Upsert(ctx context.Context, chunks []store.StoredChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	for _, chunk := range chunks {
		if len(chunk.Vector) != s.cfg.Dimension {
			return store.ErrDimensionMismatch
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	stmt := fmt.Sprintf(`
		INSERT INTO %s (id, namespace, doc_id, title, section_id, section_path, heading, heading_level, content, metadata, embedding)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			namespace     = EXCLUDED.namespace,
			doc_id        = EXCLUDED.doc_id,
			title         = EXCLUDED.title,
			section_id    = EXCLUDED.section_id,
			section_path  = EXCLUDED.section_path,
			heading       = EXCLUDED.heading,
			heading_level = EXCLUDED.heading_level,
			content       = EXCLUDED.content,
			metadata      = EXCLUDED.metadata,
			embedding     = EXCLUDED.embedding
	`, s.cfg.Table)
	for _, chunk := range chunks {
		metadata, err := marshalMetadata(chunk.Metadata)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, stmt,
			chunk.ID,
			chunk.Namespace,
			chunk.DocID,
			chunk.Title,
			chunk.SectionID,
			chunk.SectionPath,
			chunk.Heading,
			chunk.HeadingLevel,
			chunk.Content,
			metadata,
			pgvector.NewVector(chunk.Vector),
		); err != nil {
			return fmt.Errorf("postgres: upsert %s: %w", chunk.ID, err)
		}
	}
	return tx.Commit(ctx)
}

// Search returns the TopK closest hits to q.Vector under cosine distance,
// scoped to q.Namespace and constrained by filters / security filters.
func (s *Store) Search(ctx context.Context, q store.Query) ([]store.Hit, error) {
	if len(q.Vector) != s.cfg.Dimension {
		return nil, store.ErrDimensionMismatch
	}
	whereSQL, args := buildWhere(q.Namespace, q.Filters, q.SecurityFilters)
	args = append(args, pgvector.NewVector(q.Vector))
	vectorArgIdx := len(args)
	topK := q.TopK
	if topK <= 0 {
		topK = 10
	}
	args = append(args, topK)
	limitArgIdx := len(args)
	stmt := fmt.Sprintf(`
		SELECT id, namespace, doc_id, title, section_id, section_path, heading, heading_level, content, metadata, embedding, embedding <=> $%d AS distance
		FROM %s
		%s
		ORDER BY embedding <=> $%d
		LIMIT $%d
	`, vectorArgIdx, s.cfg.Table, whereSQL, vectorArgIdx, limitArgIdx)
	rows, err := s.pool.Query(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: search: %w", err)
	}
	defer rows.Close()
	out := make([]store.Hit, 0, topK)
	for rows.Next() {
		chunk, distance, err := scanChunkWithDistance(rows)
		if err != nil {
			return nil, err
		}
		// pgvector <=> is cosine distance in [0, 2]; map to a similarity in
		// [-1, 1] via score = 1 - distance so larger = closer.
		out = append(out, store.Hit{Chunk: chunk, Score: 1 - distance})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: search rows: %w", err)
	}
	return out, nil
}

// LexicalSearch ranks chunks in q.Namespace against q.Text using PostgreSQL
// full-text search: websearch_to_tsquery parses the query and ts_rank_cd
// scores cover density against the generated content_tsv column. Results are
// constrained by q.Filters and q.SecurityFilters exactly as Search is.
// Implements store.LexicalSearcher.
func (s *Store) LexicalSearch(ctx context.Context, q store.Query) ([]store.Hit, error) {
	if strings.TrimSpace(q.Text) == "" {
		return []store.Hit{}, nil
	}
	whereSQL, args := buildWhere(q.Namespace, q.Filters, q.SecurityFilters)
	args = append(args, q.Text)
	queryArgIdx := len(args)
	topK := q.TopK
	if topK <= 0 {
		topK = 10
	}
	args = append(args, topK)
	limitArgIdx := len(args)
	cfg := s.textSearchConfig()
	stmt := fmt.Sprintf(`
		SELECT id, namespace, doc_id, title, section_id, section_path, heading, heading_level, content, metadata, embedding,
			ts_rank_cd(content_tsv, websearch_to_tsquery('%s', $%d)) AS score
		FROM %s
		%s AND content_tsv @@ websearch_to_tsquery('%s', $%d)
		ORDER BY score DESC
		LIMIT $%d
	`, cfg, queryArgIdx, s.cfg.Table, whereSQL, cfg, queryArgIdx, limitArgIdx)
	rows, err := s.pool.Query(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: lexical search: %w", err)
	}
	defer rows.Close()
	out := make([]store.Hit, 0, topK)
	for rows.Next() {
		chunk, score, err := scanChunkWithDistance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, store.Hit{Chunk: chunk, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: lexical search rows: %w", err)
	}
	return out, nil
}

// List returns every chunk in the namespace that satisfies filters +
// securityFilters.
func (s *Store) List(ctx context.Context, namespace string, filters store.Filter, securityFilters store.Filter) ([]store.StoredChunk, error) {
	whereSQL, args := buildWhere(namespace, filters, securityFilters)
	stmt := fmt.Sprintf(`
		SELECT id, namespace, doc_id, title, section_id, section_path, heading, heading_level, content, metadata, embedding
		FROM %s
		%s
	`, s.cfg.Table, whereSQL)
	rows, err := s.pool.Query(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list: %w", err)
	}
	defer rows.Close()
	out := make([]store.StoredChunk, 0)
	for rows.Next() {
		chunk, err := scanChunk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, chunk)
	}
	return out, rows.Err()
}

// Get returns the chunk with the given id.
func (s *Store) Get(ctx context.Context, id string) (store.StoredChunk, error) {
	stmt := fmt.Sprintf(`
		SELECT id, namespace, doc_id, title, section_id, section_path, heading, heading_level, content, metadata, embedding
		FROM %s
		WHERE id = $1
	`, s.cfg.Table)
	row := s.pool.QueryRow(ctx, stmt, id)
	chunk, err := scanChunk(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.StoredChunk{}, store.ErrNotFound
	}
	if err != nil {
		return store.StoredChunk{}, fmt.Errorf("postgres: get: %w", err)
	}
	return chunk, nil
}

// Remove deletes the chunk with the given id.
func (s *Store) Remove(ctx context.Context, id string) error {
	stmt := fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, s.cfg.Table)
	tag, err := s.pool.Exec(ctx, stmt, id)
	if err != nil {
		return fmt.Errorf("postgres: remove: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// RemoveByFilter deletes every chunk in the namespace that matches filters
// and returns the number of rows removed.
func (s *Store) RemoveByFilter(ctx context.Context, namespace string, filters store.Filter) (int, error) {
	whereSQL, args := buildWhere(namespace, filters, nil)
	stmt := fmt.Sprintf(`DELETE FROM %s %s`, s.cfg.Table, whereSQL)
	tag, err := s.pool.Exec(ctx, stmt, args...)
	if err != nil {
		return 0, fmt.Errorf("postgres: remove by filter: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// Stats returns the chunk count and embedding dimension for namespace.
func (s *Store) Stats(ctx context.Context, namespace string) (store.Stats, error) {
	stmt := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE namespace = $1`, s.cfg.Table)
	var count int
	if err := s.pool.QueryRow(ctx, stmt, namespace).Scan(&count); err != nil {
		return store.Stats{}, fmt.Errorf("postgres: stats: %w", err)
	}
	return store.Stats{Count: count, Dim: s.cfg.Dimension}, nil
}

// --- helpers ---

// scanRow is the subset of pgx.Row / pgx.Rows we read from in scanChunk.
type scanRow interface {
	Scan(dest ...any) error
}

func scanChunk(row scanRow) (store.StoredChunk, error) {
	var (
		chunk        store.StoredChunk
		title        *string
		sectionID    *string
		sectionPath  []string
		heading      *string
		headingLevel *int
		metadataJSON []byte
		vec          pgvector.Vector
	)
	if err := row.Scan(
		&chunk.ID,
		&chunk.Namespace,
		&chunk.DocID,
		&title,
		&sectionID,
		&sectionPath,
		&heading,
		&headingLevel,
		&chunk.Content,
		&metadataJSON,
		&vec,
	); err != nil {
		return store.StoredChunk{}, err
	}
	if title != nil {
		chunk.Title = *title
	}
	if sectionID != nil {
		chunk.SectionID = *sectionID
	}
	chunk.SectionPath = sectionPath
	if heading != nil {
		chunk.Heading = *heading
	}
	if headingLevel != nil {
		chunk.HeadingLevel = *headingLevel
	}
	chunk.Vector = embed.Vector(vec.Slice())
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &chunk.Metadata); err != nil {
			return store.StoredChunk{}, fmt.Errorf("postgres: unmarshal metadata: %w", err)
		}
	}
	return chunk, nil
}

func scanChunkWithDistance(rows pgx.Rows) (store.StoredChunk, float64, error) {
	var (
		chunk        store.StoredChunk
		title        *string
		sectionID    *string
		sectionPath  []string
		heading      *string
		headingLevel *int
		metadataJSON []byte
		vec          pgvector.Vector
		distance     float64
	)
	if err := rows.Scan(
		&chunk.ID,
		&chunk.Namespace,
		&chunk.DocID,
		&title,
		&sectionID,
		&sectionPath,
		&heading,
		&headingLevel,
		&chunk.Content,
		&metadataJSON,
		&vec,
		&distance,
	); err != nil {
		return store.StoredChunk{}, 0, err
	}
	if title != nil {
		chunk.Title = *title
	}
	if sectionID != nil {
		chunk.SectionID = *sectionID
	}
	chunk.SectionPath = sectionPath
	if heading != nil {
		chunk.Heading = *heading
	}
	if headingLevel != nil {
		chunk.HeadingLevel = *headingLevel
	}
	chunk.Vector = embed.Vector(vec.Slice())
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &chunk.Metadata); err != nil {
			return store.StoredChunk{}, 0, fmt.Errorf("postgres: unmarshal metadata: %w", err)
		}
	}
	return chunk, distance, nil
}

func buildWhere(namespace string, filters store.Filter, securityFilters store.Filter) (string, []any) {
	conds := []string{"namespace = $1"}
	args := []any{namespace}
	if metadataFilter, ok := metadataJSONFilter(filters); ok {
		args = append(args, metadataFilter)
		conds = append(conds, fmt.Sprintf("metadata @> $%d", len(args)))
	}
	if metadataFilter, ok := metadataJSONFilter(securityFilters); ok {
		args = append(args, metadataFilter)
		conds = append(conds, fmt.Sprintf("metadata @> $%d", len(args)))
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

func metadataJSONFilter(filter store.Filter) ([]byte, bool) {
	if len(filter) == 0 {
		return nil, false
	}
	raw, err := json.Marshal(filter)
	if err != nil {
		return nil, false
	}
	return raw, true
}

func marshalMetadata(metadata map[string]any) ([]byte, error) {
	if len(metadata) == 0 {
		return nil, nil
	}
	return json.Marshal(metadata)
}

// isSafeIdent guards against trivial SQL injection in the configurable table
// name. We embed the table identifier into statements via fmt.Sprintf, so it
// must be a strict ASCII identifier.
func isSafeIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// errIsConnRefused returns true when err looks like a "no Postgres available"
// failure, useful for guard-style tests. Exported as a helper for callers
// writing their own optional integration tests.
func errIsConnRefused(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return false
	}
	return strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "no such host") ||
		strings.Contains(err.Error(), "context deadline exceeded")
}
