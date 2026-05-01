package steps

import (
	"encoding/json"
	"fmt"
	"strings"

	"rah/internal/config"
)

// ProviderAdapter translates between the canonical LLMRequest/LLMResponse and
// a specific provider's wire format. One adapter per provider family.
type ProviderAdapter interface {
	// Marshal encodes a canonical request into the provider's JSON wire format.
	Marshal(req LLMRequest) ([]byte, error)
	// Unmarshal decodes the provider's JSON response into a canonical LLMResponse.
	Unmarshal(body []byte) (LLMResponse, error)
	// Endpoint returns the full URL to POST the request to.
	Endpoint(baseURL, modelSlug string) string
	// AuthHeader returns the header name and value used to authenticate the request.
	// Returns ("", "") when no auth header is needed (e.g. key embedded elsewhere).
	AuthHeader(apiKey string) (name, value string)
}

// NewAdapter returns the ProviderAdapter for the given model config.
// For AdapterCustom, auth header behaviour is driven by cfg.AuthHeaderName
// and cfg.AuthHeaderPrefix — no code change needed to add new providers.
// Returns an error if the adapter kind is unknown.
func NewAdapter(cfg config.LLMModelConfig) (ProviderAdapter, error) {
	switch cfg.Adapter {
	case config.AdapterAnthropic:
		return &anthropicAdapter{}, nil
	case config.AdapterOpenAI:
		return &openAIAdapter{}, nil
	case config.AdapterGemini:
		apiVersion := cfg.APIVersion
		if apiVersion == "" {
			apiVersion = "v1beta" // default: v1beta supports system_instruction, tools, and thinking
		}
		return &geminiAdapter{apiVersion: apiVersion}, nil
	case config.AdapterOllama:
		return &ollamaAdapter{}, nil
	case config.AdapterDeepSeek:
		return &deepSeekAdapter{}, nil
	case config.AdapterCustom:
		headerName := cfg.AuthHeaderName
		if headerName == "" {
			headerName = "Authorization"
		}
		headerPrefix := cfg.AuthHeaderPrefix
		if headerPrefix == "" {
			headerPrefix = "Bearer "
		}
		return &customAdapter{authHeaderName: headerName, authHeaderPrefix: headerPrefix}, nil
	case config.AdapterBedrock:
		return newBedrockAdapter(cfg.BaseURL), nil
	default:
		return nil, fmt.Errorf("unknown LLM adapter: %q", cfg.Adapter)
	}
}

// ── Anthropic ────────────────────────────────────────────────────────────────

type anthropicAdapter struct{}

type anthropicReqMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // can be string or []block
}

type anthropicRequest struct {
	Model      string                `json:"model"`
	MaxTokens  int                   `json:"max_tokens"`
	System     string                `json:"system,omitempty"`
	Messages   []anthropicReqMessage `json:"messages"`
	Tools      []anthropicToolDef    `json:"tools,omitempty"`
	ToolChoice json.RawMessage       `json:"tool_choice,omitempty"`
	Thinking   json.RawMessage       `json:"thinking,omitempty"`
}

type anthropicToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicRespBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

