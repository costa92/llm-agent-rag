package store_test

import (
	"testing"

	"github.com/costa92/llm-agent-rag/store"
	"github.com/costa92/llm-agent-rag/store/storetest"
)

func TestInMemoryStoreConformance(t *testing.T) {
	storetest.RunConformance(t, func(t *testing.T) store.Store {
		return store.NewInMemoryStore(2)
	}, storetest.WithDimensionStrict())
}
