package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ─── Types ────────────────────────────────────────────────────────────────────

type ChatMessage struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"`
}

type ChatAction struct {
	Op     string `json:"op"`               // upsert_flow, add_endpoint, upsert_api, delete_flow, publish, upsert_rate_limit, upsert_tenant
	Name   string `json:"name,omitempty"`
	DSL    string `json:"dsl,omitempty"`
	API    string `json:"api,omitempty"`
	Path   string `json:"path,omitempty"`
	Method string `json:"method,omitempty"`
	Flow   string `json:"flow,omitempty"`
	Target string `json:"target,omitempty"`
}

type ChatRequest struct {
	Message        string        `json:"message"`
	Model          string        `json:"model"`         // alias of a registered gateway LLM model
	IncludeAPIs    bool          `json:"include_apis"`
	IncludeFlows   bool          `json:"include_flows"`
	SessionHistory []ChatMessage `json:"session_history"`
}

type ChatResponse struct {
	ConfirmMessage string       `json:"confirm_message"`
	Actions        []ChatAction `json:"actions"`
	Questions      []string     `json:"questions,omitempty"`
	DSLPreview     string       `json:"dsl_preview,omitempty"`
}

// ─── Handler 1: POST /api/ai/chat ────────────────────────────────────────────

func (s *Server) aiChatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	// Model alias is required — it must refer to a model registered in the gateway.
	if req.Model == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "no model selected — register a model in AI → Models and select it"})
		return
	}

	// Management base URL — for both LLM calls and context fetching (APIs, flows).
	managementBase := ""
	if s.managementBaseURL != nil {
		managementBase = s.managementBaseURL.String()
	} else if len(s.targets) > 0 && len(s.targets[0].URLs) > 0 {
		managementBase = s.targets[0].URLs[0]
	}

	if managementBase == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "management server not configured"})
		return
	}

	// Fetch context from management API (best-effort, 3s timeout each).
	var apiList, flowList string
	if req.IncludeAPIs {
		apiList = s.fetchContextString(managementBase+"/apis", 2000)
	}
	if req.IncludeFlows {
		flowList = s.fetchContextString(managementBase+"/flows", 2000)
	}

	// Load pinned project context.
	projectContext := ""
	if s.chatStore != nil {
		rec, _ := s.chatStore.GetProjectContext(r.Context())
		projectContext = rec.Content
	}

	// Build system prompt.
	systemPrompt := buildSystemPrompt(projectContext, apiList, flowList)

	// Build messages: last 8 history + current user message.
	history := req.SessionHistory
	if len(history) > 8 {
		history = history[len(history)-8:]
	}
	messages := make([]ChatMessage, 0, len(history)+1)
	messages = append(messages, history...)
	messages = append(messages, ChatMessage{Role: "user", Content: req.Message})

	// Call the LLM via the gateway's management API (POST /ai/llm/chat).
	// The management server routes the call through the registered model's adapter
	// — same LiteLLM-style alias resolution used throughout the AI management API.
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	text, err := s.callGatewayChat(ctx, managementBase, req.Model, systemPrompt, messages)
	if err != nil {
		http.Error(w, "upstream AI error: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Parse response, with fix-up loop.
	resp, parseErr := parseAIResponse(text)
	if parseErr != nil {
		// One retry with explicit JSON instruction.
		retryMessages := append(messages,
			ChatMessage{Role: "assistant", Content: text},
			ChatMessage{Role: "user", Content: "Your response was not valid JSON. Please respond ONLY with the JSON object, no markdown, no explanation."},
		)
		retryCtx, retryCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer retryCancel()
		retryText, retryErr := s.callGatewayChat(retryCtx, managementBase, req.Model, systemPrompt, retryMessages)
		if retryErr != nil {
			resp = &ChatResponse{
				ConfirmMessage: "I had trouble formatting my response. Please try again.",
				Questions:      []string{"Could you rephrase your request?"},
			}
		} else {
			resp, parseErr = parseAIResponse(retryText)
			if parseErr != nil {
				resp = &ChatResponse{
					ConfirmMessage: "I had trouble formatting my response. Please try again.",
					Questions:      []string{"Could you rephrase your request?"},
				}
			}
		}
	}

	// Set DSLPreview from first action that has DSL.
	for _, a := range resp.Actions {
		if a.DSL != "" {
			resp.DSLPreview = a.DSL
			break
		}
	}

	// Append audit record asynchronously.
	if s.chatStore != nil {
		go func() {
			artifacts := make([]string, 0)
			for _, a := range resp.Actions {
				if a.Name != "" {
					artifacts = append(artifacts, a.Name)
				}
			}
			gatewayName := ""
			if len(s.targets) > 0 {
				gatewayName = s.targets[0].Name
			}
			_ = s.chatStore.AppendAudit(context.Background(), ChatAuditRecord{
				ID:          fmt.Sprintf("%d", time.Now().UnixNano()),
				CreatedAt:   time.Now(),
				Summary:     resp.ConfirmMessage,
				Artifacts:   artifacts,
				GatewayName: gatewayName,
			})
		}()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// callGatewayChat calls POST /ai/llm/chat on the management server.
// The management server resolves the model alias to the right adapter and provider,
// following the same LiteLLM-style pattern used by the rest of the AI management API.
func (s *Server) callGatewayChat(ctx context.Context, managementBase, modelAlias, systemPrompt string, messages []ChatMessage) (string, error) {
	payload := map[string]any{
		"model":    modelAlias,
		"system":   systemPrompt,
		"messages": messages,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, managementBase+"/ai/llm/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.gatewayBasicCred != "" {
		req.Header.Set("Authorization", "Basic "+s.gatewayBasicCred)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Management server wraps all responses in {ok, data, error}.
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Data  struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return "", fmt.Errorf("unexpected response from management server: %s", string(respBody))
	}
	if !envelope.OK {
		return "", fmt.Errorf("%s", envelope.Error)
	}
	return envelope.Data.Content, nil
}

// fetchContextString makes a GET request to url, reads the body, and trims it to maxChars.
func (s *Server) fetchContextString(url string, maxChars int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	if s.gatewayBasicCred != "" {
		req.Header.Set("Authorization", "Basic "+s.gatewayBasicCred)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxChars*4)))
	if err != nil {
		return ""
	}
	text := string(body)
	if len(text) > maxChars {
		text = text[:maxChars]
	}
	return text
}

// parseAIResponse tries to unmarshal text into ChatResponse, stripping markdown fences if needed.
func parseAIResponse(text string) (*ChatResponse, error) {
	var resp ChatResponse
	if err := json.Unmarshal([]byte(text), &resp); err == nil {
		return &resp, nil
	}
	// Strip markdown fences: find first '{' and last '}'.
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		trimmed := text[start : end+1]
		if err := json.Unmarshal([]byte(trimmed), &resp); err == nil {
			return &resp, nil
		}
	}
	return nil, fmt.Errorf("could not parse AI response as JSON")
}

