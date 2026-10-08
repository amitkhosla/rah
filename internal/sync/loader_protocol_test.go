package sync

import (
	"strings"
	"testing"

	"github.com/amitkhosla/rah/internal/control"
	"github.com/amitkhosla/rah/internal/mcpreg"
)

// ── Functional Tests ─────────────────────────────────────────────────────────

// TestProcessAppProtocols_MCPAutoDerivation verifies that flows with
// Expose.AsMCPTool=true and matching ApiUpdate entries generate VirtualMCPServerDef
// with correct APIToolDef (name, path, method, description).
func TestProcessAppProtocols_MCPAutoDerivation(t *testing.T) {
	result := minimalBundle()

	// Clear minimal flows/APIs and set up test data
	result.Bundle.Flows = []control.FlowUpdate{
		{
			Name: "search_kb",
			Instructions: []control.StepConfig{
				{Action: "read_body"},
			},
			Description: "Search knowledge base by query",
			Expose: &control.FlowExposeConfig{
				AsMCPTool: true,
			},
			Action: "upsert",
		},
	}

	result.Bundle.Apis = []control.ApiUpdate{
		{
			Name:     "search_api",
			Path:     "/v1/kb/search",
			Method:   "POST",
			FlowName: "search_kb",
			AppName:  "myapp",
			Action:   "upsert",
		},
	}

	result.Bundle.AppProtocols = []control.AppProtocolUpdate{
		{
			AppName: "myapp",
			MCP: &control.AppMCPProtocol{
				ServerName:  "myapp_mcp",
				Description: "MyApp tools",
			},
			Action: "upsert",
		},
	}

	processAppProtocols(&result)

	if len(result.Bundle.VirtualMCPServers) != 1 {
		t.Fatalf("expected 1 VirtualMCPServer, got %d", len(result.Bundle.VirtualMCPServers))
	}

	server := result.Bundle.VirtualMCPServers[0]
	if server.Name != "myapp_mcp" {
		t.Errorf("VirtualMCPServer name: expected 'myapp_mcp', got '%s'", server.Name)
	}
	if server.Description != "MyApp tools" {
		t.Errorf("VirtualMCPServer description: expected 'MyApp tools', got '%s'", server.Description)
	}

	if len(server.Sources) != 1 {
		t.Fatalf("expected 1 ToolSource, got %d", len(server.Sources))
	}

	source := server.Sources[0]
	if source.Kind != mcpreg.ToolSourceAPI {
		t.Errorf("ToolSource.Kind: expected '%s', got '%s'", mcpreg.ToolSourceAPI, source.Kind)
	}

	if source.APITool == nil {
		t.Fatal("ToolSource.APITool is nil")
	}

	tool := source.APITool
	if tool.Name != "search_kb" {
		t.Errorf("APIToolDef.Name: expected 'search_kb', got '%s'", tool.Name)
	}
	if tool.Description != "Search knowledge base by query" {
		t.Errorf("APIToolDef.Description: expected 'Search knowledge base by query', got '%s'", tool.Description)
	}
	if tool.Path != "/v1/kb/search" {
		t.Errorf("APIToolDef.Path: expected '/v1/kb/search', got '%s'", tool.Path)
	}
	if tool.Method != "POST" {
		t.Errorf("APIToolDef.Method: expected 'POST', got '%s'", tool.Method)
	}
}

