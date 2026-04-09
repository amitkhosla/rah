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
		return &geminiAdapter{}, nil
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
	default:
		return nil, fmt.Errorf("unknown LLM adapter: %q", cfg.Adapter)
	}
}

// ── Anthropic ────────────────────────────────────────────────────────────────

type anthropicAdapter struct{}

type anthropicReqMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string                `json:"model"`
	MaxTokens int                   `json:"max_tokens"`
	System    string                `json:"system,omitempty"`
	Messages  []anthropicReqMessage `json:"messages"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (a *anthropicAdapter) Marshal(req LLMRequest) ([]byte, error) {
	msgs := make([]anthropicReqMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue // system goes in top-level field, not messages
		}
		msgs = append(msgs, anthropicReqMessage{Role: string(m.Role), Content: m.Content})
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
	return json.Marshal(anthropicRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    system,
		Messages:  msgs,
	})
}

func (a *anthropicAdapter) Unmarshal(body []byte) (LLMResponse, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return LLMResponse{}, fmt.Errorf("anthropic: unmarshal: %w", err)
	}
	if resp.Error != nil {
		return LLMResponse{}, fmt.Errorf("anthropic: api error: %s", resp.Error.Message)
	}
	var text string
	for _, block := range resp.Content {
		if block.Type == "text" {
			text = block.Text
			break
		}
	}
	return LLMResponse{
		Content:      text,
		StopReason:   resp.StopReason,
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
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
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []openAIReqMessage `json:"messages"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
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

func (o *openAIAdapter) Marshal(req LLMRequest) ([]byte, error) {
	msgs := make([]openAIReqMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, openAIReqMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue // already prepended above
		}
		msgs = append(msgs, openAIReqMessage{Role: string(m.Role), Content: m.Content})
	}
	return json.Marshal(openAIRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Messages:  msgs,
	})
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
	if len(resp.Choices) > 0 {
		text = resp.Choices[0].Message.Content
	}
	var finishReason string
	if len(resp.Choices) > 0 {
		finishReason = resp.Choices[0].FinishReason
	}
	return LLMResponse{
		Content:      text,
		StopReason:   finishReason,
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
	}, nil
}

func (o *openAIAdapter) Endpoint(baseURL, _ string) string {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
}

func (o *openAIAdapter) AuthHeader(apiKey string) (string, string) {
	return "Authorization", "Bearer " + apiKey
}

// ── Gemini ───────────────────────────────────────────────────────────────────

type geminiAdapter struct{}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiGenConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float32 `json:"temperature,omitempty"`
}

type geminiRequest struct {
	Contents          []geminiContent  `json:"contents"`
	SystemInstruction *geminiContent   `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenConfig `json:"generationConfig,omitempty"`
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

func (g *geminiAdapter) Marshal(req LLMRequest) ([]byte, error) {
	contents := make([]geminiContent, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		role := string(m.Role)
		if role == "assistant" {
			role = "model"
		}
		contents = append(contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: m.Content}},
		})
	}

	gr := geminiRequest{Contents: contents}

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
		gr.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: system}}}
	}
	if req.MaxTokens > 0 || req.Temperature > 0 {
		gr.GenerationConfig = &geminiGenConfig{
			MaxOutputTokens: req.MaxTokens,
			Temperature:     float32(req.Temperature),
		}
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
	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		text = resp.Candidates[0].Content.Parts[0].Text
	}
	var finishReason string
	if len(resp.Candidates) > 0 {
		finishReason = resp.Candidates[0].FinishReason
	}
	return LLMResponse{
		Content:      text,
		StopReason:   finishReason,
		InputTokens:  resp.UsageMetadata.PromptTokenCount,
		OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
	}, nil
}

func (g *geminiAdapter) Endpoint(baseURL, modelSlug string) string {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	return strings.TrimRight(baseURL, "/") + "/v1beta/models/" + modelSlug + ":generateContent"
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
}

type ollamaOptions struct {
	NumPredict  int     `json:"num_predict,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

type ollamaResponse struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	DoneReason       string `json:"done_reason"`
	PromptEvalCount  int    `json:"prompt_eval_count"`
	EvalCount        int    `json:"eval_count"`
	Error            string `json:"error,omitempty"`
}

func (o *ollamaAdapter) Marshal(req LLMRequest) ([]byte, error) {
	msgs := make([]openAIReqMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, openAIReqMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		msgs = append(msgs, openAIReqMessage{Role: string(m.Role), Content: m.Content})
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
	return LLMResponse{
		Content:      resp.Message.Content,
		StopReason:   resp.DoneReason,
		InputTokens:  resp.PromptEvalCount,
		OutputTokens: resp.EvalCount,
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
		msgs = append(msgs, openAIReqMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		msgs = append(msgs, openAIReqMessage{Role: string(m.Role), Content: m.Content})
	}
	return json.Marshal(openAIRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Messages:  msgs,
	})
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
	if len(resp.Choices) > 0 {
		text = resp.Choices[0].Message.Content
		finishReason = resp.Choices[0].FinishReason
	}
	return LLMResponse{
		Content:      text,
		StopReason:   finishReason,
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
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
// auth_header_name defaults to "Authorization"; auth_header_prefix defaults to "Bearer ".
// Example for x-api-key style: auth_header_name: "x-api-key", auth_header_prefix: ""

type customAdapter struct {
	authHeaderName   string
	authHeaderPrefix string
}

func (c *customAdapter) Marshal(req LLMRequest) ([]byte, error) {
	msgs := make([]openAIReqMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, openAIReqMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			continue
		}
		msgs = append(msgs, openAIReqMessage{Role: string(m.Role), Content: m.Content})
	}
	return json.Marshal(openAIRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Messages:  msgs,
	})
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
	if len(resp.Choices) > 0 {
		text = resp.Choices[0].Message.Content
		finishReason = resp.Choices[0].FinishReason
	}
	return LLMResponse{
		Content:      text,
		StopReason:   finishReason,
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
	}, nil
}

func (c *customAdapter) Endpoint(baseURL, _ string) string {
	return strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
}

func (c *customAdapter) AuthHeader(apiKey string) (string, string) {
	return c.authHeaderName, c.authHeaderPrefix + apiKey
}
