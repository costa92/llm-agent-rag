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

type Response struct {
	Text string
}
