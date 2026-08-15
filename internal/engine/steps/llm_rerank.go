package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// RerankProvider identifies the rerank API provider.
type RerankProvider string

const (
	RerankCohere RerankProvider = "cohere"
	RerankJina   RerankProvider = "jina"
)

// RerankConfig configures a rerank instruction.
type RerankConfig struct {
	Provider    RerankProvider
	BaseURL     string // override; defaults to provider default
	APIKey      string // resolved at bake time
	Model       string // e.g. "rerank-english-v3.0" for Cohere
	QuerySlot   int    // ByteSlots index for the query string
	DocsSlot    int    // ByteSlots index for JSON []string of document texts
	ResultSlot  int    // ByteSlots index to write reranked JSON []string
	TopN        int    // max results to return (0 = all)
	TimeoutMs   int    // default 10000
}

// rerankClientCache holds per-(provider+baseURL) HTTP clients.
var (
	rerankClientOnce  sync.Once
	rerankClientCache sync.Map // key: provider+"|"+baseURL â†’ *http.Client
)

func getRerankClient(provider RerankProvider, baseURL string, timeoutMs int) *http.Client {
	key := string(provider) + "|" + baseURL
	if c, ok := rerankClientCache.Load(key); ok {
		return c.(*http.Client)
	}

	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: timeout,
		},
		Timeout: timeout,
	}

	actual, _ := rerankClientCache.LoadOrStore(key, client)
	return actual.(*http.Client)
}

// cohereRerankRequest is the request format for Cohere rerank API.
type cohereRerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n,omitempty"`
	ReturnDocuments bool     `json:"return_documents"`
}

// cohereRerankResponse is the response format from Cohere rerank API.
type cohereRerankResponse struct {
	Results []struct {
		Index            int    `json:"index"`
		RelevanceScore   float64 `json:"relevance_score"`
		Document struct {
			Text string `json:"text"`
		} `json:"document"`
	} `json:"results"`
}

// jinaRerankRequest is the request format for Jina rerank API.
type jinaRerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"`
}

// jinaRerankResponse is the response format from Jina rerank API.
type jinaRerankResponse struct {
	Results []struct {
		Index            int    `json:"index"`
		RelevanceScore   float64 `json:"relevance_score"`
		Document struct {
			Text string `json:"text"`
		} `json:"document"`
	} `json:"results"`
}