// TestProcessAppProtocols_A2AFlowsGenerated verifies that flows with
// Expose.AsA2ASkill=true generate __a2a_serve_* and __a2a_card_* flows+APIs.
func TestProcessAppProtocols_A2AFlowsGenerated(t *testing.T) {
	result := minimalBundle()

	result.Bundle.Flows = []control.FlowUpdate{
		{
			Name: "list_items",
			Instructions: []control.StepConfig{
				{Action: "read_body"},
			},
			Description: "List items",
			Expose: &control.FlowExposeConfig{
				AsA2ASkill: true,
				SkillID:    "list",
			},
			Action: "upsert",
		},
	}

	result.Bundle.Apis = []control.ApiUpdate{
		{
			Name:     "list_api",
			Path:     "/v1/items",
			Method:   "GET",
			FlowName: "list_items",
			AppName:  "myapp",
			Action:   "upsert",
		},
	}

	result.Bundle.AppProtocols = []control.AppProtocolUpdate{
		{
			AppName: "myapp",
			A2A: &control.AppA2AProtocol{
				Description: "MyApp A2A agent",
				Version:     "1.0",
			},
			Action: "upsert",
		},
	}

	initialFlowCount := len(result.Bundle.Flows)
	initialAPICount := len(result.Bundle.Apis)

	processAppProtocols(&result)

	// Expect 2 new flows: __a2a_serve_myapp, __a2a_card_myapp
	if len(result.Bundle.Flows) != initialFlowCount+2 {
		t.Fatalf("expected %d flows, got %d", initialFlowCount+2, len(result.Bundle.Flows))
	}

	// Expect 2 new APIs: __a2a_serve_api_myapp, __a2a_card_api_myapp
	if len(result.Bundle.Apis) != initialAPICount+2 {
		t.Fatalf("expected %d APIs, got %d", initialAPICount+2, len(result.Bundle.Apis))
	}

	// Verify flow names
	flowNames := make(map[string]bool)
	for _, f := range result.Bundle.Flows {
		flowNames[f.Name] = true
	}

	if !flowNames["__a2a_serve_myapp"] {
		t.Error("expected __a2a_serve_myapp flow")
	}
	if !flowNames["__a2a_card_myapp"] {
		t.Error("expected __a2a_card_myapp flow")
	}

	// Verify API names
	apiNames := make(map[string]bool)
	for _, a := range result.Bundle.Apis {
		apiNames[a.Name] = true
	}

	if !apiNames["__a2a_serve_api_myapp"] {
		t.Error("expected __a2a_serve_api_myapp API")
	}
	if !apiNames["__a2a_card_api_myapp"] {
		t.Error("expected __a2a_card_api_myapp API")
	}
}

// TestProcessAppProtocols_ExtraSourcesAppended verifies that ExtraSources
// on AppMCPProtocol are appended after auto-derived tools.
func TestProcessAppProtocols_ExtraSourcesAppended(t *testing.T) {
	result := minimalBundle()

	result.Bundle.Flows = []control.FlowUpdate{
		{
			Name: "search_kb",
			Instructions: []control.StepConfig{
				{Action: "read_body"},
			},
			Expose: &control.FlowExposeConfig{
				AsMCPTool: true,
			},
			Action: "upsert",
		},
	}

	result.Bundle.Apis = []control.ApiUpdate{
		{
			Name:     "search_api",
			Path:     "/v1/kb/search",
			Method:   "POST",
			FlowName: "search_kb",
			AppName:  "myapp",
			Action:   "upsert",
		},
	}

	extraSource := mcpreg.ToolSource{
		Kind:        mcpreg.ToolSourceMCPAll,
		ServerAlias: "external_server",
	}

	result.Bundle.AppProtocols = []control.AppProtocolUpdate{
		{
			AppName: "myapp",
			MCP: &control.AppMCPProtocol{
				ServerName:   "myapp_mcp",
				ExtraSources: []mcpreg.ToolSource{extraSource},
			},
			Action: "upsert",
		},
	}

	processAppProtocols(&result)

	if len(result.Bundle.VirtualMCPServers) != 1 {
		t.Fatalf("expected 1 VirtualMCPServer, got %d", len(result.Bundle.VirtualMCPServers))
	}

	server := result.Bundle.VirtualMCPServers[0]
	// Expect 2 sources: 1 auto-derived + 1 extra
	if len(server.Sources) != 2 {
		t.Fatalf("expected 2 ToolSources, got %d", len(server.Sources))
	}

	// First should be auto-derived (api_tool)
	if server.Sources[0].Kind != mcpreg.ToolSourceAPI {
		t.Errorf("first source kind: expected %s, got %s", mcpreg.ToolSourceAPI, server.Sources[0].Kind)
	}

	// Second should be extra (mcp_all)
	if server.Sources[1].Kind != mcpreg.ToolSourceMCPAll {
		t.Errorf("second source kind: expected %s, got %s", mcpreg.ToolSourceMCPAll, server.Sources[1].Kind)
	}
	if server.Sources[1].ServerAlias != "external_server" {
		t.Errorf("second source server_alias: expected 'external_server', got '%s'", server.Sources[1].ServerAlias)
	}
}

