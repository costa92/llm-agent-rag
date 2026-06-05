# Contextual Compression Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `compress.Compressor` seam that shrinks retrieved chunks to query-relevant content between rerank and pack, with `NoopCompressor` (default), `ExtractiveCompressor` (sentence selection by embedding similarity), and `AbstractiveCompressor` (per-chunk LLM summary), gated by `SearchOptions.EnableCompression`.

**Architecture:** A new `compress` package defines `Compressor` and three implementations. `rag.System.askRound` runs the effective compressor after rerank and before pack when `EnableCompression` is set, recording which chunks shrank on `Diagnostics.CompressedChunkIDs`. Unconfigured (or no compressor) = `NoopCompressor` = current behavior byte-for-byte.

**Tech Stack:** Go, `store.Hit`/`StoredChunk`, `embed.Embedder` + `embed.CosineSimilarity`, `generate.Model`, existing `rag` pipeline, `internal/apisnapshot` API gate.

This is **Plan 3 of 3** (Step-back → AskConversation → Compression) from `docs/superpowers/specs/2026-06-05-rag-query-transforms-design.md`.

Key types (already in the codebase):
- `store.Hit{Chunk store.StoredChunk; Score float64}`
- `store.StoredChunk{ID string; ...; Content string; Vector embed.Vector; Metadata map[string]any}` — only `Content` is mutated; `Content` is a string (value), so modifying a copied `Hit` does not alias the originals.
- `embed.Embedder{Embed(ctx, text) (embed.Vector, error); Dimension() int}`, `embed.Vector []float32`, `embed.CosineSimilarity(a, b embed.Vector) float64`
- `obs.StageTiming{Stage string; Duration time.Duration}`

---

### Task 1: `compress` package core — `Compressor`, `NoopCompressor`, errors

**Files:**
- Create: `compress/compress.go`
- Create: `compress/errors.go`
- Test: `compress/compress_test.go`

- [ ] **Step 1: Write the failing test**

Create `compress/compress_test.go`:

```go
package compress

import (
	"context"
	"testing"

	"github.com/costa92/llm-agent-rag/store"
)

func hit(id, content string, score float64) store.Hit {
	return store.Hit{Chunk: store.StoredChunk{ID: id, Content: content}, Score: score}
}

func TestNoopCompressorReturnsHitsUnchanged(t *testing.T) {
	in := []store.Hit{hit("a", "first sentence. second sentence.", 0.9)}
	out, err := NoopCompressor{}.Compress(context.Background(), "query", in)
	if err != nil {
		t.Fatalf("Compress(): %v", err)
	}
	if len(out) != 1 || out[0].Chunk.Content != "first sentence. second sentence." {
		t.Fatalf("out = %#v, want unchanged", out)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./compress/ -v`
Expected: FAIL — package/`NoopCompressor` does not exist (compile/build error).

- [ ] **Step 3: Create the package core**

Create `compress/compress.go`:

```go
// Package compress shrinks retrieved chunks to query-relevant content. It
// runs between rerank and pack in the answer pipeline. Compressor is the
// seam; NoopCompressor, ExtractiveCompressor, and AbstractiveCompressor are
// the built-in implementations.
package compress

import (
	"context"

	"github.com/costa92/llm-agent-rag/store"
)

// Compressor shrinks each hit's Content to the material relevant to query.
// Implementations preserve Chunk.ID, Score, and Metadata so an answer's
// citation provenance is unaffected.
type Compressor interface {
	Compress(ctx context.Context, query string, hits []store.Hit) ([]store.Hit, error)
}

// NoopCompressor returns hits unchanged. It is the default compressor, so an
// unconfigured pipeline behaves exactly as before.
type NoopCompressor struct{}

// Compress returns hits unchanged.
func (NoopCompressor) Compress(_ context.Context, _ string, hits []store.Hit) ([]store.Hit, error) {
	return hits, nil
}
```

Create `compress/errors.go`:

```go
package compress

import "errors"

// ErrEmbedderRequired is returned by ExtractiveCompressor when its Embedder
// is nil.
var ErrEmbedderRequired = errors.New("compress: embedder required")

// ErrModelRequired is returned by AbstractiveCompressor when its Model is nil.
var ErrModelRequired = errors.New("compress: model required")
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./compress/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add compress/compress.go compress/errors.go compress/compress_test.go
git commit -m "feat(compress): add Compressor seam with NoopCompressor

New compress package: Compressor shrinks retrieved chunk content to
query-relevant material between rerank and pack. NoopCompressor is the
identity default."
```

