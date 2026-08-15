package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ── JSON-RPC 2.0 types ────────────────────────────────────────────────────────

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ── Tool definitions ──────────────────────────────────────────────────────────

// mcpToolDef is the MCP tools/list wire shape for a single tool.
type mcpToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// mcpToolMeta holds routing info used by proxyToolCall.
type mcpToolMeta struct {
	HTTPMethod  string            // GET, POST, DELETE
	Path        string            // e.g. "/ai/llm/models" — may contain "{param}" or "{param1}/{param2}" placeholders
	BodyParam   string            // argument key holding the JSON body, empty if none
	PathParam   string            // argument key replacing the "{param}" placeholder, empty if none (legacy single param)
	PathParams  map[string]string // map of {placeholder} name to argument key for multiple path params, empty if none
	BodyFields  []string          // list of argument keys to include in JSON body (auto-builds body), empty if none
	QuerySuffix string            // literal query string appended (e.g. "cursor=0&limit=50"), empty if none
}

var studioMCPTools = []mcpToolDef{
	// ── LLM models ──────────────────────────────────────────────────────────────
	{
		Name:        "register_llm_model",
		Description: "Register or update an LLM model configuration on the gateway.",
		InputSchema: bodySchema("JSON body of the LLM model definition"),
	},
	{
		Name:        "list_llm_models",
		Description: "List all registered LLM models on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "delete_llm_model",
		Description: "Delete an LLM model by its alias.",
		InputSchema: paramSchema("alias", "Alias of the LLM model to delete"),
	},

	// ── MCP servers ─────────────────────────────────────────────────────────────
	{
		Name:        "register_mcp_server",
		Description: "Register or update an external MCP server configuration on the gateway.",
		InputSchema: bodySchema("JSON body of the MCPServerConfig definition"),
	},
	{
		Name:        "list_mcp_servers",
		Description: "List all registered external MCP servers on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "delete_mcp_server",
		Description: "Delete an external MCP server by its alias.",
		InputSchema: paramSchema("alias", "Alias of the MCP server to delete"),
	},

	// ── Virtual MCP servers ──────────────────────────────────────────────────────
	{
		Name:        "create_virtual_server",
		Description: "Create or update a virtual MCP server definition on the gateway.",
		InputSchema: bodySchema("JSON body of the VirtualMCPServerDef definition"),
	},
	{
		Name:        "list_virtual_servers",
		Description: "List all virtual MCP server definitions on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "delete_virtual_server",
		Description: "Delete a virtual MCP server by name.",
		InputSchema: paramSchema("name", "Name of the virtual MCP server to delete"),
	},

	// ── API tools ────────────────────────────────────────────────────────────────
	{
		Name:        "register_api_tool",
		Description: "Register or update an API tool definition on the gateway.",
		InputSchema: bodySchema("JSON body of the APIToolDef definition"),
	},
	{
		Name:        "list_api_tools",
		Description: "List all registered API tool definitions on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "delete_api_tool",
		Description: "Delete an API tool by name.",
		InputSchema: paramSchema("name", "Name of the API tool to delete"),
	},

	// ── Gateway management ───────────────────────────────────────────────────────
	{
		Name:        "sync_flow",
		Description: "Sync a flow/API definition to the gateway (POST /sync).",
		InputSchema: bodySchema("JSON sync payload containing flows and APIs"),
	},
	{
		Name:        "list_gateway_apis",
		Description: "List all APIs currently registered on the gateway.",
		InputSchema: emptySchema(),
	},

	// ── Tenant management ────────────────────────────────────────────────────────
	{
		Name:        "create_tenant",
		Description: "Create or update a tenant on the gateway.",
		InputSchema: bodySchema("JSON body of the tenant definition"),
	},
	{
		Name:        "list_tenants",
		Description: "List all tenants (first 50) on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "delete_tenant",
		Description: "Delete a tenant by alias.",
		InputSchema: paramSchema("alias", "Alias of the tenant to delete"),
	},
	{
		Name:        "get_tenant",
		Description: "Get details of a specific tenant by alias.",
		InputSchema: paramSchema("alias", "Alias of the tenant to look up"),
	},
	{
		Name:        "get_rate_limit_config",
		Description: "Get a specific rate limit configuration by name.",
		InputSchema: paramSchema("name", "Name of the rate limit configuration"),
	},
	{
		Name:        "list_egress_profiles",
		Description: "List all egress upstream profiles configured on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "upsert_egress_profile",
		Description: "Create or update an egress upstream profile.",
		InputSchema: bodySchema("JSON body of the egress profile definition"),
	},

	// ── Rate limit configs v2 ─────────────────────────────────────────────────────
	{
		Name:        "list_rate_limit_configs_v2",
		Description: "List all rate limit v2 configurations on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "upsert_rate_limit_config_v2",
		Description: "Create or update a rate limit v2 configuration on the gateway.",
		InputSchema: bodySchema("JSON body of the RateLimitConfigV2 definition"),
	},
	{
		Name:        "delete_rate_limit_config_v2",
		Description: "Delete a rate limit v2 configuration by name.",
		InputSchema: paramSchema("name", "Name of the rate limit config to delete"),
	},

	// ── Tiers ─────────────────────────────────────────────────────────────────────
	{
		Name:        "list_tiers",
		Description: "List all tier definitions on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "upsert_tier",
		Description: "Create or update a tier definition on the gateway.",
		InputSchema: bodySchema("JSON body of the TierDef definition"),
	},
	{
		Name:        "delete_tier",
		Description: "Delete a tier by name.",
		InputSchema: paramSchema("name", "Name of the tier to delete"),
	},

	// ── Upstream services ─────────────────────────────────────────────────────────
	{
		Name:        "list_upstream_services",
		Description: "List all upstream service definitions on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "upsert_upstream_service",
		Description: "Create or update an upstream service definition on the gateway.",
		InputSchema: bodySchema("JSON body of the UpstreamServiceDef definition"),
	},
	{
		Name:        "delete_upstream_service",
		Description: "Delete an upstream service by name.",
		InputSchema: paramSchema("name", "Name of the upstream service to delete"),
	},

	// ── Concurrency ───────────────────────────────────────────────────────────────
	{
		Name:        "get_concurrency_config",
		Description: "Get the current concurrency configuration on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "set_concurrency_config",
		Description: "Set the concurrency configuration on the gateway.",
		InputSchema: bodySchema("JSON body of the concurrency configuration"),
	},

	// ── AI routes ─────────────────────────────────────────────────────────────────
	{
		Name:        "list_ai_routes",
		Description: "List all AI route configurations on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "set_ai_routes",
		Description: "Replace all AI route configurations on the gateway.",
		InputSchema: bodySchema("JSON array body of AI route definitions"),
	},

	// ── AI quotas ─────────────────────────────────────────────────────────────────
	{
		Name:        "list_ai_quotas",
		Description: "List all AI tenant quota configurations on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "upsert_ai_quota",
		Description: "Create or update an AI tenant quota configuration.",
		InputSchema: bodySchema("JSON body of the AI quota definition"),
	},
	{
		Name:        "delete_ai_quota",
		Description: "Delete an AI quota configuration by tenant ID.",
		InputSchema: paramSchema("tenant_id", "Tenant ID of the quota to delete"),
	},

	// ── Schedules ──────────────────────────────────────────────────────────────────
	{
		Name:        "list_schedules",
		Description: "List all schedules on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "create_schedule",
		Description: "Create or update a runtime schedule on the gateway.",
		InputSchema: bodySchema("JSON body of the schedule configuration"),
	},
	{
		Name:        "delete_schedule",
		Description: "Delete a schedule by name.",
		InputSchema: paramSchema("name", "Name of the schedule to delete"),
	},
	{
		Name:        "get_schedule_history",
		Description: "Get execution history for a schedule.",
		InputSchema: paramSchema("name", "Name of the schedule"),
	},

	// ── WebSocket ──────────────────────────────────────────────────────────────────
	{
		Name:        "list_ws_sessions",
		Description: "List all active WebSocket sessions on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "list_ws_upstreams",
		Description: "List all WebSocket upstream configurations on the gateway.",
		InputSchema: emptySchema(),
	},

	// ── Flow execution ─────────────────────────────────────────────────────────────
	{
		Name:        "run_flow",
		Description: "Trigger a named flow directly with optional tenant and constants.",
		InputSchema: bodySchema("JSON body with tenant_alias and constants"),
	},

	// ── Named queries ──────────────────────────────────────────────────────────
	{
		Name:        "list_named_queries",
		Description: "List all named SQL queries configured for data sources.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "upsert_named_query",
		Description: "Create or update a named SQL query.",
		InputSchema: bodySchema("Named query definition as JSON: {\"name\":\"...\",\"sql\":\"...\",\"batch_by\":\"$1\",\"batch_window\":\"500us\",\"batch_max\":100}"),
	},
	{
		Name:        "delete_named_query",
		Description: "Delete a named SQL query by name.",
		InputSchema: paramSchema("name", "Name of the query to delete"),
	},

	// ── Redis sources ──────────────────────────────────────────────────────────
	{
		Name:        "list_redis_sources",
		Description: "List all configured Redis source names.",
		InputSchema: emptySchema(),
	},

	// ── Migrations ─────────────────────────────────────────────────────────────
	{
		Name:        "list_migrations",
		Description: "List applied database migrations.",
		InputSchema: emptySchema(),
	},

	// ── Datastore and app release tools ──────────────────────────────────────
	{
		Name:        "list_datastores",
		Description: "List all configured datastores in the gateway",
		InputSchema: emptySchema(),
	},
	{
		Name:        "test_datastore",
		Description: "Test connectivity to a configured datastore by name",
		InputSchema: paramSchema("name", "datastore name"),
	},
	{
		Name:        "list_apps",
		Description: "List all apps registered in the gateway",
		InputSchema: emptySchema(),
	},
	{
		Name:        "generate_app_blueprint",
		Description: "Generate flow YAML templates for an app based on its type and configuration",
		InputSchema: multiParamSchema([]string{"app_name", "type", "tenant_mode"}, []string{"oauth_provider", "callback_path", "login_path", "logout_path"}),
	},
	{
		Name:        "list_app_releases",
		Description: "List all releases for a specific app",
		InputSchema: paramSchema("app_name", "app name"),
	},
	{
		Name:        "create_app_release",
		Description: "Create a new release version for an app",
		InputSchema: multiParamSchema([]string{"app_name", "version", "flow_names"}, []string{"channel", "notes"}),
	},
	{
		Name:        "promote_app_release",
		Description: "Promote a release version to active status in a channel",
		InputSchema: multiParamSchema([]string{"app_name", "version"}, []string{"channel"}),
	},
}

