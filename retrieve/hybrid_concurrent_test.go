package retrieve

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/costa92/llm-agent-rag/store"
)

// slowRetriever is a controlled mock that sleeps `delay` before returning its
// configured hits/trace/err. It is used to prove that HybridRetriever fans out
// its sub-retrievers concurrently — wall-clock cost should be max(delays)
// rather than sum(delays).
type slowRetriever struct {
	hits  []store.Hit
	trace Trace
	err   error
	delay time.Duration
}

func (s slowRetriever) Retrieve(ctx context.Context, _ Request) ([]store.Hit, Trace, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, Trace{}, ctx.Err()
	}
	return append([]store.Hit(nil), s.hits...), s.trace, s.err
}

// TestHybridRetriever_RunsRetrieversConcurrently proves that the four sub
// retrievers (Dense, Lexical, Structure, Graph) execute in parallel rather
// than sequentially. Each mock sleeps 100ms; sequential code would take ~400ms,
// concurrent fan-out should take ~100ms. Threshold 250ms leaves headroom for
// CI flake while still being well below the 400ms sequential floor.
func TestHybridRetriever_RunsRetrieversConcurrently(t *testing.T) {
	const delay = 100 * time.Millisecond
	dense := slowRetriever{hits: hitList("d1"), delay: delay}
	lexical := slowRetriever{hits: hitList("l1"), delay: delay}
	structure := slowRetriever{hits: hitList("s1"), delay: delay}
	graph := slowRetriever{hits: hitList("g1"), delay: delay}

	r := HybridRetriever{
		Dense:     dense,
		Lexical:   lexical,
		Structure: structure,
		Graph:     graph,
	}
	start := time.Now()
	hits, _, err := r.Retrieve(context.Background(), Request{
		Query:           "concurrent",
		TopK:            10,
		EnableStructure: true,
		EnableGraph:     true,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if len(hits) != 4 {
		t.Fatalf("len(hits) = %d, want 4 (d1, l1, s1, g1)", len(hits))
	}
	if elapsed >= 250*time.Millisecond {
		t.Fatalf("Retrieve took %v; expected < 250ms with concurrent fan-out (sequential would be ~%v)",
			elapsed, 4*delay)
	}
	t.Logf("HybridRetriever fan-out elapsed=%v (sequential baseline ~%v)", elapsed, 4*delay)
}

// erroringRetriever returns a fixed error.
type erroringRetriever struct {
	err   error
	delay time.Duration
}

func (e erroringRetriever) Retrieve(ctx context.Context, _ Request) ([]store.Hit, Trace, error) {
	if e.delay > 0 {
		select {
		case <-time.After(e.delay):
		case <-ctx.Done():
			return nil, Trace{}, ctx.Err()
		}
	}
	return nil, Trace{}, e.err
}

// TestHybridRetriever_PreservesErrorPrecedence_DenseWins is a safety-pin test
// — currently green with sequential code, must remain green with concurrent
// fan-out. Even if Graph completes first temporally, Dense's error must win
// because the original sequential code returned Dense first.
func TestHybridRetriever_PreservesErrorPrecedence_DenseWins(t *testing.T) {
	denseErr := errors.New("dense failed")
	lexErr := errors.New("lexical failed")
	structErr := errors.New("structure failed")
	graphErr := errors.New("graph failed")

	r := HybridRetriever{
		// Dense is slowest — proves precedence is by index, not arrival.
		Dense:     erroringRetriever{err: denseErr, delay: 50 * time.Millisecond},
		Lexical:   erroringRetriever{err: lexErr},
		Structure: erroringRetriever{err: structErr},
		Graph:     erroringRetriever{err: graphErr},
	}
	_, _, err := r.Retrieve(context.Background(), Request{
		Query:           "errs",
		EnableStructure: true,
		EnableGraph:     true,
	})
	if !errors.Is(err, denseErr) {
		t.Fatalf("err = %v, want Dense error (%v) — precedence Dense > Lexical > Structure > Graph", err, denseErr)
	}
}

// TestHybridRetriever_PreservesErrorPrecedence_LexicalWinsWhenDenseOK is a
// safety-pin test that ensures Lexical's error wins when Dense succeeds but
// Lexical/Structure/Graph all fail.
func TestHybridRetriever_PreservesErrorPrecedence_LexicalWinsWhenDenseOK(t *testing.T) {
	lexErr := errors.New("lexical failed")
	structErr := errors.New("structure failed")
	graphErr := errors.New("graph failed")

	dense := &recordingRetriever{hitsByQ: map[string][]store.Hit{"errs": hitList("d1")}}
	r := HybridRetriever{
		Dense:     dense,
		Lexical:   erroringRetriever{err: lexErr, delay: 30 * time.Millisecond},
		Structure: erroringRetriever{err: structErr},
		Graph:     erroringRetriever{err: graphErr},
	}
	_, _, err := r.Retrieve(context.Background(), Request{
		Query:           "errs",
		EnableStructure: true,
		EnableGraph:     true,
	})
	if !errors.Is(err, lexErr) {
		t.Fatalf("err = %v, want Lexical error (%v) — precedence Dense > Lexical > Structure > Graph", err, lexErr)
	}
}

// TestHybridRetriever_FusionOrderingDeterministicUnderConcurrency runs the
// same hybrid query many times with four active retrievers. The trace.Fusion
// slice is sorted (RRFScore desc, ChunkID asc) — strictly deterministic by
// construction — so under concurrent fan-out it must be byte-for-byte
// identical across runs regardless of goroutine completion order. The
// returned []store.Hit, sorted only by RRFScore (no ChunkID tiebreak), may
// vary in tie-breaking order depending on map iteration; this test does NOT
// pin that pre-existing behavior. It pins the contract that fusion
// attribution is deterministic — which is what callers observe via Trace.
func TestHybridRetriever_FusionOrderingDeterministicUnderConcurrency(t *testing.T) {
	const q = "deterministic"
	buildR := func() HybridRetriever {
		return HybridRetriever{
			Dense:     slowRetriever{hits: hitList("c1", "c2", "c3"), delay: 1 * time.Millisecond},
			Lexical:   slowRetriever{hits: hitList("c2", "c3", "c4"), delay: 2 * time.Millisecond},
			Structure: slowRetriever{hits: hitList("c3", "c4", "c5"), delay: 1 * time.Millisecond},
			Graph:     slowRetriever{hits: hitList("c4", "c5", "c1"), delay: 1 * time.Millisecond},
		}
	}
	req := Request{Query: q, TopK: 20, EnableStructure: true, EnableGraph: true}

	_, baseTrace, err := buildR().Retrieve(context.Background(), req)
	if err != nil {
		t.Fatalf("baseline Retrieve(): %v", err)
	}
	// Sanity: Fusion attribution should be non-empty and stably ordered.
	if len(baseTrace.Fusion) == 0 {
		t.Fatalf("baseline Fusion is empty; expected attributions for every fused chunk")
	}

	for i := 0; i < 50; i++ {
		_, trace, err := buildR().Retrieve(context.Background(), req)
		if err != nil {
			t.Fatalf("run %d Retrieve(): %v", i, err)
		}
		if !reflect.DeepEqual(trace.Fusion, baseTrace.Fusion) {
			t.Fatalf("run %d: Fusion attribution differs from baseline\n  got: %+v\n want: %+v",
				i, trace.Fusion, baseTrace.Fusion)
		}
	}
}

// TestHybridRetriever_TracePathsPreserved verifies the indexed-slot pattern
// correctly threads each retriever's trace into the right field of the merged
// trace. A naive concurrent rewrite that overwrites a shared trace var would
// fail this.
func TestHybridRetriever_TracePathsPreserved(t *testing.T) {
	denseTrace := Trace{
		EffectiveQuery: "A",
		QueryVariants:  []string{"A", "alt-A"},
		RoutePath:      []string{"r1", "r2"},
		AutoRoutePath:  []string{"ar1"},
	}
	structureTrace := Trace{
		SearchPath:       []string{"x", "y"},
		MatchedSections:  []string{"s1"},
		ExpandedSections: []string{"s2"},
		ExpandedChunkIDs: []string{"c1", "c2"},
	}
	graphTrace := Trace{
		Graph: GraphTrace{
			SeedEntityIDs:    []string{"e1"},
			ReachedEntityIDs: []string{"e1", "e2"},
			MaxHop:           2,
		},
	}

	// Stagger delays so different goroutines complete first across runs.
	dense := slowRetriever{hits: hitList("d1"), trace: denseTrace, delay: 5 * time.Millisecond}
	lexical := slowRetriever{hits: hitList("l1"), delay: 1 * time.Millisecond}
	structure := slowRetriever{hits: hitList("s1"), trace: structureTrace, delay: 3 * time.Millisecond}
	graph := slowRetriever{hits: hitList("g1"), trace: graphTrace, delay: 2 * time.Millisecond}

	r := HybridRetriever{Dense: dense, Lexical: lexical, Structure: structure, Graph: graph}
	_, trace, err := r.Retrieve(context.Background(), Request{
		Query:           "trace",
		TopK:            10,
		EnableStructure: true,
		EnableGraph:     true,
	})
	if err != nil {
		t.Fatalf("Retrieve(): %v", err)
	}
	if trace.EffectiveQuery != "A" {
		t.Fatalf("EffectiveQuery = %q, want %q (from Dense)", trace.EffectiveQuery, "A")
	}
	if !reflect.DeepEqual(trace.QueryVariants, []string{"A", "alt-A"}) {
		t.Fatalf("QueryVariants = %v, want [A alt-A] (from Dense)", trace.QueryVariants)
	}
	if !reflect.DeepEqual(trace.RoutePath, []string{"r1", "r2"}) {
		t.Fatalf("RoutePath = %v, want [r1 r2] (from Dense)", trace.RoutePath)
	}
	if !reflect.DeepEqual(trace.AutoRoutePath, []string{"ar1"}) {
		t.Fatalf("AutoRoutePath = %v, want [ar1] (from Dense)", trace.AutoRoutePath)
	}
	if !reflect.DeepEqual(trace.SearchPath, []string{"x", "y"}) {
		t.Fatalf("SearchPath = %v, want [x y] (from Structure)", trace.SearchPath)
	}
	if !reflect.DeepEqual(trace.MatchedSections, []string{"s1"}) {
		t.Fatalf("MatchedSections = %v, want [s1] (from Structure)", trace.MatchedSections)
	}
	if !reflect.DeepEqual(trace.ExpandedSections, []string{"s2"}) {
		t.Fatalf("ExpandedSections = %v, want [s2] (from Structure)", trace.ExpandedSections)
	}
	if !reflect.DeepEqual(trace.ExpandedChunkIDs, []string{"c1", "c2"}) {
		t.Fatalf("ExpandedChunkIDs = %v, want [c1 c2] (from Structure)", trace.ExpandedChunkIDs)
	}
	if !reflect.DeepEqual(trace.Graph.SeedEntityIDs, []string{"e1"}) {
		t.Fatalf("Graph.SeedEntityIDs = %v, want [e1] (from Graph)", trace.Graph.SeedEntityIDs)
	}
	if !reflect.DeepEqual(trace.Graph.ReachedEntityIDs, []string{"e1", "e2"}) {
		t.Fatalf("Graph.ReachedEntityIDs = %v, want [e1 e2] (from Graph)", trace.Graph.ReachedEntityIDs)
	}
	if trace.Graph.MaxHop != 2 {
		t.Fatalf("Graph.MaxHop = %d, want 2 (from Graph)", trace.Graph.MaxHop)
	}
}
