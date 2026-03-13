package studio

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed ui/dist
var uiFS embed.FS

type ServerConfig struct {
	Targets   []Target `json:"targets"`
	StoreKind string   `json:"store_kind"`
	StorePath string   `json:"store_path"`
}

type DeployRequest struct {
	ReleaseID             string            `json:"release_id,omitempty"`
	InstructionSetVersion string            `json:"instruction_set_version,omitempty"`
	APIVersions           map[string]string `json:"api_versions,omitempty"`
	Levels                []string          `json:"levels"`
	TargetNames           []string          `json:"target_names"`
	Payload               json.RawMessage   `json:"payload,omitempty"`
}

type DeployResult struct {
	Target    string `json:"target"`
	URL       string `json:"url"`
	Status    int    `json:"status"`
	ReleaseID string `json:"release_id"`
	Error     string `json:"error,omitempty"`
}

type DeployRecord struct {
	At        timeJSON       `json:"at"`
	ReleaseID string         `json:"release_id"`
	Levels    []string       `json:"levels"`
	Targets   []string       `json:"targets"`
	Results   []DeployResult `json:"results"`
}

type ReleaseRecord struct {
	ReleaseID             string            `json:"release_id"`
	CreatedAt             timeJSON          `json:"created_at"`
	InstructionSetVersion string            `json:"instruction_set_version,omitempty"`
	APIVersions           map[string]string `json:"api_versions,omitempty"`
	Payload               json.RawMessage   `json:"payload"`
}

type Target struct {
	Name  string   `json:"name"`
	Level string   `json:"level"`
	URLs  []string `json:"urls"`
}

type PaletteBlock struct {
	Type           string            `json:"type"`
	Title          string            `json:"title"`
	Description    string            `json:"description"`
	Category       string            `json:"category"`
	Capability     string            `json:"capability"`
	SupportsNested bool              `json:"supports_nested"`
	NextHints      []string          `json:"next_hints,omitempty"`
	Defaults       map[string]string `json:"defaults"`
}

type SchemaResponse struct {
	Version    string         `json:"version"`
	Categories []string       `json:"categories"`
	Blocks     []PaletteBlock `json:"blocks"`
}

type SuggestionResponse struct {
	StepType        string   `json:"step_type"`
	SuggestedFlows  []string `json:"suggested_flows"`
	SuggestedBlocks []string `json:"suggested_blocks"`
	Notes           []string `json:"notes"`
}

type OpenAPIImportRequest struct {
	Spec string `json:"spec"`
}

type ImportedAPI struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Method string `json:"method"`
}

type OpenAPIImportResponse struct {
	Source string        `json:"source"`
	APIs   []ImportedAPI `json:"apis"`
}

type timeJSON struct{ T string }

func nowJSON() timeJSON { return timeJSON{T: nowUTC().Format(time.RFC3339)} }

type Server struct {
	managementBaseURL *url.URL
	httpClient        *http.Client
	targets           []Target
	store             ReleaseStore

	historyMu sync.RWMutex
	history   []DeployRecord
	seqMu     sync.Mutex
	seq       uint64
}

func NewServer(managementBaseURL string, cfg ServerConfig) (*Server, error) {
	var parsed *url.URL
	if strings.TrimSpace(managementBaseURL) != "" {
		u, err := url.Parse(strings.TrimRight(managementBaseURL, "/"))
		if err != nil {
			return nil, err
		}
		if u.Scheme == "" || u.Host == "" {
			return nil, errors.New("management url must include scheme and host")
		}
		parsed = u
	}
	targets := cfg.Targets
	if len(targets) == 0 && parsed != nil {
		targets = []Target{{Name: "default", Level: "default", URLs: []string{parsed.String()}}}
	}
	if len(targets) == 0 {
		return nil, errors.New("no deployment targets configured")
	}
	for i := range targets {
		if strings.TrimSpace(targets[i].Name) == "" || strings.TrimSpace(targets[i].Level) == "" || len(targets[i].URLs) == 0 {
			return nil, errors.New("invalid target config")
		}
		for _, raw := range targets[i].URLs {
			u, err := url.Parse(strings.TrimSpace(raw))
			if err != nil || u == nil || u.Scheme == "" || u.Host == "" {
				return nil, errors.New("invalid target url: " + raw)
			}
		}
	}
	return &Server{managementBaseURL: parsed, httpClient: http.DefaultClient, targets: targets, store: releaseStoreFromConfig(cfg.StoreKind, cfg.StorePath)}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/schema", s.schemaHandler)
	mux.HandleFunc("/api/suggestions", s.suggestionsHandler)
	mux.HandleFunc("/api/targets", s.targetsHandler)
	mux.HandleFunc("/api/deploy", s.deployHandler)
	mux.HandleFunc("/api/openapi/import", s.importOpenAPIHandler)
	mux.HandleFunc("/api/getAllApis", s.getAllApisProxy)
	mux.HandleFunc("/api/sync", s.syncProxy)

	// Serve the React SPA from the embedded ui/dist directory.
	// Any path that doesn't match a real file falls back to index.html
	// so that the browser can handle it (no server-side routing needed).
	sub, _ := fs.Sub(uiFS, "ui/dist")
	mux.Handle("/", newSPAHandler(http.FS(sub)))
	return mux
}