// toolMetaTable maps tool name → routing metadata.
var toolMetaTable = map[string]mcpToolMeta{
	"register_llm_model":          {HTTPMethod: http.MethodPost, Path: "/ai/llm/models", BodyParam: "body"},
	"list_llm_models":             {HTTPMethod: http.MethodGet, Path: "/ai/llm/models"},
	"delete_llm_model":            {HTTPMethod: http.MethodDelete, Path: "/ai/llm/models/{alias}", PathParam: "alias"},
	"register_mcp_server":         {HTTPMethod: http.MethodPost, Path: "/ai/mcp/servers", BodyParam: "body"},
	"list_mcp_servers":            {HTTPMethod: http.MethodGet, Path: "/ai/mcp/servers"},
	"delete_mcp_server":           {HTTPMethod: http.MethodDelete, Path: "/ai/mcp/servers/{alias}", PathParam: "alias"},
	"create_virtual_server":       {HTTPMethod: http.MethodPost, Path: "/ai/mcp/virtual", BodyParam: "body"},
	"list_virtual_servers":        {HTTPMethod: http.MethodGet, Path: "/ai/mcp/virtual"},
	"delete_virtual_server":       {HTTPMethod: http.MethodDelete, Path: "/ai/mcp/virtual/{name}", PathParam: "name"},
	"register_api_tool":           {HTTPMethod: http.MethodPost, Path: "/ai/tools/apis", BodyParam: "body"},
	"list_api_tools":              {HTTPMethod: http.MethodGet, Path: "/ai/tools/apis"},
	"delete_api_tool":             {HTTPMethod: http.MethodDelete, Path: "/ai/tools/apis/{name}", PathParam: "name"},
	"sync_flow":                   {HTTPMethod: http.MethodPost, Path: "/sync", BodyParam: "body"},
	"list_gateway_apis":           {HTTPMethod: http.MethodGet, Path: "/getAllApis"},
	"create_tenant":               {HTTPMethod: http.MethodPost, Path: "/tenants", BodyParam: "body"},
	"list_tenants":                {HTTPMethod: http.MethodGet, Path: "/tenants", QuerySuffix: "cursor=0&limit=50"},
	"delete_tenant":               {HTTPMethod: http.MethodDelete, Path: "/tenants/{alias}", PathParam: "alias"},
	"get_tenant":                  {HTTPMethod: http.MethodGet, Path: "/tenants/{alias}", PathParam: "alias"},
	"get_rate_limit_config":       {HTTPMethod: http.MethodGet, Path: "/rate-limit-configs/{name}", PathParam: "name"},
	"list_egress_profiles":        {HTTPMethod: http.MethodGet, Path: "/egress/profiles"},
	"upsert_egress_profile":       {HTTPMethod: http.MethodPost, Path: "/egress/profiles", BodyParam: "body"},
	"list_rate_limit_configs_v2":  {HTTPMethod: http.MethodGet, Path: "/rate-limit-configs-v2"},
	"upsert_rate_limit_config_v2": {HTTPMethod: http.MethodPost, Path: "/rate-limit-configs-v2", BodyParam: "body"},
	"delete_rate_limit_config_v2": {HTTPMethod: http.MethodDelete, Path: "/rate-limit-configs-v2/{name}", PathParam: "name"},
	"list_tiers":                  {HTTPMethod: http.MethodGet, Path: "/tiers"},
	"upsert_tier":                 {HTTPMethod: http.MethodPost, Path: "/tiers", BodyParam: "body"},
	"delete_tier":                 {HTTPMethod: http.MethodDelete, Path: "/tiers/{name}", PathParam: "name"},
	"list_upstream_services":      {HTTPMethod: http.MethodGet, Path: "/upstream-services"},
	"upsert_upstream_service":     {HTTPMethod: http.MethodPost, Path: "/upstream-services", BodyParam: "body"},
	"delete_upstream_service":     {HTTPMethod: http.MethodDelete, Path: "/upstream-services/{name}", PathParam: "name"},
	"get_concurrency_config":      {HTTPMethod: http.MethodGet, Path: "/admin/concurrency"},
	"set_concurrency_config":      {HTTPMethod: http.MethodPost, Path: "/admin/concurrency", BodyParam: "body"},
	"list_ai_routes":              {HTTPMethod: http.MethodGet, Path: "/ai/routes"},
	"set_ai_routes":               {HTTPMethod: http.MethodPut, Path: "/ai/routes", BodyParam: "body"},
	"list_ai_quotas":              {HTTPMethod: http.MethodGet, Path: "/ai/quotas"},
	"upsert_ai_quota":             {HTTPMethod: http.MethodPost, Path: "/ai/quotas", BodyParam: "body"},
	"delete_ai_quota":             {HTTPMethod: http.MethodDelete, Path: "/ai/quotas/{tenant_id}", PathParam: "tenant_id"},
	"list_schedules":              {HTTPMethod: http.MethodGet, Path: "/schedules"},
	"create_schedule":             {HTTPMethod: http.MethodPost, Path: "/schedules/runtime", BodyParam: "body"},
	"delete_schedule":             {HTTPMethod: http.MethodDelete, Path: "/schedules/runtime/{name}", PathParam: "name"},
	"get_schedule_history":        {HTTPMethod: http.MethodGet, Path: "/schedules/{name}/history", PathParam: "name"},
	"list_ws_sessions":            {HTTPMethod: http.MethodGet, Path: "/ws/sessions"},
	"list_ws_upstreams":           {HTTPMethod: http.MethodGet, Path: "/ws/upstreams"},
	"run_flow":                    {HTTPMethod: http.MethodPost, Path: "/flows/{name}/run", PathParam: "name", BodyParam: "body"},
	"list_named_queries":          {HTTPMethod: http.MethodGet, Path: "/named-queries"},
	"upsert_named_query":          {HTTPMethod: http.MethodPost, Path: "/named-queries", BodyParam: "body"},
	"delete_named_query":          {HTTPMethod: http.MethodDelete, Path: "/named-queries/{name}", PathParam: "name"},
	"list_redis_sources":          {HTTPMethod: http.MethodGet, Path: "/redis-sources"},
	"list_migrations":             {HTTPMethod: http.MethodGet, Path: "/migrations"},
	"list_datastores":             {HTTPMethod: http.MethodGet, Path: "/config/datastores"},
	"test_datastore":              {HTTPMethod: http.MethodPost, Path: "/config/datastores/{name}/test", PathParam: "name"},
	"list_apps":                   {HTTPMethod: http.MethodGet, Path: "/apps"},
	"generate_app_blueprint":      {HTTPMethod: http.MethodPost, Path: "/apps/{app_name}/blueprint", PathParam: "app_name", BodyFields: []string{"type", "tenant_mode", "oauth_provider", "callback_path", "login_path", "logout_path"}},
	"list_app_releases":           {HTTPMethod: http.MethodGet, Path: "/apps/{app_name}/releases", PathParam: "app_name"},
	"create_app_release":          {HTTPMethod: http.MethodPost, Path: "/apps/{app_name}/releases", PathParam: "app_name", BodyFields: []string{"version", "flow_names", "channel", "notes"}},
	"promote_app_release":         {HTTPMethod: http.MethodPost, Path: "/apps/{app_name}/releases/{version}/promote", PathParams: map[string]string{"app_name": "app_name", "version": "version"}, BodyFields: []string{"channel"}},
}