type anthropicResponse struct {
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      anthropicAdapterUsage `json:"usage"`
	Error      *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type anthropicAdapterUsage struct {
	InputTokens    int `json:"input_tokens"`
	OutputTokens   int `json:"output_tokens"`
	ThinkingTokens int `json:"thinking_tokens"`
}

// marshalAnthropicContent converts a CanonicalMessage's content blocks into
// the Anthropic wire format (JSON array of block objects).
func marshalAnthropicContent(m CanonicalMessage) (json.RawMessage, error) {
	if !m.HasBlocks() {
		return json.Marshal(m.Content)
	}
	type blockObj = map[string]interface{}
	blocks := make([]blockObj, 0, len(m.ContentBlocks))
	for _, cb := range m.ContentBlocks {
		switch cb.Type {
		case BlockToolUse:
			input := cb.ToolInput
			if input == nil {
				input = json.RawMessage(`{}`)
			}
			blocks = append(blocks, blockObj{
				"type":  "tool_use",
				"id":    cb.ToolUseID,
				"name":  cb.ToolName,
				"input": input,
			})
		case BlockToolResult:
			blocks = append(blocks, blockObj{
				"type":        "tool_result",
				"tool_use_id": cb.ToolCallID,
				"content":     cb.ToolResult,
			})
		case BlockText:
			blocks = append(blocks, blockObj{
				"type": "text",
				"text": cb.Text,
			})
		case BlockThinking:
			blocks = append(blocks, blockObj{
				"type": "thinking",
				"text": cb.Text,
			})
		default:
			// skip unknown block types (e.g. image)
		}
	}
	return json.Marshal(blocks)
}

func (a *anthropicAdapter) Marshal(req LLMRequest) ([]byte, error) {
	msgs := make([]anthropicReqMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue // system goes in top-level field, not messages
		}
		content, err := marshalAnthropicContent(m)
		if err != nil {
			return nil, fmt.Errorf("anthropic: marshal message: %w", err)
		}
		msgs = append(msgs, anthropicReqMessage{Role: string(m.Role), Content: content})
	}
	// If no explicit system provided but messages contain a system role, use it
	system := req.System
	if system == "" {
		for _, m := range req.Messages {
			if m.Role == RoleSystem {
				system = m.Content
				break
			}
		}
	}

	ar := anthropicRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    system,
		Messages:  msgs,
	}

	// Tools
	if len(req.Tools) > 0 {
		ar.Tools = make([]anthropicToolDef, len(req.Tools))
		for i, t := range req.Tools {
			schema := t.InputSchema
			if schema == nil {
				schema = json.RawMessage(`{}`)
			}
			ar.Tools[i] = anthropicToolDef{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: schema,
			}
		}
	}

	// ToolChoice
	if req.ToolChoice != nil {
		tc := req.ToolChoice
		var raw json.RawMessage
		var err error
		if tc.Type == ToolChoiceTool {
			raw, err = json.Marshal(map[string]interface{}{
				"type": "tool",
				"name": tc.Name,
			})
		} else {
			raw, err = json.Marshal(map[string]interface{}{
				"type": string(tc.Type),
			})
		}
		if err != nil {
			return nil, fmt.Errorf("anthropic: marshal tool_choice: %w", err)
		}
		ar.ToolChoice = raw
	}

	// Thinking
	if req.Thinking != nil && req.Thinking.Enabled {
		raw, err := json.Marshal(map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": req.Thinking.BudgetTokens,
		})
		if err != nil {
			return nil, fmt.Errorf("anthropic: marshal thinking: %w", err)
		}
		ar.Thinking = raw
	}

	return json.Marshal(ar)
}

func (a *anthropicAdapter) Unmarshal(body []byte) (LLMResponse, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return LLMResponse{}, fmt.Errorf("anthropic: unmarshal: %w", err)
	}
	if resp.Error != nil {
		return LLMResponse{}, fmt.Errorf("anthropic: api error: %s", resp.Error.Message)
	}

	var blocks []anthropicRespBlock
	if len(resp.Content) > 0 && resp.Content[0] == '[' {
		if err := json.Unmarshal(resp.Content, &blocks); err != nil {
			return LLMResponse{}, fmt.Errorf("anthropic: unmarshal content blocks: %w", err)
		}
	}

	var contentBlocks []ContentBlock
	var firstText string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			contentBlocks = append(contentBlocks, ContentBlock{Type: BlockText, Text: b.Text})
			if firstText == "" {
				firstText = b.Text
			}
		case "tool_use":
			contentBlocks = append(contentBlocks, ContentBlock{
				Type:      BlockToolUse,
				ToolUseID: b.ID,
				ToolName:  b.Name,
				ToolInput: b.Input,
			})
		case "thinking":
			contentBlocks = append(contentBlocks, ContentBlock{Type: BlockThinking, Text: b.Thinking})
		}
	}

	return LLMResponse{
		Content:        firstText,
		StopReason:     resp.StopReason,
		InputTokens:    resp.Usage.InputTokens,
		OutputTokens:   resp.Usage.OutputTokens,
		ContentBlocks:  contentBlocks,
		ThinkingTokens: resp.Usage.ThinkingTokens,
	}, nil
}

func (a *anthropicAdapter) Endpoint(baseURL, _ string) string {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return strings.TrimRight(baseURL, "/") + "/v1/messages"
}

func (a *anthropicAdapter) AuthHeader(apiKey string) (string, string) {
	return "x-api-key", apiKey
}

// ── OpenAI ───────────────────────────────────────────────────────────────────

type openAIAdapter struct{}