// Rerank returns an engine.Instruction that reranks documents using Cohere or Jina.
//
// At runtime:
//  1. Reads query from ctx.ByteSlots[cfg.QuerySlot]
//  2. Reads documents as JSON []string from ctx.ByteSlots[cfg.DocsSlot]
//  3. Builds provider-specific request JSON
//  4. POSTs to provider API with auth header
//  5. Parses response and sorts by relevance_score descending
//  6. Extracts document texts in reranked order
//  7. Writes JSON []string to ctx.ByteSlots[cfg.ResultSlot]
//  8. On HTTP error (4xx/5xx): sets ctx.ResponseStatus = 502, returns StopPlan
func Rerank(cfg RerankConfig) engine.Instruction {
	// Set provider defaults
	baseURL := cfg.BaseURL
	if baseURL == "" {
		switch cfg.Provider {
		case RerankCohere:
			baseURL = "https://api.cohere.com/v1/rerank"
		case RerankJina:
			baseURL = "https://api.jina.ai/v1/rerank"
		default:
			baseURL = "https://api.cohere.com/v1/rerank"
		}
	}

	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 10_000
	}

	return engine.Instruction{
		Name: fmt.Sprintf("rerank[%s]", cfg.Provider),
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Read query
			var query string
			if cfg.QuerySlot >= 0 && cfg.QuerySlot < len(ctx.ByteSlots) {
				query = string(ctx.ByteSlots[cfg.QuerySlot])
			}
			if query == "" {
				// Empty query — leave result as empty array
				if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
					emptyResult := []byte("[]")
					ctx.ByteSlots[cfg.ResultSlot] = ctx.Alloc(len(emptyResult))
					copy(ctx.ByteSlots[cfg.ResultSlot], emptyResult)
				}
				return state.PC + 1
			}

			// 2. Read and decode documents
			var docs []string
			if cfg.DocsSlot >= 0 && cfg.DocsSlot < len(ctx.ByteSlots) {
				docsJSON := ctx.ByteSlots[cfg.DocsSlot]
				if len(docsJSON) > 0 {
					if err := json.Unmarshal(docsJSON, &docs); err != nil {
						ctx.ResponseStatus = 400
						ctx.Failed = true
						ctx.ErrorCode = 400
						msg := "rerank: invalid docs JSON"
						ctx.ErrorMsg = ctx.Alloc(len(msg))
						copy(ctx.ErrorMsg, msg)
						return engine.StopPlan
					}
				}
			}

			// If no documents, return empty array
			if len(docs) == 0 {
				if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
					emptyResult := []byte("[]")
					ctx.ByteSlots[cfg.ResultSlot] = ctx.Alloc(len(emptyResult))
					copy(ctx.ByteSlots[cfg.ResultSlot], emptyResult)
				}
				return state.PC + 1
			}

			// 3. Build request based on provider
			var reqBody []byte
			var reqErr error

			switch cfg.Provider {
			case RerankCohere:
				cohereReq := cohereRerankRequest{
					Model:           cfg.Model,
					Query:           query,
					Documents:       docs,
					TopN:            cfg.TopN,
					ReturnDocuments: true,
				}
				reqBody, reqErr = json.Marshal(cohereReq)

			case RerankJina:
				jinaReq := jinaRerankRequest{
					Model:     cfg.Model,
					Query:     query,
					Documents: docs,
					TopN:      cfg.TopN,
				}
				reqBody, reqErr = json.Marshal(jinaReq)

			default:
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "rerank: unknown provider"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			if reqErr != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "rerank: request marshal failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 4. HTTP POST to provider API
			client := getRerankClient(cfg.Provider, baseURL, timeoutMs)

			reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
			httpReq, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, baseURL, bytes.NewReader(reqBody))
			if reqErr != nil {
				cancel()
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "rerank: request build failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			httpReq.Header.Set("Content-Type", "application/json")

			// Add auth header if API key is present
			if cfg.APIKey != "" {
				httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
			}

			resp, doErr := client.Do(httpReq)
			cancel()

			if doErr != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "rerank: upstream error"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			respBody, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()

			// Check for error status codes
			if resp.StatusCode == 429 || resp.StatusCode >= 400 {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "rerank: provider error"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			if readErr != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "rerank: response read failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 5. Parse response and extract reranked documents
			var rerankedDocs []string

			switch cfg.Provider {
			case RerankCohere:
				var cohereResp cohereRerankResponse
				if err := json.Unmarshal(respBody, &cohereResp); err != nil {
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "rerank: response parse failed"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
				// Results are already sorted by relevance (descending) by Cohere
				for _, result := range cohereResp.Results {
					rerankedDocs = append(rerankedDocs, result.Document.Text)
				}

			case RerankJina:
				var jinaResp jinaRerankResponse
				if err := json.Unmarshal(respBody, &jinaResp); err != nil {
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "rerank: response parse failed"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
				// Results are already sorted by relevance (descending) by Jina
				for _, result := range jinaResp.Results {
					rerankedDocs = append(rerankedDocs, result.Document.Text)
				}
			}

			// 6. Write reranked result as JSON array to slot
			if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
				resultJSON, err := json.Marshal(rerankedDocs)
				if err != nil {
					ctx.ResponseStatus = 500
					ctx.Failed = true
					ctx.ErrorCode = 500
					msg := "rerank: result marshal failed"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
				ctx.ByteSlots[cfg.ResultSlot] = ctx.Alloc(len(resultJSON))
				copy(ctx.ByteSlots[cfg.ResultSlot], resultJSON)
			}

			return state.PC + 1
		},
	}
}
