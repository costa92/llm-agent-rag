package ingest

import (
	"fmt"
	"strings"
)

// Metadata keys that splitters write onto each Chunk's Metadata map. They
// record document provenance and section structure for downstream stages.
const (
	// MetadataSourceIDKey is the chunk-metadata key for the source document's SourceID.
	MetadataSourceIDKey = "source_id"
	// MetadataVersionKey is the chunk-metadata key for the source document's Version.
	MetadataVersionKey = "version"
	// MetadataChecksumKey is the chunk-metadata key for the source document's Checksum.
	MetadataChecksumKey = "checksum"
	// MetadataEmbeddingVersionKey is the chunk-metadata key for the embedding model version.
	MetadataEmbeddingVersionKey = "embedding_version"
	// MetadataHeadingKey is the chunk-metadata key for the chunk's section heading.
	MetadataHeadingKey = "heading"
	// MetadataHeadingLevelKey is the chunk-metadata key for the heading depth.
	MetadataHeadingLevelKey = "heading_level"
	// MetadataSectionPathKey is the chunk-metadata key for the heading breadcrumb.
	MetadataSectionPathKey = "section_path"
)

// Splitter divides a Document into Chunks. It is the chunking seam: the
// built-ins are CharSplitter and MarkdownSplitter.
type Splitter interface {
	// Split divides doc into chunks no larger than maxChars.
	Split(doc Document, maxChars int) []Chunk
}

// CharSplitter splits a document into fixed-size character windows.
type CharSplitter struct {
	MaxChars int // MaxChars is the default chunk size in characters.
	Overlap  int // Overlap is the number of characters shared between adjacent chunks.
}

// MarkdownSplitter splits a Markdown document along its heading structure.
type MarkdownSplitter struct {
	MaxChars int // MaxChars is the default chunk size in characters.
	Overlap  int // Overlap is the number of characters shared between adjacent chunks.
}

// NewCharSplitter returns a CharSplitter with the given chunk size and overlap.
func NewCharSplitter(maxChars, overlap int) CharSplitter {
	return CharSplitter{MaxChars: maxChars, Overlap: overlap}
}

// NewMarkdownSplitter returns a MarkdownSplitter with the given chunk size and overlap.
func NewMarkdownSplitter(maxChars, overlap int) MarkdownSplitter {
	return MarkdownSplitter{MaxChars: maxChars, Overlap: overlap}
}

// Split divides doc into fixed-size character windows.
func (c CharSplitter) Split(doc Document, maxChars int) []Chunk {
	if maxChars <= 0 {
		maxChars = c.MaxChars
	}
	if maxChars <= 0 {
		maxChars = 500
	}
	text := strings.TrimSpace(doc.Content)
	if text == "" {
		return nil
	}
	if doc.ID == "" {
		doc.ID = "doc"
	}
	parts := splitText(text, maxChars, c.Overlap)
	out := make([]Chunk, 0, len(parts))
	for i, part := range parts {
		md := copyMeta(doc.Metadata)
		md["chunk_index"] = i
		md["chunk_total"] = len(parts)
		if doc.SourceID != "" {
			md[MetadataSourceIDKey] = doc.SourceID
		}
		if doc.Version != "" {
			md[MetadataVersionKey] = doc.Version
		}
		if doc.Checksum != "" {
			md[MetadataChecksumKey] = doc.Checksum
		}
		if doc.EmbeddingVersion != "" {
			md[MetadataEmbeddingVersionKey] = doc.EmbeddingVersion
		}
		out = append(out, Chunk{
			ID:       fmt.Sprintf("%s:%d", doc.ID, i),
			DocID:    doc.ID,
			Index:    i,
			Total:    len(parts),
			Title:    doc.Title,
			Content:  part,
			Metadata: md,
		})
	}
	return out
}