---

### Task 2: `ExtractiveCompressor` — sentence selection by embedding similarity

**Files:**
- Create: `compress/extractive.go`
- Test: `compress/extractive_test.go`

- [ ] **Step 1: Write the failing tests**

Create `compress/extractive_test.go`:

```go
package compress

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/store"
)

// keywordEmbedder embeds text as [containsParis, 1]. The constant second
// dimension keeps every vector non-zero so cosine similarity is never NaN.
type keywordEmbedder struct{}

func (keywordEmbedder) Embed(_ context.Context, text string) (embed.Vector, error) {
	contains := float32(0)
	if strings.Contains(strings.ToLower(text), "paris") {
		contains = 1
	}
	return embed.Vector{contains, 1}, nil
}

func (keywordEmbedder) Dimension() int { return 2 }

func TestExtractiveCompressorKeepsMostRelevantSentence(t *testing.T) {
	c := ExtractiveCompressor{Embedder: keywordEmbedder{}, MaxSentences: 1}
	in := []store.Hit{{
		Chunk: store.StoredChunk{
			ID:       "a",
			Content:  "Berlin is large. Paris is the capital of France. Rome is old.",
			Metadata: map[string]any{"k": "v"},
		},
		Score: 0.9,
	}}
	out, err := c.Compress(context.Background(), "paris", in)
	if err != nil {
		t.Fatalf("Compress(): %v", err)
	}
	if out[0].Chunk.Content != "Paris is the capital of France." {
		t.Fatalf("Content = %q, want only the Paris sentence", out[0].Chunk.Content)
	}
	if out[0].Chunk.ID != "a" || out[0].Score != 0.9 || out[0].Chunk.Metadata["k"] != "v" {
		t.Fatalf("provenance not preserved: %#v", out[0])
	}
	// Original input must not be mutated.
	if in[0].Chunk.Content != "Berlin is large. Paris is the capital of France. Rome is old." {
		t.Fatalf("input mutated: %q", in[0].Chunk.Content)
	}
}

func TestExtractiveCompressorKeepsShortChunksWhole(t *testing.T) {
	c := ExtractiveCompressor{Embedder: keywordEmbedder{}, MaxSentences: 2}
	in := []store.Hit{hit("a", "Only one sentence here.", 0.5)}
	out, err := c.Compress(context.Background(), "paris", in)
	if err != nil {
		t.Fatalf("Compress(): %v", err)
	}
	if out[0].Chunk.Content != "Only one sentence here." {
		t.Fatalf("Content = %q, want unchanged (<= MaxSentences)", out[0].Chunk.Content)
	}
}

func TestExtractiveCompressorRequiresEmbedder(t *testing.T) {
	_, err := ExtractiveCompressor{}.Compress(context.Background(), "paris", nil)
	if !errors.Is(err, ErrEmbedderRequired) {
		t.Fatalf("err = %v, want ErrEmbedderRequired", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./compress/ -run TestExtractiveCompressor -v`
Expected: FAIL — `undefined: ExtractiveCompressor`.

- [ ] **Step 3: Create the extractive compressor**

Create `compress/extractive.go`:

