package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/store"
)

// Compile-time check that *Store implements the optional GraphStore
// capability.
var _ store.GraphStore = (*Store)(nil)

// maxGraphDepth is the hard traversal-depth cap (KG-7); the same invariant
// the in-memory store enforces. maxNeighborhoodRows bounds the recursive
// CTE against traversal explosion.
const (
	maxGraphDepth       = 2
	maxNeighborhoodRows = 4096
)

func (s *Store) entitiesTable() string  { return s.cfg.Table + "_entities" }
func (s *Store) relationsTable() string { return s.cfg.Table + "_relations" }

// UpsertGraph union-merges g into the namespace's graph.
func (s *Store) UpsertGraph(ctx context.Context, namespace string, g graph.Graph) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	ent := s.entitiesTable()
	entStmt := fmt.Sprintf(`
		INSERT INTO %s (namespace, id, name, type, description, source_chunk_ids, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (namespace, id) DO UPDATE SET
			name        = EXCLUDED.name,
			type        = EXCLUDED.type,
			description = CASE WHEN EXCLUDED.description = '' THEN %s.description ELSE EXCLUDED.description END,
			source_chunk_ids = array(
				SELECT unnest(%s.source_chunk_ids)
				UNION
				SELECT unnest(EXCLUDED.source_chunk_ids)),
			metadata    = COALESCE(EXCLUDED.metadata, %s.metadata)`, ent, ent, ent, ent)
	for _, e := range g.Entities {
		md, err := marshalMetadata(e.Metadata)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, entStmt, namespace, e.ID, e.Name, e.Type, e.Description, e.SourceChunkIDs, md); err != nil {
			return fmt.Errorf("postgres: upsert entity %s: %w", e.ID, err)
		}
	}

	rel := s.relationsTable()
	relStmt := fmt.Sprintf(`
		INSERT INTO %s (namespace, id, source, target, relation, description, source_chunk_ids, weight)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (namespace, id) DO UPDATE SET
			relation    = EXCLUDED.relation,
			description = CASE WHEN EXCLUDED.description = '' THEN %s.description ELSE EXCLUDED.description END,
			source_chunk_ids = array(
				SELECT unnest(%s.source_chunk_ids)
				UNION
				SELECT unnest(EXCLUDED.source_chunk_ids)),
			weight      = %s.weight + EXCLUDED.weight`, rel, rel, rel, rel)
	for _, r := range g.Relations {
		if _, err := tx.Exec(ctx, relStmt, namespace, r.ID, r.Source, r.Target, r.Relation, r.Description, r.SourceChunkIDs, r.Weight); err != nil {
			return fmt.Errorf("postgres: upsert relation %s: %w", r.ID, err)
		}
	}
	return tx.Commit(ctx)
}

// RemoveGraphBySource drops the given chunk IDs from every entity's and
// relation's provenance and garbage-collects any row left unreferenced.
func (s *Store) RemoveGraphBySource(ctx context.Context, namespace string, chunkIDs []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	for _, tbl := range []string{s.entitiesTable(), s.relationsTable()} {
		upd := fmt.Sprintf(`
			UPDATE %s SET source_chunk_ids = array(
				SELECT unnest(source_chunk_ids) EXCEPT SELECT unnest($2::text[]))
			WHERE namespace = $1`, tbl)
		if _, err := tx.Exec(ctx, upd, namespace, chunkIDs); err != nil {
			return fmt.Errorf("postgres: remove graph source from %s: %w", tbl, err)
		}
		del := fmt.Sprintf(`
			DELETE FROM %s
			WHERE namespace = $1
			  AND (source_chunk_ids IS NULL OR cardinality(source_chunk_ids) = 0)`, tbl)
		if _, err := tx.Exec(ctx, del, namespace); err != nil {
			return fmt.Errorf("postgres: gc %s: %w", tbl, err)
		}
	}
	return tx.Commit(ctx)
}