// TestProcessAppProtocols_OAuthMetaGenerated verifies that protocol.MCP.Auth set
// (with A2A protocol) generates __oauth_meta_* flow + API.
func TestProcessAppProtocols_OAuthMetaGenerated(t *testing.T) {
	result := minimalBundle()

	result.Bundle.Flows = []control.FlowUpdate{
		{
			Name: "test_flow",
			Expose: &control.FlowExposeConfig{
				AsA2ASkill: true,
				SkillID:    "test_skill",
			},
			Instructions: []control.StepConfig{
				{Action: "return"},
			},
			Action: "upsert",
		},
	}

	result.Bundle.Apis = []control.ApiUpdate{
		{
			Name:     "test_api",
			Path:     "/v1/test",
			FlowName: "test_flow",
			AppName:  "myapp",
			Action:   "upsert",
		},
	}

	result.Bundle.AppProtocols = []control.AppProtocolUpdate{
		{
			AppName: "myapp",
			MCP: &control.AppMCPProtocol{
				ServerName: "myapp_mcp",
				Auth: &control.MCPOAuthConfig{
					Issuer:   "https://auth.example.com",
					Audience: "myapp",
					Scopes:   []string{"read", "write"},
				},
			},
			A2A: &control.AppA2AProtocol{
				Description: "MyApp A2A",
			},
			Action: "upsert",
		},
	}

	initialFlowCount := len(result.Bundle.Flows)
	initialAPICount := len(result.Bundle.Apis)

	processAppProtocols(&result)

	// Expect 3 new flows: __a2a_serve_myapp, __a2a_card_myapp, __oauth_meta_myapp
	if len(result.Bundle.Flows) != initialFlowCount+3 {
		t.Fatalf("expected %d flows, got %d", initialFlowCount+3, len(result.Bundle.Flows))
	}

	// Expect 3 new APIs: __a2a_serve_api_myapp, __a2a_card_api_myapp, __oauth_meta_api_myapp
	if len(result.Bundle.Apis) != initialAPICount+3 {
		t.Fatalf("expected %d APIs, got %d", initialAPICount+3, len(result.Bundle.Apis))
	}

	// Verify oauth flow exists
	var oauthFlow *control.FlowUpdate
	for i := range result.Bundle.Flows {
		if result.Bundle.Flows[i].Name == "__oauth_meta_myapp" {
			oauthFlow = &result.Bundle.Flows[i]
			break
		}
	}

	if oauthFlow == nil {
		t.Fatal("expected __oauth_meta_myapp flow")
	}

	// Verify oauth API exists with correct path
	var oauthAPI *control.ApiUpdate
	for i := range result.Bundle.Apis {
		if result.Bundle.Apis[i].Name == "__oauth_meta_api_myapp" {
			oauthAPI = &result.Bundle.Apis[i]
			break
		}
	}

	if oauthAPI == nil {
		t.Fatal("expected __oauth_meta_api_myapp API")
	}

	if oauthAPI.Path != "/v1/.well-known/oauth-protected-resource" {
		t.Errorf("oauth API path: expected '/v1/.well-known/oauth-protected-resource', got '%s'", oauthAPI.Path)
	}
	if oauthAPI.Method != "GET" {
		t.Errorf("oauth API method: expected 'GET', got '%s'", oauthAPI.Method)
	}

	// Verify VirtualMCPServer has Auth set
	if len(result.Bundle.VirtualMCPServers) != 1 {
		t.Fatalf("expected 1 VirtualMCPServer, got %d", len(result.Bundle.VirtualMCPServers))
	}

	server := result.Bundle.VirtualMCPServers[0]
	if server.Auth == nil {
		t.Fatal("VirtualMCPServer.Auth is nil")
	}
	if server.Auth.Issuer != "https://auth.example.com" {
		t.Errorf("Auth.Issuer: expected 'https://auth.example.com', got '%s'", server.Auth.Issuer)
	}
	if server.Auth.Audience != "myapp" {
		t.Errorf("Auth.Audience: expected 'myapp', got '%s'", server.Auth.Audience)
	}
	if len(server.Auth.Scopes) != 2 || server.Auth.Scopes[0] != "read" || server.Auth.Scopes[1] != "write" {
		t.Errorf("Auth.Scopes: expected ['read', 'write'], got %v", server.Auth.Scopes)
	}
}