```go
package compress

import (
	"context"
	"sort"
	"strings"

	"github.com/costa92/llm-agent-rag/embed"
	"github.com/costa92/llm-agent-rag/store"
)

// ExtractiveCompressor keeps only the sentences in each chunk most relevant
// to the query, scored by embedding cosine similarity. It never rewrites
// text, so kept content stays verbatim source and citations remain exact.
type ExtractiveCompressor struct {
	Embedder     embed.Embedder // Embedder scores sentences against the query.
	MaxSentences int            // MaxSentences kept per chunk; <= 0 defaults to 2.
}

// Compress keeps the top-MaxSentences sentences per chunk by relevance to
// query, preserving their original order. Chunks with no more than
// MaxSentences sentences are returned whole.
func (c ExtractiveCompressor) Compress(ctx context.Context, query string, hits []store.Hit) ([]store.Hit, error) {
	if c.Embedder == nil {
		return nil, ErrEmbedderRequired
	}
	maxSentences := c.MaxSentences
	if maxSentences <= 0 {
		maxSentences = 2
	}
	qv, err := c.Embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]store.Hit, len(hits))
	for i, h := range hits {
		sentences := splitSentences(h.Chunk.Content)
		if len(sentences) <= maxSentences {
			out[i] = h
			continue
		}
		type scored struct {
			idx   int
			text  string
			score float64
		}
		ranked := make([]scored, 0, len(sentences))
		for j, s := range sentences {
			sv, err := c.Embedder.Embed(ctx, s)
			if err != nil {
				return nil, err
			}
			ranked = append(ranked, scored{idx: j, text: s, score: embed.CosineSimilarity(qv, sv)})
		}
		sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].score > ranked[b].score })
		kept := ranked[:maxSentences]
		sort.SliceStable(kept, func(a, b int) bool { return kept[a].idx < kept[b].idx })
		parts := make([]string, len(kept))
		for k, s := range kept {
			parts[k] = s.text
		}
		h.Chunk.Content = strings.Join(parts, " ")
		out[i] = h
	}
	return out, nil
}

// splitSentences splits text on '.', '!', '?' terminators, keeping each
// terminator with its sentence. Whitespace-only fragments are dropped.
func splitSentences(text string) []string {
	var out []string
	var b strings.Builder
	for _, r := range text {
		b.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			if s := strings.TrimSpace(b.String()); s != "" {
				out = append(out, s)
			}
			b.Reset()
		}
	}
	if s := strings.TrimSpace(b.String()); s != "" {
		out = append(out, s)
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./compress/ -run TestExtractiveCompressor -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add compress/extractive.go compress/extractive_test.go
git commit -m "feat(compress): add ExtractiveCompressor

Keeps the top-N query-relevant sentences per chunk by embedding cosine
similarity, preserving original order. Content stays verbatim, so
citations remain exact."
```

---

### Task 3: `AbstractiveCompressor` — per-chunk LLM summary

**Files:**
- Create: `compress/abstractive.go`
- Test: `compress/abstractive_test.go`

- [ ] **Step 1: Write the failing tests**

Create `compress/abstractive_test.go`:

```go
package compress

import (
	"context"
	"errors"
	"testing"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/store"
)

type stubModel struct {
	resp string
}

func (m stubModel) Generate(_ context.Context, _ generate.Request) (generate.Response, error) {
	return generate.Response{Text: m.resp}, nil
}

func TestAbstractiveCompressorReplacesContentWithSummary(t *testing.T) {
	c := AbstractiveCompressor{Model: stubModel{resp: "  short summary.  "}}
	in := []store.Hit{{
		Chunk: store.StoredChunk{ID: "a", Content: "a very long passage about many things"},
		Score: 0.7,
	}}
	out, err := c.Compress(context.Background(), "query", in)
	if err != nil {
		t.Fatalf("Compress(): %v", err)
	}
	if out[0].Chunk.Content != "short summary." {
		t.Fatalf("Content = %q, want trimmed summary", out[0].Chunk.Content)
	}
	if out[0].Chunk.ID != "a" || out[0].Score != 0.7 {
		t.Fatalf("provenance not preserved: %#v", out[0])
	}
}

func TestAbstractiveCompressorEmptySummaryKeepsOriginal(t *testing.T) {
	c := AbstractiveCompressor{Model: stubModel{resp: "   "}}
	in := []store.Hit{hit("a", "original content", 0.5)}
	out, err := c.Compress(context.Background(), "query", in)
	if err != nil {
		t.Fatalf("Compress(): %v", err)
	}
	if out[0].Chunk.Content != "original content" {
		t.Fatalf("Content = %q, want original kept on empty summary", out[0].Chunk.Content)
	}
}

func TestAbstractiveCompressorRequiresModel(t *testing.T) {
	_, err := AbstractiveCompressor{}.Compress(context.Background(), "query", nil)
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./compress/ -run TestAbstractiveCompressor -v`
Expected: FAIL — `undefined: AbstractiveCompressor`.

- [ ] **Step 3: Create the abstractive compressor**

Create `compress/abstractive.go`:

