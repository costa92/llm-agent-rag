package store_test

import (
	"testing"

	"github.com/costa92/llm-agent-rag/store"
	"github.com/costa92/llm-agent-rag/store/storetest"
)

func TestInMemoryStoreGraphConformance(t *testing.T) {
	storetest.RunGraphConformance(t, func(t *testing.T) store.Store {
		return store.NewInMemoryStore(2)
	})
}