// Neighborhood traverses from the seed entity IDs out to depth hops
// (clamped to [0, maxGraphDepth]) via a bounded recursive CTE.
func (s *Store) Neighborhood(ctx context.Context, namespace string, seedIDs []string, depth int) (graph.Subgraph, error) {
	if depth < 0 {
		depth = 0
	}
	if depth > maxGraphDepth {
		depth = maxGraphDepth
	}
	sub := graph.Subgraph{Depth: map[string]int{}}
	if len(seedIDs) == 0 {
		return sub, nil
	}

	walkSQL := fmt.Sprintf(`
		WITH RECURSIVE walk(id, depth) AS (
			SELECT e.id, 0
			FROM %s e
			WHERE e.namespace = $1 AND e.id = ANY($2)
		  UNION ALL
			SELECT nb.nbr, w.depth + 1
			FROM walk w
			JOIN LATERAL (
				SELECT r.target AS nbr FROM %s r WHERE r.namespace = $1 AND r.source = w.id
				UNION
				SELECT r.source AS nbr FROM %s r WHERE r.namespace = $1 AND r.target = w.id
			) nb ON true
			WHERE w.depth < $3
		)
		SELECT id, min(depth) AS depth FROM walk GROUP BY id LIMIT $4`,
		s.entitiesTable(), s.relationsTable(), s.relationsTable())

	rows, err := s.pool.Query(ctx, walkSQL, namespace, seedIDs, depth, maxNeighborhoodRows)
	if err != nil {
		return graph.Subgraph{}, fmt.Errorf("postgres: neighborhood walk: %w", err)
	}
	reached := map[string]int{}
	for rows.Next() {
		var id string
		var d int
		if err := rows.Scan(&id, &d); err != nil {
			rows.Close()
			return graph.Subgraph{}, fmt.Errorf("postgres: scan walk: %w", err)
		}
		reached[id] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return graph.Subgraph{}, fmt.Errorf("postgres: neighborhood walk: %w", err)
	}
	if len(reached) == 0 {
		return sub, nil
	}
	ids := make([]string, 0, len(reached))
	for id := range reached {
		ids = append(ids, id)
		sub.Depth[id] = reached[id]
	}
	sort.Strings(ids)

	ents, err := s.queryEntities(ctx,
		fmt.Sprintf(`SELECT id, name, type, description, source_chunk_ids, metadata
			FROM %s WHERE namespace = $1 AND id = ANY($2) ORDER BY id`, s.entitiesTable()),
		namespace, ids)
	if err != nil {
		return graph.Subgraph{}, err
	}
	sub.Entities = ents

	rels, err := s.queryRelations(ctx,
		fmt.Sprintf(`SELECT id, source, target, relation, description, source_chunk_ids, weight
			FROM %s WHERE namespace = $1 AND source = ANY($2) AND target = ANY($2) ORDER BY id`, s.relationsTable()),
		namespace, ids)
	if err != nil {
		return graph.Subgraph{}, err
	}
	sub.Relations = rels
	return sub, nil
}

// FindEntities returns entities whose name matches any of names
// (case-folded exact match).
func (s *Store) FindEntities(ctx context.Context, namespace string, names []string) ([]graph.Entity, error) {
	if len(names) == 0 {
		return nil, nil
	}
	lowered := make([]string, len(names))
	for i, n := range names {
		lowered[i] = graph.NormalizeName(n)
	}
	return s.queryEntities(ctx,
		fmt.Sprintf(`SELECT id, name, type, description, source_chunk_ids, metadata
			FROM %s WHERE namespace = $1 AND lower(name) = ANY($2) ORDER BY id`, s.entitiesTable()),
		namespace, lowered)
}

func (s *Store) queryEntities(ctx context.Context, sql string, args ...any) ([]graph.Entity, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: query entities: %w", err)
	}
	defer rows.Close()
	var out []graph.Entity
	for rows.Next() {
		var e graph.Entity
		var md []byte
		if err := rows.Scan(&e.ID, &e.Name, &e.Type, &e.Description, &e.SourceChunkIDs, &md); err != nil {
			return nil, fmt.Errorf("postgres: scan entity: %w", err)
		}
		if len(md) > 0 {
			if err := json.Unmarshal(md, &e.Metadata); err != nil {
				return nil, fmt.Errorf("postgres: unmarshal entity metadata: %w", err)
			}
		}
		out = append(out, e)
	}
	return out, rowsErr(rows)
}

func (s *Store) queryRelations(ctx context.Context, sql string, args ...any) ([]graph.Relation, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: query relations: %w", err)
	}
	defer rows.Close()
	var out []graph.Relation
	for rows.Next() {
		var r graph.Relation
		if err := rows.Scan(&r.ID, &r.Source, &r.Target, &r.Relation, &r.Description, &r.SourceChunkIDs, &r.Weight); err != nil {
			return nil, fmt.Errorf("postgres: scan relation: %w", err)
		}
		out = append(out, r)
	}
	return out, rowsErr(rows)
}

func rowsErr(rows pgx.Rows) error {
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: rows: %w", err)
	}
	return nil
}