```go
package compress

import (
	"context"
	"fmt"
	"strings"

	"github.com/costa92/llm-agent-rag/generate"
	"github.com/costa92/llm-agent-rag/store"
)

// AbstractiveCompressor replaces each chunk's Content with an LLM-generated,
// query-focused summary. It achieves higher compression than the extractive
// compressor, but the summary is model-generated text, not verbatim source —
// so it carries a hallucination risk and one model call per chunk.
type AbstractiveCompressor struct {
	Model generate.Model // Model produces the per-chunk summary.
}

// Compress summarizes each chunk toward the query. An empty model reply
// leaves that chunk's content unchanged.
func (c AbstractiveCompressor) Compress(ctx context.Context, query string, hits []store.Hit) ([]store.Hit, error) {
	if c.Model == nil {
		return nil, ErrModelRequired
	}
	out := make([]store.Hit, len(hits))
	for i, h := range hits {
		prompt := fmt.Sprintf(`Summarize the passage below, keeping only what is relevant to the query. Be concise. Output only the summary, no commentary.

Query: %s

Passage:
%s`, query, h.Chunk.Content)
		resp, err := c.Model.Generate(ctx, generate.Request{
			Messages: []generate.Message{{Role: "user", Content: prompt}},
		})
		if err != nil {
			return nil, err
		}
		if summary := strings.TrimSpace(resp.Text); summary != "" {
			h.Chunk.Content = summary
		}
		out[i] = h
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./compress/ -run TestAbstractiveCompressor -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add compress/abstractive.go compress/abstractive_test.go
git commit -m "feat(compress): add AbstractiveCompressor

Replaces each chunk's content with a query-focused LLM summary; an empty
reply leaves the chunk unchanged."
```

---

### Task 4: Wire compression into the `rag` pipeline

**Files:**
- Modify: `rag/options.go` — add `Compressor` to `Options` (after `QueryPlanner`, the last field, line 295) and `EnableCompression` to `SearchOptions` (after `ExpansionDepth`, line 37)
- Modify: `rag/system.go` — import `compress`; add `compressor compress.Compressor` field to `System` (after `queryPlanner QueryPlanner`, line 344); add `compressor: opts.Compressor,` to the `New` struct literal (after `queryPlanner: opts.QueryPlanner,`, line 426); add `effectiveCompressor` helper (after `effectiveQueryPlanner`, line 518); add `CompressedChunkIDs []string` to `Diagnostics` (after `Reflection ReflectionDiagnostics`, line 76)
- Modify: `rag/ask.go` — insert the compress stage in `askRound` after the rerank block (line 437) and populate `Diagnostics.CompressedChunkIDs` (after `GraphTrace`, line 512)
- Update: `api/v1.snapshot.txt` (regenerated)
- Test: `rag/compress_wiring_test.go`

- [ ] **Step 1: Write the failing tests**

Create `rag/compress_wiring_test.go`:

```go
package rag

import (
	"context"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-rag/compress"
	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/store"
)

// truncatingCompressor shortens every chunk's content to its first word so
// the test can observe that compression ran and was recorded.
type truncatingCompressor struct{}

func (truncatingCompressor) Compress(_ context.Context, _ string, hits []store.Hit) ([]store.Hit, error) {
	out := make([]store.Hit, len(hits))
	for i, h := range hits {
		if fields := strings.Fields(h.Chunk.Content); len(fields) > 0 {
			h.Chunk.Content = fields[0]
		}
		out[i] = h
	}
	return out, nil
}

func TestEffectiveCompressorDefaultsToNoop(t *testing.T) {
	sys := New(Options{})
	if _, ok := sys.effectiveCompressor().(compress.NoopCompressor); !ok {
		t.Fatalf("effectiveCompressor() = %T, want compress.NoopCompressor", sys.effectiveCompressor())
	}
	withC := New(Options{Compressor: truncatingCompressor{}})
	if _, ok := withC.effectiveCompressor().(truncatingCompressor); !ok {
		t.Fatalf("effectiveCompressor() = %T, want truncatingCompressor", withC.effectiveCompressor())
	}
}

func TestAskRecordsCompressedChunkIDsWhenEnabled(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Compressor: truncatingCompressor{}})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of France", AskOptions{
		Search: SearchOptions{Namespace: "geo", EnableCompression: true},
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Diagnostics.CompressedChunkIDs) == 0 {
		t.Fatalf("CompressedChunkIDs empty, want the compressed chunk recorded")
	}
}

func TestAskNoCompressionWhenDisabled(t *testing.T) {
	sys := New(Options{Model: fakeModel{}, Compressor: truncatingCompressor{}})
	if _, err := sys.Import(context.Background(), []ingest.Document{
		{ID: "doc1", Content: "Paris is the capital of France."},
	}, ingest.ImportOptions{Namespace: "geo"}); err != nil {
		t.Fatalf("Import(): %v", err)
	}
	ans, err := sys.Ask(context.Background(), "capital of France", AskOptions{
		Search: SearchOptions{Namespace: "geo"}, // EnableCompression defaults false
	})
	if err != nil {
		t.Fatalf("Ask(): %v", err)
	}
	if len(ans.Diagnostics.CompressedChunkIDs) != 0 {
		t.Fatalf("CompressedChunkIDs = %v, want none when disabled", ans.Diagnostics.CompressedChunkIDs)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./rag/ -run 'TestEffectiveCompressor|TestAskRecordsCompressed|TestAskNoCompression' -v`
