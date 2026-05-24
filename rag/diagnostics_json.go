package rag

// This file implements the v1.6.0 C-DiagnosticsExport canonical codec
// for rag.Diagnostics.
//
// Why a separate codec instead of json: tags?
//
//   - The v1.6.0 design lock forbids adding json: tags to existing
//     exported structs (Diagnostics, ReflectionDiagnostics, ChunkScore,
//     GraphTrace, ...). The wire shapes here are UNEXPORTED so future
//     reshuffles do not break callers.
//
//   - time.Duration fields encode as int64 nanoseconds (JSON has no
//     native duration; ns-since-epoch is the de-facto convention).
//
//   - graph.Subgraph is serialized as a minimal STRUCTURAL projection:
//     entity IDs, edge (relation) IDs, and the max-hop number. The full
//     Subgraph carries Entity{Name, Type, Description, ...} and per-
//     edge Description/Weight values that would balloon the payload and
//     leak content into evidence dumps. Callers that need the full
//     Subgraph should keep it in-memory and serialize on their own; the
//     v1.6.0 codec promises only IDs.
//
//   - All unmarshals use json.Decoder.DisallowUnknownFields(); unknown
//     top-level keys error so schema drift surfaces immediately.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/costa92/llm-agent-rag/graph"
	"github.com/costa92/llm-agent-rag/obs"
	"github.com/costa92/llm-agent-rag/rerank"
	"github.com/costa92/llm-agent-rag/retrieve"
)

// --- obs ---

type tokenUsageWire struct {
	PromptTokens     int  `json:"prompt_tokens,omitempty"`
	CompletionTokens int  `json:"completion_tokens,omitempty"`
	TotalTokens      int  `json:"total_tokens,omitempty"`
	Estimated        bool `json:"estimated,omitempty"`
}

func tokenUsageToWire(t obs.TokenUsage) tokenUsageWire {
	return tokenUsageWire{
		PromptTokens:     t.PromptTokens,
		CompletionTokens: t.CompletionTokens,
		TotalTokens:      t.TotalTokens,
		Estimated:        t.Estimated,
	}
}

func tokenUsageFromWire(w tokenUsageWire) obs.TokenUsage {
	return obs.TokenUsage{
		PromptTokens:     w.PromptTokens,
		CompletionTokens: w.CompletionTokens,
		TotalTokens:      w.TotalTokens,
		Estimated:        w.Estimated,
	}
}

type stageTimingWire struct {
	Stage       string `json:"stage"`
	DurationNs  int64  `json:"duration_ns"`
}

func stageTimingToWire(s obs.StageTiming) stageTimingWire {
	return stageTimingWire{Stage: s.Stage, DurationNs: int64(s.Duration)}
}

func stageTimingFromWire(w stageTimingWire) obs.StageTiming {
	return obs.StageTiming{Stage: w.Stage, Duration: time.Duration(w.DurationNs)}
}

type stageTokenUsageWire struct {
	Stage string         `json:"stage"`
	Usage tokenUsageWire `json:"usage"`
}

func stageTokenUsageToWire(s obs.StageTokenUsage) stageTokenUsageWire {
	return stageTokenUsageWire{Stage: s.Stage, Usage: tokenUsageToWire(s.Usage)}
}

func stageTokenUsageFromWire(w stageTokenUsageWire) obs.StageTokenUsage {
	return obs.StageTokenUsage{Stage: w.Stage, Usage: tokenUsageFromWire(w.Usage)}
}

type callCountsWire struct {
	Embed    int `json:"embed,omitempty"`
	Generate int `json:"generate,omitempty"`
}

type metricsWire struct {
	TotalDurationNs int64                 `json:"total_duration_ns,omitempty"`
	Stages          []stageTimingWire     `json:"stages,omitempty"`
	Calls           callCountsWire        `json:"calls,omitempty"`
	Tokens          tokenUsageWire        `json:"tokens,omitempty"`
	StageTokenUsage []stageTokenUsageWire `json:"stage_token_usage,omitempty"`
}

