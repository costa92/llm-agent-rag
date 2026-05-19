package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/store"
)

// Compile-time check that *Store implements the optional CommunityStore
// capability — the sibling of GraphStore (postgres/graph.go).
var _ store.CommunityStore = (*Store)(nil)

func (s *Store) communitiesTable() string { return s.cfg.Table + "_communities" }

func (s *Store) communityReportsTable() string { return s.cfg.Table + "_community_reports" }

// GraphSnapshot returns the full stored graph for a namespace — every entity
// and relation from the namespace's _entities/_relations tables, in
// deterministic (ID-sorted) order. It is the input community detection
// needs. An unknown namespace yields an empty graph and no error.
func (s *Store) GraphSnapshot(ctx context.Context, namespace string) (graph.Graph, error) {
	ents, err := s.queryEntities(ctx,
		fmt.Sprintf(`SELECT id, name, type, description, source_chunk_ids, metadata
			FROM %s WHERE namespace = $1 ORDER BY id`, s.entitiesTable()),
		namespace)
	if err != nil {
		return graph.Graph{}, err
	}
	rels, err := s.queryRelations(ctx,
		fmt.Sprintf(`SELECT id, source, target, relation, description, source_chunk_ids, weight
			FROM %s WHERE namespace = $1 ORDER BY id`, s.relationsTable()),
		namespace)
	if err != nil {
		return graph.Graph{}, err
	}
	return graph.Graph{Entities: ents, Relations: rels}, nil
}

// UpsertCommunities replaces the namespace's community set. Detection always
// produces the full set for a namespace, so this is replace-all: DELETE the
// namespace's rows then bulk-insert the new set, both inside one
// transaction. An empty set just clears the namespace.
func (s *Store) UpsertCommunities(ctx context.Context, namespace string, communities []graph.Community) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tbl := s.communitiesTable()
	if _, err := tx.Exec(ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE namespace = $1`, tbl), namespace); err != nil {
		return fmt.Errorf("postgres: delete communities: %w", err)
	}
	insert := fmt.Sprintf(`
		INSERT INTO %s (namespace, community_id, level, parent_id, entity_ids, relation_ids)
		VALUES ($1, $2, $3, $4, $5, $6)`, tbl)
	for _, c := range communities {
		if _, err := tx.Exec(ctx, insert,
			namespace, c.ID, c.Level, c.ParentID, c.EntityIDs, c.RelationIDs,
		); err != nil {
			return fmt.Errorf("postgres: upsert community %s: %w", c.ID, err)
		}
	}
	return tx.Commit(ctx)
}

// Communities returns the namespace's stored community set, ordered by
// community ID. An unknown namespace yields nil and no error.
func (s *Store) Communities(ctx context.Context, namespace string) ([]graph.Community, error) {
	rows, err := s.pool.Query(ctx,
		fmt.Sprintf(`SELECT community_id, level, parent_id, entity_ids, relation_ids
			FROM %s WHERE namespace = $1 ORDER BY community_id`, s.communitiesTable()),
		namespace)
	if err != nil {
		return nil, fmt.Errorf("postgres: query communities: %w", err)
	}
	defer rows.Close()
	var out []graph.Community
	for rows.Next() {
		var c graph.Community
		if err := rows.Scan(&c.ID, &c.Level, &c.ParentID, &c.EntityIDs, &c.RelationIDs); err != nil {
			return nil, fmt.Errorf("postgres: scan community: %w", err)
		}
		out = append(out, c)
	}
	if err := rowsErr(rows); err != nil {
		return nil, err
	}
	return out, nil
}

// PutCommunityReport persists report under (namespace, community_id),
// overwriting any existing report for that community via ON CONFLICT — the
// report cache's write side.
func (s *Store) PutCommunityReport(ctx context.Context, namespace string, report graph.CommunityReport) error {
	stmt := fmt.Sprintf(`
		INSERT INTO %s (namespace, community_id, title, summary, content_hash)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (namespace, community_id) DO UPDATE SET
			title        = EXCLUDED.title,
			summary      = EXCLUDED.summary,
			content_hash = EXCLUDED.content_hash`, s.communityReportsTable())
	if _, err := s.pool.Exec(ctx, stmt,
		namespace, report.CommunityID, report.Title, report.Summary, report.ContentHash,
	); err != nil {
		return fmt.Errorf("postgres: put community report %s: %w", report.CommunityID, err)
	}
	return nil
}

// CommunityReport returns the stored report for a community. The bool is
// false (and the error nil) for an unknown community ID — a cache miss.
func (s *Store) CommunityReport(ctx context.Context, namespace, communityID string) (graph.CommunityReport, bool, error) {
	stmt := fmt.Sprintf(`SELECT community_id, title, summary, content_hash
		FROM %s WHERE namespace = $1 AND community_id = $2`, s.communityReportsTable())
	var (
		report                      graph.CommunityReport
		title, summary, contentHash *string
	)
	err := s.pool.QueryRow(ctx, stmt, namespace, communityID).
		Scan(&report.CommunityID, &title, &summary, &contentHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return graph.CommunityReport{}, false, nil
	}
	if err != nil {
		return graph.CommunityReport{}, false, fmt.Errorf("postgres: query community report: %w", err)
	}
	if title != nil {
		report.Title = *title
	}
	if summary != nil {
		report.Summary = *summary
	}
	if contentHash != nil {
		report.ContentHash = *contentHash
	}
	return report, true, nil
}
