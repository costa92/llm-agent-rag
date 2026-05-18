package generate

type Message struct {
	Role    string
	Content string
}

type Request struct {
	SystemPrompt string
	Messages     []Message
	Metadata     map[string]any
}

// Usage is the token cost of one generation. Model adapters that know real
// token counts populate it; it is the zero value when usage is unknown, in
// which case callers may estimate counts themselves.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type Response struct {
	Text  string
	Usage Usage
}