func metricsToWireObs(m obs.Metrics) metricsWire {
	stages := make([]stageTimingWire, len(m.Stages))
	for i, s := range m.Stages {
		stages[i] = stageTimingToWire(s)
	}
	stu := make([]stageTokenUsageWire, len(m.StageTokenUsage))
	for i, s := range m.StageTokenUsage {
		stu[i] = stageTokenUsageToWire(s)
	}
	return metricsWire{
		TotalDurationNs: int64(m.TotalDuration),
		Stages:          stages,
		Calls:           callCountsWire{Embed: m.Calls.Embed, Generate: m.Calls.Generate},
		Tokens:          tokenUsageToWire(m.Tokens),
		StageTokenUsage: stu,
	}
}

func metricsFromWireObs(w metricsWire) obs.Metrics {
	stages := make([]obs.StageTiming, len(w.Stages))
	for i, s := range w.Stages {
		stages[i] = stageTimingFromWire(s)
	}
	var stu []obs.StageTokenUsage
	if len(w.StageTokenUsage) > 0 {
		stu = make([]obs.StageTokenUsage, len(w.StageTokenUsage))
		for i, s := range w.StageTokenUsage {
			stu[i] = stageTokenUsageFromWire(s)
		}
	}
	return obs.Metrics{
		TotalDuration:   time.Duration(w.TotalDurationNs),
		Stages:          stages,
		Calls:           obs.CallCounts{Embed: w.Calls.Embed, Generate: w.Calls.Generate},
		Tokens:          tokenUsageFromWire(w.Tokens),
		StageTokenUsage: stu,
	}
}

// --- retrieve ---