type openAIReqMessage struct {
	Role       string           `json:"role"`
	Content    json.RawMessage  `json:"content"`
	ToolCallID string           `json:"tool_call_id,omitempty"` // for role=="tool"
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`   // for role=="assistant" with tool calls
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // always "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON string
	} `json:"function"`
}

type openAIToolDef struct {
	Type     string        `json:"type"` // always "function"
	Function openAIFuncDef `json:"function"`
}

type openAIFuncDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // JSON Schema
}

type openAIRequest struct {
	Model               string             `json:"model"`
	MaxTokens           *int               `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int               `json:"max_completion_tokens,omitempty"`
	Messages            []openAIReqMessage `json:"messages"`
	Tools               []openAIToolDef    `json:"tools,omitempty"`
	ToolChoice          any                `json:"tool_choice,omitempty"`
	ReasoningEffort     string             `json:"reasoning_effort,omitempty"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content   *string          `json:"content"`
			ToolCalls []openAIToolCall  `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// marshalOpenAIContent converts a CanonicalMessage to an openAIReqMessage.
func marshalOpenAIMessage(m CanonicalMessage) (openAIReqMessage, error) {
	if !m.HasBlocks() {
		content, err := json.Marshal(m.Content)
		if err != nil {
			return openAIReqMessage{}, err
		}
		return openAIReqMessage{Role: string(m.Role), Content: content}, nil
	}

	// Check for tool_use blocks (assistant returning tool calls)
	var toolCalls []openAIToolCall
	var toolResultBlock *ContentBlock
	for i, cb := range m.ContentBlocks {
		if cb.Type == BlockToolUse {
			args := string(cb.ToolInput)
			if args == "" {
				args = "{}"
			}
			tc := openAIToolCall{
				ID:   cb.ToolUseID,
				Type: "function",
			}
			tc.Function.Name = cb.ToolName
			tc.Function.Arguments = args
			toolCalls = append(toolCalls, tc)
		}
		if cb.Type == BlockToolResult {
			toolResultBlock = &m.ContentBlocks[i]
		}
	}

	if toolResultBlock != nil {
		// tool result message
		content, err := json.Marshal(toolResultBlock.ToolResult)
		if err != nil {
			return openAIReqMessage{}, err
		}
		return openAIReqMessage{
			Role:       "tool",
			Content:    content,
			ToolCallID: toolResultBlock.ToolCallID,
		}, nil
	}

	if len(toolCalls) > 0 {
		// assistant with tool calls — content is null
		nullJSON := json.RawMessage(`null`)
		return openAIReqMessage{
			Role:      string(m.Role),
			Content:   nullJSON,
			ToolCalls: toolCalls,
		}, nil
	}

	// Regular blocks (e.g. text only)
	// Collect text content
	var sb strings.Builder
	for _, cb := range m.ContentBlocks {
		if cb.Type == BlockText {
			sb.WriteString(cb.Text)
		}
	}
	content, err := json.Marshal(sb.String())
	if err != nil {
		return openAIReqMessage{}, err
	}
	return openAIReqMessage{Role: string(m.Role), Content: content}, nil
}

func (o *openAIAdapter) Marshal(req LLMRequest) ([]byte, error) {
	msgs := make([]openAIReqMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		content, _ := json.Marshal(req.System)
		msgs = append(msgs, openAIReqMessage{Role: "system", Content: content})
	}
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue // already prepended above
		}
		msg, err := marshalOpenAIMessage(m)
		if err != nil {
			return nil, fmt.Errorf("openai: marshal message: %w", err)
		}
		msgs = append(msgs, msg)
	}
	r := openAIRequest{Model: req.Model, Messages: msgs}
	if req.MaxTokens > 0 {
		n := req.MaxTokens
		if req.UseCompletionTokens {
			r.MaxCompletionTokens = &n // o-series / gpt-5+ require this field name
		} else {
			r.MaxTokens = &n
		}
	}

	// Tools
	if len(req.Tools) > 0 {
		r.Tools = make([]openAIToolDef, len(req.Tools))
		for i, t := range req.Tools {
			r.Tools[i] = openAIToolDef{
				Type: "function",
				Function: openAIFuncDef{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			}
		}
	}

	// ToolChoice
	if req.ToolChoice != nil {
		tc := req.ToolChoice
		switch tc.Type {
		case ToolChoiceAuto:
			r.ToolChoice = "auto"
		case ToolChoiceRequired:
			r.ToolChoice = "required"
		case ToolChoiceNone:
			r.ToolChoice = "none"
		case ToolChoiceTool:
			r.ToolChoice = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": tc.Name,
				},
			}
		}
	}

	// Thinking effort (OpenAI o-series)
	if req.Thinking != nil && req.Thinking.Effort != "" {
		r.ReasoningEffort = req.Thinking.Effort
	}

	return json.Marshal(r)
}

func (o *openAIAdapter) Unmarshal(body []byte) (LLMResponse, error) {
	var resp openAIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return LLMResponse{}, fmt.Errorf("openai: unmarshal: %w", err)
	}
	if resp.Error != nil {
		return LLMResponse{}, fmt.Errorf("openai: api error: %s", resp.Error.Message)
	}
	var text string
	var finishReason string
	var contentBlocks []ContentBlock

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		finishReason = choice.FinishReason
		if choice.Message.Content != nil {
			text = *choice.Message.Content
		}
		for _, tc := range choice.Message.ToolCalls {
			contentBlocks = append(contentBlocks, ContentBlock{
				Type:      BlockToolUse,
				ToolUseID: tc.ID,
				ToolName:  tc.Function.Name,
				ToolInput: json.RawMessage(tc.Function.Arguments),
			})
		}
	}

	return LLMResponse{
		Content:       text,
		StopReason:    finishReason,
		InputTokens:   resp.Usage.PromptTokens,
		OutputTokens:  resp.Usage.CompletionTokens,
		ContentBlocks: contentBlocks,
	}, nil
}

