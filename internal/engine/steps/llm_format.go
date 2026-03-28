package steps

import (
	"encoding/json"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// ParseMessageFormatConfig holds the bake-time slot assignments for ParseMessageFormat.
type ParseMessageFormatConfig struct {
	BodySlot        int // input: ByteSlots index with the raw JSON request body
	MessagesSlot    int // output: ByteSlots index for JSON-encoded []CanonicalMessage
	SystemSlot      int // output: ByteSlots index for extracted system prompt (-1 = don't extract)
	DetectedFmtSlot int // output: ByteSlots index for detected format string (-1 = don't store)
}

// ParseMessageFormat returns an Instruction that reads a provider-specific JSON
// body from cfg.BodySlot, detects the format (anthropic/openai/gemini), parses it
// into []CanonicalMessage, and writes results to the configured output slots.
func ParseMessageFormat(cfg ParseMessageFormatConfig) engine.Instruction {
	return engine.Instruction{
		Name: "PARSE_MESSAGE_FORMAT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			body := ctx.ByteSlots[cfg.BodySlot]
			if len(body) == 0 {
				return state.PC + 1
			}

			// Parse top-level keys only to detect format.
			var topLevel map[string]json.RawMessage
			if err := json.Unmarshal(body, &topLevel); err != nil {
				ctx.ResponseStatus = 400
				ctx.Failed = true
				msg := []byte("invalid JSON body")
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			_, hasContents := topLevel["contents"]
			_, hasMessages := topLevel["messages"]
			_, hasSystem := topLevel["system"]

			var format string
			switch {
			case hasContents:
				format = "gemini"
			case hasMessages && hasSystem:
				format = "anthropic"
			case hasMessages:
				format = "openai"
			default:
				ctx.ResponseStatus = 400
				ctx.Failed = true
				msg := []byte("unknown LLM request format: missing 'contents' or 'messages' key")
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			var messages []CanonicalMessage
			var systemText string

			switch format {
			case "anthropic":
				type anthropicMsg struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}
				var req struct {
					System   string         `json:"system"`
					Messages []anthropicMsg `json:"messages"`
				}
				if err := json.Unmarshal(body, &req); err != nil {
					ctx.ResponseStatus = 400
					ctx.Failed = true
					msg := []byte("failed to parse anthropic request body")
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
				systemText = req.System
				messages = make([]CanonicalMessage, 0, len(req.Messages))
				for _, m := range req.Messages {
					switch m.Role {
					case "user":
						messages = append(messages, CanonicalMessage{Role: RoleUser, Content: m.Content})
					case "assistant":
						messages = append(messages, CanonicalMessage{Role: RoleAssistant, Content: m.Content})
					}
				}

			case "openai":
				type openAIMsg struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}
				var req struct {
					Messages []openAIMsg `json:"messages"`
				}
				if err := json.Unmarshal(body, &req); err != nil {
					ctx.ResponseStatus = 400
					ctx.Failed = true
					msg := []byte("failed to parse openai request body")
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
				systemExtracted := false
				messages = make([]CanonicalMessage, 0, len(req.Messages))
				for _, m := range req.Messages {
					switch m.Role {
					case "system":
						if !systemExtracted {
							systemText = m.Content
							systemExtracted = true
						}
					case "user":
						messages = append(messages, CanonicalMessage{Role: RoleUser, Content: m.Content})
					case "assistant":
						messages = append(messages, CanonicalMessage{Role: RoleAssistant, Content: m.Content})
					}
				}

			case "gemini":
				type geminiPart struct {
					Text string `json:"text"`
				}
				type geminiContent struct {
					Role  string       `json:"role"`
					Parts []geminiPart `json:"parts"`
				}
				var req struct {
					Contents          []geminiContent `json:"contents"`
					SystemInstruction *struct {
						Parts []geminiPart `json:"parts"`
					} `json:"systemInstruction"`
				}
				if err := json.Unmarshal(body, &req); err != nil {
					ctx.ResponseStatus = 400
					ctx.Failed = true
					msg := []byte("failed to parse gemini request body")
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
				if req.SystemInstruction != nil && len(req.SystemInstruction.Parts) > 0 {
					systemText = req.SystemInstruction.Parts[0].Text
				}
				messages = make([]CanonicalMessage, 0, len(req.Contents))
				for _, c := range req.Contents {
					var role MessageRole
					switch c.Role {
					case "user":
						role = RoleUser
					case "model":
						role = RoleAssistant
					default:
						continue
					}
					var text string
					if len(c.Parts) > 0 {
						text = c.Parts[0].Text
					}
					messages = append(messages, CanonicalMessage{Role: role, Content: text})
				}
			}

			// JSON-encode canonical messages and write to MessagesSlot.
			encoded, err := json.Marshal(messages)
			if err != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				msg := []byte("failed to encode canonical messages")
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}
			msgsSlice := ctx.Alloc(len(encoded))
			copy(msgsSlice, encoded)
			ctx.ByteSlots[cfg.MessagesSlot] = msgsSlice

			// Write system text to SystemSlot if configured.
			if cfg.SystemSlot >= 0 && systemText != "" {
				sysSlice := ctx.Alloc(len(systemText))
				copy(sysSlice, systemText)
				ctx.ByteSlots[cfg.SystemSlot] = sysSlice
			}

			// Write detected format to DetectedFmtSlot if configured.
			if cfg.DetectedFmtSlot >= 0 {
				fmtSlice := ctx.Alloc(len(format))
				copy(fmtSlice, format)
				ctx.ByteSlots[cfg.DetectedFmtSlot] = fmtSlice
			}

			return state.PC + 1
		},
	}
}

// FormatResponseConfig holds the bake-time parameters for FormatResponse.
type FormatResponseConfig struct {
	ResponseSlot int    // input: ByteSlots index with the response text string
	OutputSlot   int    // output: ByteSlots index for formatted JSON
	Format       string // "anthropic", "openai", "gemini" — static, baked at compile time
	Model        string // model name to include in response (static, from step config)
}

// FormatResponse returns an Instruction that reads a response text from
// cfg.ResponseSlot and encodes it as the appropriate provider JSON shape
// into cfg.OutputSlot.
func FormatResponse(cfg FormatResponseConfig) engine.Instruction {
	return engine.Instruction{
		Name: "FORMAT_RESPONSE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			responseText := string(ctx.ByteSlots[cfg.ResponseSlot])

			if len(ctx.ByteSlots[cfg.ResponseSlot]) == 0 {
				empty := []byte("{}")
				slot := ctx.Alloc(len(empty))
				copy(slot, empty)
				ctx.ByteSlots[cfg.OutputSlot] = slot
				return state.PC + 1
			}

			var encoded []byte
			var err error

			switch cfg.Format {
			case "anthropic":
				type anthropicContent struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				type anthropicUsage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				}
				type anthropicResp struct {
					ID         string             `json:"id"`
					Type       string             `json:"type"`
					Role       string             `json:"role"`
					Model      string             `json:"model"`
					Content    []anthropicContent `json:"content"`
					StopReason string             `json:"stop_reason"`
					Usage      anthropicUsage     `json:"usage"`
				}
				encoded, err = json.Marshal(anthropicResp{
					ID:    "msg_rah",
					Type:  "message",
					Role:  "assistant",
					Model: cfg.Model,
					Content: []anthropicContent{
						{Type: "text", Text: responseText},
					},
					StopReason: "end_turn",
					Usage:      anthropicUsage{InputTokens: 0, OutputTokens: 0},
				})

			case "openai":
				type openAIMessage struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}
				type openAIChoice struct {
					Index        int           `json:"index"`
					Message      openAIMessage `json:"message"`
					FinishReason string        `json:"finish_reason"`
				}
				type openAIUsage struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
					TotalTokens      int `json:"total_tokens"`
				}
				type openAIResp struct {
					ID      string        `json:"id"`
					Object  string        `json:"object"`
					Model   string        `json:"model"`
					Choices []openAIChoice `json:"choices"`
					Usage   openAIUsage   `json:"usage"`
				}
				encoded, err = json.Marshal(openAIResp{
					ID:     "chatcmpl-rah",
					Object: "chat.completion",
					Model:  cfg.Model,
					Choices: []openAIChoice{
						{
							Index:        0,
							Message:      openAIMessage{Role: "assistant", Content: responseText},
							FinishReason: "stop",
						},
					},
					Usage: openAIUsage{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0},
				})

			case "gemini":
				type geminiPart struct {
					Text string `json:"text"`
				}
				type geminiContent struct {
					Parts []geminiPart `json:"parts"`
					Role  string       `json:"role"`
				}
				type geminiCandidate struct {
					Content      geminiContent `json:"content"`
					FinishReason string        `json:"finishReason"`
					Index        int           `json:"index"`
				}
				type geminiUsage struct {
					PromptTokenCount     int `json:"promptTokenCount"`
					CandidatesTokenCount int `json:"candidatesTokenCount"`
				}
				type geminiResp struct {
					Candidates    []geminiCandidate `json:"candidates"`
					UsageMetadata geminiUsage       `json:"usageMetadata"`
				}
				encoded, err = json.Marshal(geminiResp{
					Candidates: []geminiCandidate{
						{
							Content: geminiContent{
								Parts: []geminiPart{{Text: responseText}},
								Role:  "model",
							},
							FinishReason: "STOP",
							Index:        0,
						},
					},
					UsageMetadata: geminiUsage{PromptTokenCount: 0, CandidatesTokenCount: 0},
				})

			default:
				// Unknown format — write empty JSON object.
				empty := []byte("{}")
				slot := ctx.Alloc(len(empty))
				copy(slot, empty)
				ctx.ByteSlots[cfg.OutputSlot] = slot
				return state.PC + 1
			}

			if err != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				msg := []byte("failed to encode formatted response")
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			outSlice := ctx.Alloc(len(encoded))
			copy(outSlice, encoded)
			ctx.ByteSlots[cfg.OutputSlot] = outSlice

			return state.PC + 1
		},
	}
}
