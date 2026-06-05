package compress

import "errors"

// ErrEmbedderRequired is returned by ExtractiveCompressor when its Embedder
// is nil.
var ErrEmbedderRequired = errors.New("compress: embedder required")

// ErrModelRequired is returned by AbstractiveCompressor when its Model is nil.
var ErrModelRequired = errors.New("compress: model required")