func (o *openAIAdapter) Endpoint(baseURL, _ string) string {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	// Strip any trailing /v1 or /v1/ the caller may have included in base_url
	// to avoid double-path like https://api.openai.com/v1/v1/chat/completions.
	base := strings.TrimRight(baseURL, "/")
	base = strings.TrimSuffix(base, "/v1")
	return base + "/v1/chat/completions"
}

func (o *openAIAdapter) AuthHeader(apiKey string) (string, string) {
	return "Authorization", "Bearer " + apiKey
}

// ── Gemini ───────────────────────────────────────────────────────────────────

type geminiAdapter struct {
	// apiVersion is "v1" (default, stable) or "v1beta" (newer models: Gemini 2.0+, Gemma).
	// Set via LLMModelConfig.APIVersion; defaults to "v1" when empty.
	apiVersion string
}

type geminiPart struct {
	Text         string          `json:"text,omitempty"`
	Thought      bool            `json:"thought,omitempty"`
	FunctionCall *struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall,omitempty"`
	FunctionResponse *struct {
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	} `json:"functionResponse,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiThinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

type geminiGenConfig struct {
	MaxOutputTokens int                   `json:"maxOutputTokens,omitempty"`
	Temperature     float32               `json:"temperature,omitempty"`
	ThinkingConfig  *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

type geminiFunctionDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiTools struct {
	FunctionDeclarations []geminiFunctionDecl `json:"functionDeclarations"`
}

type geminiToolConfig struct {
	FunctionCallingConfig struct {
		Mode                 string   `json:"mode"`
		AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
	} `json:"functionCallingConfig"`
}

type geminiRequest struct {
	Contents          []geminiContent   `json:"contents"`
	SystemInstruction *geminiContent    `json:"system_instruction,omitempty"`
	GenerationConfig  *geminiGenConfig  `json:"generationConfig,omitempty"`
	Tools             []geminiTools     `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig `json:"toolConfig,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// sanitizeSchemaForGemini removes keys that Gemini rejects from a JSON Schema.
// Recursively cleans nested properties and items.
func sanitizeSchemaForGemini(schema json.RawMessage) json.RawMessage {
	if len(schema) == 0 {
		return schema
	}
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil {
		return schema
	}
	removeKeys := []string{"$schema", "$defs", "$ref", "additionalProperties", "exclusiveMinimum", "exclusiveMaximum"}
	for _, k := range removeKeys {
		delete(m, k)
	}
	// Recursively sanitize properties values
	if props, ok := m["properties"]; ok {
		if propsMap, ok := props.(map[string]any); ok {
			for k, v := range propsMap {
				if vMap, ok := v.(map[string]any); ok {
					raw, err := json.Marshal(vMap)
					if err == nil {
						var cleaned map[string]any
						cleaned_raw := sanitizeSchemaForGemini(raw)
						if err2 := json.Unmarshal(cleaned_raw, &cleaned); err2 == nil {
							propsMap[k] = cleaned
						}
					}
				}
			}
		}
	}
	// Recursively sanitize items
	if items, ok := m["items"]; ok {
		if itemsMap, ok := items.(map[string]any); ok {
			raw, err := json.Marshal(itemsMap)
			if err == nil {
				var cleaned map[string]any
				cleaned_raw := sanitizeSchemaForGemini(raw)
				if err2 := json.Unmarshal(cleaned_raw, &cleaned); err2 == nil {
					m["items"] = cleaned
				}
			}
		}
	}
	result, err := json.Marshal(m)
	if err != nil {
		return schema
	}
	return result
}

// sanitizeGeminiToolUseID replaces characters that Gemini rejects in tool IDs.
func sanitizeGeminiToolUseID(id string) string {
	id = strings.ReplaceAll(id, ":", "_")
	id = strings.ReplaceAll(id, ".", "_")
	return id
}

// normalizeMessagesForGemini applies Gemini-specific history normalization to a
// local copy of the messages slice.
func normalizeMessagesForGemini(messages []CanonicalMessage) []CanonicalMessage {
	// Work on a copy — do not modify the caller's slice.
	msgs := make([]CanonicalMessage, len(messages))
	copy(msgs, messages)

	// 1. Strip BlockThinking content blocks from all messages and sanitize IDs.
	for i := range msgs {
		if !msgs[i].HasBlocks() {
			continue
		}
		filtered := msgs[i].ContentBlocks[:0:0]
		for _, cb := range msgs[i].ContentBlocks {
			if cb.Type == BlockThinking {
				continue
			}
			// Sanitize tool-use IDs
			if cb.Type == BlockToolUse {
				cb.ToolUseID = sanitizeGeminiToolUseID(cb.ToolUseID)
			}
			if cb.Type == BlockToolResult {
				cb.ToolCallID = sanitizeGeminiToolUseID(cb.ToolCallID)
			}
			filtered = append(filtered, cb)
		}
		msgs[i].ContentBlocks = filtered
	}

	// 2. Remove orphaned tool results (no matching preceding tool_use).
	// Build set of tool_use IDs in the message just before each tool_result message.
	result := make([]CanonicalMessage, 0, len(msgs))
	for i, m := range msgs {
		if m.HasBlocks() {
			// Check if this message contains ONLY tool_result blocks
			allToolResult := true
			for _, cb := range m.ContentBlocks {
				if cb.Type != BlockToolResult {
					allToolResult = false
					break
				}
			}
			if allToolResult && len(m.ContentBlocks) > 0 {
				// Find matching tool_use in previous assistant message
				hasPrev := false
				for j := i - 1; j >= 0; j-- {
					if msgs[j].Role == RoleAssistant {
						prevIDs := map[string]bool{}
						for _, cb := range msgs[j].ContentBlocks {
							if cb.Type == BlockToolUse {
								prevIDs[cb.ToolUseID] = true
							}
						}
						// Check if all tool_results have a matching tool_use
						allMatched := true
						for _, cb := range m.ContentBlocks {
							if !prevIDs[cb.ToolCallID] {
								allMatched = false
								break
							}
						}
						if allMatched {
							hasPrev = true
						}
						break
					}
				}
				if !hasPrev {
					continue // drop orphaned tool results
				}
			}
		}
		result = append(result, m)
	}
	msgs = result

	// 3. Merge consecutive same-role messages.
	merged := make([]CanonicalMessage, 0, len(msgs))
	for _, m := range msgs {
		if len(merged) > 0 && merged[len(merged)-1].Role == m.Role {
			prev := &merged[len(merged)-1]
			if m.HasBlocks() || prev.HasBlocks() {
				// Merge content blocks
				var prevBlocks []ContentBlock
				if prev.HasBlocks() {
					prevBlocks = prev.ContentBlocks
				} else if prev.Content != "" {
					prevBlocks = []ContentBlock{{Type: BlockText, Text: prev.Content}}
				}
				var newBlocks []ContentBlock
				if m.HasBlocks() {
					newBlocks = m.ContentBlocks
				} else if m.Content != "" {
					newBlocks = []ContentBlock{{Type: BlockText, Text: m.Content}}
				}
				prev.ContentBlocks = append(prevBlocks, newBlocks...)
				prev.Content = ""
			} else {
				prev.Content = prev.Content + "\n" + m.Content
			}
			continue
		}
		merged = append(merged, m)
	}
	msgs = merged

	// 4. Drop leading assistant/model messages.
	for len(msgs) > 0 {
		r := string(msgs[0].Role)
		if r == "assistant" || r == "model" {
			msgs = msgs[1:]
		} else {
			break
		}
	}

	return msgs
}

// buildGeminiContent converts a CanonicalMessage to a geminiContent.
func buildGeminiContent(m CanonicalMessage) (geminiContent, error) {
	role := string(m.Role)
	if role == "assistant" {
		role = "model"
	}

	if !m.HasBlocks() {
		return geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: m.Content}},
		}, nil
	}

	parts := make([]geminiPart, 0, len(m.ContentBlocks))
	for _, cb := range m.ContentBlocks {
		switch cb.Type {
		case BlockToolUse:
			input := cb.ToolInput
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			fc := &struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			}{Name: cb.ToolName, Args: input}
			parts = append(parts, geminiPart{FunctionCall: fc})
		case BlockToolResult:
			respPayload, err := json.Marshal(map[string]string{"content": cb.ToolResult})
			if err != nil {
				return geminiContent{}, err
			}
			fr := &struct {
				Name     string          `json:"name"`
				Response json.RawMessage `json:"response"`
			}{Name: cb.ToolName, Response: respPayload}
			parts = append(parts, geminiPart{FunctionResponse: fr})
			role = "user" // tool results are always user-role in Gemini
		case BlockText:
			parts = append(parts, geminiPart{Text: cb.Text})
		case BlockThinking:
			// Should have been stripped by normalization, but skip if present
		}
	}

	return geminiContent{Role: role, Parts: parts}, nil
}

func (g *geminiAdapter) Marshal(req LLMRequest) ([]byte, error) {
	// Normalize messages (local copy)
	normalizedMsgs := normalizeMessagesForGemini(req.Messages)

	contents := make([]geminiContent, 0, len(normalizedMsgs))
	for _, m := range normalizedMsgs {
		if m.Role == RoleSystem {
			continue
		}
		gc, err := buildGeminiContent(m)
		if err != nil {
			return nil, fmt.Errorf("gemini: marshal message: %w", err)
		}
		contents = append(contents, gc)
	}

	gr := geminiRequest{Contents: contents}

	// System instruction
	system := req.System
	if system == "" {
		for _, m := range req.Messages {
			if m.Role == RoleSystem {
				system = m.Content
				break
			}
		}
	}
	if system != "" {
		// system_instruction is only supported in v1beta.
		// Gemma models also don't support it regardless of API version.
		// For both cases, prepend as a user/model exchange instead.
		useSystemField := g.apiVersion == "v1beta" &&
			!strings.Contains(strings.ToLower(req.Model), "gemma")
		if useSystemField {
			gr.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: system}}}
		} else {
			gr.Contents = append([]geminiContent{
				{Role: "user", Parts: []geminiPart{{Text: system}}},
				{Role: "model", Parts: []geminiPart{{Text: "Understood."}}},
			}, gr.Contents...)
		}
	}

	// GenerationConfig
	var genCfg *geminiGenConfig
	if req.MaxTokens > 0 || req.Temperature > 0 {
		genCfg = &geminiGenConfig{
			MaxOutputTokens: req.MaxTokens,
			Temperature:     float32(req.Temperature),
		}
	}
	// Thinking budget
	if req.Thinking != nil && req.Thinking.BudgetTokens > 0 {
		if genCfg == nil {
			genCfg = &geminiGenConfig{}
		}
		genCfg.ThinkingConfig = &geminiThinkingConfig{ThinkingBudget: req.Thinking.BudgetTokens}
	}
	gr.GenerationConfig = genCfg

	// Tools
	if len(req.Tools) > 0 {
		decls := make([]geminiFunctionDecl, len(req.Tools))
		for i, t := range req.Tools {
			decls[i] = geminiFunctionDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  sanitizeSchemaForGemini(t.InputSchema),
			}
		}
		gr.Tools = []geminiTools{{FunctionDeclarations: decls}}
	}

	// ToolConfig
	if req.ToolChoice != nil {
		tc := req.ToolChoice
		toolCfg := &geminiToolConfig{}
		switch tc.Type {
		case ToolChoiceAuto:
			toolCfg.FunctionCallingConfig.Mode = "AUTO"
		case ToolChoiceRequired:
			toolCfg.FunctionCallingConfig.Mode = "ANY"
		case ToolChoiceNone:
			toolCfg.FunctionCallingConfig.Mode = "NONE"
		case ToolChoiceTool:
			toolCfg.FunctionCallingConfig.Mode = "ANY"
			toolCfg.FunctionCallingConfig.AllowedFunctionNames = []string{tc.Name}
		}
		gr.ToolConfig = toolCfg
	}

	return json.Marshal(gr)
}

func (g *geminiAdapter) Unmarshal(body []byte) (LLMResponse, error) {
	var resp geminiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return LLMResponse{}, fmt.Errorf("gemini: unmarshal: %w", err)
	}
	if resp.Error != nil {
		return LLMResponse{}, fmt.Errorf("gemini: api error: %s", resp.Error.Message)
	}

	var text string
	var finishReason string
	var contentBlocks []ContentBlock

	if len(resp.Candidates) > 0 {
		cand := resp.Candidates[0]
		finishReason = cand.FinishReason
		for _, part := range cand.Content.Parts {
			if part.FunctionCall != nil {
				inputRaw, err := json.Marshal(part.FunctionCall.Args)
				if err != nil {
					inputRaw = json.RawMessage(`{}`)
				}
				contentBlocks = append(contentBlocks, ContentBlock{
					Type:      BlockToolUse,
					ToolName:  part.FunctionCall.Name,
					ToolInput: inputRaw,
				})
			} else if part.Thought {
				contentBlocks = append(contentBlocks, ContentBlock{Type: BlockThinking, Text: part.Text})
			} else if part.Text != "" {
				contentBlocks = append(contentBlocks, ContentBlock{Type: BlockText, Text: part.Text})
				if text == "" {
					text = part.Text
				}
			}
		}
	}

	return LLMResponse{
		Content:       text,
		StopReason:    finishReason,
		InputTokens:   resp.UsageMetadata.PromptTokenCount,
		OutputTokens:  resp.UsageMetadata.CandidatesTokenCount,
		ContentBlocks: contentBlocks,
	}, nil
}

func (g *geminiAdapter) Endpoint(baseURL, modelSlug string) string {
	apiVer := g.apiVersion
	if apiVer == "" {
		apiVer = "v1" // default: stable production API
	}
	if baseURL == "" {
		return "https://generativelanguage.googleapis.com/" + apiVer + "/models/" + modelSlug + ":generateContent"
	}
	base := strings.TrimRight(baseURL, "/")
	// Vertex AI: base_url includes full path up to /models, just append model + action.
	// e.g. https://us-central1-aiplatform.googleapis.com/v1/projects/ID/locations/us-central1/publishers/google/models
	if strings.Contains(base, "aiplatform.googleapis.com") {
		return base + "/" + modelSlug + ":generateContent"
	}
	// If the base_url already contains a version segment (/v1 or /v1beta), the user
	// has set the version explicitly in base_url — honour it, don't add another.
	if strings.Contains(base, "/v1") {
		return base + "/models/" + modelSlug + ":generateContent"
	}
	// No version in base_url: append the configured api_version.
	return base + "/" + apiVer + "/models/" + modelSlug + ":generateContent"
}

func (g *geminiAdapter) AuthHeader(apiKey string) (string, string) {
	return "x-goog-api-key", apiKey
}

// ── Ollama ───────────────────────────────────────────────────────────────────
// Ollama exposes an OpenAI-compatible chat endpoint with stream:false.

type ollamaAdapter struct{}

type ollamaRequest struct {
	Model    string             `json:"model"`
	Messages []openAIReqMessage `json:"messages"`
	Stream   bool               `json:"stream"`
	Options  *ollamaOptions     `json:"options,omitempty"`
	Tools    []openAIToolDef    `json:"tools,omitempty"`
}

type ollamaOptions struct {
	NumPredict  int     `json:"num_predict,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

type ollamaResponse struct {
	Message struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		ToolCalls []openAIToolCall  `json:"tool_calls,omitempty"`
	} `json:"message"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error,omitempty"`
}

