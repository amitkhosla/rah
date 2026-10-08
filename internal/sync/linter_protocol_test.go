package sync

import (
	"testing"

	"github.com/amitkhosla/rah/internal/control"
	"github.com/amitkhosla/rah/internal/mcpreg"
)

// --- flow_expose_no_description ---

func TestLintProtocol_FlowExposeNoDescription(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{
				{
					Name: "exposed_flow",
					Expose: &control.FlowExposeConfig{
						AsMCPTool: true,
					},
					Description: "",
					Instructions: []control.StepConfig{
						{Action: "bind_body", Value: "{}"},
					},
				},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "flow_expose_no_description")
	if len(found) == 0 {
		t.Error("expected flow_expose_no_description issue, got none")
	}
}

func TestLintProtocol_FlowExposeWithDescription(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{
				{
					Name: "exposed_flow",
					Expose: &control.FlowExposeConfig{
						AsMCPTool: true,
					},
					Description: "This flow does something",
					Instructions: []control.StepConfig{
						{Action: "bind_body", Value: "{}"},
					},
				},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "flow_expose_no_description")
	if len(found) != 0 {
		t.Errorf("expected no flow_expose_no_description issues, got %d", len(found))
	}
}

// --- flow_expose_no_api_binding ---

func TestLintProtocol_FlowExposeNoAPIBinding(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{
				{
					Name: "exposed_flow",
					Expose: &control.FlowExposeConfig{
						AsMCPTool: true,
					},
					Description: "A flow",
					Instructions: []control.StepConfig{
						{Action: "bind_body", Value: "{}"},
					},
				},
			},
			Apis: []control.ApiUpdate{},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "flow_expose_no_api_binding")
	if len(found) == 0 {
		t.Error("expected flow_expose_no_api_binding issue, got none")
	}
}

func TestLintProtocol_FlowExposeWithAPIBinding(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{
				{
					Name: "exposed_flow",
					Expose: &control.FlowExposeConfig{
						AsMCPTool: true,
					},
					Description: "A flow",
					Instructions: []control.StepConfig{
						{Action: "bind_body", Value: "{}"},
					},
				},
			},
			Apis: []control.ApiUpdate{
				{
					Path:     "/api/test",
					FlowName: "exposed_flow",
					Action:   "upsert",
				},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "flow_expose_no_api_binding")
	if len(found) != 0 {
		t.Errorf("expected no flow_expose_no_api_binding issues, got %d", len(found))
	}
}

// --- app_protocol_unknown_app ---

func TestLintProtocol_AppProtocolUnknownApp(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{},
			Apis: []control.ApiUpdate{
				{
					Path:    "/api/test",
					AppName: "known_app",
					Action:  "upsert",
				},
			},
			AppProtocols: []control.AppProtocolUpdate{
				{
					AppName: "unknown_app",
					MCP: &control.AppMCPProtocol{
						ServerName: "unknown_mcp",
					},
					Action: "upsert",
				},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "app_protocol_unknown_app")
	if len(found) == 0 {
		t.Error("expected app_protocol_unknown_app issue, got none")
	}
}

// --- app_protocol_mcp_no_tools ---

func TestLintProtocol_AppProtocolMCPNoTools(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{
				{
					Name: "regular_flow",
					Instructions: []control.StepConfig{
						{Action: "bind_body", Value: "{}"},
					},
				},
			},
			Apis: []control.ApiUpdate{
				{
					Path:     "/api/test",
					AppName:  "myapp",
					FlowName: "regular_flow",
					Action:   "upsert",
				},
			},
			AppProtocols: []control.AppProtocolUpdate{
				{
					AppName: "myapp",
					MCP: &control.AppMCPProtocol{
						ServerName: "myapp_mcp",
					},
					Action: "upsert",
				},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "app_protocol_mcp_no_tools")
	if len(found) == 0 {
		t.Error("expected app_protocol_mcp_no_tools warning, got none")
	}
}

