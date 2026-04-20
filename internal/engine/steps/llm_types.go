package steps

// MessageRole is the canonical role for a conversation message.
type MessageRole string

const (
	RoleSystem     MessageRole = "system"
	RoleUser       MessageRole = "user"
	RoleAssistant  MessageRole = "assistant"
	RoleToolResult MessageRole = "tool_result"
)

// CanonicalMessage is the provider-agnostic representation of one conversation turn.
// All provider adapters translate to/from this format.
type CanonicalMessage struct {
	Role       MessageRole `json:"role"`
	Content    string      `json:"content"`
	ToolCallID string      `json:"tool_call_id,omitempty"` // non-empty for tool_result messages
}

// LLMRequest is the provider-agnostic request sent to any LLM.
type LLMRequest struct {
	Messages       []CanonicalMessage
	System         string         // system prompt (separate from messages; empty = omit)
	Model          string         // model slug as declared in the catalog
	MaxTokens      int
	Temperature    float64
	ProviderParams map[string]any // provider-specific fields merged into wire-format body
	// UseCompletionTokens: when true the OpenAI adapter sends max_completion_tokens
	// instead of max_tokens. Required for o-series and newer GPT-5+ models.
	UseCompletionTokens bool
}

// LLMResponse is the provider-agnostic response from any LLM.
type LLMResponse struct {
	Content      string
	StopReason   string // "end_turn", "max_tokens", "tool_use", "stop", etc.
	InputTokens  int
	OutputTokens int
}

// estimateTokens returns a rough token count estimate for a string.
// Uses the widely-accepted heuristic of 1 token ≈ 4 characters.
func estimateTokens(s string) int {
	return (len(s) + 3) / 4
}

// estimateRequestTokens returns the total estimated token count for an LLMRequest.
func estimateRequestTokens(req LLMRequest) int {
	total := estimateTokens(req.System)
	for _, m := range req.Messages {
		total += estimateTokens(m.Content) + 4 // +4 for role/formatting overhead
	}
	return total
}
