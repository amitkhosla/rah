package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// BlueprintRequest is the input to the app blueprint generator.
type BlueprintRequest struct {
	Type          string `json:"type"`                     // "web" | "api-service" | "event-processor" | "webhook"
	TenantMode    string `json:"tenant_mode"`              // "tenant_aware" | "tenant_agnostic"
	OAuthProvider string `json:"oauth_provider,omitempty"` // e.g. "google", "github"
	CallbackPath  string `json:"callback_path,omitempty"`  // e.g. "/callback"
	LoginPath     string `json:"login_path,omitempty"`     // e.g. "/login"
	LogoutPath    string `json:"logout_path,omitempty"`    // e.g. "/logout"
}

// FlowBlueprint represents a single generated flow template.
type FlowBlueprint struct {
	Name string `json:"name"`
	YAML string `json:"yaml"`
}

// BlueprintResponse is the output of the blueprint generator.
type BlueprintResponse struct {
	AppName string          `json:"app_name"`
	Flows   []FlowBlueprint `json:"flows"`
}

// BlueprintHandler generates flow templates for a given app type.
// Route: POST /apps/{name}/blueprint
func (s *ManagementServer) BlueprintHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract app name from URL path: /apps/{name}/blueprint
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/apps/"), "/")
	if len(pathParts) < 1 || pathParts[0] == "" {
		http.Error(w, "app name required", http.StatusBadRequest)
		return
	}
	appName := pathParts[0]

	var req BlueprintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Type == "" {
		http.Error(w, "type field required", http.StatusBadRequest)
		return
	}

	// Generate flows based on app type
	var flows []FlowBlueprint
	switch req.Type {
	case "web":
		flows = generateWebFlows(appName, req)
	case "api-service":
		flows = generateAPIServiceFlows(appName)
	case "event-processor":
		flows = generateEventProcessorFlows(appName)
	case "webhook":
		flows = generateWebhookFlows(appName)
	default:
		http.Error(w, fmt.Sprintf("unsupported app type: %s", req.Type), http.StatusBadRequest)
		return
	}

	resp := BlueprintResponse{
		AppName: appName,
		Flows:   flows,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// generateWebFlows produces login, callback, and logout flows for OAuth web apps.
func generateWebFlows(appName string, req BlueprintRequest) []FlowBlueprint {
	callbackPath := req.CallbackPath
	if callbackPath == "" {
		callbackPath = "/callback"
	}

	var flows []FlowBlueprint

	// Login flow: build OAuth2 authorize URL and redirect
	loginYAML := fmt.Sprintf(`steps:
  - name: build_oauth_url
    kind: set_slot
    slot: oauth_url
    value: |
      https://accounts.google.com/o/oauth2/v2/auth?
        client_id=YOUR_CLIENT_ID&
        redirect_uri=https://yourapp.example.com%s&
        scope=openid%%20email%%20profile&
        state=STATE_VALUE&
        response_type=code
  - name: redirect_to_oauth
    kind: http_redirect
    location_slot: oauth_url
    status_code: 302
`, callbackPath)

	flows = append(flows, FlowBlueprint{
		Name: fmt.Sprintf("%s-login", appName),
		YAML: loginYAML,
	})

	// Callback flow: exchange code for tokens and set session cookie
	callbackYAML := fmt.Sprintf(`steps:
  - name: extract_code
    kind: extract_query_param
    param_name: code
    target_slot: auth_code
  - name: exchange_code_for_tokens
    kind: oauth2token
    provider: %s
    code_slot: auth_code
    redirect_uri: https://yourapp.example.com%s
    client_id_slot: client_id
    client_secret_slot: client_secret
    target_slot: tokens
  - name: set_session_cookie
    kind: set_cookie
    name: session_token
    value_slot: tokens.access_token
    max_age_sec: 3600
    http_only: true
    secure: true
    same_site: Lax
  - name: redirect_to_app
    kind: http_redirect
    location: /
    status_code: 302
`, req.OAuthProvider, callbackPath)

	flows = append(flows, FlowBlueprint{
		Name: fmt.Sprintf("%s-callback", appName),
		YAML: callbackYAML,
	})

	// Logout flow: clear session cookie and redirect
	logoutYAML := `steps:
  - name: clear_session_cookie
    kind: set_cookie
    name: session_token
    value: ""
    max_age_sec: -1
    http_only: true
    secure: true
  - name: redirect_to_home
    kind: http_redirect
    location: /
    status_code: 302
`

	flows = append(flows, FlowBlueprint{
		Name: fmt.Sprintf("%s-logout", appName),
		YAML: logoutYAML,
	})

	return flows
}

// generateAPIServiceFlows produces a basic auth validation flow.
func generateAPIServiceFlows(appName string) []FlowBlueprint {
	authYAML := `steps:
  - name: validate_api_key
    kind: comment
    comment: "Validate incoming X-API-Key header via validate_apikey step"
  - name: validate_oauth_token
    kind: comment
    comment: "OR validate Bearer token via validate_token step with your OAuth provider config"
  - name: set_user_context
    kind: comment
    comment: "Extract user/app identity and store in slots for downstream use"
`

	return []FlowBlueprint{
		{
			Name: fmt.Sprintf("%s-auth", appName),
			YAML: authYAML,
		},
	}
}

// generateEventProcessorFlows produces handler and DLQ flows.
func generateEventProcessorFlows(appName string) []FlowBlueprint {
	handlerYAML := `steps:
  - name: extract_event
    kind: comment
    comment: "Event payload is available in request body"
  - name: process_event
    kind: comment
    comment: "Implement your event processing logic here"
  - name: store_result
    kind: comment
    comment: "Optionally store result or emit downstream events"
`

	dlqYAML := `steps:
  - name: log_failure
    kind: comment
    comment: "Log the failed event and reason"
  - name: store_dlq
    kind: comment
    comment: "Store in dead-letter queue for manual review"
  - name: alert
    kind: comment
    comment: "Optionally alert operators"
`

	return []FlowBlueprint{
		{
			Name: fmt.Sprintf("%s-handler", appName),
			YAML: handlerYAML,
		},
		{
			Name: fmt.Sprintf("%s-dlq", appName),
			YAML: dlqYAML,
		},
	}
}

// generateWebhookFlows produces HMAC verification and processing flows.
func generateWebhookFlows(appName string) []FlowBlueprint {
	verifyYAML := `steps:
  - name: extract_signature
    kind: extract_header
    header_name: X-Webhook-Signature
    target_slot: signature
  - name: verify_hmac
    kind: comment
    comment: "Verify signature_slot against request body using your shared secret"
  - name: set_verification_result
    kind: set_slot
    slot: verified
    value: "true"
`

	processYAML := `steps:
  - name: parse_webhook_body
    kind: comment
    comment: "Parse incoming webhook payload"
  - name: validate_event_type
    kind: comment
    comment: "Check event_type field and route accordingly"
  - name: process_payload
    kind: comment
    comment: "Implement your business logic"
  - name: return_success
    kind: http_response
    status_code: 200
    body: |
      {"status": "received"}
`

	return []FlowBlueprint{
		{
			Name: fmt.Sprintf("%s-verify", appName),
			YAML: verifyYAML,
		},
		{
			Name: fmt.Sprintf("%s-process", appName),
			YAML: processYAML,
		},
	}
}

// AppHandler dispatches app CRUD operations.
// Covers both legacy /apps paths and new blueprint routes.
func (s *ManagementServer) AppHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.Contains(path, "/blueprint") {
		s.BlueprintHandler(w, r)
		return
	}
	// Add more app routes here as needed (releases, etc.)
	http.Error(w, "not found", http.StatusNotFound)
}
