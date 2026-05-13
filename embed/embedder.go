package embed

import "context"

type Embedder interface {
	Embed(ctx context.Context, text string) (Vector, error)
	Dimension() int
}