type routeCandidateWire struct {
	Path       []string `json:"path,omitempty"`
	Score      float64  `json:"score"`
	Confidence float64  `json:"confidence"`
	Queries    []string `json:"queries,omitempty"`
	Signals    []string `json:"signals,omitempty"`
	Selected   bool     `json:"selected,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

func routeCandidateToWire(c retrieve.RouteCandidate) routeCandidateWire {
	return routeCandidateWire{
		Path: c.Path, Score: c.Score, Confidence: c.Confidence,
		Queries: c.Queries, Signals: c.Signals,
		Selected: c.Selected, Reason: c.Reason,
	}
}

func routeCandidateFromWire(w routeCandidateWire) retrieve.RouteCandidate {
	return retrieve.RouteCandidate{
		Path: w.Path, Score: w.Score, Confidence: w.Confidence,
		Queries: w.Queries, Signals: w.Signals,
		Selected: w.Selected, Reason: w.Reason,
	}
}

type routePolicyTraceWire struct {
	Mode                string   `json:"mode,omitempty"`
	ConfidenceThreshold float64  `json:"confidence_threshold,omitempty"`
	ConfidenceGap       float64  `json:"confidence_gap,omitempty"`
	Gap                 float64  `json:"gap,omitempty"`
	Fanout              int      `json:"fanout,omitempty"`
	CandidateCount      int      `json:"candidate_count,omitempty"`
	SelectedCount       int      `json:"selected_count,omitempty"`
	Rationale           []string `json:"rationale,omitempty"`
}

func routePolicyTraceToWire(p retrieve.RoutePolicyTrace) routePolicyTraceWire {
	return routePolicyTraceWire{
		Mode: p.Mode, ConfidenceThreshold: p.ConfidenceThreshold,
		ConfidenceGap: p.ConfidenceGap, Gap: p.Gap,
		Fanout: p.Fanout, CandidateCount: p.CandidateCount,
		SelectedCount: p.SelectedCount, Rationale: p.Rationale,
	}
}

func routePolicyTraceFromWire(w routePolicyTraceWire) retrieve.RoutePolicyTrace {
	return retrieve.RoutePolicyTrace{
		Mode: w.Mode, ConfidenceThreshold: w.ConfidenceThreshold,
		ConfidenceGap: w.ConfidenceGap, Gap: w.Gap,
		Fanout: w.Fanout, CandidateCount: w.CandidateCount,
		SelectedCount: w.SelectedCount, Rationale: w.Rationale,
	}
}

type trajectoryStepWire struct {
	Route            []string `json:"route,omitempty"`
	Confidence       float64  `json:"confidence,omitempty"`
	Mode             string   `json:"mode,omitempty"`
	HitCount         int      `json:"hit_count,omitempty"`
	HitIDs           []string `json:"hit_ids,omitempty"`
	MatchedSections  []string `json:"matched_sections,omitempty"`
	ExpandedSections []string `json:"expanded_sections,omitempty"`
	Rationale        string   `json:"rationale,omitempty"`
}

func trajectoryStepToWire(s retrieve.TrajectoryStep) trajectoryStepWire {
	return trajectoryStepWire{
		Route: s.Route, Confidence: s.Confidence, Mode: s.Mode,
		HitCount: s.HitCount, HitIDs: s.HitIDs,
		MatchedSections: s.MatchedSections, ExpandedSections: s.ExpandedSections,
		Rationale: s.Rationale,
	}
}

func trajectoryStepFromWire(w trajectoryStepWire) retrieve.TrajectoryStep {
	return retrieve.TrajectoryStep{
		Route: w.Route, Confidence: w.Confidence, Mode: w.Mode,
		HitCount: w.HitCount, HitIDs: w.HitIDs,
		MatchedSections: w.MatchedSections, ExpandedSections: w.ExpandedSections,
		Rationale: w.Rationale,
	}
}

// subgraphWire is the minimal projection: entity IDs, edge (relation) IDs,
// and the max-hop number from the parent GraphTrace. It is documented in
// CHANGELOG v1.6.0 Compatibility as a v1.6.0 contract — future versions
// MAY extend this projection (but not reshape it incompatibly).
type subgraphWire struct {
	EntityIDs []string `json:"entity_ids,omitempty"`
	EdgeIDs   []string `json:"edge_ids,omitempty"`
}

// subgraphToWire projects a *graph.Subgraph to entity IDs + relation IDs.
// A nil input returns nil so the parent field can use omitempty.
func subgraphToWire(s *graph.Subgraph) *subgraphWire {
	if s == nil {
		return nil
	}
	ents := make([]string, 0, len(s.Entities))
	for _, e := range s.Entities {
		ents = append(ents, e.ID)
	}
	rels := make([]string, 0, len(s.Relations))
	for _, r := range s.Relations {
		rels = append(rels, r.ID)
	}
	return &subgraphWire{EntityIDs: ents, EdgeIDs: rels}
}

// subgraphFromWire rebuilds a *graph.Subgraph with only entity and edge
// IDs populated. Depth is set to nil (the projection drops it); other
// graph-side fields (Entity.Name, Entity.Type, Relation.Source/Target,
// Relation.Weight) are zero. Callers needing the full Subgraph should
// keep it in-memory.
func subgraphFromWire(w *subgraphWire) *graph.Subgraph {
	if w == nil {
		return nil
	}
	ents := make([]graph.Entity, 0, len(w.EntityIDs))
	for _, id := range w.EntityIDs {
		ents = append(ents, graph.Entity{ID: id})
	}
	rels := make([]graph.Relation, 0, len(w.EdgeIDs))
	for _, id := range w.EdgeIDs {
		rels = append(rels, graph.Relation{ID: id})
	}
	return &graph.Subgraph{Entities: ents, Relations: rels}
}

type graphTraceWire struct {
	SeedEntityIDs    []string      `json:"seed_entity_ids,omitempty"`
	ReachedEntityIDs []string      `json:"reached_entity_ids,omitempty"`
	MaxHop           int           `json:"max_hop,omitempty"`
	CommunityIDs     []string      `json:"community_ids,omitempty"`
	EvidenceSubgraph *subgraphWire `json:"evidence_subgraph,omitempty"`
}

func graphTraceToWire(g retrieve.GraphTrace) graphTraceWire {
	return graphTraceWire{
		SeedEntityIDs:    g.SeedEntityIDs,
		ReachedEntityIDs: g.ReachedEntityIDs,
		MaxHop:           g.MaxHop,
		CommunityIDs:     g.CommunityIDs,
		EvidenceSubgraph: subgraphToWire(g.EvidenceSubgraph),
	}
}

func graphTraceFromWire(w graphTraceWire) retrieve.GraphTrace {
	return retrieve.GraphTrace{
		SeedEntityIDs:    w.SeedEntityIDs,
		ReachedEntityIDs: w.ReachedEntityIDs,
		MaxHop:           w.MaxHop,
		CommunityIDs:     w.CommunityIDs,
		EvidenceSubgraph: subgraphFromWire(w.EvidenceSubgraph),
		// Paths is intentionally dropped — graph.RankedPath carries the
		// full Entity slice and would inflate evidence dumps. Future
		// versions may add it.
	}
}

// --- rerank ---

type rerankScoreWire struct {
	ChunkID     string  `json:"chunk_id"`
	InputScore  float64 `json:"input_score,omitempty"`
	OutputScore float64 `json:"output_score,omitempty"`
	InputRank   int     `json:"input_rank,omitempty"`
	OutputRank  int     `json:"output_rank,omitempty"`
	RankDelta   int     `json:"rank_delta,omitempty"`
}

func rerankScoreToWire(s rerank.RerankScore) rerankScoreWire {
	return rerankScoreWire{
		ChunkID: s.ChunkID, InputScore: s.InputScore, OutputScore: s.OutputScore,
		InputRank: s.InputRank, OutputRank: s.OutputRank, RankDelta: s.RankDelta,
	}
}

func rerankScoreFromWire(w rerankScoreWire) rerank.RerankScore {
	return rerank.RerankScore{
		ChunkID: w.ChunkID, InputScore: w.InputScore, OutputScore: w.OutputScore,
		InputRank: w.InputRank, OutputRank: w.OutputRank, RankDelta: w.RankDelta,
	}
}

// --- rag/inject ---

type injectionFindingWire struct {
	ChunkID  string   `json:"chunk_id"`
	Patterns []string `json:"patterns,omitempty"`
	Action   string   `json:"action,omitempty"`
}

func injectionFindingToWire(f InjectionFinding) injectionFindingWire {
	return injectionFindingWire{ChunkID: f.ChunkID, Patterns: f.Patterns, Action: f.Action}
}

func injectionFindingFromWire(w injectionFindingWire) InjectionFinding {
	return InjectionFinding{ChunkID: w.ChunkID, Patterns: w.Patterns, Action: w.Action}
}

// --- rag/reflection ---

type chunkScoreWire struct {
	HitID     string  `json:"hit_id"`
	Relevance float64 `json:"relevance,omitempty"`
	Support   float64 `json:"support,omitempty"`
	Reason    string  `json:"reason,omitempty"`
}

func chunkScoreToWire(c ChunkScore) chunkScoreWire {
	return chunkScoreWire{HitID: c.HitID, Relevance: c.Relevance, Support: c.Support, Reason: c.Reason}
}

func chunkScoreFromWire(w chunkScoreWire) ChunkScore {
	return ChunkScore{HitID: w.HitID, Relevance: w.Relevance, Support: w.Support, Reason: w.Reason}
}

type reflectionRoundDiagnosticsWire struct {
	Round               int                  `json:"round"`
	InputQuery          string               `json:"input_query,omitempty"`
	EffectiveQuery      string               `json:"effective_query,omitempty"`
	RewrittenQuery      string               `json:"rewritten_query,omitempty"`
	ReturnedChunkIDs    []string             `json:"returned_chunk_ids,omitempty"`
	PromptChunkIDs      []string             `json:"prompt_chunk_ids,omitempty"`
	UniqueDocCount      int                  `json:"unique_doc_count,omitempty"`
	TopScore            float64              `json:"top_score,omitempty"`
	Decision            string               `json:"decision,omitempty"`
	DecisionMode        string               `json:"decision_mode,omitempty"`
	DecisionReason      string               `json:"decision_reason,omitempty"`
	RawDecisionText     string               `json:"raw_decision_text,omitempty"`
	DecisionPrompt      string               `json:"decision_prompt,omitempty"`
	RoutePath           []string             `json:"route_path,omitempty"`
	AutoRoutePath       []string             `json:"auto_route_path,omitempty"`
	AutoRouteCandidates []routeCandidateWire `json:"auto_route_candidates,omitempty"`
	SearchTrajectory    []trajectoryStepWire `json:"search_trajectory,omitempty"`
	GraphTrace          graphTraceWire       `json:"graph_trace,omitempty"`
	ChunkScores         []chunkScoreWire     `json:"chunk_scores,omitempty"`
	FollowupQueries     []string             `json:"followup_queries,omitempty"`
}

func reflectionRoundToWire(r ReflectionRoundDiagnostics) reflectionRoundDiagnosticsWire {
	cands := make([]routeCandidateWire, len(r.AutoRouteCandidates))
	for i, c := range r.AutoRouteCandidates {
		cands[i] = routeCandidateToWire(c)
	}
	traj := make([]trajectoryStepWire, len(r.SearchTrajectory))
	for i, s := range r.SearchTrajectory {
		traj[i] = trajectoryStepToWire(s)
	}
	cs := make([]chunkScoreWire, len(r.ChunkScores))
	for i, c := range r.ChunkScores {
		cs[i] = chunkScoreToWire(c)
	}
	return reflectionRoundDiagnosticsWire{
		Round:               r.Round,
		InputQuery:          r.InputQuery,
		EffectiveQuery:      r.EffectiveQuery,
		RewrittenQuery:      r.RewrittenQuery,
		ReturnedChunkIDs:    r.ReturnedChunkIDs,
		PromptChunkIDs:      r.PromptChunkIDs,
		UniqueDocCount:      r.UniqueDocCount,
		TopScore:            r.TopScore,
		Decision:            string(r.Decision),
		DecisionMode:        string(r.DecisionMode),
		DecisionReason:      r.DecisionReason,
		RawDecisionText:     r.RawDecisionText,
		DecisionPrompt:      r.DecisionPrompt,
		RoutePath:           r.RoutePath,
		AutoRoutePath:       r.AutoRoutePath,
		AutoRouteCandidates: cands,
		SearchTrajectory:    traj,
		GraphTrace:          graphTraceToWire(r.GraphTrace),
		ChunkScores:         cs,
		FollowupQueries:     r.FollowupQueries,
	}
}

func reflectionRoundFromWire(w reflectionRoundDiagnosticsWire) ReflectionRoundDiagnostics {
	cands := make([]retrieve.RouteCandidate, len(w.AutoRouteCandidates))
	for i, c := range w.AutoRouteCandidates {
		cands[i] = routeCandidateFromWire(c)
	}
	traj := make([]retrieve.TrajectoryStep, len(w.SearchTrajectory))
	for i, s := range w.SearchTrajectory {
		traj[i] = trajectoryStepFromWire(s)
	}
	cs := make([]ChunkScore, len(w.ChunkScores))
	for i, c := range w.ChunkScores {
		cs[i] = chunkScoreFromWire(c)
	}
	return ReflectionRoundDiagnostics{
		Round:               w.Round,
		InputQuery:          w.InputQuery,
		EffectiveQuery:      w.EffectiveQuery,
		RewrittenQuery:      w.RewrittenQuery,
		ReturnedChunkIDs:    w.ReturnedChunkIDs,
		PromptChunkIDs:      w.PromptChunkIDs,
		UniqueDocCount:      w.UniqueDocCount,
		TopScore:            w.TopScore,
		Decision:            ReflectionDecision(w.Decision),
		DecisionMode:        ReflectionMode(w.DecisionMode),
		DecisionReason:      w.DecisionReason,
		RawDecisionText:     w.RawDecisionText,
		DecisionPrompt:      w.DecisionPrompt,
		RoutePath:           w.RoutePath,
		AutoRoutePath:       w.AutoRoutePath,
		AutoRouteCandidates: cands,
		SearchTrajectory:    traj,
		GraphTrace:          graphTraceFromWire(w.GraphTrace),
		ChunkScores:         cs,
		FollowupQueries:     w.FollowupQueries,
	}
}

type reflectionDiagnosticsWire struct {
	Mode                string                           `json:"mode,omitempty"`
	Rounds              int                              `json:"rounds,omitempty"`
	AdoptedRound        int                              `json:"adopted_round,omitempty"`
	StopReason          string                           `json:"stop_reason,omitempty"`
	FailureFallback     bool                             `json:"failure_fallback,omitempty"`
	FailureReason       string                           `json:"failure_reason,omitempty"`
	DecisionModelCalls  int                              `json:"decision_model_calls,omitempty"`
	RewriteModelCalls   int                              `json:"rewrite_model_calls,omitempty"`
	RoundDetails        []reflectionRoundDiagnosticsWire `json:"round_details,omitempty"`
	FollowupQueriesUsed int                              `json:"followup_queries_used,omitempty"`
}

func reflectionToWire(r ReflectionDiagnostics) reflectionDiagnosticsWire {
	rd := make([]reflectionRoundDiagnosticsWire, len(r.RoundDetails))
	for i, d := range r.RoundDetails {
		rd[i] = reflectionRoundToWire(d)
	}
	return reflectionDiagnosticsWire{
		Mode:                string(r.Mode),
		Rounds:              r.Rounds,
		AdoptedRound:        r.AdoptedRound,
		StopReason:          r.StopReason,
		FailureFallback:     r.FailureFallback,
		FailureReason:       r.FailureReason,
		DecisionModelCalls:  r.DecisionModelCalls,
		RewriteModelCalls:   r.RewriteModelCalls,
		RoundDetails:        rd,
		FollowupQueriesUsed: r.FollowupQueriesUsed,
	}
}

func reflectionFromWire(w reflectionDiagnosticsWire) ReflectionDiagnostics {
	rd := make([]ReflectionRoundDiagnostics, len(w.RoundDetails))
	for i, d := range w.RoundDetails {
		rd[i] = reflectionRoundFromWire(d)
	}
	return ReflectionDiagnostics{
		Mode:                ReflectionMode(w.Mode),
		Rounds:              w.Rounds,
		AdoptedRound:        w.AdoptedRound,
		StopReason:          w.StopReason,
		FailureFallback:     w.FailureFallback,
		FailureReason:       w.FailureReason,
		DecisionModelCalls:  w.DecisionModelCalls,
		RewriteModelCalls:   w.RewriteModelCalls,
		RoundDetails:        rd,
		FollowupQueriesUsed: w.FollowupQueriesUsed,
	}
}

// --- rag/global + rag/drift ---

type communityReportWire struct {
	CommunityID string `json:"community_id"`
	Title       string `json:"title,omitempty"`
	Summary     string `json:"summary,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

func communityReportToWire(c graph.CommunityReport) communityReportWire {
	return communityReportWire{
		CommunityID: c.CommunityID, Title: c.Title,
		Summary: c.Summary, ContentHash: c.ContentHash,
	}
}

func communityReportFromWire(w communityReportWire) graph.CommunityReport {
	return graph.CommunityReport{
		CommunityID: w.CommunityID, Title: w.Title,
		Summary: w.Summary, ContentHash: w.ContentHash,
	}
}

type globalDiagnosticsWire struct {
	CommunityIDs     []string              `json:"community_ids,omitempty"`
	MapScores        map[string]int        `json:"map_scores,omitempty"`
	MapCalls         int                   `json:"map_calls,omitempty"`
	ReduceCalls      int                   `json:"reduce_calls,omitempty"`
	ConsultedReports []communityReportWire `json:"consulted_reports,omitempty"`
}

func globalToWire(g GlobalDiagnostics) globalDiagnosticsWire {
	cr := make([]communityReportWire, len(g.ConsultedReports))
	for i, r := range g.ConsultedReports {
		cr[i] = communityReportToWire(r)
	}
	return globalDiagnosticsWire{
		CommunityIDs: g.CommunityIDs, MapScores: g.MapScores,
		MapCalls: g.MapCalls, ReduceCalls: g.ReduceCalls,
		ConsultedReports: cr,
	}
}

func globalFromWire(w globalDiagnosticsWire) GlobalDiagnostics {
	cr := make([]graph.CommunityReport, len(w.ConsultedReports))
	for i, r := range w.ConsultedReports {
		cr[i] = communityReportFromWire(r)
	}
	return GlobalDiagnostics{
		CommunityIDs: w.CommunityIDs, MapScores: w.MapScores,
		MapCalls: w.MapCalls, ReduceCalls: w.ReduceCalls,
		ConsultedReports: cr,
	}
}

type driftDiagnosticsWire struct {
	PrimerCommunityIDs []string              `json:"primer_community_ids,omitempty"`
	Rounds             int                   `json:"rounds,omitempty"`
	RoundEntityIDs     [][]string            `json:"round_entity_ids,omitempty"`
	ConsultedReports   []communityReportWire `json:"consulted_reports,omitempty"`
}

func driftToWire(d DriftDiagnostics) driftDiagnosticsWire {
	cr := make([]communityReportWire, len(d.ConsultedReports))
	for i, r := range d.ConsultedReports {
		cr[i] = communityReportToWire(r)
	}
	return driftDiagnosticsWire{
		PrimerCommunityIDs: d.PrimerCommunityIDs, Rounds: d.Rounds,
		RoundEntityIDs: d.RoundEntityIDs, ConsultedReports: cr,
	}
}

func driftFromWire(w driftDiagnosticsWire) DriftDiagnostics {
	cr := make([]graph.CommunityReport, len(w.ConsultedReports))
	for i, r := range w.ConsultedReports {
		cr[i] = communityReportFromWire(r)
	}
	return DriftDiagnostics{
		PrimerCommunityIDs: w.PrimerCommunityIDs, Rounds: w.Rounds,
		RoundEntityIDs: w.RoundEntityIDs, ConsultedReports: cr,
	}
}

// --- top-level Diagnostics ---

type diagnosticsWire struct {
	HitCount            int                       `json:"hit_count,omitempty"`
	ReturnedChunkIDs    []string                  `json:"returned_chunk_ids,omitempty"`
	PromptChunkIDs      []string                  `json:"prompt_chunk_ids,omitempty"`
	MatchedSections     []string                  `json:"matched_sections,omitempty"`
	ExpandedChunkIDs    []string                  `json:"expanded_chunk_ids,omitempty"`
	AutoRouteCandidates []routeCandidateWire      `json:"auto_route_candidates,omitempty"`
	RoutePolicy         routePolicyTraceWire      `json:"route_policy,omitempty"`
	SearchTrajectory    []trajectoryStepWire      `json:"search_trajectory,omitempty"`
	RerankScores        []rerankScoreWire         `json:"rerank_scores,omitempty"`
	Metrics             metricsWire               `json:"metrics,omitempty"`
	InjectionFindings   []injectionFindingWire    `json:"injection_findings,omitempty"`
	GraphTrace          graphTraceWire            `json:"graph_trace,omitempty"`
	Global              globalDiagnosticsWire     `json:"global,omitempty"`
	Drift               driftDiagnosticsWire      `json:"drift,omitempty"`
	Reflection          reflectionDiagnosticsWire `json:"reflection,omitempty"`
}

func diagnosticsToWire(d Diagnostics) diagnosticsWire {
	cands := make([]routeCandidateWire, len(d.AutoRouteCandidates))
	for i, c := range d.AutoRouteCandidates {
		cands[i] = routeCandidateToWire(c)
	}
	traj := make([]trajectoryStepWire, len(d.SearchTrajectory))
	for i, s := range d.SearchTrajectory {
		traj[i] = trajectoryStepToWire(s)
	}
	rs := make([]rerankScoreWire, len(d.RerankScores))
	for i, s := range d.RerankScores {
		rs[i] = rerankScoreToWire(s)
	}
	inj := make([]injectionFindingWire, len(d.InjectionFindings))
	for i, f := range d.InjectionFindings {
		inj[i] = injectionFindingToWire(f)
	}
	return diagnosticsWire{
		HitCount:            d.HitCount,
		ReturnedChunkIDs:    d.ReturnedChunkIDs,
		PromptChunkIDs:      d.PromptChunkIDs,
		MatchedSections:     d.MatchedSections,
		ExpandedChunkIDs:    d.ExpandedChunkIDs,
		AutoRouteCandidates: cands,
		RoutePolicy:         routePolicyTraceToWire(d.RoutePolicy),
		SearchTrajectory:    traj,
		RerankScores:        rs,
		Metrics:             metricsToWireObs(d.Metrics),
		InjectionFindings:   inj,
		GraphTrace:          graphTraceToWire(d.GraphTrace),
		Global:              globalToWire(d.Global),
		Drift:               driftToWire(d.Drift),
		Reflection:          reflectionToWire(d.Reflection),
	}
}

func diagnosticsFromWire(w diagnosticsWire) Diagnostics {
	cands := make([]retrieve.RouteCandidate, len(w.AutoRouteCandidates))
	for i, c := range w.AutoRouteCandidates {
		cands[i] = routeCandidateFromWire(c)
	}
	traj := make([]retrieve.TrajectoryStep, len(w.SearchTrajectory))
	for i, s := range w.SearchTrajectory {
		traj[i] = trajectoryStepFromWire(s)
	}
	rs := make([]rerank.RerankScore, len(w.RerankScores))
	for i, s := range w.RerankScores {
		rs[i] = rerankScoreFromWire(s)
	}
	inj := make([]InjectionFinding, len(w.InjectionFindings))
	for i, f := range w.InjectionFindings {
		inj[i] = injectionFindingFromWire(f)
	}
	return Diagnostics{
		HitCount:            w.HitCount,
		ReturnedChunkIDs:    w.ReturnedChunkIDs,
		PromptChunkIDs:      w.PromptChunkIDs,
		MatchedSections:     w.MatchedSections,
		ExpandedChunkIDs:    w.ExpandedChunkIDs,
		AutoRouteCandidates: cands,
		RoutePolicy:         routePolicyTraceFromWire(w.RoutePolicy),
		SearchTrajectory:    traj,
		RerankScores:        rs,
		Metrics:             metricsFromWireObs(w.Metrics),
		InjectionFindings:   inj,
		GraphTrace:          graphTraceFromWire(w.GraphTrace),
		Global:              globalFromWire(w.Global),
		Drift:               driftFromWire(w.Drift),
		Reflection:          reflectionFromWire(w.Reflection),
	}
}

// MarshalDiagnostics serializes a Diagnostics to canonical JSON. The
// embedded *graph.Subgraph in GraphTrace.EvidenceSubgraph is reduced to
// a structural projection (entity IDs + edge IDs); the full Subgraph
// payload is intentionally NOT serialized — see file header.
func MarshalDiagnostics(d Diagnostics) ([]byte, error) {
	return json.Marshal(diagnosticsToWire(d))
}

// UnmarshalDiagnostics parses canonical JSON written by
// MarshalDiagnostics. Unknown top-level fields are rejected
// (DisallowUnknownFields). The rebuilt *graph.Subgraph carries only
// entity IDs and edge IDs (the projection contract documented on
// MarshalDiagnostics).
func UnmarshalDiagnostics(b []byte) (Diagnostics, error) {
	var w diagnosticsWire
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return Diagnostics{}, fmt.Errorf("rag: unmarshal diagnostics: %w", err)
	}
	return diagnosticsFromWire(w), nil
}