func (o *ollamaAdapter) Marshal(req LLMRequest) ([]byte, error) {
	msgs := make([]openAIReqMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		content, _ := json.Marshal(req.System)
		msgs = append(msgs, openAIReqMessage{Role: "system", Content: content})
	}
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		msg, err := marshalOpenAIMessage(m)
		if err != nil {
			return nil, fmt.Errorf("ollama: marshal message: %w", err)
		}
		msgs = append(msgs, msg)
	}
	or := ollamaRequest{
		Model:    req.Model,
		Messages: msgs,
		Stream:   false,
	}
	if req.MaxTokens > 0 || req.Temperature > 0 {
		or.Options = &ollamaOptions{
			NumPredict:  req.MaxTokens,
			Temperature: req.Temperature,
		}
	}
	// Tools
	if len(req.Tools) > 0 {
		or.Tools = make([]openAIToolDef, len(req.Tools))
		for i, t := range req.Tools {
			or.Tools[i] = openAIToolDef{
				Type: "function",
				Function: openAIFuncDef{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			}
		}
	}
	return json.Marshal(or)
}

func (o *ollamaAdapter) Unmarshal(body []byte) (LLMResponse, error) {
	var resp ollamaResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return LLMResponse{}, fmt.Errorf("ollama: unmarshal: %w", err)
	}
	if resp.Error != "" {
		return LLMResponse{}, fmt.Errorf("ollama: api error: %s", resp.Error)
	}

	var contentBlocks []ContentBlock
	for _, tc := range resp.Message.ToolCalls {
		contentBlocks = append(contentBlocks, ContentBlock{
			Type:      BlockToolUse,
			ToolUseID: tc.ID,
			ToolName:  tc.Function.Name,
			ToolInput: json.RawMessage(tc.Function.Arguments),
		})
	}

	return LLMResponse{
		Content:       resp.Message.Content,
		StopReason:    resp.DoneReason,
		InputTokens:   resp.PromptEvalCount,
		OutputTokens:  resp.EvalCount,
		ContentBlocks: contentBlocks,
	}, nil
}

