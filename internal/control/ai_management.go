package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"rah/internal/config"
	"rah/internal/mcpreg"
	"strconv"
	"strings"
	"time"
)

// aiResponse is the standard envelope for all /ai/* endpoints.
type aiResponse struct {
	OK    bool        `json:"ok"`
	Data  interface{} `json:"data,omitempty"`
	Error string      `json:"error,omitempty"`
}

// persistenceKeys used in the DomainAIConfig datastore domain.
const (
	aiKeyLLMModels  = "llm_models"
	aiKeyMCPServers = "mcp_servers"
)

// persistenceKeys used in the DomainMCPTools datastore domain.
const (
	mcpToolsKeyAPITools        = "api_tools"
	mcpToolsKeyVirtualServers  = "virtual_servers"
)

func writeAIOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(aiResponse{OK: true, Data: data})
}

func writeAIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(aiResponse{OK: false, Error: msg})
}

// RegisterAIRoutes wires the /ai/ route group into mux.
//
// cfgMgr is the live config manager whose LLM catalog is mutated in place.
// rebake is called (in a goroutine) after every mutation so that flows
// compiled against the model/MCP catalog see the new values immediately.
// dsm is optional: when non-nil and the DomainAIConfig domain is configured,
// mutations are persisted so they survive gateway restarts.
// mcpReg is the in-memory registry for virtual MCP servers and API tools.
func RegisterAIRoutes(mux *http.ServeMux, cfgMgr *config.Manager, rebake func(), dsm *DataStoreManager, mcpReg *mcpreg.Registry) {
	// Bootstrap persisted AI config before wiring handlers — this merges
	// runtime-added models/servers on top of the file-based config.
	if dsm != nil && dsm.IsConfigured(config.DomainAIConfig) {
		if err := loadPersistedAIConfig(cfgMgr, dsm); err != nil {
			log.Printf("[AI] failed to load persisted ai_config: %v", err)
		}
	}

	// Bootstrap persisted MCP tools and virtual servers.
	if mcpReg != nil && dsm != nil && dsm.IsConfigured(config.DomainMCPTools) {
		if err := LoadFromDatastore(context.Background(), dsm, mcpReg); err != nil {
			log.Printf("[AI] failed to load persisted mcp_tools: %v", err)
		}
	}

	// LLM model routes.
	mux.HandleFunc("/ai/llm/models", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			llmListModelsHandler(w, r, cfgMgr)
		case http.MethodPost:
			llmUpsertModelHandler(w, r, cfgMgr, rebake, dsm)
		default:
			writeAIError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// DELETE /ai/llm/models/{slug}  — note trailing slash to match sub-paths.
	mux.HandleFunc("/ai/llm/models/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeAIError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		slug := strings.TrimPrefix(r.URL.Path, "/ai/llm/models/")
		if slug == "" {
			writeAIError(w, http.StatusBadRequest, "slug required in path")
			return
		}
		llmDeleteModelHandler(w, r, cfgMgr, slug, rebake, dsm)
	})

	// MCP server routes.
	mux.HandleFunc("/ai/mcp/servers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			mcpListServersHandler(w, r, cfgMgr)
		case http.MethodPost:
			mcpUpsertServerHandler(w, r, cfgMgr, rebake, dsm)
		default:
			writeAIError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// Sub-path routes for individual MCP servers.
	mux.HandleFunc("/ai/mcp/servers/", func(w http.ResponseWriter, r *http.Request) {
		tail := strings.TrimPrefix(r.URL.Path, "/ai/mcp/servers/")
		if tail == "" {
			writeAIError(w, http.StatusBadRequest, "alias required in path")
			return
		}

		// Dispatch on suffix: {alias}/tools, {alias}/ping, or bare {alias} (DELETE).
		switch {
		case strings.HasSuffix(tail, "/tools"):
			alias := strings.TrimSuffix(tail, "/tools")
			if r.Method != http.MethodGet {
				writeAIError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			mcpProbeToolsHandler(w, r, cfgMgr, alias)

		case strings.HasSuffix(tail, "/ping"):
			alias := strings.TrimSuffix(tail, "/ping")
			if r.Method != http.MethodPost {
				writeAIError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			mcpPingHandler(w, r, cfgMgr, alias)

		default:
			// bare alias — only DELETE is supported.
			if r.Method != http.MethodDelete {
				writeAIError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			alias := tail
			mcpDeleteServerHandler(w, r, cfgMgr, alias, rebake, dsm)
		}
	})

	// API Tools (global catalog) — only wired when mcpReg is provided.
	if mcpReg != nil {
		mux.HandleFunc("/ai/tools/apis", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				writeAIOK(w, mcpReg.ListAPITools())
			case http.MethodPost:
				apiToolUpsertHandler(w, r, mcpReg, rebake, dsm)
			default:
				writeAIError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
		})

		mux.HandleFunc("/ai/tools/apis/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				writeAIError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			name := strings.TrimPrefix(r.URL.Path, "/ai/tools/apis/")
			if name == "" {
				writeAIError(w, http.StatusBadRequest, "tool name required in path")
				return
			}
			apiToolDeleteHandler(w, r, mcpReg, name, rebake, dsm)
		})

		// Virtual MCP Servers.
		mux.HandleFunc("/ai/mcp/virtual", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				tenantID := parseTenantIDQuery(r)
				writeAIOK(w, mcpReg.ListServers(tenantID))
			case http.MethodPost:
				virtualServerUpsertHandler(w, r, mcpReg, rebake, dsm)
			default:
				writeAIError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
		})

		mux.HandleFunc("/ai/mcp/virtual/", func(w http.ResponseWriter, r *http.Request) {
			name := strings.TrimPrefix(r.URL.Path, "/ai/mcp/virtual/")
			if name == "" {
				writeAIError(w, http.StatusBadRequest, "server name required in path")
				return
			}
			tenantID := parseTenantIDQuery(r)
			switch r.Method {
			case http.MethodGet:
				def, ok := mcpReg.GetServer(tenantID, name)
				if !ok {
					writeAIError(w, http.StatusNotFound, fmt.Sprintf("virtual MCP server %q not found", name))
					return
				}
				writeAIOK(w, def)
			case http.MethodDelete:
				if !mcpReg.DeleteServer(tenantID, name) {
					writeAIError(w, http.StatusNotFound, fmt.Sprintf("virtual MCP server %q not found", name))
					return
				}
				log.Printf("[AI] deleted virtual MCP server %q (tenantID=%d)", name, tenantID)
				persistMCPTools(mcpReg, dsm)
				if rebake != nil {
					go rebake()
				}
				writeAIOK(w, map[string]string{"name": name})
			default:
				writeAIError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
		})
	}
}

// ─── LLM handlers ────────────────────────────────────────────────────────────

func llmListModelsHandler(w http.ResponseWriter, _ *http.Request, cfgMgr *config.Manager) {
	writeAIOK(w, cfgMgr.LLM().Models)
}

func llmUpsertModelHandler(w http.ResponseWriter, r *http.Request, cfgMgr *config.Manager, rebake func(), dsm *DataStoreManager) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024))
	if err != nil {
		writeAIError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	// Support both single model and array of models.
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		// Batch registration
		var models []config.LLMModelConfig
		if err := json.Unmarshal(body, &models); err != nil {
			writeAIError(w, http.StatusBadRequest, "invalid JSON array: "+err.Error())
			return
		}
		registered := make([]string, 0, len(models))
		for _, model := range models {
			if model.Alias == "" {
				writeAIError(w, http.StatusBadRequest, "each model must have a non-empty alias")
				return
			}
			cfgMgr.UpsertLLMModel(model)
			registered = append(registered, model.Alias)
		}
		log.Printf("[AI] batch upserted %d LLM models: %v", len(models), registered)
		persistAIModels(cfgMgr, dsm)
		go rebake()
		writeAIOK(w, map[string]any{"registered": registered, "count": len(registered)})
		return
	}

	// Single model registration
	var model config.LLMModelConfig
	if err := json.Unmarshal(body, &model); err != nil {
		writeAIError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if model.Alias == "" {
		writeAIError(w, http.StatusBadRequest, "alias must not be empty")
		return
	}

	cfgMgr.UpsertLLMModel(model)
	log.Printf("[AI] upserted LLM model %q", model.Alias)

	persistAIModels(cfgMgr, dsm)
	go rebake()

	writeAIOK(w, model)
}

