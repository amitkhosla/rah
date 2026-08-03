package control

import (
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/mcpreg"
)

// newTestMSWithAI returns a ManagementServer wired with a config.Manager and
// an mcpreg.Registry so AI/MCP sync paths are exercised.
func newTestMSWithAI(t *testing.T) (*ManagementServer, *config.Manager, *mcpreg.Registry) {
	t.Helper()
	ms := newTestMS(t)
	cfgMgr := config.Default()
	reg := mcpreg.NewRegistry()
	ms.SetCfgMgr(cfgMgr)
	ms.SetMCPReg(reg)
	return ms, cfgMgr, reg
}

func TestApplyUnifiedSyncLLMModels(t *testing.T) {
	ms, cfgMgr, _ := newTestMSWithAI(t)

	req := UnifiedSyncRequest{
		SyncUUID: "sync-llm-1",
		LLMModels: []config.LLMModelConfig{
			{Alias: "gpt4o", Provider: "openai", Adapter: "openai"},
		},
	}
	if err := ms.ApplyUnifiedSync(req); err != nil {
		t.Fatalf("ApplyUnifiedSync: %v", err)
	}

	found := false
	for _, m := range cfgMgr.LLM().Models {
		if m.Alias == "gpt4o" {
			found = true
			if m.Provider != "openai" {
				t.Errorf("model provider = %q, want openai", m.Provider)
			}
		}
	}
	if !found {
		t.Error("model gpt4o not found in cfgMgr after sync")
	}
}

func TestApplyUnifiedSyncLLMModelsUpsert(t *testing.T) {
	ms, cfgMgr, _ := newTestMSWithAI(t)

	// Insert then update the same alias.
	req1 := UnifiedSyncRequest{
		SyncUUID: "sync-llm-2a",
		LLMModels: []config.LLMModelConfig{
			{Alias: "claude3", Provider: "anthropic-old", Adapter: "anthropic"},
		},
	}
	if err := ms.ApplyUnifiedSync(req1); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	req2 := UnifiedSyncRequest{
		SyncUUID: "sync-llm-2b",
		LLMModels: []config.LLMModelConfig{
			{Alias: "claude3", Provider: "anthropic-new", Adapter: "anthropic"},
		},
	}
	if err := ms.ApplyUnifiedSync(req2); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	count := 0
	for _, m := range cfgMgr.LLM().Models {
		if m.Alias == "claude3" {
			count++
			if m.Provider != "anthropic-new" {
				t.Errorf("expected updated provider anthropic-new, got %q", m.Provider)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one claude3 entry, got %d", count)
	}
}

func TestApplyUnifiedSyncMCPServers(t *testing.T) {
	ms, cfgMgr, _ := newTestMSWithAI(t)

	req := UnifiedSyncRequest{
		SyncUUID: "sync-mcp-1",
		MCPServers: []config.MCPServerConfig{
			{Alias: "tools-mcp", Transport: "http", URL: "http://tools.example.com"},
		},
	}
	if err := ms.ApplyUnifiedSync(req); err != nil {
		t.Fatalf("ApplyUnifiedSync: %v", err)
	}

	found := false
	for _, s := range cfgMgr.LLM().MCPServers {
		if s.Alias == "tools-mcp" {
			found = true
			if s.URL != "http://tools.example.com" {
				t.Errorf("server URL = %q, want http://tools.example.com", s.URL)
			}
		}
	}
	if !found {
		t.Error("MCP server tools-mcp not found in cfgMgr after sync")
	}
}

func TestApplyUnifiedSyncVirtualMCPServers(t *testing.T) {
	ms, _, reg := newTestMSWithAI(t)

	req := UnifiedSyncRequest{
		SyncUUID: "sync-vmcp-1",
		VirtualMCPServers: []mcpreg.VirtualMCPServerDef{
			{Name: "kb-server", Sources: []mcpreg.ToolSource{{Kind: "api_tool"}}},
		},
	}
	if err := ms.ApplyUnifiedSync(req); err != nil {
		t.Fatalf("ApplyUnifiedSync: %v", err)
	}

	def, ok := reg.GetServer(0, "kb-server")
	if !ok {
		t.Fatal("virtual MCP server kb-server not found in registry after sync")
	}
	if def.Name != "kb-server" {
		t.Errorf("server name = %q, want kb-server", def.Name)
	}
}

func TestApplyUnifiedSyncAPITools(t *testing.T) {
	ms, _, reg := newTestMSWithAI(t)

	req := UnifiedSyncRequest{
		SyncUUID: "sync-apitool-1",
		APITools: []mcpreg.APIToolDef{
			{Name: "search", Path: "/v1/kb/search", Method: "POST"},
		},
	}
	if err := ms.ApplyUnifiedSync(req); err != nil {
		t.Fatalf("ApplyUnifiedSync: %v", err)
	}

	tool, ok := reg.GetAPITool("search")
	if !ok {
		t.Fatal("API tool search not found in registry after sync")
	}
	if tool.Path != "/v1/kb/search" {
		t.Errorf("tool path = %q, want /v1/kb/search", tool.Path)
	}
}

func TestApplyUnifiedSyncSkipsWhenNoCfgMgr(t *testing.T) {
	// Without SetCfgMgr, LLMModels and MCPServers blocks should be silently skipped.
	ms := newTestMS(t)
	req := UnifiedSyncRequest{
		SyncUUID: "sync-nocfg",
		LLMModels: []config.LLMModelConfig{
			{Alias: "gpt4o", Provider: "openai", Adapter: "openai"},
		},
		MCPServers: []config.MCPServerConfig{
			{Alias: "tools-mcp", Transport: "http", URL: "http://tools.example.com"},
		},
	}
	// Must not panic or return an error.
	if err := ms.ApplyUnifiedSync(req); err != nil {
		t.Fatalf("ApplyUnifiedSync without cfgMgr: %v", err)
	}
}

func TestApplyUnifiedSyncSkipsWhenNoMCPReg(t *testing.T) {
	// Without SetMCPReg, VirtualMCPServers and APITools blocks should be silently skipped.
	ms := newTestMS(t)
	req := UnifiedSyncRequest{
		SyncUUID: "sync-noreg",
		VirtualMCPServers: []mcpreg.VirtualMCPServerDef{
			{Name: "kb-server"},
		},
		APITools: []mcpreg.APIToolDef{
			{Name: "search", Path: "/v1/kb/search", Method: "POST"},
		},
	}
	if err := ms.ApplyUnifiedSync(req); err != nil {
		t.Fatalf("ApplyUnifiedSync without mcpReg: %v", err)
	}
}