Expected: FAIL — `effectiveCompressor` / `Options.Compressor` / `SearchOptions.EnableCompression` / `Diagnostics.CompressedChunkIDs` undefined (compile errors).

- [ ] **Step 3: Add the `Options` and `SearchOptions` fields**

In `rag/options.go`:

(a) In `SearchOptions`, add immediately after the `ExpansionDepth int` field (line 37):

```go
	EnableCompression            bool           // EnableCompression turns on contextual compression of retrieved chunks.
```

(b) In `Options`, add immediately after the `QueryPlanner QueryPlanner` field (the last field, line 295):

```go
	// Compressor, when set, shrinks retrieved chunk content to
	// query-relevant material between rerank and pack on a System.Ask run
	// that sets SearchOptions.EnableCompression. A nil Compressor defaults
	// to compress.NoopCompressor (no compression).
	Compressor compress.Compressor
```

Then add the import `"github.com/costa92/llm-agent-rag/compress"` to `rag/options.go`'s import block.

- [ ] **Step 4: Add the `System` field, `New` wiring, `effectiveCompressor`, and `Diagnostics` field**

In `rag/system.go`:

(a) Add `"github.com/costa92/llm-agent-rag/compress"` to the import block.

(b) In the `System` struct, add immediately after the `queryPlanner QueryPlanner` field (line 344):

```go
	compressor compress.Compressor
```

(c) In `New`, in the struct literal building `s`, add immediately after `queryPlanner: opts.QueryPlanner,` (line 426):

```go
		compressor: opts.Compressor,
```

(d) Add this helper immediately after `effectiveQueryPlanner` (after line 518):

```go

// effectiveCompressor returns the configured Compressor, or a
// compress.NoopCompressor when none was set, so the compression stage is
// always callable and defaults to a no-op. Mirrors effectiveQueryPlanner.
func (s *System) effectiveCompressor() compress.Compressor {
	if s.compressor == nil {
		return compress.NoopCompressor{}
	}
	return s.compressor
}
```

(e) In the `Diagnostics` struct, add immediately after the `Reflection ReflectionDiagnostics` field (line 76):

```go
	// CompressedChunkIDs lists the chunks whose Content contextual
	// compression shortened on this run; empty when compression is off or
	// nothing shrank. Additive.
	CompressedChunkIDs []string
```

- [ ] **Step 5: Insert the compress stage in `askRound`**

In `rag/ask.go`, immediately after the rerank block's closing brace (line 437) and before `packedHits := rankedHits` (line 438), insert:

```go
	var compressedIDs []string
	if opts.Search.EnableCompression {
		stageStart = time.Now()
		preLen := make(map[string]int, len(rankedHits))
		for _, h := range rankedHits {
			preLen[h.Chunk.ID] = len(h.Chunk.Content)
		}
		compressed, cerr := s.effectiveCompressor().Compress(ctx, originalQuestion, rankedHits)
		if cerr != nil {
			return askRoundResult{}, cerr
		}
		rankedHits = compressed
		for _, h := range rankedHits {
			if was, ok := preLen[h.Chunk.ID]; ok && len(h.Chunk.Content) < was {
				compressedIDs = append(compressedIDs, h.Chunk.ID)
			}
		}
		metrics.Stages = append(metrics.Stages, obs.StageTiming{Stage: "compress", Duration: time.Since(stageStart)})
	}
```