func llmDeleteModelHandler(w http.ResponseWriter, _ *http.Request, cfgMgr *config.Manager, slug string, rebake func(), dsm *DataStoreManager) {
	if !cfgMgr.DeleteLLMModel(slug) {
		writeAIError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", slug))
		return
	}
	log.Printf("[AI] deleted LLM model %q", slug)

	persistAIModels(cfgMgr, dsm)
	go rebake()

	writeAIOK(w, map[string]string{"alias": slug})
}

// ─── MCP handlers ─────────────────────────────────────────────────────────────

func mcpListServersHandler(w http.ResponseWriter, _ *http.Request, cfgMgr *config.Manager) {
	writeAIOK(w, cfgMgr.LLM().MCPServers)
}

func mcpUpsertServerHandler(w http.ResponseWriter, r *http.Request, cfgMgr *config.Manager, rebake func(), dsm *DataStoreManager) {
	var srv config.MCPServerConfig
	if err := json.NewDecoder(r.Body).Decode(&srv); err != nil {
		writeAIError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if srv.Alias == "" {
		writeAIError(w, http.StatusBadRequest, "alias must not be empty")
		return
	}

	cfgMgr.UpsertMCPServer(srv)
	log.Printf("[AI] upserted MCP server %q", srv.Alias)

	persistAIMCPServers(cfgMgr, dsm)
	go rebake()

	writeAIOK(w, srv)
}

func mcpDeleteServerHandler(w http.ResponseWriter, _ *http.Request, cfgMgr *config.Manager, alias string, rebake func(), dsm *DataStoreManager) {
	if !cfgMgr.DeleteMCPServer(alias) {
		writeAIError(w, http.StatusNotFound, fmt.Sprintf("MCP server %q not found", alias))
		return
	}
	log.Printf("[AI] deleted MCP server %q", alias)

	persistAIMCPServers(cfgMgr, dsm)
	go rebake()

	writeAIOK(w, map[string]string{"alias": alias})
}

// mcpProbeToolsHandler sends a JSON-RPC tools/list call to the MCP server
// identified by alias and returns the result.tools array.
func mcpProbeToolsHandler(w http.ResponseWriter, _ *http.Request, cfgMgr *config.Manager, alias string) {
	srv := findMCPServer(cfgMgr, alias)
	if srv == nil {
		writeAIError(w, http.StatusNotFound, fmt.Sprintf("MCP server %q not found", alias))
		return
	}
	if srv.URL == "" {
		writeAIError(w, http.StatusBadRequest, fmt.Sprintf("MCP server %q has no URL configured", alias))
		return
	}

	timeout := time.Duration(srv.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]interface{}{},
	})

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, bytes.NewReader(payload))
	if err != nil {
		writeAIError(w, http.StatusInternalServerError, "failed to build request: "+err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeAIError(w, http.StatusBadGateway, "MCP server unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		writeAIError(w, http.StatusBadGateway, "failed to read MCP response: "+err.Error())
		return
	}

	// Parse the JSON-RPC response.
	var rpcResp struct {
		Result struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		writeAIError(w, http.StatusBadGateway, "invalid JSON-RPC response from MCP server: "+err.Error())
		return
	}
	if rpcResp.Error != nil {
		writeAIError(w, http.StatusBadGateway,
			fmt.Sprintf("MCP server returned error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message))
		return
	}

	writeAIOK(w, rpcResp.Result.Tools)
}

// mcpPingHandler sends a minimal JSON-RPC initialize probe to the MCP server
// and returns the measured round-trip latency.
func mcpPingHandler(w http.ResponseWriter, _ *http.Request, cfgMgr *config.Manager, alias string) {
	srv := findMCPServer(cfgMgr, alias)
	if srv == nil {
		writeAIError(w, http.StatusNotFound, fmt.Sprintf("MCP server %q not found", alias))
		return
	}
	if srv.URL == "" {
		writeAIError(w, http.StatusBadRequest, fmt.Sprintf("MCP server %q has no URL configured", alias))
		return
	}

	timeout := time.Duration(srv.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]string{"name": "rah-gateway", "version": "1.0"},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, bytes.NewReader(payload))
	if err != nil {
		writeAIError(w, http.StatusInternalServerError, "failed to build request: "+err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		writeAIOK(w, map[string]interface{}{
			"alias":      alias,
			"reachable":  false,
			"latency_ms": latencyMs,
			"error":      err.Error(),
		})
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	writeAIOK(w, map[string]interface{}{
		"alias":       alias,
		"reachable":   true,
		"status_code": resp.StatusCode,
		"latency_ms":  latencyMs,
	})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// findMCPServer returns a pointer to the named server within a snapshot, or nil.
func findMCPServer(cfgMgr *config.Manager, alias string) *config.MCPServerConfig {
	servers := cfgMgr.LLM().MCPServers
	for i := range servers {
		if servers[i].Alias == alias {
			return &servers[i]
		}
	}
	return nil
}

// ─── Persistence helpers ──────────────────────────────────────────────────────

// persistAIModels writes the current model catalog to the DomainAIConfig store.
// Errors are logged but do not affect the in-memory state.
func persistAIModels(cfgMgr *config.Manager, dsm *DataStoreManager) {
	if dsm == nil || !dsm.IsConfigured(config.DomainAIConfig) {
		return
	}
	data, err := json.Marshal(cfgMgr.LLM().Models)
	if err != nil {
		log.Printf("[AI] failed to marshal LLM models for persistence: %v", err)
		return
	}
	if err := dsm.PutGlobal(context.Background(), config.DomainAIConfig, aiKeyLLMModels, data); err != nil {
		log.Printf("[AI] failed to persist LLM models: %v", err)
	}
}

// persistAIMCPServers writes the current MCP server list to the DomainAIConfig store.
func persistAIMCPServers(cfgMgr *config.Manager, dsm *DataStoreManager) {
	if dsm == nil || !dsm.IsConfigured(config.DomainAIConfig) {
		return
	}
	data, err := json.Marshal(cfgMgr.LLM().MCPServers)
	if err != nil {
		log.Printf("[AI] failed to marshal MCP servers for persistence: %v", err)
		return
	}
	if err := dsm.PutGlobal(context.Background(), config.DomainAIConfig, aiKeyMCPServers, data); err != nil {
		log.Printf("[AI] failed to persist MCP servers: %v", err)
	}
}

// loadPersistedAIConfig reads LLM models and MCP servers from the datastore
// and merges them on top of the file-based catalog. File-based entries that
// share the same slug/alias are not overwritten — runtime additions accumulate.
func loadPersistedAIConfig(cfgMgr *config.Manager, dsm *DataStoreManager) error {
	ctx := context.Background()

	if raw, ok, err := dsm.GetGlobal(ctx, config.DomainAIConfig, aiKeyLLMModels); err == nil && ok {
		var models []config.LLMModelConfig
		if err := json.Unmarshal(raw, &models); err != nil {
			log.Printf("[AI] skipping unparseable persisted LLM models: %v", err)
		} else {
			for _, m := range models {
				cfgMgr.UpsertLLMModel(m)
			}
			log.Printf("[AI] loaded %d persisted LLM model(s)", len(models))
		}
	}

	if raw, ok, err := dsm.GetGlobal(ctx, config.DomainAIConfig, aiKeyMCPServers); err == nil && ok {
		var servers []config.MCPServerConfig
		if err := json.Unmarshal(raw, &servers); err != nil {
			log.Printf("[AI] skipping unparseable persisted MCP servers: %v", err)
		} else {
			for _, s := range servers {
				cfgMgr.UpsertMCPServer(s)
			}
			log.Printf("[AI] loaded %d persisted MCP server(s)", len(servers))
		}
	}

	return nil
}

// ─── MCPReg handlers ──────────────────────────────────────────────────────────

func apiToolUpsertHandler(w http.ResponseWriter, r *http.Request, reg *mcpreg.Registry, rebake func(), dsm *DataStoreManager) {
	var tool mcpreg.APIToolDef
	if err := json.NewDecoder(r.Body).Decode(&tool); err != nil {
		writeAIError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if tool.Name == "" {
		writeAIError(w, http.StatusBadRequest, "name must not be empty")
		return
	}

	reg.UpsertAPITool(tool)
	log.Printf("[AI] upserted API tool %q", tool.Name)

	persistMCPTools(reg, dsm)
	if rebake != nil {
		go rebake()
	}

	writeAIOK(w, tool)
}

func apiToolDeleteHandler(w http.ResponseWriter, _ *http.Request, reg *mcpreg.Registry, name string, rebake func(), dsm *DataStoreManager) {
	if !reg.DeleteAPITool(name) {
		writeAIError(w, http.StatusNotFound, fmt.Sprintf("API tool %q not found", name))
		return
	}
	log.Printf("[AI] deleted API tool %q", name)

	persistMCPTools(reg, dsm)
	if rebake != nil {
		go rebake()
	}

	writeAIOK(w, map[string]string{"name": name})
}

func virtualServerUpsertHandler(w http.ResponseWriter, r *http.Request, reg *mcpreg.Registry, rebake func(), dsm *DataStoreManager) {
	var def mcpreg.VirtualMCPServerDef
	if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
		writeAIError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if def.Name == "" {
		writeAIError(w, http.StatusBadRequest, "name must not be empty")
		return
	}

	reg.UpsertServer(def)
	log.Printf("[AI] upserted virtual MCP server %q (tenantID=%d)", def.Name, def.TenantID)

	persistMCPTools(reg, dsm)
	if rebake != nil {
		go rebake()
	}

	writeAIOK(w, def)
}

// parseTenantIDQuery reads the ?tenant_id=N query parameter. Returns 0 if absent or invalid.
func parseTenantIDQuery(r *http.Request) uint16 {
	raw := r.URL.Query().Get("tenant_id")
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseUint(raw, 10, 16)
	if err != nil {
		return 0
	}
	return uint16(v)
}

// ─── MCPReg persistence helpers ───────────────────────────────────────────────

// persistMCPTools writes the full mcpReg snapshot (API tools + virtual servers)
// to the DomainMCPTools store. Errors are logged; they do not affect in-memory state.
func persistMCPTools(reg *mcpreg.Registry, dsm *DataStoreManager) {
	if dsm == nil || !dsm.IsConfigured(config.DomainMCPTools) {
		return
	}
	ctx := context.Background()

	if data, err := json.Marshal(reg.ListAPITools()); err != nil {
		log.Printf("[AI] failed to marshal API tools for persistence: %v", err)
	} else if err := dsm.PutGlobal(ctx, config.DomainMCPTools, mcpToolsKeyAPITools, data); err != nil {
		log.Printf("[AI] failed to persist API tools: %v", err)
	}

	if data, err := json.Marshal(reg.ListServers(0)); err != nil {
		log.Printf("[AI] failed to marshal virtual MCP servers for persistence: %v", err)
	} else if err := dsm.PutGlobal(ctx, config.DomainMCPTools, mcpToolsKeyVirtualServers, data); err != nil {
		log.Printf("[AI] failed to persist virtual MCP servers: %v", err)
	}
}

// LoadFromDatastore populates reg from the DomainMCPTools store.
// Errors are logged but do not cause a fatal — best-effort load at startup.
func LoadFromDatastore(ctx context.Context, dsm *DataStoreManager, reg *mcpreg.Registry) error {
	if dsm == nil || !dsm.IsConfigured(config.DomainMCPTools) {
		return nil
	}

	if raw, ok, err := dsm.GetGlobal(ctx, config.DomainMCPTools, mcpToolsKeyAPITools); err == nil && ok {
		var tools []mcpreg.APIToolDef
		if err := json.Unmarshal(raw, &tools); err != nil {
			log.Printf("[AI] skipping unparseable persisted API tools: %v", err)
		} else {
			for _, t := range tools {
				reg.UpsertAPITool(t)
			}
			log.Printf("[AI] loaded %d persisted API tool(s)", len(tools))
		}
	}

	if raw, ok, err := dsm.GetGlobal(ctx, config.DomainMCPTools, mcpToolsKeyVirtualServers); err == nil && ok {
		var servers []mcpreg.VirtualMCPServerDef
		if err := json.Unmarshal(raw, &servers); err != nil {
			log.Printf("[AI] skipping unparseable persisted virtual MCP servers: %v", err)
		} else {
			for _, s := range servers {
				reg.UpsertServer(s)
			}
			log.Printf("[AI] loaded %d persisted virtual MCP server(s)", len(servers))
		}
	}

	return nil
}
