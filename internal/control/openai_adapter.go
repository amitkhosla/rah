package control

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/amitkhosla/rah/internal/config"
	steps "github.com/amitkhosla/rah/internal/engine/steps"
)

type openAIPassReq struct {
	Model               string           `json:"model"`
	MaxTokens           *int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int             `json:"max_completion_tokens,omitempty"`
	Messages            []openAIPassMsg  `json:"messages"`
	Tools               []openAIPassTool `json:"tools,omitempty"`
	Stream              bool             `json:"stream,omitempty"`
}

type openAIPassMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIPassTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

type openAIPassResp struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// RegisterOpenAIAdapter registers POST /v1/chat/completions on mux.
// getLLM is called on every request so newly-registered models are visible immediately.
// sm is used to resolve api_key_ref values like "env:OPENAI_KEY"; pass nil to skip resolution.
func RegisterOpenAIAdapter(mux *http.ServeMux, getLLM func() config.LLMConfig, sm steps.SecretLoader) {
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			openAIPassError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			openAIPassError(w, http.StatusBadRequest, "failed to read body")
			return
		}
		var req openAIPassReq
		if err := json.Unmarshal(body, &req); err != nil {
			openAIPassError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}

		// Build catalog from live config so models registered via the UI are visible.
		llmCfg := getLLM()
		catalog := make(map[string]config.LLMModelConfig, len(llmCfg.Models))
		for _, m := range llmCfg.Models {
			catalog[m.Alias] = m
			if m.ModelID != "" && m.ModelID != m.Alias {
				catalog[m.ModelID] = m
			}
		}

		modelAlias := req.Model
		if modelAlias == "" {
			modelAlias = llmCfg.Default
		}
		mc, ok := catalog[modelAlias]
		if !ok {
			openAIPassError(w, http.StatusBadRequest, "unknown model: "+modelAlias)
			return
		}

		var systemContent string
		messages := make([]steps.CanonicalMessage, 0, len(req.Messages))
		for _, m := range req.Messages {
			if m.Role == "system" {
				systemContent = m.Content
				continue
			}
			messages = append(messages, steps.CanonicalMessage{
				Role:    steps.MessageRole(m.Role),
				Content: m.Content,
			})
		}

		maxTokens := mc.MaxTokens
		if req.MaxCompletionTokens != nil {
			maxTokens = *req.MaxCompletionTokens
		} else if req.MaxTokens != nil {
			maxTokens = *req.MaxTokens
		}
		if maxTokens <= 0 {
			maxTokens = 2000
		}

		var tools []steps.ToolDefinition
		for _, t := range req.Tools {
			tools = append(tools, steps.ToolDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				InputSchema: t.Function.Parameters,
			})
		}

		apiKey := mc.APIKeyRef
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			apiKey = strings.TrimPrefix(auth, "Bearer ")
		} else if sm != nil && apiKey != "" {
			if resolved, resolveErr := sm.Resolve(r.Context(), apiKey); resolveErr == nil {
				apiKey = string(resolved)
			}
		}

		llmReq := steps.LLMRequest{
			Messages:  messages,
			System:    systemContent,
			Model:     mc.ModelID,
			MaxTokens: maxTokens,
			Tools:     tools,
		}
		llmResp, err := steps.ExecuteLLMDirect(mc, apiKey, llmReq, 0)
		if err != nil {
			openAIPassError(w, http.StatusBadGateway, err.Error())
			return
		}

		finishReason := llmResp.StopReason
		if finishReason == "" {
			finishReason = "stop"
		}
		var resp openAIPassResp
		resp.ID = "rah-" + mc.Alias
		resp.Object = "chat.completion"
		resp.Model = modelAlias
		resp.Choices = append(resp.Choices, struct {
			Index   int `json:"index"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}{
			FinishReason: finishReason,
		})
		resp.Choices[0].Message.Role = "assistant"
		resp.Choices[0].Message.Content = llmResp.Content
		resp.Usage.PromptTokens = llmResp.InputTokens
		resp.Usage.CompletionTokens = llmResp.OutputTokens
		resp.Usage.TotalTokens = llmResp.InputTokens + llmResp.OutputTokens

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}

func openAIPassError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]any{
		"error": map[string]string{"message": msg, "type": "api_error"},
	})
	_, _ = w.Write(b)
}