func TestLintProtocol_AppProtocolMCPWithTools(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{
				{
					Name: "tool_flow",
					Expose: &control.FlowExposeConfig{
						AsMCPTool: true,
					},
					Instructions: []control.StepConfig{
						{Action: "bind_body", Value: "{}"},
					},
				},
			},
			Apis: []control.ApiUpdate{
				{
					Path:     "/api/test",
					AppName:  "myapp",
					FlowName: "tool_flow",
					Action:   "upsert",
				},
			},
			AppProtocols: []control.AppProtocolUpdate{
				{
					AppName: "myapp",
					MCP: &control.AppMCPProtocol{
						ServerName: "myapp_mcp",
					},
					Action: "upsert",
				},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "app_protocol_mcp_no_tools")
	if len(found) != 0 {
		t.Errorf("expected no app_protocol_mcp_no_tools warnings, got %d", len(found))
	}
}

// --- app_protocol_a2a_no_skills ---

func TestLintProtocol_AppProtocolA2ANoSkills(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{
				{
					Name: "regular_flow",
					Instructions: []control.StepConfig{
						{Action: "bind_body", Value: "{}"},
					},
				},
			},
			Apis: []control.ApiUpdate{
				{
					Path:     "/api/test",
					AppName:  "myapp",
					FlowName: "regular_flow",
					Action:   "upsert",
				},
			},
			AppProtocols: []control.AppProtocolUpdate{
				{
					AppName: "myapp",
					A2A: &control.AppA2AProtocol{
						Description: "My A2A",
					},
					Action: "upsert",
				},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "app_protocol_a2a_no_skills")
	if len(found) == 0 {
		t.Error("expected app_protocol_a2a_no_skills warning, got none")
	}
}

func TestLintProtocol_AppProtocolA2AWithSkills(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{
				{
					Name: "skill_flow",
					Expose: &control.FlowExposeConfig{
						AsA2ASkill: true,
					},
					Instructions: []control.StepConfig{
						{Action: "bind_body", Value: "{}"},
					},
				},
			},
			Apis: []control.ApiUpdate{
				{
					Path:     "/api/test",
					AppName:  "myapp",
					FlowName: "skill_flow",
					Action:   "upsert",
				},
			},
			AppProtocols: []control.AppProtocolUpdate{
				{
					AppName: "myapp",
					A2A: &control.AppA2AProtocol{
						Description: "My A2A",
					},
					Action: "upsert",
				},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "app_protocol_a2a_no_skills")
	if len(found) != 0 {
		t.Errorf("expected no app_protocol_a2a_no_skills warnings, got %d", len(found))
	}
}

// --- app_protocol_mcp_name_conflict ---

func TestLintProtocol_AppProtocolMCPNameConflict(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{},
			Apis: []control.ApiUpdate{
				{
					Path:    "/api/test",
					AppName: "myapp",
					Action:  "upsert",
				},
			},
			AppProtocols: []control.AppProtocolUpdate{
				{
					AppName: "myapp",
					MCP: &control.AppMCPProtocol{
						ServerName: "myserver",
					},
					Action: "upsert",
				},
			},
			VirtualMCPServers: []mcpreg.VirtualMCPServerDef{
				{
					Name: "myserver",
					Sources: []mcpreg.ToolSource{
						{Kind: "api_tool"},
					},
				},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "app_protocol_mcp_name_conflict")
	if len(found) == 0 {
		t.Error("expected app_protocol_mcp_name_conflict error, got none")
	}
}

func TestLintProtocol_AppProtocolMCPNameNoConflict(t *testing.T) {
	result := LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{},
			Apis: []control.ApiUpdate{
				{
					Path:    "/api/test",
					AppName: "myapp",
					Action:  "upsert",
				},
			},
			AppProtocols: []control.AppProtocolUpdate{
				{
					AppName: "myapp",
					MCP: &control.AppMCPProtocol{
						ServerName: "myserver",
					},
					Action: "upsert",
				},
			},
			VirtualMCPServers: []mcpreg.VirtualMCPServerDef{
				{
					Name: "different_server",
					Sources: []mcpreg.ToolSource{
						{Kind: "api_tool"},
					},
				},
			},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
	issues := Lint(result)
	found := findIssues(issues, "app_protocol_mcp_name_conflict")
	if len(found) != 0 {
		t.Errorf("expected no app_protocol_mcp_name_conflict errors, got %d", len(found))
	}
}
