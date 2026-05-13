package ingest

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestImporterImport(t *testing.T) {
	source := StaticSource(
		Document{ID: "a", Content: "abcdef"},
		Document{ID: "b", Content: "xyz"},
	)
	importer := NewImporter(source, NewCharSplitter(4, 1))

	chunks, result, err := importer.Import(context.Background())
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	if result.Documents != 2 || result.Chunks != len(chunks) {
		t.Fatalf("result = %+v, chunks=%d", result, len(chunks))
	}

	wantIDs := []string{"a:0", "a:1", "b:0"}
	if !reflect.DeepEqual(result.ChunkIDs, wantIDs) {
		t.Fatalf("ChunkIDs = %v, want %v", result.ChunkIDs, wantIDs)
	}
}

func TestImporterPropagatesSourceError(t *testing.T) {
	wantErr := errors.New("boom")
	importer := NewImporter(SourceFunc(func(context.Context) ([]Document, error) {
		return nil, wantErr
	}), NewCharSplitter(4, 1))

	_, _, err := importer.Import(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Import() err = %v, want %v", err, wantErr)
	}
}

func TestImporterRequiresSourceAndSplitter(t *testing.T) {
	_, _, err := (&Importer{}).Import(context.Background())
	if err != ErrNilSource {
		t.Fatalf("Import() err = %v, want %v", err, ErrNilSource)
	}

	_, _, err = (&Importer{Source: StaticSource(Document{ID: "doc"})}).Import(context.Background())
	if err != ErrNilSplitter {
		t.Fatalf("Import() err = %v, want %v", err, ErrNilSplitter)
	}
}