// TestProcessAppProtocols_ToolNameOverride verifies that flow.Expose.ToolName set
// overrides flow.Name in APIToolDef.Name.
func TestProcessAppProtocols_ToolNameOverride(t *testing.T) {
	result := minimalBundle()

	result.Bundle.Flows = []control.FlowUpdate{
		{
			Name: "internal_search",
			Instructions: []control.StepConfig{
				{Action: "read_body"},
			},
			Description: "Search operation",
			Expose: &control.FlowExposeConfig{
				AsMCPTool: true,
				ToolName:  "kb_search",
			},
			Action: "upsert",
		},
	}

	result.Bundle.Apis = []control.ApiUpdate{
		{
			Name:     "search_api",
			Path:     "/v1/search",
			FlowName: "internal_search",
			AppName:  "myapp",
			Action:   "upsert",
		},
	}

	result.Bundle.AppProtocols = []control.AppProtocolUpdate{
		{
			AppName: "myapp",
			MCP: &control.AppMCPProtocol{
				ServerName: "myapp_mcp",
			},
			Action: "upsert",
		},
	}

	processAppProtocols(&result)

	if len(result.Bundle.VirtualMCPServers) != 1 {
		t.Fatalf("expected 1 VirtualMCPServer, got %d", len(result.Bundle.VirtualMCPServers))
	}

	server := result.Bundle.VirtualMCPServers[0]
	if len(server.Sources) != 1 {
		t.Fatalf("expected 1 ToolSource, got %d", len(server.Sources))
	}

	tool := server.Sources[0].APITool
	if tool.Name != "kb_search" {
		t.Errorf("APIToolDef.Name: expected 'kb_search' (override), got '%s'", tool.Name)
	}
}

// ── Negative Tests ───────────────────────────────────────────────────────────

// TestProcessAppProtocols_NoExpose_NoAPIToolDef verifies that flows without Expose
// or with Expose.AsMCPTool=false do not generate APIToolDef entries.
func TestProcessAppProtocols_NoExpose_NoAPIToolDef(t *testing.T) {
	result := minimalBundle()

	result.Bundle.Flows = []control.FlowUpdate{
		{
			Name: "regular_flow",
			Instructions: []control.StepConfig{
				{Action: "read_body"},
			},
			// Expose is nil
			Action: "upsert",
		},
	}

	result.Bundle.Apis = []control.ApiUpdate{
		{
			Name:     "regular_api",
			Path:     "/v1/test",
			FlowName: "regular_flow",
			AppName:  "myapp",
			Action:   "upsert",
		},
	}

	result.Bundle.AppProtocols = []control.AppProtocolUpdate{
		{
			AppName: "myapp",
			MCP: &control.AppMCPProtocol{
				ServerName: "myapp_mcp",
			},
			Action: "upsert",
		},
	}

	processAppProtocols(&result)

	// VirtualMCPServer should still be created, but with no sources
	if len(result.Bundle.VirtualMCPServers) != 1 {
		t.Fatalf("expected 1 VirtualMCPServer, got %d", len(result.Bundle.VirtualMCPServers))
	}

	server := result.Bundle.VirtualMCPServers[0]
	if len(server.Sources) != 0 {
		t.Errorf("expected 0 ToolSources (flow has no Expose), got %d", len(server.Sources))
	}
}