// spaHandler serves static files from fsys, falling back to index.html for
// any path that does not correspond to a real file (SPA client-side routing).
type spaHandler struct{ fsys http.FileSystem }

func newSPAHandler(fsys http.FileSystem) http.Handler { return &spaHandler{fsys: fsys} }

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Attempt to open the requested path as a real file.
	if r.URL.Path != "/" {
		f, err := h.fsys.Open(r.URL.Path)
		if err == nil {
			f.Close()
			http.FileServer(h.fsys).ServeHTTP(w, r)
			return
		}
	}
	// Fall back to index.html (SPA entry point).
	f, err := h.fsys.Open("index.html")
	if err != nil {
		http.Error(w,
			"UI not built. Run: cd internal/studio/ui && npm install && npm run build",
			http.StatusServiceUnavailable)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.Copy(w, f)
}

func defaultBlocks() []PaletteBlock {
	return []PaletteBlock{
		{Type: "generate_tid", Title: "Generate TID", Description: "Create distributed transaction id", Category: "core", Capability: "gateway-traceability", Defaults: map[string]string{"prefix": "RAH-", "as": "x_tid"}},
		{Type: "extract", Title: "Extract Value", Description: "Extract from headers/query/host/body", Category: "core", Capability: "request-context", Defaults: map[string]string{"condition": "header.X-Tenant || query.t_id || host.subdomain", "as": "tenant_key"}},
		{Type: "if", Title: "If / Else", Description: "Condition based routing between child flows", Category: "control", Capability: "branching", SupportsNested: true, Defaults: map[string]string{"condition": "token.length > 50", "then": "jwt_validation", "else": "legacy_auth"}},
		{Type: "switch", Title: "Switch", Description: "Branch by computed status", Category: "control", Capability: "multi-branch", SupportsNested: true, Defaults: map[string]string{"as": "jwt_status", "cases": "valid=extract_and_mask,expired=return_401"}},
		{Type: "proxy", Title: "Proxy", Description: "Forward request to upstream", Category: "integration", Capability: "upstream-proxy", Defaults: map[string]string{"to": "var.target_url"}},
		{Type: "mcp_call", Title: "MCP Call", Description: "Call MCP or virtual MCP capability", Category: "ai", Capability: "mcp", Defaults: map[string]string{"provider": "virtual-mcp", "tool": "catalog.search", "as": "mcp_result"}},
	}
}

func (s *Server) schemaHandler(w http.ResponseWriter, _ *http.Request) {
	blocks := defaultBlocks()
	seen := map[string]struct{}{}
	cats := make([]string, 0)
	for _, b := range blocks {
		if _, ok := seen[b.Category]; !ok {
			seen[b.Category] = struct{}{}
			cats = append(cats, b.Category)
		}
	}
	sort.Strings(cats)
	_ = json.NewEncoder(w).Encode(SchemaResponse{Version: "v2-modular-ui", Categories: cats, Blocks: blocks})
}

func (s *Server) suggestionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stepType := r.URL.Query().Get("step_type")
	resp := SuggestionResponse{StepType: stepType, Notes: []string{"Use sections separately: flows, APIs, releases/deployments."}}
	switch stepType {
	case "if":
		resp.SuggestedFlows = []string{"jwt_validation", "legacy_auth"}
		resp.SuggestedBlocks = []string{"switch", "proxy"}
	default:
		resp.SuggestedFlows = []string{"routing_logic"}
		resp.SuggestedBlocks = []string{"proxy"}
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) targetsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	releases, _ := s.store.List(context.Background())
	s.historyMu.RLock()
	hist := append([]DeployRecord(nil), s.history...)
	s.historyMu.RUnlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"targets": s.targets, "history": hist, "releases": releases, "stores_supported": []string{"memory", "file"}})
}

