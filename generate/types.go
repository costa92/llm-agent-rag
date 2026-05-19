package generate

// Message is one chat message in a generation Request.
type Message struct {
	Role    string // Role is the speaker role, e.g. "user" or "assistant".
	Content string // Content is the message text.
}

// Request is a single text-generation request.
type Request struct {
	SystemPrompt string         // SystemPrompt is the system instruction prepended to the conversation.
	Messages     []Message      // Messages is the ordered conversation history.
	Metadata     map[string]any // Metadata carries caller-supplied passthrough values.
}

// Usage is the token cost of one generation. Model adapters that know real
// token counts populate it; it is the zero value when usage is unknown, in
// which case callers may estimate counts themselves.
type Usage struct {
	PromptTokens     int // PromptTokens is the number of tokens in the request.
	CompletionTokens int // CompletionTokens is the number of tokens in the response text.
	TotalTokens      int // TotalTokens is the combined prompt and completion token count.
}

// Response is the result of a text-generation Request.
type Response struct {
	Text  string // Text is the generated output.
	Usage Usage  // Usage is the token cost of the generation, when known.
}