// TestProcessAppProtocols_NoAPIBinding_SkipsFlow verifies that flows marked for MCP
// exposure but without matching ApiUpdate are not included in VirtualMCPServerDef.
func TestProcessAppProtocols_NoAPIBinding_SkipsFlow(t *testing.T) {
	result := minimalBundle()

	result.Bundle.Flows = []control.FlowUpdate{
		{
			Name: "exposed_flow",
			Instructions: []control.StepConfig{
				{Action: "read_body"},
			},
			Expose: &control.FlowExposeConfig{
				AsMCPTool: true,
			},
			Action: "upsert",
		},
	}

	// Note: no matching API for "exposed_flow" with AppName="myapp"
	result.Bundle.Apis = []control.ApiUpdate{}

	result.Bundle.AppProtocols = []control.AppProtocolUpdate{
		{
			AppName: "myapp",
			MCP: &control.AppMCPProtocol{
				ServerName: "myapp_mcp",
			},
			Action: "upsert",
		},
	}

	processAppProtocols(&result)

	if len(result.Bundle.VirtualMCPServers) != 1 {
		t.Fatalf("expected 1 VirtualMCPServer, got %d", len(result.Bundle.VirtualMCPServers))
	}

	server := result.Bundle.VirtualMCPServers[0]
	if len(server.Sources) != 0 {
		t.Errorf("expected 0 ToolSources (no matching API), got %d", len(server.Sources))
	}
}

// TestProcessAppProtocols_EmptyAppProtocols_NoOp verifies that empty or nil
// AppProtocols does not modify the bundle.
func TestProcessAppProtocols_EmptyAppProtocols_NoOp(t *testing.T) {
	result := minimalBundle()

	initialFlowCount := len(result.Bundle.Flows)
	initialAPICount := len(result.Bundle.Apis)
	initialVirtualCount := len(result.Bundle.VirtualMCPServers)

	// Empty AppProtocols
	result.Bundle.AppProtocols = []control.AppProtocolUpdate{}

	processAppProtocols(&result)

	if len(result.Bundle.Flows) != initialFlowCount {
		t.Errorf("flows changed: expected %d, got %d", initialFlowCount, len(result.Bundle.Flows))
	}
	if len(result.Bundle.Apis) != initialAPICount {
		t.Errorf("APIs changed: expected %d, got %d", initialAPICount, len(result.Bundle.Apis))
	}
	if len(result.Bundle.VirtualMCPServers) != initialVirtualCount {
		t.Errorf("VirtualMCPServers changed: expected %d, got %d", initialVirtualCount, len(result.Bundle.VirtualMCPServers))
	}

	// Test with nil
	result2 := minimalBundle()
	result2.Bundle.AppProtocols = nil

	initialFlowCount2 := len(result2.Bundle.Flows)
	initialAPICount2 := len(result2.Bundle.Apis)
	initialVirtualCount2 := len(result2.Bundle.VirtualMCPServers)

	processAppProtocols(&result2)

	if len(result2.Bundle.Flows) != initialFlowCount2 {
		t.Errorf("flows changed (nil): expected %d, got %d", initialFlowCount2, len(result2.Bundle.Flows))
	}
	if len(result2.Bundle.Apis) != initialAPICount2 {
		t.Errorf("APIs changed (nil): expected %d, got %d", initialAPICount2, len(result2.Bundle.Apis))
	}
	if len(result2.Bundle.VirtualMCPServers) != initialVirtualCount2 {
		t.Errorf("VirtualMCPServers changed (nil): expected %d, got %d", initialVirtualCount2, len(result2.Bundle.VirtualMCPServers))
	}
}

// ── Non-Functional Tests ─────────────────────────────────────────────────────