// buildSystemPrompt assembles the system prompt from context pieces.
func buildSystemPrompt(projectContext, apiList, flowList string) string {
	var sb strings.Builder
	sb.WriteString(`You are an API gateway configuration assistant for RAH gateway.
Help operators configure flows, APIs, endpoints, and policies.

## Operations (use in the "op" field)
- upsert_flow: create or update a flow (requires: name, dsl)
- add_endpoint: add endpoint to an API (requires: api, path, method, flow)
- upsert_api: create or update an API definition (requires: name)
- delete_flow: delete a flow (requires: name)
- publish: publish to a gateway target (requires: target)
- upsert_rate_limit: create or update a rate limit config (requires: name)
- upsert_tenant: create or update a tenant (requires: name)

## RAH Flow DSL (JSON)
Flows are JSON arrays of instruction objects. Each instruction has an "action" field plus action-specific fields.
The "dsl" field in upsert_flow must be a JSON array of instruction objects — NOT YAML.

### Core instruction actions

**Binding (read from request):**
- {"action":"bind_header","key":"X-Tenant-ID","as":"tenant_alias"}
- {"action":"bind_query_param","key":"client_id","as":"cache_key"}
- {"action":"bind_body","key":"field","as":"var"}
- {"action":"bind_request_url","as":"req_path"}

**Control flow:**
- {"action":"if","condition":"var != null","then":"flow-name-hit","else":"flow-name-miss"}
- {"action":"return","status":200,"body":"{\"ok\":true}"}
- {"action":"return","status":200,"body_var":"my_var"}

**Cache:**
- {"action":"cache_get","key_identifier":"cache_key","as":"cached_val"}
- {"action":"cache_put","key_identifier":"cache_key","source":"my_var","ttl":3600}

**Registry / tenant:**
- {"action":"registry_lookup","key_identifier":"tenant_alias"}
- {"action":"load_service_url","key":"primary","as":"upstream_url"}
- {"action":"load_identifier","key":"some_key","as":"some_var"}

**HTTP call:**
- {"action":"http_call","url_var":"upstream_url","method":"GET","forward_incoming_headers":true,"forward_response_headers":true,"response_body_var":"resp_body","timeout":5000}

**Header mutation:**
- {"action":"set_request_header","key":"X-Internal-ID","value_var":"req_id"}
- {"action":"set_request_header","key":"X-Gateway","value":"rah"}
- {"action":"remove_request_header","key":"X-Tenant-ID"}

**ID generation / templating:**
- {"action":"store_internal_tx_id","as":"new_id"}
- {"action":"render_template","value":"prefix:${alias}","as":"cache_key"}

**Echo / timestamp:**
- {"action":"echo_request"}
- {"action":"current_timestamp","as":"ts","format":"unix_ms"}

**Rate limiting:**
- {"action":"check_upstream_rate_limit"}

**JSON extraction:**
- {"action":"json_extract_emit","variable":"json_var","params":[{"path":"field","key_prefix":"","op_type":"put","target":"cache","value_slot":"","ttl":"300","async":"false"}]}

### Sync payload format
A sync payload registers flows AND the API endpoint that routes to the entry flow:
{
  "sync_uuid": "unique-id",
  "flows": [
    {"name": "my-flow", "action": "upsert", "instructions": [...]}
  ],
  "apis": [{"name": "my-api", "path": "/path", "method": "GET", "flow_name": "my-flow"}]
}

## Reference examples

### 1. Static response (baseline)
Flow instructions: [{"action":"return","status":200,"body":"{\"ok\":true}"}]

### 2. Echo request (debug)
Flow instructions: [{"action":"echo_request"}]

### 3. Current timestamp
Flow instructions: [{"action":"current_timestamp","as":"ts","format":"unix_ms"},{"action":"return","status":200,"body_var":"ts"}]

### 4. Cached client identity (cache read/write + conditional branch)
Entry flow "id-flow" instructions:
[{"action":"bind_query_param","key":"client_id","as":"cache_key"},{"action":"cache_get","key_identifier":"cache_key","as":"cached_id"},{"action":"if","condition":"cached_id != null","then":"id-hit","else":"id-miss"}]
Miss flow "id-miss" instructions:
[{"action":"store_internal_tx_id","as":"new_id"},{"action":"cache_put","key_identifier":"cache_key","source":"new_id","ttl":3600},{"action":"return","status":200,"body_var":"new_id"}]
Hit flow "id-hit" instructions:
[{"action":"return","status":200,"body_var":"cached_id"}]

### 5. Per-tenant upstream proxy with response cache
Entry flow instructions:
[{"action":"bind_header","key":"X-Tenant-ID","as":"tenant_alias"},{"action":"registry_lookup","key_identifier":"tenant_alias"},{"action":"load_service_url","key":"primary","as":"upstream_url"},{"action":"bind_request_url","as":"req_path"},{"action":"cache_get","key_identifier":"req_path","as":"cached_resp"},{"action":"if","condition":"cached_resp != null","then":"proxy-hit","else":"proxy-miss"}]
Miss flow instructions:
[{"action":"http_call","url_var":"upstream_url","method":"GET","forward_incoming_headers":true,"forward_response_headers":true,"response_body_var":"resp_body","timeout":5000},{"action":"cache_put","key_identifier":"req_path","source":"resp_body","ttl":60},{"action":"return","status":200,"body_var":"resp_body"}]
Hit flow instructions:
[{"action":"return","status":200,"body_var":"cached_resp"}]

### 6. Rate-limited proxy
Flow instructions:
[{"action":"bind_header","key":"X-Tenant-ID","as":"tenant_alias"},{"action":"registry_lookup","key_identifier":"tenant_alias"},{"action":"check_upstream_rate_limit"},{"action":"load_service_url","key":"primary","as":"upstream_url"},{"action":"http_call","url_var":"upstream_url","method":"GET","forward_incoming_headers":true,"response_body_var":"resp_body","timeout":5000},{"action":"return","status":200,"body_var":"resp_body"}]

### 7. Header enrichment proxy
Flow instructions:
[{"action":"bind_header","key":"X-Tenant-ID","as":"tenant_alias"},{"action":"registry_lookup","key_identifier":"tenant_alias"},{"action":"store_internal_tx_id","as":"req_id"},{"action":"load_service_url","key":"primary","as":"upstream_url"},{"action":"set_request_header","key":"X-Internal-Request-ID","value_var":"req_id"},{"action":"set_request_header","key":"X-Gateway","value":"rah"},{"action":"remove_request_header","key":"X-Tenant-ID"},{"action":"http_call","url_var":"upstream_url","method":"GET","forward_incoming_headers":true,"forward_response_headers":true,"response_body_var":"resp_body","timeout":5000},{"action":"return","status":200,"body_var":"resp_body"}]

### 8. Lazy tenant loading (on-demand config from external tenant service)
Entry flow "lazy-tenant-flow" instructions:
[{"action":"bind_header","key":"X-Tenant-ID","as":"alias"},{"action":"render_template","value":"upstream_url:${alias}","as":"url_cache_key"},{"action":"render_template","value":"api_key:${alias}","as":"key_cache_key"},{"action":"cache_get","key_identifier":"url_cache_key","as":"upstream_url"},{"action":"if","condition":"upstream_url != null","then":"lazy-call-upstream","else":"lazy-fetch-tenant"}]
Fetch flow "lazy-fetch-tenant" instructions:
[{"action":"render_template","value":"http://tenant-service/tenants/${alias}","as":"tenant_svc_url"},{"action":"http_call","url_var":"tenant_svc_url","method":"GET","response_body_var":"tenant_json","timeout":5000},{"action":"json_extract_emit","variable":"tenant_json","params":[{"path":"upstream_url","key_prefix":"","op_type":"put","target":"cache","value_slot":"","ttl":"300","async":"false"},{"path":"api_key","key_prefix":"","op_type":"put","target":"cache","value_slot":"","ttl":"300","async":"false"}]},{"action":"cache_get","key_identifier":"url_cache_key","as":"upstream_url"},{"action":"cache_get","key_identifier":"key_cache_key","as":"api_key"},{"action":"set_request_header","key":"X-Api-Key","source":"api_key"},{"action":"http_call","url_var":"upstream_url","method":"GET","forward_incoming_headers":true,"forward_response_headers":true,"response_body_var":"resp_body","timeout":5000},{"action":"return","status":200,"body_var":"resp_body"}]
Cached-hit flow "lazy-call-upstream" instructions:
[{"action":"cache_get","key_identifier":"key_cache_key","as":"api_key"},{"action":"set_request_header","key":"X-Api-Key","source":"api_key"},{"action":"http_call","url_var":"upstream_url","method":"GET","forward_incoming_headers":true,"forward_response_headers":true,"response_body_var":"resp_body","timeout":5000},{"action":"return","status":200,"body_var":"resp_body"}]

## Key design rules
- Flows that branch (via "if") must specify the FULL flow name in "then"/"else" — these are separate named flows.
- "dsl" is always a JSON array string — never YAML, never an object.
- All cache operations are per-tenant isolated automatically.
- Use "body_var" to return a slot variable, "body" for a literal JSON string.
- "registry_lookup" must precede "load_service_url" or "load_identifier".
- "check_upstream_rate_limit" must follow "registry_lookup".
`)
	if projectContext != "" {
		sb.WriteString("\nProject constraints: ")
		sb.WriteString(projectContext)
		sb.WriteString("\n")
	}
	if apiList != "" {
		sb.WriteString("\nCurrent APIs: ")
		sb.WriteString(apiList)
		sb.WriteString("\n")
	}
	if flowList != "" {
		sb.WriteString("\nCurrent flows: ")
		sb.WriteString(flowList)
		sb.WriteString("\n")
	}
	sb.WriteString(`
Respond ONLY with valid JSON (no markdown, no explanation outside JSON):
{"confirm_message":"one sentence summary","actions":[...],"questions":["if anything unclear"]}`)
	return sb.String()
}

// ─── Handler 2: GET/PUT /api/ai/project-context ───────────────────────────────

func (s *Server) aiProjectContextHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projCtx, _ := s.chatStore.GetProjectContext(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(projCtx)
	case http.MethodPut:
		var rec ProjectContextRecord
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		rec.ID = "project_context"
		rec.UpdatedAt = time.Now()
		if err := s.chatStore.PutProjectContext(r.Context(), rec); err != nil {
			http.Error(w, "store error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Handler 3: GET /api/ai/chat-history ─────────────────────────────────────

func (s *Server) aiChatHistoryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	audits, _ := s.chatStore.ListAudits(r.Context())
	if len(audits) > 20 {
		audits = audits[len(audits)-20:]
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(audits)
}
