package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/control"
	"github.com/amitkhosla/rah/internal/mcpreg"
)

// minimalBundle returns a LoadResult with one flow and one API so the
// empty_bundle check in lintLevel0 does not fire, letting us test only
// the specific rules we care about.
func minimalBundle() LoadResult {
	return LoadResult{
		Bundle: control.UnifiedSyncRequest{
			Flows: []control.FlowUpdate{
				{Name: "echo", Instructions: []control.StepConfig{{Action: "echo_request"}}, Action: "upsert"},
			},
			Apis: []control.ApiUpdate{
				{Name: "api1", Path: "/v1/test", FlowName: "echo", Action: "upsert"},
			},
			LLMModels:         []config.LLMModelConfig{},
			MCPServers:        []config.MCPServerConfig{},
			VirtualMCPServers: []mcpreg.VirtualMCPServerDef{},
			APITools:          []mcpreg.APIToolDef{},
		},
		SourceMap: SourceMap{},
		Issues:    []LintIssue{},
	}
}

func hasIssueWithRule(issues []LintIssue, rule string) bool {
	for _, iss := range issues {
		if iss.Rule == rule {
			return true
		}
	}
	return false
}

// ── Load() initialisation ────────────────────────────────────────────────────

func TestLoadInitialisesAISlicesAsEmpty(t *testing.T) {
	dir := t.TempDir()
	// Write a minimal valid YAML so Load succeeds.
	yaml := `
flows:
  - name: echo
    action: upsert
    instructions:
      - action: echo_request
apis:
  - name: api1
    path: /v1/test
    flow_name: echo
    action: upsert
`
	if err := os.WriteFile(filepath.Join(dir, "bundle.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if result.Bundle.LLMModels == nil {
		t.Error("LLMModels must be an empty slice, not nil")
	}
	if result.Bundle.MCPServers == nil {
		t.Error("MCPServers must be an empty slice, not nil")
	}
	if result.Bundle.VirtualMCPServers == nil {
		t.Error("VirtualMCPServers must be an empty slice, not nil")
	}
	if result.Bundle.APITools == nil {
		t.Error("APITools must be an empty slice, not nil")
	}
}

// ── lintLevel0: LLM model checks ────────────────────────────────────────────

func TestLintLevel0LLMModelMissingAlias(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.LLMModels = []config.LLMModelConfig{
		{Provider: "openai", Adapter: "openai"},
	}
	issues := lintLevel0(lr)
	if !hasIssueWithRule(issues, "llm_model_missing_alias") {
		t.Error("expected llm_model_missing_alias issue, got none")
	}
}

func TestLintLevel0LLMModelMissingProvider(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.LLMModels = []config.LLMModelConfig{
		{Alias: "gpt4", Adapter: "openai"},
	}
	issues := lintLevel0(lr)
	if !hasIssueWithRule(issues, "llm_model_missing_provider") {
		t.Error("expected llm_model_missing_provider issue, got none")
	}
}

func TestLintLevel0LLMModelInvalidAdapter(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.LLMModels = []config.LLMModelConfig{
		{Alias: "m1", Provider: "acme", Adapter: "unknown_adapter"},
	}
	issues := lintLevel0(lr)
	if !hasIssueWithRule(issues, "llm_model_invalid_adapter") {
		t.Error("expected llm_model_invalid_adapter issue, got none")
	}
}

func TestLintLevel0LLMModelValidPasses(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.LLMModels = []config.LLMModelConfig{
		{Alias: "claude-3", Provider: "anthropic", Adapter: "anthropic"},
	}
	issues := lintLevel0(lr)
	for _, iss := range issues {
		if iss.Rule == "llm_model_missing_alias" || iss.Rule == "llm_model_missing_provider" || iss.Rule == "llm_model_invalid_adapter" {
			t.Errorf("unexpected issue %q for valid LLM model", iss.Rule)
		}
	}
}

// ── lintLevel0: MCP server checks ───────────────────────────────────────────

func TestLintLevel0MCPServerMissingAlias(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.MCPServers = []config.MCPServerConfig{
		{Transport: "http", URL: "http://mcp.example.com"},
	}
	issues := lintLevel0(lr)
	if !hasIssueWithRule(issues, "mcp_server_missing_alias") {
		t.Error("expected mcp_server_missing_alias issue, got none")
	}
}

func TestLintLevel0MCPServerInvalidTransport(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.MCPServers = []config.MCPServerConfig{
		{Alias: "mcp1", Transport: "grpc"},
	}
	issues := lintLevel0(lr)
	if !hasIssueWithRule(issues, "mcp_server_invalid_transport") {
		t.Error("expected mcp_server_invalid_transport issue, got none")
	}
}

func TestLintLevel0MCPServerMissingURL(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.MCPServers = []config.MCPServerConfig{
		{Alias: "mcp1", Transport: "http"},
	}
	issues := lintLevel0(lr)
	if !hasIssueWithRule(issues, "mcp_server_missing_url") {
		t.Error("expected mcp_server_missing_url issue, got none")
	}
}

// ── lintLevel0: Virtual MCP server checks ───────────────────────────────────

func TestLintLevel0VirtualMCPServerMissingName(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.VirtualMCPServers = []mcpreg.VirtualMCPServerDef{
		{Sources: []mcpreg.ToolSource{{Kind: "api_tool"}}},
	}
	issues := lintLevel0(lr)
	if !hasIssueWithRule(issues, "virtual_mcp_server_missing_name") {
		t.Error("expected virtual_mcp_server_missing_name issue, got none")
	}
}

func TestLintLevel0VirtualMCPServerNoSources(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.VirtualMCPServers = []mcpreg.VirtualMCPServerDef{
		{Name: "my-server"},
	}
	issues := lintLevel0(lr)
	if !hasIssueWithRule(issues, "virtual_mcp_server_no_sources") {
		t.Error("expected virtual_mcp_server_no_sources warning, got none")
	}
}

func TestLintLevel0VirtualMCPServerInvalidSourceKind(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.VirtualMCPServers = []mcpreg.VirtualMCPServerDef{
		{Name: "my-server", Sources: []mcpreg.ToolSource{{Kind: "bad_kind"}}},
	}
	issues := lintLevel0(lr)
	if !hasIssueWithRule(issues, "virtual_mcp_server_invalid_source_kind") {
		t.Error("expected virtual_mcp_server_invalid_source_kind issue, got none")
	}
}

// ── lintLevel0: API tool checks ──────────────────────────────────────────────

func TestLintLevel0APIToolMissingName(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.APITools = []mcpreg.APIToolDef{
		{Path: "/v1/search", Method: "GET"},
	}
	issues := lintLevel0(lr)
	if !hasIssueWithRule(issues, "api_tool_missing_name") {
		t.Error("expected api_tool_missing_name issue, got none")
	}
}

func TestLintLevel0APIToolMissingPath(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.APITools = []mcpreg.APIToolDef{
		{Name: "search", Method: "GET"},
	}
	issues := lintLevel0(lr)
	if !hasIssueWithRule(issues, "api_tool_missing_path") {
		t.Error("expected api_tool_missing_path issue, got none")
	}
}

func TestLintLevel0APIToolInvalidMethod(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.APITools = []mcpreg.APIToolDef{
		{Name: "search", Path: "/v1/search", Method: "CONNECT"},
	}
	issues := lintLevel0(lr)
	if !hasIssueWithRule(issues, "api_tool_invalid_method") {
		t.Error("expected api_tool_invalid_method issue, got none")
	}
}

// ── lintLevel2: virtual MCP server → MCP server alias cross-reference ───────

func TestLintLevel2VirtualMCPUnresolvedServerAlias(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.VirtualMCPServers = []mcpreg.VirtualMCPServerDef{
		{
			Name: "my-server",
			Sources: []mcpreg.ToolSource{
				{Kind: "mcp_all", ServerAlias: "nonexistent-mcp"},
			},
		},
	}
	// MCPServers is empty, so the alias cannot resolve.
	issues := lintLevel2(lr)
	if !hasIssueWithRule(issues, "virtual_mcp_unresolved_server_alias") {
		t.Error("expected virtual_mcp_unresolved_server_alias warning, got none")
	}
}

func TestLintLevel2VirtualMCPResolvedServerAliasNoWarn(t *testing.T) {
	lr := minimalBundle()
	lr.Bundle.MCPServers = []config.MCPServerConfig{
		{Alias: "known-mcp", Transport: "http", URL: "http://mcp.example.com"},
	}
	lr.Bundle.VirtualMCPServers = []mcpreg.VirtualMCPServerDef{
		{
			Name: "my-server",
			Sources: []mcpreg.ToolSource{
				{Kind: "mcp_all", ServerAlias: "known-mcp"},
			},
		},
	}
	issues := lintLevel2(lr)
	if hasIssueWithRule(issues, "virtual_mcp_unresolved_server_alias") {
		t.Error("unexpected virtual_mcp_unresolved_server_alias warning when alias resolves")
	}
}

// ── mergeBundles: last-file-wins dedup for AI entity types ──────────────────

func TestMergeBundlesLLMModelsLastFileWins(t *testing.T) {
	dir := t.TempDir()
	// Two files define the same LLM model alias — last file (b.yaml) should win.
	aYAML := `
flows:
  - name: echo
    action: upsert
    instructions:
      - action: echo_request
apis:
  - name: api1
    path: /v1/test
    flow_name: echo
    action: upsert
llm_models:
  - alias: gpt4
    provider: openai-a
    adapter: openai
`
	bYAML := `
llm_models:
  - alias: gpt4
    provider: openai-b
    adapter: openai
`
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(aYAML), 0644); err != nil {
		t.Fatalf("write a.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.yaml"), []byte(bYAML), 0644); err != nil {
		t.Fatalf("write b.yaml: %v", err)
	}
	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	models := result.Bundle.LLMModels
	count := 0
	for _, m := range models {
		if m.Alias == "gpt4" {
			count++
			if m.Provider != "openai-b" {
				t.Errorf("expected last-file-wins provider openai-b, got %q", m.Provider)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one gpt4 model, got %d", count)
	}
}

func TestMergeBundlesMCPServersLastFileWins(t *testing.T) {
	dir := t.TempDir()
	aYAML := `
flows:
  - name: echo
    action: upsert
    instructions:
      - action: echo_request
apis:
  - name: api1
    path: /v1/test
    flow_name: echo
    action: upsert
mcp_servers:
  - alias: my-mcp
    transport: http
    url: http://a.example.com
`
	bYAML := `
mcp_servers:
  - alias: my-mcp
    transport: http
    url: http://b.example.com
`
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(aYAML), 0644); err != nil {
		t.Fatalf("write a.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.yaml"), []byte(bYAML), 0644); err != nil {
		t.Fatalf("write b.yaml: %v", err)
	}
	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	count := 0
	for _, s := range result.Bundle.MCPServers {
		if s.Alias == "my-mcp" {
			count++
			if s.URL != "http://b.example.com" {
				t.Errorf("expected last-file-wins URL http://b.example.com, got %q", s.URL)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one my-mcp server, got %d", count)
	}
}