func (o *ollamaAdapter) Endpoint(baseURL, _ string) string {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return strings.TrimRight(baseURL, "/") + "/api/chat"
}

func (o *ollamaAdapter) AuthHeader(_ string) (string, string) {
	return "", "" // Ollama typically runs locally without auth
}

// ── DeepSeek ─────────────────────────────────────────────────────────────────
// DeepSeek exposes an OpenAI-compatible endpoint; reuses OpenAI wire types.

type deepSeekAdapter struct{}

func (d *deepSeekAdapter) Marshal(req LLMRequest) ([]byte, error) {
	msgs := make([]openAIReqMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		content, _ := json.Marshal(req.System)
		msgs = append(msgs, openAIReqMessage{Role: "system", Content: content})
	}
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		msg, err := marshalOpenAIMessage(m)
		if err != nil {
			return nil, fmt.Errorf("deepseek: marshal message: %w", err)
		}
		msgs = append(msgs, msg)
	}
	r := openAIRequest{Model: req.Model, Messages: msgs}
	if req.MaxTokens > 0 {
		n := req.MaxTokens
		r.MaxTokens = &n
	}
	// Tools
	if len(req.Tools) > 0 {
		r.Tools = make([]openAIToolDef, len(req.Tools))
		for i, t := range req.Tools {
			r.Tools[i] = openAIToolDef{
				Type: "function",
				Function: openAIFuncDef{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			}
		}
	}
	if req.ToolChoice != nil {
		tc := req.ToolChoice
		switch tc.Type {
		case ToolChoiceAuto:
			r.ToolChoice = "auto"
		case ToolChoiceRequired:
			r.ToolChoice = "required"
		case ToolChoiceNone:
			r.ToolChoice = "none"
		case ToolChoiceTool:
			r.ToolChoice = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": tc.Name,
				},
			}
		}
	}
	return json.Marshal(r)
}

