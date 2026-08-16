package steps

import (
	"encoding/json"
	"strings"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// TransformMessagesConfig holds the bake-time configuration for TransformMessages.
type TransformMessagesConfig struct {
	// Input/output slots
	HistorySlot  int // []CanonicalMessage JSON — read and write back
	SystemSlot   int // system prompt text — read or write (-1 = unused)
	ThinkingSlot int // extracted thinking text — write only (-1 = unused)
	FormatSlot   int // read source format string from slot (-1 = use SourceFormat field)

	// Format config (used if FormatSlot < 0)
	SourceFormat string // "anthropic" | "openai" | "gemini" | "" (auto from FormatSlot)
	TargetFormat string // "anthropic" | "openai" | "gemini"

	// Content transforms (each independent, all default false)
	// Applied in fixed order: ExtractSystem, StripThinking/ExtractThinking,
	// FlattenContent, AdaptRoles, NormalizeTools, InjectSystem.
	StripThinking   bool // remove {type:"thinking"} content blocks from assistant messages
	ExtractThinking bool // move thinking text to ThinkingSlot instead of stripping
	ExtractSystem   bool // pull system-role messages out of history â†’ SystemSlot
	InjectSystem    bool // prepend SystemSlot content as system-role message into history
	FlattenContent  bool // collapse [{type:"text",text:"x"}] arrays â†’ plain string "x"
	AdaptRoles      bool // map role names: "model"â†""assistant", normalize tool roles
	NormalizeTools  bool // rewrite tool_result messages for target format compatibility

	// History truncation (applied after all content transforms above)
	MaxMessages     int  // hard cap on message count; 0 = no cap
	MaxTokens       int  // token budget; 0 = no cap (uses estimateTokens from llm_types.go)
	RemoveOrphans   bool // remove tool_result blocks with no matching preceding tool_use
	EnsureStartUser bool // after truncation, drop leading non-user messages
}

// contentBlock is a single element in an Anthropic-style content array.
type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

// parseContentBlocks attempts to parse content as a JSON array of content
// blocks. Returns nil if the content is not a JSON array or is malformed.
func parseContentBlocks(content string) []contentBlock {
	if len(content) == 0 || content[0] != '[' {
		return nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal([]byte(content), &blocks); err != nil {
		return nil
	}
	return blocks
}

// applyExtractSystem removes system-role messages from msgs, concatenates their
// content, and writes the result to ByteSlots[systemSlot] (if >= 0).
func applyExtractSystem(msgs []CanonicalMessage, ctx *rctx.Context, state *engine.ExecutionState, systemSlot int) []CanonicalMessage {
	if systemSlot < 0 {
		// Still remove system messages from history even if no slot to write to.
		out := msgs[:0:len(msgs)]
		for _, m := range msgs {
			if m.Role != RoleSystem {
				out = append(out, m)
			}
		}
		return out
	}

	var systemParts []string
	out := make([]CanonicalMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == RoleSystem {
			systemParts = append(systemParts, m.Content)
		} else {
			out = append(out, m)
		}
	}
	if len(systemParts) > 0 {
		combined := strings.Join(systemParts, "\n")
		state.WriteSlot(ctx, systemSlot, []byte(combined))
	}
	return out
}

// applyStripOrExtractThinking removes thinking blocks from assistant messages.
// If extract is true, the thinking text is collected and written to thinkingSlot.
// Handles both the legacy Content-string format (JSON array of contentBlock) and
// the structured ContentBlocks []ContentBlock field.
func applyStripOrExtractThinking(msgs []CanonicalMessage, ctx *rctx.Context, state *engine.ExecutionState, extract bool, thinkingSlot int) []CanonicalMessage {
	var thinkingParts []string

	for i, m := range msgs {
		if m.Role != RoleAssistant {
			continue
		}

		// Structured ContentBlocks path.
		if len(m.ContentBlocks) > 0 {
			var kept []ContentBlock
			for _, b := range m.ContentBlocks {
				if b.Type == BlockThinking {
					if extract {
						thinkingParts = append(thinkingParts, b.Text)
					}
					// Drop — do not include in kept.
				} else {
					kept = append(kept, b)
				}
			}
			msgs[i].ContentBlocks = kept
			continue
		}

		// Legacy Content-string path (JSON array of contentBlock).
		blocks := parseContentBlocks(m.Content)
		if blocks == nil {
			continue
		}

		var textBlocks []contentBlock
		for _, b := range blocks {
			if b.Type == "thinking" {
				if extract {
					thinkingParts = append(thinkingParts, b.Thinking)
				}
				// Skip — don't include in textBlocks
			} else {
				textBlocks = append(textBlocks, b)
			}
		}

		// Re-serialize remaining blocks back into content.
		if len(textBlocks) == 0 {
			msgs[i].Content = ""
		} else if len(textBlocks) == 1 && textBlocks[0].Type == "text" {
			// Collapse single text block to plain string.
			msgs[i].Content = textBlocks[0].Text
		} else {
			encoded, err := json.Marshal(textBlocks)
			if err != nil {
				msgs[i].Content = ""
			} else {
				msgs[i].Content = string(encoded)
			}
		}
	}

	if extract && len(thinkingParts) > 0 && thinkingSlot >= 0 {
		combined := strings.Join(thinkingParts, "\n---\n")
		state.WriteSlot(ctx, thinkingSlot, []byte(combined))
	}

	return msgs
}

// applyFlattenContent collapses content arrays to plain strings by extracting
// only the text fields and joining them.
func applyFlattenContent(msgs []CanonicalMessage) []CanonicalMessage {
	for i, m := range msgs {
		blocks := parseContentBlocks(m.Content)
		if blocks == nil {
			continue
		}
		var textParts []string
		for _, b := range blocks {
			if b.Text != "" {
				textParts = append(textParts, b.Text)
			}
		}
		msgs[i].Content = strings.Join(textParts, "")
	}
	return msgs
}

// applyAdaptRoles normalizes role names between providers.
func applyAdaptRoles(msgs []CanonicalMessage, targetFormat string) []CanonicalMessage {
	for i, m := range msgs {
		switch string(m.Role) {
		case "model":
			// Gemini "model" â†’ canonical "assistant" for non-Gemini targets
			if targetFormat != "gemini" {
				msgs[i].Role = RoleAssistant
			}
		case "assistant":
			// canonical "assistant" â†’ Gemini "model" for Gemini target
			if targetFormat == "gemini" {
				msgs[i].Role = "model"
			}
		case "function":
			// Old OpenAI function calling â†’ new tool calling
			msgs[i].Role = "tool"
		}
	}
	return msgs
}

// applyNormalizeTools rewrites tool_result messages for the target format.
func applyNormalizeTools(msgs []CanonicalMessage, targetFormat string) []CanonicalMessage {
	for i, m := range msgs {
		if m.Role != RoleToolResult {
			continue
		}
		switch targetFormat {
		case "gemini":
			// Gemini: tool results use "function" role
			msgs[i].Role = "function"
		case "openai":
			// OpenAI: tool results stay as tool_result role; ToolCallID should be set
			// (nothing to mutate here without more context; the ToolCallID is already in the struct)
		case "anthropic":
			// Anthropic: tool results stay as tool_result role
		}
	}
	return msgs
}

// applyInjectSystem prepends a system-role message from SystemSlot to history.
func applyInjectSystem(msgs []CanonicalMessage, ctx *rctx.Context, systemSlot int) []CanonicalMessage {
	if systemSlot < 0 {
		return msgs
	}
	systemContent := ctx.ByteSlots[systemSlot]
	if len(systemContent) == 0 {
		return msgs
	}
	sysMsg := CanonicalMessage{Role: RoleSystem, Content: string(systemContent)}
	result := make([]CanonicalMessage, 0, len(msgs)+1)
	result = append(result, sysMsg)
	result = append(result, msgs...)
	return result
}

// TransformMessages returns an Instruction that applies structural wire-format
// transformations to canonical messages when routing between LLM providers.
// Each transform is independently togglable and all are deterministic (no LLM calls).
// Transforms are applied in fixed order:
//  1. ExtractSystem
//  2. StripThinking / ExtractThinking
//  3. FlattenContent
//  4. AdaptRoles
//  5. NormalizeTools
//  6. InjectSystem
func TransformMessages(cfg TransformMessagesConfig) engine.Instruction {
	return engine.Instruction{
		Name: "transform_messages",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Decode history from slot.
			msgs := decodeHistory(ctx.ByteSlots[cfg.HistorySlot])
			if len(msgs) == 0 {
				return state.PC + 1
			}

			// Determine effective target format (FormatSlot overrides SourceFormat field
			// for source; TargetFormat is always static from cfg).
			targetFormat := cfg.TargetFormat
			if cfg.FormatSlot >= 0 {
				if raw := ctx.ByteSlots[cfg.FormatSlot]; len(raw) > 0 {
					// FormatSlot holds the source format; target is still cfg.TargetFormat.
					// The FormatSlot value is the source format — it can be used for
					// format-specific logic. Here we just use TargetFormat for transforms.
					_ = string(raw) // source format available if needed in future
				}
			}

			// 1. ExtractSystem
			if cfg.ExtractSystem {
				msgs = applyExtractSystem(msgs, ctx, state, cfg.SystemSlot)
			}

			// 2. StripThinking / ExtractThinking
			if cfg.ExtractThinking {
				msgs = applyStripOrExtractThinking(msgs, ctx, state, true, cfg.ThinkingSlot)
			} else if cfg.StripThinking {
				msgs = applyStripOrExtractThinking(msgs, ctx, state, false, -1)
			}

			// 3. FlattenContent
			if cfg.FlattenContent {
				msgs = applyFlattenContent(msgs)
			}

			// 4. AdaptRoles
			if cfg.AdaptRoles {
				msgs = applyAdaptRoles(msgs, targetFormat)
			}

			// 5. NormalizeTools
			if cfg.NormalizeTools {
				msgs = applyNormalizeTools(msgs, targetFormat)
			}

			// 6. InjectSystem
			if cfg.InjectSystem {
				msgs = applyInjectSystem(msgs, ctx, cfg.SystemSlot)
			}

			// â"€â"€ Truncation â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
			needsTruncation := cfg.MaxMessages > 0 || cfg.MaxTokens > 0

			if needsTruncation && len(msgs) > 0 {
				// 7a. Hard message count cap: keep the most recent MaxMessages messages.
				if cfg.MaxMessages > 0 && len(msgs) > cfg.MaxMessages {
					msgs = msgs[len(msgs)-cfg.MaxMessages:]
				}

				// 7b. Token budget: drop oldest messages until under budget.
				if cfg.MaxTokens > 0 {
					for len(msgs) > 1 {
						total := 0
						for _, m := range msgs {
							if m.HasBlocks() {
								for _, b := range m.ContentBlocks {
									total += estimateTokens(b.Text) + estimateTokens(b.ToolResult) + 4
								}
							} else {
								total += estimateTokens(m.Content) + 4
							}
						}
						if total <= cfg.MaxTokens {
							break
						}
						msgs = msgs[1:] // drop oldest
					}
				}

				// 7c. Remove orphaned tool_results.
				// A tool_result ContentBlock is orphaned if its ToolCallID has no
				// matching tool_use ContentBlock (with the same ToolUseID) anywhere
				// in the retained window.
				if cfg.RemoveOrphans && len(msgs) > 0 {
					// Build set of all tool_use IDs in the retained window.
					activeToolUseIDs := make(map[string]bool)
					for _, m := range msgs {
						for _, b := range m.ContentBlocks {
							if b.Type == BlockToolUse && b.ToolUseID != "" {
								activeToolUseIDs[b.ToolUseID] = true
							}
						}
					}
					// Filter out messages whose every block is an orphaned tool_result.
					filtered := msgs[:0]
					for _, m := range msgs {
						if m.HasBlocks() {
							hasNonOrphan := false
							for _, b := range m.ContentBlocks {
								if b.Type != BlockToolResult {
									hasNonOrphan = true
									break
								}
								if activeToolUseIDs[b.ToolCallID] {
									hasNonOrphan = true
									break
								}
							}
							if hasNonOrphan {
								filtered = append(filtered, m)
							}
							// else: skip — all blocks are orphaned tool_results
						} else {
							filtered = append(filtered, m)
						}
					}
					msgs = filtered
				}

				// 7d. Ensure history starts with a user message.
				if cfg.EnsureStartUser {
					for len(msgs) > 0 && msgs[0].Role != RoleUser {
						msgs = msgs[1:]
					}
				}
			}

			// Write transformed history back to slot.
			writeHistorySlot(ctx, state, cfg.HistorySlot, msgs)
			return state.PC + 1
		},
	}
}