// TestProcessAppProtocols_AppIsolation verifies that two apps each with their own
// protocol generate separate VirtualMCPServerDef entries with no cross-contamination.
func TestProcessAppProtocols_AppIsolation(t *testing.T) {
	result := minimalBundle()

	result.Bundle.Flows = []control.FlowUpdate{
		{
			Name: "app1_search",
			Instructions: []control.StepConfig{
				{Action: "read_body"},
			},
			Description: "App1 search",
			Expose: &control.FlowExposeConfig{
				AsMCPTool: true,
			},
			Action: "upsert",
		},
		{
			Name: "app2_list",
			Instructions: []control.StepConfig{
				{Action: "read_body"},
			},
			Description: "App2 list",
			Expose: &control.FlowExposeConfig{
				AsMCPTool: true,
			},
			Action: "upsert",
		},
	}

	result.Bundle.Apis = []control.ApiUpdate{
		{
			Name:     "app1_api",
			Path:     "/app1/search",
			FlowName: "app1_search",
			AppName:  "app1",
			Action:   "upsert",
		},
		{
			Name:     "app2_api",
			Path:     "/app2/list",
			FlowName: "app2_list",
			AppName:  "app2",
			Action:   "upsert",
		},
	}

	result.Bundle.AppProtocols = []control.AppProtocolUpdate{
		{
			AppName: "app1",
			MCP: &control.AppMCPProtocol{
				ServerName:  "app1_mcp",
				Description: "App1 MCP",
			},
			Action: "upsert",
		},
		{
			AppName: "app2",
			MCP: &control.AppMCPProtocol{
				ServerName:  "app2_mcp",
				Description: "App2 MCP",
			},
			Action: "upsert",
		},
	}

	processAppProtocols(&result)

	if len(result.Bundle.VirtualMCPServers) != 2 {
		t.Fatalf("expected 2 VirtualMCPServers, got %d", len(result.Bundle.VirtualMCPServers))
	}

	// Check first server
	if result.Bundle.VirtualMCPServers[0].Name != "app1_mcp" {
		t.Errorf("first server name: expected 'app1_mcp', got '%s'", result.Bundle.VirtualMCPServers[0].Name)
	}
	if len(result.Bundle.VirtualMCPServers[0].Sources) != 1 {
		t.Errorf("first server sources: expected 1, got %d", len(result.Bundle.VirtualMCPServers[0].Sources))
	}
	if result.Bundle.VirtualMCPServers[0].Sources[0].APITool.Name != "app1_search" {
		t.Errorf("first server tool name: expected 'app1_search', got '%s'", result.Bundle.VirtualMCPServers[0].Sources[0].APITool.Name)
	}

	// Check second server
	if result.Bundle.VirtualMCPServers[1].Name != "app2_mcp" {
		t.Errorf("second server name: expected 'app2_mcp', got '%s'", result.Bundle.VirtualMCPServers[1].Name)
	}
	if len(result.Bundle.VirtualMCPServers[1].Sources) != 1 {
		t.Errorf("second server sources: expected 1, got %d", len(result.Bundle.VirtualMCPServers[1].Sources))
	}
	if result.Bundle.VirtualMCPServers[1].Sources[0].APITool.Name != "app2_list" {
		t.Errorf("second server tool name: expected 'app2_list', got '%s'", result.Bundle.VirtualMCPServers[1].Sources[0].APITool.Name)
	}
}