func (d *deepSeekAdapter) Unmarshal(body []byte) (LLMResponse, error) {
	var resp openAIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return LLMResponse{}, fmt.Errorf("deepseek: unmarshal: %w", err)
	}
	if resp.Error != nil {
		return LLMResponse{}, fmt.Errorf("deepseek: api error: %s", resp.Error.Message)
	}
	var text, finishReason string
	var contentBlocks []ContentBlock
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		finishReason = choice.FinishReason
		if choice.Message.Content != nil {
			text = *choice.Message.Content
		}
		for _, tc := range choice.Message.ToolCalls {
			contentBlocks = append(contentBlocks, ContentBlock{
				Type:      BlockToolUse,
				ToolUseID: tc.ID,
				ToolName:  tc.Function.Name,
				ToolInput: json.RawMessage(tc.Function.Arguments),
			})
		}
	}
	return LLMResponse{
		Content:       text,
		StopReason:    finishReason,
		InputTokens:   resp.Usage.PromptTokens,
		OutputTokens:  resp.Usage.CompletionTokens,
		ContentBlocks: contentBlocks,
	}, nil
}

func (d *deepSeekAdapter) Endpoint(baseURL, _ string) string {
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	return strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
}

func (d *deepSeekAdapter) AuthHeader(apiKey string) (string, string) {
	return "Authorization", "Bearer " + apiKey
}

