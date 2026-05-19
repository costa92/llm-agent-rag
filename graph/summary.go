package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/costa92/llm-agent-rag/generate"
)

// ErrCommunitySummarizerModelRequired is returned by LLMCommunitySummarizer
// when no generate.Model is configured.
var ErrCommunitySummarizerModelRequired = errors.New("graph: community summarizer requires a generate.Model")

// CommunityReport is an LLM-written summary of one community: a short title
// and a paragraph summary. ContentHash records the membership of the
// community the report was built from, so a re-detected community with the
// same membership reuses its cached report and a changed one misses.
type CommunityReport struct {
	CommunityID string
	Title       string
	Summary     string
	ContentHash string // CommunityContentHash of the source community
}

// CommunitySummarizer writes a report for one community. Implementations may
// be LLM-backed or deterministic; callers supply their own so the package
// stays vendor-neutral — the same seam pattern as EntityExtractor.
type CommunitySummarizer interface {
	Summarize(ctx context.Context, c Community, g Graph) (CommunityReport, error)
}

// CommunityContentHash is a deterministic content hash of a community's
// membership: a hex SHA-256 over the sorted EntityIDs followed by the sorted
// RelationIDs, with a fixed separator. Two communities with the same
// membership hash identically; any membership change flips the hash. It is
// the cache key for a community's report.
func CommunityContentHash(c Community) string {
	ents := append([]string(nil), c.EntityIDs...)
	sort.Strings(ents)
	rels := append([]string(nil), c.RelationIDs...)
	sort.Strings(rels)

	h := sha256.New()
	for _, id := range ents {
		h.Write([]byte(id))
		h.Write([]byte{0x1f}) // unit separator — cannot appear in an ID
	}
	h.Write([]byte{0x1e}) // record separator — entities/relations boundary
	for _, id := range rels {
		h.Write([]byte(id))
		h.Write([]byte{0x1f})
	}
	return hex.EncodeToString(h.Sum(nil))
}

const summarySystemPrompt = `You summarize one community of a knowledge graph.
You are given the community's member entities (name, type, description) and the relations among them.
Write a SHORT report: a concise title naming the community's theme, then a single paragraph summarizing what the community is about.

Output exactly this shape, and nothing else:
Title: <a short title>
<one paragraph of summary>

No markdown, no fences, no commentary.`

// LLMCommunitySummarizer summarizes a community by prompting a generate.Model.
// It builds the prompt from the community's member entity names+descriptions
// and relation descriptions (looked up in the namespace graph by ID), asks
// for a short title and a paragraph summary, and parses the response
// leniently — a malformed response is never fatal, mirroring
// LLMEntityExtractor.
type LLMCommunitySummarizer struct {
	Model generate.Model
}

// Summarize implements CommunitySummarizer. The returned report always carries
// CommunityID and ContentHash; Title and Summary are leniently parsed from the
// model's response. A nil Model returns ErrCommunitySummarizerModelRequired.
func (s LLMCommunitySummarizer) Summarize(ctx context.Context, c Community, g Graph) (CommunityReport, error) {
	if s.Model == nil {
		return CommunityReport{}, ErrCommunitySummarizerModelRequired
	}
	resp, err := s.Model.Generate(ctx, generate.Request{
		SystemPrompt: summarySystemPrompt,
		Messages:     []generate.Message{{Role: "user", Content: communityPrompt(c, g)}},
	})
	if err != nil {
		return CommunityReport{}, err
	}
	title, summary := parseCommunityReport(resp.Text)
	return CommunityReport{
		CommunityID: c.ID,
		Title:       title,
		Summary:     summary,
		ContentHash: CommunityContentHash(c),
	}, nil
}

// communityPrompt renders a community's members and relations into a stable,
// deterministic prompt body. Entities and relations are emitted in the
// community's (already sorted) ID order; unknown IDs are skipped.
func communityPrompt(c Community, g Graph) string {
	entByID := make(map[string]Entity, len(g.Entities))
	for _, e := range g.Entities {
		entByID[e.ID] = e
	}
	relByID := make(map[string]Relation, len(g.Relations))
	for _, r := range g.Relations {
		relByID[r.ID] = r
	}

	var b strings.Builder
	b.WriteString("Entities:\n")
	for _, id := range c.EntityIDs {
		e, ok := entByID[id]
		if !ok {
			continue
		}
		b.WriteString("- ")
		b.WriteString(e.Name)
		if e.Type != "" {
			b.WriteString(" (")
			b.WriteString(e.Type)
			b.WriteString(")")
		}
		if e.Description != "" {
			b.WriteString(": ")
			b.WriteString(e.Description)
		}
		b.WriteString("\n")
	}
	b.WriteString("Relations:\n")
	for _, id := range c.RelationIDs {
		r, ok := relByID[id]
		if !ok {
			continue
		}
		b.WriteString("- ")
		b.WriteString(r.Source)
		b.WriteString(" ")
		b.WriteString(r.Relation)
		b.WriteString(" ")
		b.WriteString(r.Target)
		if r.Description != "" {
			b.WriteString(": ")
			b.WriteString(r.Description)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// parseCommunityReport leniently extracts a title and a summary from a model
// response. The first line matching "Title: <text>" (case-insensitive,
// tolerating leading code fences or preamble) becomes the title; every other
// non-fence line is joined into the summary. A response with no title line
// still parses — the title is empty and the whole body is the summary.
func parseCommunityReport(out string) (title, summary string) {
	var summaryLines []string
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "```") { // skip markdown fences
			continue
		}
		if title == "" {
			if rest, ok := cutTitlePrefix(line); ok {
				title = strings.TrimSpace(rest)
				continue
			}
		}
		summaryLines = append(summaryLines, line)
	}
	return title, strings.Join(summaryLines, " ")
}

// cutTitlePrefix returns the text after a leading "Title:" marker
// (case-insensitive) and whether the line carried one.
func cutTitlePrefix(line string) (string, bool) {
	const marker = "title:"
	if len(line) >= len(marker) && strings.EqualFold(line[:len(marker)], marker) {
		return line[len(marker):], true
	}
	return "", false
}