func (s *Server) importOpenAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req OpenAPIImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	apis, source, err := parseOpenAPISpec(req.Spec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(OpenAPIImportResponse{Source: source, APIs: apis})
}

func parseOpenAPISpec(spec string) ([]ImportedAPI, string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, "", errors.New("spec is required")
	}
	var data map[string]any
	source := "json"
	if err := json.Unmarshal([]byte(spec), &data); err != nil {
		apis, err2 := parseOpenAPIYAML(spec)
		if err2 != nil {
			return nil, "", errors.New("spec must be valid OpenAPI JSON or YAML")
		}
		return apis, "yaml", nil
	}
	pathsRaw, ok := data["paths"].(map[string]any)
	if !ok {
		return nil, source, errors.New("openapi spec missing paths object")
	}
	apis := make([]ImportedAPI, 0)
	for p, methodsRaw := range pathsRaw {
		methodsMap, ok := methodsRaw.(map[string]any)
		if !ok {
			continue
		}
		for m, opRaw := range methodsMap {
			ml := strings.ToUpper(m)
			switch ml {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead, http.MethodOptions:
			default:
				continue
			}
			name := strings.ToLower(ml) + "_" + strings.ReplaceAll(strings.Trim(p, "/"), "/", "_")
			if opMap, ok := opRaw.(map[string]any); ok {
				if opID, ok := opMap["operationId"].(string); ok && strings.TrimSpace(opID) != "" {
					name = opID
				}
			}
			apis = append(apis, ImportedAPI{Name: name, Path: p, Method: ml})
		}
	}
	sort.Slice(apis, func(i, j int) bool {
		if apis[i].Path == apis[j].Path {
			return apis[i].Method < apis[j].Method
		}
		return apis[i].Path < apis[j].Path
	})
	return apis, source, nil
}

func parseOpenAPIYAML(spec string) ([]ImportedAPI, error) {
	lines := strings.Split(spec, "\n")
	inPaths := false
	pathsIndent := -1
	pathIndent := -1
	methodIndent := -1
	currentPath := ""
	currentMethod := ""
	apis := make([]ImportedAPI, 0)

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if strings.HasPrefix(trim, "paths:") {
			inPaths = true
			pathsIndent = indent
			continue
		}
		if !inPaths {
			continue
		}
		if indent <= pathsIndent {
			// exited paths block
			break
		}

		if strings.HasPrefix(trim, "/") && strings.HasSuffix(trim, ":") {
			if pathIndent == -1 {
				pathIndent = indent
			}
			if indent == pathIndent {
				currentPath = strings.TrimSuffix(trim, ":")
				currentMethod = ""
				continue
			}
		}
		if currentPath == "" {
			continue
		}

		if strings.HasSuffix(trim, ":") {
			m := strings.ToUpper(strings.TrimSuffix(trim, ":"))
			switch m {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead, http.MethodOptions:
				if methodIndent == -1 {
					methodIndent = indent
				}
				if indent == methodIndent {
					currentMethod = m
					name := strings.ToLower(m) + "_" + strings.ReplaceAll(strings.Trim(currentPath, "/"), "/", "_")
					apis = append(apis, ImportedAPI{Name: name, Path: currentPath, Method: m})
					continue
				}
			}
		}

		if currentMethod != "" && strings.HasPrefix(trim, "operationId:") {
			op := strings.TrimSpace(strings.TrimPrefix(trim, "operationId:"))
			op = strings.Trim(op, "\"'")
			if op != "" && len(apis) > 0 {
				apis[len(apis)-1].Name = op
			}
		}
	}

	if len(apis) == 0 {
		return nil, errors.New("openapi yaml paths not found")
	}
	sort.Slice(apis, func(i, j int) bool {
		if apis[i].Path == apis[j].Path {
			return apis[i].Method < apis[j].Method
		}
		return apis[i].Path < apis[j].Path
	})
	return apis, nil
}