// ── Custom (OpenAI-compatible wire, config-driven auth) ───────────────────────
// Use adapter: custom for any OpenAI-compatible provider without code changes:
//   HuggingFace TGI, vLLM, LM Studio, Groq, Together AI, Fireworks, etc.
//
// base_url is the FULL endpoint URL. Use {model} as a placeholder for the model ID.
// Examples:
//
//	https://api.groq.com/openai/v1/chat/completions
//	http://localhost:8080/v1/chat/completions
//	https://my-proxy.internal/llm/{model}/chat
//
// auth_header_name defaults to "Authorization"; auth_header_prefix defaults to "Bearer ".
// Example for x-api-key style: auth_header_name: "x-api-key", auth_header_prefix: ""

type customAdapter struct {
	authHeaderName   string
	authHeaderPrefix string
}

func (c *customAdapter) Marshal(req LLMRequest) ([]byte, error) {
	msgs := make([]openAIReqMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		content, _ := json.Marshal(req.System)
		msgs = append(msgs, openAIReqMessage{Role: "system", Content: content})
	}
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		msg, err := marshalOpenAIMessage(m)
		if err != nil {
			return nil, fmt.Errorf("custom: marshal message: %w", err)
		}
		msgs = append(msgs, msg)
	}
	r := openAIRequest{Model: req.Model, Messages: msgs}
	if req.MaxTokens > 0 {
		n := req.MaxTokens
		r.MaxTokens = &n
	}
	return json.Marshal(r)
}

func (c *customAdapter) Unmarshal(body []byte) (LLMResponse, error) {
	var resp openAIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return LLMResponse{}, fmt.Errorf("custom: unmarshal: %w", err)
	}
	if resp.Error != nil {
		return LLMResponse{}, fmt.Errorf("custom: api error: %s", resp.Error.Message)
	}
	var text, finishReason string
	var contentBlocks []ContentBlock
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		finishReason = choice.FinishReason
		if choice.Message.Content != nil {
			text = *choice.Message.Content
		}
		for _, tc := range choice.Message.ToolCalls {
			contentBlocks = append(contentBlocks, ContentBlock{
				Type:      BlockToolUse,
				ToolUseID: tc.ID,
				ToolName:  tc.Function.Name,
				ToolInput: json.RawMessage(tc.Function.Arguments),
			})
		}
	}
	return LLMResponse{
		Content:       text,
		StopReason:    finishReason,
		InputTokens:   resp.Usage.PromptTokens,
		OutputTokens:  resp.Usage.CompletionTokens,
		ContentBlocks: contentBlocks,
	}, nil
}

func (c *customAdapter) Endpoint(baseURL, modelSlug string) string {
	// base_url is the full endpoint URL template.
	// {model} is replaced with the actual model ID, allowing per-model routing paths.
	return strings.ReplaceAll(baseURL, "{model}", modelSlug)
}

func (c *customAdapter) AuthHeader(apiKey string) (string, string) {
	return c.authHeaderName, c.authHeaderPrefix + apiKey
}
