package steps

import "encoding/json"

// MessageRole is the canonical role for a conversation message.
type MessageRole string

const (
	RoleSystem     MessageRole = "system"
	RoleUser       MessageRole = "user"
	RoleAssistant  MessageRole = "assistant"
	RoleToolResult MessageRole = "tool_result"
)

// ── Content blocks ────────────────────────────────────────────────────────────

type ContentBlockType string

const (
	BlockText       ContentBlockType = "text"
	BlockToolUse    ContentBlockType = "tool_use"    // assistant → calls a tool
	BlockToolResult ContentBlockType = "tool_result" // user → result of a tool call
	BlockThinking   ContentBlockType = "thinking"    // assistant reasoning output
	BlockImage      ContentBlockType = "image"       // reserved; adapters skip if unsupported
)

type ContentBlock struct {
	Type ContentBlockType `json:"type"`

	// text / thinking
	Text string `json:"text,omitempty"`

	// tool_use (assistant turn — model wants to call a tool)
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`

	// tool_result (user turn — result of executing a tool)
	ToolCallID  string `json:"tool_call_id,omitempty"`
	ToolResult  string `json:"tool_result,omitempty"`
	IsToolError bool   `json:"is_tool_error,omitempty"`
}

// ── Tool definitions ──────────────────────────────────────────────────────────

// ToolDefinition is the canonical, provider-agnostic representation of one tool.
// Moved here from llm_mcp.go so all pipeline components reference a single type.
// InputSchema holds a JSON Schema object (the universal tool schema format used
// by Anthropic, OpenAI, and Gemini natively).
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// ── Tool choice ───────────────────────────────────────────────────────────────

type ToolChoiceType string

const (
	ToolChoiceAuto     ToolChoiceType = "auto"     // model decides (default)
	ToolChoiceRequired ToolChoiceType = "required" // must call at least one tool
	ToolChoiceNone     ToolChoiceType = "none"     // no tool calls allowed
	ToolChoiceTool     ToolChoiceType = "tool"     // force a specific named tool
)

type ToolChoice struct {
	Type ToolChoiceType `json:"type"`
	Name string         `json:"name,omitempty"` // only when Type == ToolChoiceTool
}

// ── Thinking / Reasoning ─────────────────────────────────────────────────────

// ThinkingConfig enables extended reasoning. BudgetTokens is used by Anthropic
// and Gemini 2.5; Effort ("low"|"medium"|"high") is used by OpenAI o-series.
type ThinkingConfig struct {
	Enabled      bool   `json:"enabled"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
	Effort       string `json:"effort,omitempty"`
}

// ── Canonical message ─────────────────────────────────────────────────────────

// CanonicalMessage is the provider-agnostic representation of one conversation turn.
// All provider adapters translate to/from this format.
type CanonicalMessage struct {
	Role          MessageRole    `json:"role"`
	Content       string         `json:"content"`          // plain text (kept for backward compat)
	ContentBlocks []ContentBlock `json:"blocks,omitempty"` // structured content blocks
	ToolCallID    string         `json:"tool_call_id,omitempty"`
}

// HasBlocks reports whether this message carries structured content blocks
// instead of (or in addition to) plain text.
func (m CanonicalMessage) HasBlocks() bool { return len(m.ContentBlocks) > 0 }

// ── LLM request / response ────────────────────────────────────────────────────

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
	// NEW: tool definitions, choice strategy, and reasoning config
	Tools      []ToolDefinition
	ToolChoice *ToolChoice
	Thinking   *ThinkingConfig
}

// LLMResponse is the provider-agnostic response from any LLM.
type LLMResponse struct {
	Content      string
	StopReason   string // "end_turn", "max_tokens", "tool_use", "stop", etc.
	InputTokens  int
	OutputTokens int
	// NEW: all response blocks (text + tool_use + thinking); ThinkingTokens for extended reasoning
	ContentBlocks  []ContentBlock
	ThinkingTokens int
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