// ── Schema helpers ────────────────────────────────────────────────────────────

func bodySchema(desc string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"body": map[string]any{
				"type":        "string",
				"description": desc,
			},
		},
		"required": []string{"body"},
	}
}

func paramSchema(name, desc string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			name: map[string]any{
				"type":        "string",
				"description": desc,
			},
		},
		"required": []string{name},
	}
}

func emptySchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func multiParamSchema(required []string, optional []string) map[string]any {
	props := map[string]any{}
	for _, name := range required {
		props[name] = map[string]any{
			"type":        "string",
			"description": name,
		}
	}
	for _, name := range optional {
		props[name] = map[string]any{
			"type":        "string",
			"description": name,
		}
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

// ── JSON-RPC helpers ──────────────────────────────────────────────────────────

func mcpWriteResult(w http.ResponseWriter, id json.RawMessage, result any) {
	resp := mcpResponse{JSONRPC: "2.0", ID: id, Result: result}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func mcpWriteError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	resp := mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &mcpRPCError{Code: code, Message: msg},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func mcpWriteToolResult(w http.ResponseWriter, id json.RawMessage, text string, isError bool) {
	result := map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"isError": isError,
	}
	mcpWriteResult(w, id, result)
}

// ── MCPHandler ────────────────────────────────────────────────────────────────

// MCPHandler handles POST /mcp — a JSON-RPC 2.0 MCP endpoint that exposes
// gateway management operations as MCP tools.
func (s *Server) MCPHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	var req mcpRequest
	if err := json.Unmarshal(body, &req); err != nil {
		mcpWriteError(w, nil, -32700, "parse error: "+err.Error())
		return
	}

	// Auth gate — skip for initialize (MCP protocol requirement).
	if req.Method != "initialize" && s.config.MCPSecret != "" {
		token := ""
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
		if token == "" {
			token = r.Header.Get("X-Studio-Key")
		}
		if token != s.config.MCPSecret {
			mcpWriteError(w, req.ID, -32001, "unauthorized")
			return
		}
	}

	switch req.Method {
	case "initialize":
		s.mcpHandleInitialize(w, req)
	case "tools/list":
		s.mcpHandleToolsList(w, req)
	case "tools/call":
		s.mcpHandleToolsCall(w, r.Context(), req)
	case "notifications/initialized":
		mcpWriteResult(w, req.ID, map[string]any{})
	default:
		mcpWriteError(w, req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *Server) mcpHandleInitialize(w http.ResponseWriter, req mcpRequest) {
	result := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "rah-studio",
			"version": "1.0",
		},
	}
	mcpWriteResult(w, req.ID, result)
}

func (s *Server) mcpHandleToolsList(w http.ResponseWriter, req mcpRequest) {
	result := map[string]any{
		"tools": studioMCPTools,
	}
	mcpWriteResult(w, req.ID, result)
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Target    string          `json:"target,omitempty"` // optional: named gateway target
}

func (s *Server) mcpHandleToolsCall(w http.ResponseWriter, ctx context.Context, req mcpRequest) {
	var params mcpToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		mcpWriteError(w, req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	text, isError := s.proxyToolCall(ctx, params.Name, params.Arguments, params.Target)
	mcpWriteToolResult(w, req.ID, text, isError)
}

// proxyToolCall resolves the tool name to a management server request, executes
// it, and returns the response body (or error message) as a string.
func (s *Server) proxyToolCall(ctx context.Context, toolName string, arguments json.RawMessage, targetName string) (text string, isError bool) {
	meta, ok := toolMetaTable[toolName]
	if !ok {
		return fmt.Sprintf("unknown tool: %s", toolName), true
	}

	// Resolve management base URL.
	base := ""
	if targetName != "" {
		// Named target requested — find it.
		for _, t := range s.targets {
			if t.Name == targetName && len(t.URLs) > 0 {
				base = t.URLs[0]
				break
			}
		}
		if base == "" {
			return fmt.Sprintf("unknown target: %s", targetName), true
		}
	} else if s.managementBaseURL != nil {
		base = s.managementBaseURL.String()
	} else if len(s.targets) > 0 && len(s.targets[0].URLs) > 0 {
		base = s.targets[0].URLs[0]
	}
	if base == "" {
		return "no management endpoint configured", true
	}

	// Decode arguments map (may be empty for no-param tools).
	var args map[string]string
	if len(arguments) > 0 && string(arguments) != "null" && string(arguments) != "{}" {
		if err := json.Unmarshal(arguments, &args); err != nil {
			return "invalid arguments: " + err.Error(), true
		}
	}

	// Build the path — substitute path param placeholders if needed.
	path := meta.Path
	if meta.PathParam != "" {
		paramVal, ok := args[meta.PathParam]
		if !ok || strings.TrimSpace(paramVal) == "" {
			return fmt.Sprintf("missing required argument: %s", meta.PathParam), true
		}
		path = strings.ReplaceAll(path, "{"+meta.PathParam+"}", paramVal)
	}
	// Support multiple path params via PathParams map
	if len(meta.PathParams) > 0 {
		for placeholder, argKey := range meta.PathParams {
			paramVal, ok := args[argKey]
			if !ok || strings.TrimSpace(paramVal) == "" {
				return fmt.Sprintf("missing required argument: %s", argKey), true
			}
			path = strings.ReplaceAll(path, "{"+placeholder+"}", paramVal)
		}
	}

	// Build request body if needed.
	var bodyReader io.Reader
	if meta.BodyParam != "" {
		bodyStr, ok := args[meta.BodyParam]
		if !ok || strings.TrimSpace(bodyStr) == "" {
			return fmt.Sprintf("missing required argument: %s", meta.BodyParam), true
		}
		bodyReader = bytes.NewBufferString(bodyStr)
	}
	// Support auto-building JSON body from specified fields
	if len(meta.BodyFields) > 0 {
		bodyObj := make(map[string]any)
		for _, field := range meta.BodyFields {
			if val, ok := args[field]; ok && strings.TrimSpace(val) != "" {
				// Try to parse as JSON, otherwise treat as string
				var jsonVal any
				if err := json.Unmarshal([]byte(val), &jsonVal); err != nil {
					jsonVal = val
				}
				bodyObj[field] = jsonVal
			}
		}
		bodyBytes, err := json.Marshal(bodyObj)
		if err != nil {
			return "failed to build request body: " + err.Error(), true
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// Build target URL.
	targetURL, err := buildTargetURL(base, path, meta.QuerySuffix)
	if err != nil {
		return "invalid management endpoint: " + err.Error(), true
	}

	// Build and send the HTTP request.
	httpReq, err := http.NewRequestWithContext(ctx, meta.HTTPMethod, targetURL, bodyReader)
	if err != nil {
		return "failed to build request: " + err.Error(), true
	}
	if meta.BodyParam != "" || len(meta.BodyFields) > 0 {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return "management API unreachable: " + err.Error(), true
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "failed to read management response: " + err.Error(), true
	}

	responseText := strings.TrimSpace(string(respBody))
	if responseText == "" {
		responseText = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}

	// Treat 4xx/5xx as tool errors so the LLM knows something went wrong.
	if resp.StatusCode >= 400 {
		return fmt.Sprintf("HTTP %d: %s", resp.StatusCode, responseText), true
	}

	return responseText, false
}