Then, in the `Diagnostics` struct literal inside `answer` (line 500-513), add immediately after the `GraphTrace: retrieveTrace.Graph,` line (line 512):

```go
			CompressedChunkIDs:  append([]string(nil), compressedIDs...),
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./rag/ -run 'TestEffectiveCompressor|TestAskRecordsCompressed|TestAskNoCompression' -v`
Expected: PASS (all three).

- [ ] **Step 7: Build and regenerate the API snapshot**

Run: `go build ./...`
Expected: exit 0.

Run: `go test ./internal/apisnapshot/ -run TestAPISnapshot -update`
Expected: PASS, logs `updated baseline`. Adds the `compress` package symbols (`Compressor`, `NoopCompressor`, `ExtractiveCompressor`, `AbstractiveCompressor`, `ErrEmbedderRequired`, `ErrModelRequired`), `Options.Compressor`, `SearchOptions.EnableCompression`, and `Diagnostics.CompressedChunkIDs`.

- [ ] **Step 8: Run the full suite**

Run: `go test ./...`
Expected: all packages PASS, no `FAIL`.

- [ ] **Step 9: Commit**

```bash
git add rag/options.go rag/system.go rag/ask.go rag/compress_wiring_test.go api/v1.snapshot.txt
git commit -m "feat(rag): run contextual compression between rerank and pack

Adds Options.Compressor + SearchOptions.EnableCompression and runs the
effective compressor (default NoopCompressor) after rerank, recording
shortened chunks on Diagnostics.CompressedChunkIDs. Regenerates the v1
API snapshot for the additive surface."
```

---

## Self-Review

**Spec coverage (Component C section of the spec):**
- New `compress` package + `Compressor` interface (`Compress(ctx, query, hits) ([]store.Hit, error)`) → Task 1. ✓
- `NoopCompressor` default → Task 1. ✓
- `ExtractiveCompressor` (sentence split + embedder scoring, keep top relevant, verbatim) → Task 2. ✓
- `AbstractiveCompressor{Model}` (per-chunk LLM summary) → Task 3. ✓
- Preserve ID/Score/Metadata → asserted in Task 2 + Task 3 tests; only `Content` is mutated. ✓
- `Options.Compressor` (nil → Noop) → Task 4 Step 3b + `effectiveCompressor` (Step 4d) + test (Step 1). ✓
- `SearchOptions.EnableCompression` (mirrors `EnableRerank`) → Task 4 Step 3a. ✓
- Inserted between rerank and pack, operating on `rankedHits`, timed as `obs.StageTiming{Stage:"compress"}` → Task 4 Step 5. ✓
- Trace: compressed chunk IDs (following `pack.Trace.TruncatedChunkIDs` style) → `Diagnostics.CompressedChunkIDs`, Task 4 Steps 4e + 5 + tests. ✓
- Unconfigured = byte-for-byte unchanged: `EnableCompression` defaults false (stage skipped); even when true, a nil compressor is `NoopCompressor` → Task 4 Step 1 `TestAskNoCompressionWhenDisabled`. ✓
- API snapshot update → Task 4 Step 7. ✓

**Deliberate scope decision (documented):** the trace records compressed chunk IDs only, not a pre/post character-count map. IDs match the existing `pack.Trace.TruncatedChunkIDs` style and are the minimal useful signal; a count map is future-additive. The compression query is `originalQuestion` (the user's question), consistent with how rerank and pack key off `originalQuestion`.

**Placeholder scan:** No TBD/TODO; every code step shows complete code; every command states expected output. ✓

**Type consistency:** `Compressor.Compress(ctx context.Context, query string, hits []store.Hit) ([]store.Hit, error)` is identical across the interface (Task 1), `NoopCompressor` (Task 1), `ExtractiveCompressor` (Task 2), `AbstractiveCompressor` (Task 3), `truncatingCompressor` test stub (Task 4), and the `effectiveCompressor()`/`askRound` call site (Task 4). `compress.NoopCompressor` referenced in `effectiveCompressor` (Task 4 Step 4d) matches its definition (Task 1 Step 3). `Diagnostics.CompressedChunkIDs` field name matches between definition (Task 4 Step 4e), literal population (Task 4 Step 5), and test assertions (Task 4 Step 1). `ErrEmbedderRequired` / `ErrModelRequired` defined in Task 1 (`errors.go`), used in Tasks 2/3 and their tests. ✓