// TestProcessAppProtocols_PreservesExistingVirtualServers verifies that
// explicit VirtualMCPServers in the bundle are preserved unchanged.
func TestProcessAppProtocols_PreservesExistingVirtualServers(t *testing.T) {
	result := minimalBundle()

	// Pre-existing VirtualMCPServer (manually added, not auto-derived)
	existingServer := mcpreg.VirtualMCPServerDef{
		Name:        "manual_server",
		Description: "Manually added server",
		Sources: []mcpreg.ToolSource{
			{
				Kind:        mcpreg.ToolSourceMCPAll,
				ServerAlias: "external_mcp",
			},
		},
	}
	result.Bundle.VirtualMCPServers = append(result.Bundle.VirtualMCPServers, existingServer)

	result.Bundle.Flows = []control.FlowUpdate{
		{
			Name: "auto_flow",
			Instructions: []control.StepConfig{
				{Action: "read_body"},
			},
			Expose: &control.FlowExposeConfig{
				AsMCPTool: true,
			},
			Action: "upsert",
		},
	}

	result.Bundle.Apis = []control.ApiUpdate{
		{
			Name:     "auto_api",
			Path:     "/v1/auto",
			FlowName: "auto_flow",
			AppName:  "myapp",
			Action:   "upsert",
		},
	}

	result.Bundle.AppProtocols = []control.AppProtocolUpdate{
		{
			AppName: "myapp",
			MCP: &control.AppMCPProtocol{
				ServerName: "auto_server",
			},
			Action: "upsert",
		},
	}

	processAppProtocols(&result)

	// Should have 2 servers: existing + 1 auto-derived
	if len(result.Bundle.VirtualMCPServers) != 2 {
		t.Fatalf("expected 2 VirtualMCPServers, got %d", len(result.Bundle.VirtualMCPServers))
	}

	// Verify existing server is first and unchanged
	if result.Bundle.VirtualMCPServers[0].Name != "manual_server" {
		t.Errorf("first server name: expected 'manual_server', got '%s'", result.Bundle.VirtualMCPServers[0].Name)
	}
	if result.Bundle.VirtualMCPServers[0].Description != "Manually added server" {
		t.Errorf("first server description: expected 'Manually added server', got '%s'", result.Bundle.VirtualMCPServers[0].Description)
	}
	if len(result.Bundle.VirtualMCPServers[0].Sources) != 1 {
		t.Errorf("first server sources count changed: expected 1, got %d", len(result.Bundle.VirtualMCPServers[0].Sources))
	}

	// Verify auto-derived server is second
	if result.Bundle.VirtualMCPServers[1].Name != "auto_server" {
		t.Errorf("second server name: expected 'auto_server', got '%s'", result.Bundle.VirtualMCPServers[1].Name)
	}
}

// TestProcessAppProtocols_GeneratedFlowsNamespaced verifies that all generated flows
// and APIs have "__" prefix to distinguish them from user-defined flows.
func TestProcessAppProtocols_GeneratedFlowsNamespaced(t *testing.T) {
	result := minimalBundle()

	result.Bundle.Flows = []control.FlowUpdate{
		{
			Name: "user_skill",
			Instructions: []control.StepConfig{
				{Action: "read_body"},
			},
			Expose: &control.FlowExposeConfig{
				AsA2ASkill: true,
				SkillID:    "user_skill",
			},
			Action: "upsert",
		},
	}

	result.Bundle.Apis = []control.ApiUpdate{
		{
			Name:     "user_api",
			Path:     "/v1/skill",
			FlowName: "user_skill",
			AppName:  "myapp",
			Action:   "upsert",
		},
	}

	result.Bundle.AppProtocols = []control.AppProtocolUpdate{
		{
			AppName: "myapp",
			A2A: &control.AppA2AProtocol{
				Description: "MyApp",
			},
			MCP: &control.AppMCPProtocol{
				ServerName: "myapp_mcp",
				Auth: &control.MCPOAuthConfig{
					Issuer: "https://auth.example.com",
				},
			},
			Action: "upsert",
		},
	}

	processAppProtocols(&result)

	// Check generated flows start with "__"
	for _, flow := range result.Bundle.Flows {
		if flow.Name != "user_skill" { // skip user flow
			if !strings.HasPrefix(flow.Name, "__") {
				t.Errorf("generated flow '%s' does not start with '__'", flow.Name)
			}
		}
	}

	// Check generated APIs start with "__"
	for _, api := range result.Bundle.Apis {
		if api.Name != "user_api" { // skip user API
			if !strings.HasPrefix(api.Name, "__") {
				t.Errorf("generated API '%s' does not start with '__'", api.Name)
			}
		}
	}
}

