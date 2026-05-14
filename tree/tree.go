package tree

import (
	"strings"

	"github.com/costa92/llm-agent-rag/ingest"
)

type Node struct {
	ID       string
	DocID    string
	Title    string
	Heading  string
	Level    int
	Path     []string
	Content  string
	Children []*Node
}

type DocumentTree struct {
	DocID string
	Root  *Node
	index map[string]*Node
}

func Build(doc ingest.Document, chunks []ingest.Chunk) *DocumentTree {
	root := &Node{
		ID:      doc.ID + ":root",
		DocID:   doc.ID,
		Title:   doc.Title,
		Heading: doc.Title,
		Level:   0,
	}
	tree := &DocumentTree{
		DocID: doc.ID,
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
		path := metadataPath(chunk.Metadata)
		parent := root
		var accumulated []string
		for i, heading := range path {
			accumulated = append(accumulated, heading)
			key := pathKey(accumulated)
			node, ok := pathIndex[key]
			if !ok {
				node = &Node{
					ID:      doc.ID + ":" + key,
					DocID:   doc.ID,
					Title:   doc.Title,
					Heading: heading,
					Level:   i + 1,
					Path:    append([]string(nil), accumulated...),
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
			Heading: metadataHeading(chunk.Metadata),
			Level:   metadataLevel(chunk.Metadata),
			Path:    path,
			Content: chunk.Content,
		}
		parent.Children = append(parent.Children, leaf)
		tree.index[leaf.ID] = leaf
	}

	return tree
}

func (t *DocumentTree) Find(id string) (*Node, bool) {
	if t == nil || t.index == nil {
		return nil, false
	}
	node, ok := t.index[id]
	return node, ok
}

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
