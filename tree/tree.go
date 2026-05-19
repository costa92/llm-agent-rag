// Package tree builds a document's hierarchical structure. DocumentTree is
// the section/heading tree of a Document made of Node values; Build
// constructs one from a Document and its chunks, and BuildStored constructs
// one from chunks already held in a store.
package tree

import (
	"strings"

	"github.com/costa92/llm-agent-rag/ingest"
	"github.com/costa92/llm-agent-rag/store"
)

// Node is one node in a DocumentTree — either a section heading (no content)
// or a leaf chunk (with content).
type Node struct {
	ID       string   // ID uniquely identifies the node within its document.
	DocID    string   // DocID is the ID of the document the node belongs to.
	Title    string   // Title is the document title.
	Heading  string   // Heading is the node's section heading.
	Level    int      // Level is the node's heading depth (0 = document root).
	Path     []string // Path is the heading breadcrumb from the root to the node.
	Content  string   // Content is the chunk text; empty for section nodes.
	Parent   *Node    // Parent is the enclosing node; nil for the root.
	Children []*Node  // Children are the node's direct child nodes.
}

// DocumentTree is the section/heading hierarchy of a single document, built
// from its chunks and rooted at a synthetic document node.
type DocumentTree struct {
	DocID string // DocID is the ID of the document this tree describes.
	Root  *Node  // Root is the synthetic document-level root node.
	index map[string]*Node
}

// Build constructs a DocumentTree from a document and its ingest chunks.
func Build(doc ingest.Document, chunks []ingest.Chunk) *DocumentTree {
	stored := make([]store.StoredChunk, 0, len(chunks))
	for _, chunk := range chunks {
		stored = append(stored, store.StoredChunk{
			ID:           chunk.ID,
			DocID:        chunk.DocID,
			Title:        chunk.Title,
			SectionPath:  metadataPath(chunk.Metadata),
			Heading:      metadataHeading(chunk.Metadata),
			HeadingLevel: metadataLevel(chunk.Metadata),
			Content:      chunk.Content,
		})
	}
	return BuildStored(doc.ID, doc.Title, stored)
}

// BuildStored constructs a DocumentTree from chunks already held in a store.
func BuildStored(docID, title string, chunks []store.StoredChunk) *DocumentTree {
	root := &Node{
		ID:      docID + ":root",
		DocID:   docID,
		Title:   title,
		Heading: title,
		Level:   0,
	}
	tree := &DocumentTree{
		DocID: docID,
		Root:  root,
		index: map[string]*Node{root.ID: root},
	}
	if len(chunks) == 0 {
		return tree
	}

	pathIndex := map[string]*Node{
		"": root,
	}

	for _, chunk := range chunks {
		path := append([]string(nil), chunk.SectionPath...)
		parent := root
		var accumulated []string
		for i, heading := range path {
			accumulated = append(accumulated, heading)
			key := pathKey(accumulated)
			node, ok := pathIndex[key]
			if !ok {
				node = &Node{
					ID:      docID + ":" + key,
					DocID:   docID,
					Title:   title,
					Heading: heading,
					Level:   i + 1,
					Path:    append([]string(nil), accumulated...),
					Parent:  parent,
				}
				parent.Children = append(parent.Children, node)
				pathIndex[key] = node
				tree.index[node.ID] = node
			}
			parent = node
		}

		if strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		leaf := &Node{
			ID:      chunk.ID,
			DocID:   chunk.DocID,
			Title:   chunk.Title,
			Heading: chunk.Heading,
			Level:   chunk.HeadingLevel,
			Path:    path,
			Content: chunk.Content,
			Parent:  parent,
		}
		parent.Children = append(parent.Children, leaf)
		tree.index[leaf.ID] = leaf
	}

	return tree
}

// Find returns the node with the given ID, and whether it was found.
func (t *DocumentTree) Find(id string) (*Node, bool) {
	if t == nil || t.index == nil {
		return nil, false
	}
	node, ok := t.index[id]
	return node, ok
}

// Sections returns every section node (heading nodes with no content) in
// pre-order.
func (t *DocumentTree) Sections() []*Node {
	if t == nil || t.Root == nil {
		return nil
	}
	var out []*Node
	var walk func(*Node)
	walk = func(node *Node) {
		if node == nil {
			return
		}
		if node.Level > 0 && len(node.Content) == 0 {
			out = append(out, node)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(t.Root)
	return out
}

// Leaves returns every leaf node (nodes carrying chunk content) in pre-order.
func (t *DocumentTree) Leaves() []*Node {
	if t == nil || t.Root == nil {
		return nil
	}
	var out []*Node
	var walk func(*Node)
	walk = func(node *Node) {
		if node == nil {
			return
		}
		if node.Content != "" {
			out = append(out, node)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(t.Root)
	return out
}

func metadataPath(metadata map[string]any) []string {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata[ingest.MetadataSectionPathKey]
	if !ok {
		return nil
	}
	switch value := raw.(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func metadataHeading(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	value, _ := metadata[ingest.MetadataHeadingKey].(string)
	return value
}

func metadataLevel(metadata map[string]any) int {
	if len(metadata) == 0 {
		return 0
	}
	switch value := metadata[ingest.MetadataHeadingLevelKey].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func pathKey(path []string) string {
	return strings.Join(path, "/")
}