func (s *Server) deployHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	rec, err := s.resolveRelease(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	selected := s.selectTargets(req.Levels, req.TargetNames)
	if len(selected) == 0 {
		http.Error(w, "no matching targets", http.StatusBadRequest)
		return
	}

	results := make([]DeployResult, 0)
	for _, t := range selected {
		for _, raw := range t.URLs {
			targetURL, err := buildTargetURL(raw, "/sync", "")
			if err != nil {
				results = append(results, DeployResult{Target: t.Name, URL: raw, ReleaseID: rec.ReleaseID, Error: err.Error()})
				continue
			}
			hReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(rec.Payload))
			if err != nil {
				results = append(results, DeployResult{Target: t.Name, URL: targetURL, ReleaseID: rec.ReleaseID, Error: err.Error()})
				continue
			}
			hReq.Header.Set("Content-Type", "application/json")
			resp, err := s.httpClient.Do(hReq)
			if err != nil {
				results = append(results, DeployResult{Target: t.Name, URL: targetURL, ReleaseID: rec.ReleaseID, Error: err.Error()})
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			results = append(results, DeployResult{Target: t.Name, URL: targetURL, ReleaseID: rec.ReleaseID, Status: resp.StatusCode})
		}
	}
	s.historyMu.Lock()
	s.history = append([]DeployRecord{{At: nowJSON(), ReleaseID: rec.ReleaseID, Levels: req.Levels, Targets: req.TargetNames, Results: results}}, s.history...)
	if len(s.history) > 200 {
		s.history = s.history[:200]
	}
	s.historyMu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"release_id": rec.ReleaseID, "results": results})
}

func (s *Server) resolveRelease(req DeployRequest) (ReleaseRecord, error) {
	rid := strings.TrimSpace(req.ReleaseID)
	if len(req.Payload) > 0 {
		if rid == "" {
			rid = s.nextReleaseID()
		}
		rec := ReleaseRecord{ReleaseID: rid, CreatedAt: nowJSON(), InstructionSetVersion: strings.TrimSpace(req.InstructionSetVersion), APIVersions: req.APIVersions, Payload: req.Payload}
		if err := s.store.Put(context.Background(), rec); err != nil {
			return ReleaseRecord{}, err
		}
		return rec, nil
	}
	if rid == "" {
		return ReleaseRecord{}, errors.New("payload or release_id is required")
	}
	return s.store.Get(context.Background(), rid)
}

func (s *Server) nextReleaseID() string {
	s.seqMu.Lock()
	defer s.seqMu.Unlock()
	s.seq++
	return "rel-" + strconv.FormatUint(s.seq, 10)
}

func (s *Server) selectTargets(levels, names []string) []Target {
	ls, ns := map[string]struct{}{}, map[string]struct{}{}
	for _, l := range levels {
		if t := strings.TrimSpace(l); t != "" {
			ls[t] = struct{}{}
		}
	}
	for _, n := range names {
		if t := strings.TrimSpace(n); t != "" {
			ns[t] = struct{}{}
		}
	}
	out := make([]Target, 0)
	for _, t := range s.targets {
		_, okL := ls[t.Level]
		_, okN := ns[t.Name]
		if (len(ls) == 0 && len(ns) == 0) || okL || okN {
			out = append(out, t)
		}
	}
	return out
}

func buildTargetURL(baseRaw, endpointPath, rawQuery string) (string, error) {
	baseURL, err := url.Parse(strings.TrimSpace(baseRaw))
	if err != nil || baseURL == nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return "", errors.New("invalid target url")
	}
	joined := *baseURL
	joined.Path = joinURLPath(baseURL.Path, endpointPath)
	joined.RawQuery = rawQuery
	return joined.String(), nil
}

func joinURLPath(basePath, endpointPath string) string {
	if basePath == "" || basePath == "/" {
		if strings.HasPrefix(endpointPath, "/") {
			return endpointPath
		}
		return "/" + endpointPath
	}
	b := strings.TrimRight(basePath, "/")
	e := strings.TrimLeft(endpointPath, "/")
	return b + "/" + e
}

func (s *Server) getAllApisProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyToDefault(w, r, http.MethodGet, "/getAllApis")
}
func (s *Server) syncProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyToDefault(w, r, http.MethodPost, "/sync")
}
func (s *Server) proxyToDefault(w http.ResponseWriter, r *http.Request, method, path string) {
	if r.Method != method {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	base := ""
	if s.managementBaseURL != nil {
		base = s.managementBaseURL.String()
	} else if len(s.targets) > 0 && len(s.targets[0].URLs) > 0 {
		base = s.targets[0].URLs[0]
	}
	if base == "" {
		http.Error(w, "no default management endpoint", http.StatusBadGateway)
		return
	}
	targetURL, err := buildTargetURL(base, path, r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid management endpoint", http.StatusBadGateway)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), method, targetURL, r.Body)
	if err != nil {
		http.Error(w, "Failed to build proxy request", http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()
	resp, err := s.httpClient.Do(req)
	if err != nil {
		http.Error(w, "Failed to call gateway management API", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
