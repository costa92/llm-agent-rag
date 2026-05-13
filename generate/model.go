package generate

import "context"

type Model interface {
	Generate(ctx context.Context, req Request) (Response, error)
}