func splitText(text string, maxChars, overlap int) []string {
	if len(text) <= maxChars {
		return []string{text}
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxChars {
		overlap = maxChars / 2
	}
	out := make([]string, 0, len(text)/maxChars+1)
	for start := 0; start < len(text); {
		hardEnd := start + maxChars + maxChars/5
		if hardEnd >= len(text) {
			out = append(out, strings.TrimSpace(text[start:]))
			break
		}
		breakAt := -1
		if idx := strings.LastIndex(text[start:hardEnd], "\n\n"); idx >= 0 {
			breakAt = start + idx
		}
		if breakAt < 0 {
			end := start + maxChars
			if ws := strings.LastIndexByte(text[start:end], ' '); ws > 0 {
				breakAt = start + ws
			} else {
				breakAt = end
			}
		}
		nextStart := breakAt
		if breakAt < len(text)-1 && text[breakAt] == '\n' && text[breakAt+1] == '\n' {
			nextStart = breakAt + 2
		}
		out = append(out, strings.TrimSpace(text[start:breakAt]))
		next := nextStart - overlap
		if next <= start {
			next = start + 1
		}
		start = next
	}
	cleaned := out[:0]
	for _, chunk := range out {
		if chunk != "" {
			cleaned = append(cleaned, chunk)
		}
	}
	return cleaned
}

func copyMeta(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Split divides doc along its Markdown heading structure into chunks.
func (m MarkdownSplitter) Split(doc Document, maxChars int) []Chunk {
	if maxChars <= 0 {
		maxChars = m.MaxChars
	}
	if maxChars <= 0 {
		maxChars = 500
	}
	text := strings.TrimSpace(doc.Content)
	if text == "" {
		return nil
	}
	sections := parseMarkdownSections(text)
	if len(sections) == 0 {
		return CharSplitter{MaxChars: maxChars, Overlap: m.Overlap}.Split(doc, maxChars)
	}

	if doc.ID == "" {
		doc.ID = "doc"
	}

	var chunks []Chunk
	chunkIndex := 0
	for _, section := range sections {
		for _, part := range splitText(section.content, maxChars, m.Overlap) {
			if strings.TrimSpace(part) == "" {
				continue
			}
			md := copyMeta(doc.Metadata)
			applyLineageMetadata(md, doc)
			if section.heading != "" {
				md[MetadataHeadingKey] = section.heading
				md[MetadataHeadingLevelKey] = section.level
			}
			if len(section.path) > 0 {
				md[MetadataSectionPathKey] = append([]string(nil), section.path...)
			}
			chunks = append(chunks, Chunk{
				ID:       fmt.Sprintf("%s:%d", doc.ID, chunkIndex),
				DocID:    doc.ID,
				Index:    chunkIndex,
				Title:    doc.Title,
				Content:  part,
				Metadata: md,
			})
			chunkIndex++
		}
	}
	for i := range chunks {
		chunks[i].Total = len(chunks)
		chunks[i].Metadata["chunk_index"] = i
		chunks[i].Metadata["chunk_total"] = len(chunks)
	}
	if len(chunks) == 0 {
		return CharSplitter{MaxChars: maxChars, Overlap: m.Overlap}.Split(doc, maxChars)
	}
	return chunks
}

type markdownSection struct {
	heading string
	level   int
	path    []string
	content string
}

func parseMarkdownSections(text string) []markdownSection {
	lines := strings.Split(text, "\n")
	var sections []markdownSection
	var path []string
	current := markdownSection{}

	flush := func() {
		current.content = strings.TrimSpace(current.content)
		if current.content == "" {
			return
		}
		cp := current
		cp.path = append([]string(nil), current.path...)
		sections = append(sections, cp)
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if level, heading, ok := parseMarkdownHeading(trimmed); ok {
			flush()
			if level <= 0 {
				level = 1
			}
			if len(path) >= level {
				path = append([]string(nil), path[:level-1]...)
			}
			path = append(path, heading)
			current = markdownSection{
				heading: heading,
				level:   level,
				path:    append([]string(nil), path...),
			}
			continue
		}
		if current.content != "" {
			current.content += "\n"
		}
		current.content += line
	}
	flush()
	return sections
}

func parseMarkdownHeading(line string) (level int, heading string, ok bool) {
	if line == "" || line[0] != '#' {
		return 0, "", false
	}
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	heading = strings.TrimSpace(line[level+1:])
	if heading == "" {
		return 0, "", false
	}
	return level, heading, true
}

func applyLineageMetadata(md map[string]any, doc Document) {
	if doc.SourceID != "" {
		md[MetadataSourceIDKey] = doc.SourceID
	}
	if doc.Version != "" {
		md[MetadataVersionKey] = doc.Version
	}
	if doc.Checksum != "" {
		md[MetadataChecksumKey] = doc.Checksum
	}
	if doc.EmbeddingVersion != "" {
		md[MetadataEmbeddingVersionKey] = doc.EmbeddingVersion
	}
}
