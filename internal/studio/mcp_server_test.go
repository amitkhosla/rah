package studio

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const wantToolCount = 49

func TestMCPToolsListCount(t *testing.T) {
	if got := len(studioMCPTools); got != wantToolCount {
		t.Errorf("studioMCPTools has %d tools, want %d", got, wantToolCount)
	}
}

func TestMCPToolsAllHaveMetaEntry(t *testing.T) {
	for _, tool := range studioMCPTools {
		if _, ok := toolMetaTable[tool.Name]; !ok {
			t.Errorf("tool %q has no entry in toolMetaTable", tool.Name)
		}
	}
}

func TestMCPMetaTableHasNoOrphan(t *testing.T) {
	nameSet := make(map[string]bool, len(studioMCPTools))
	for _, tool := range studioMCPTools {
		nameSet[tool.Name] = true
	}
	for name := range toolMetaTable {
		if !nameSet[name] {
			t.Errorf("toolMetaTable entry %q has no matching tool definition", name)
		}
	}
}

func TestMCPNewToolsRouting(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		path      string
		bodyParam string
		pathParam string
	}{
		{"list_rate_limit_configs_v2", http.MethodGet, "/rate-limit-configs-v2", "", ""},
		{"upsert_rate_limit_config_v2", http.MethodPost, "/rate-limit-configs-v2", "body", ""},
		{"delete_rate_limit_config_v2", http.MethodDelete, "/rate-limit-configs-v2/{name}", "", "name"},
		{"list_tiers", http.MethodGet, "/tiers", "", ""},
		{"upsert_tier", http.MethodPost, "/tiers", "body", ""},
		{"delete_tier", http.MethodDelete, "/tiers/{name}", "", "name"},
		{"list_upstream_services", http.MethodGet, "/upstream-services", "", ""},
		{"upsert_upstream_service", http.MethodPost, "/upstream-services", "body", ""},
		{"delete_upstream_service", http.MethodDelete, "/upstream-services/{name}", "", "name"},
		{"get_concurrency_config", http.MethodGet, "/admin/concurrency", "", ""},
		{"set_concurrency_config", http.MethodPost, "/admin/concurrency", "body", ""},
		{"list_ai_routes", http.MethodGet, "/ai/routes", "", ""},
		{"set_ai_routes", http.MethodPut, "/ai/routes", "body", ""},
		{"list_ai_quotas", http.MethodGet, "/ai/quotas", "", ""},
		{"upsert_ai_quota", http.MethodPost, "/ai/quotas", "body", ""},
		{"delete_ai_quota", http.MethodDelete, "/ai/quotas/{tenant_id}", "", "tenant_id"},
	}
	for _, tc := range cases {
		meta, ok := toolMetaTable[tc.name]
		if !ok {
			t.Errorf("toolMetaTable missing entry for %q", tc.name)
			continue
		}
		if meta.HTTPMethod != tc.method {
			t.Errorf("%q: HTTPMethod = %q, want %q", tc.name, meta.HTTPMethod, tc.method)
		}
		if meta.Path != tc.path {
			t.Errorf("%q: Path = %q, want %q", tc.name, meta.Path, tc.path)
		}
		if meta.BodyParam != tc.bodyParam {
			t.Errorf("%q: BodyParam = %q, want %q", tc.name, meta.BodyParam, tc.bodyParam)
		}
		if meta.PathParam != tc.pathParam {
			t.Errorf("%q: PathParam = %q, want %q", tc.name, meta.PathParam, tc.pathParam)
		}
	}
}

func TestMCPHandlerToolsList(t *testing.T) {
	s, err := NewServer("http://127.0.0.1:9999", ServerConfig{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})
	rr := httptest.NewRecorder()
	s.MCPHandler(rr, httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body)))

	if rr.Code != http.StatusOK {
		t.Fatalf("MCPHandler returned %d, body: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode tools/list response: %v", err)
	}
	if got := len(resp.Result.Tools); got != wantToolCount {
		t.Errorf("tools/list returned %d tools, want %d", got, wantToolCount)
	}
}
