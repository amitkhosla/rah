package registry

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// TenantServer exposes the RegistryManager over HTTP for management-plane
// operations. All mutating endpoints hold the RegistryManager mutex for the
// duration of the write; reads are answered from the atomic snapshot and
// therefore do not block the hot path.
type TenantServer struct {
	mgr *RegistryManager

	// ExtraSubHandler is an optional http.Handler invoked for /tenants/{alias}/...
	// paths that are not recognised by tenantSubHandler's built-in dispatch table.
	// Set this before calling RegisterHandlers to extend the sub-path routing
	// without registering an additional /tenants/ pattern on the same mux.
	ExtraSubHandler http.Handler
}

// NewTenantServer creates a TenantServer backed by the given RegistryManager.
func NewTenantServer(mgr *RegistryManager) *TenantServer {
	return &TenantServer{mgr: mgr}
}

// ─── Request / Response shapes ────────────────────────────────────────────────

type upsertTenantRequest struct {
	Aliases     []string          `json:"aliases"`      // first alias is the canonical one
	ServiceURLs map[string]string `json:"service_urls"` // name → URL    e.g. "primary" → "https://..."
	Identifiers map[string]string `json:"identifiers"`  // name → secret e.g. "api_key" → "sk-..."
	Metadata    map[string]string `json:"metadata"`     // arbitrary key-value metadata
}

type addAliasRequest struct {
	Alias string `json:"alias"` // the new alias to link to the same tenant
}

type setRateLimitConfigRequest struct {
	Name        string `json:"name"`
	PerSec      uint32 `json:"per_sec"`
	PerMin      uint32 `json:"per_min"`
	BurstFactor uint16 `json:"burst_factor"` // 100=1x, 150=1.5x, 200=2x; 0 treated as 100
}

type setModifierRequest struct {
	ScalePct   int16 `json:"scale_pct"`   // %-adjustment: +20 → 120%, -50 → 50%, 0 = none
	Blocked    bool  `json:"blocked"`     // block all traffic from this tenant
	RLDisabled bool  `json:"rl_disabled"` // disable rate limiting for this tenant
}

type upsertRateLimitOverrideRequest struct {
	RateLimitName string `json:"rate_limit"`   // named config to override (e.g. "premium_api")
	RatePerSec    uint32 `json:"rate_per_sec"`
	RatePerMin    uint32 `json:"rate_per_min"`
	Blocked       bool   `json:"blocked"`      // access denied for this API / endpoint
	RLDisabled    bool   `json:"rl_disabled"`  // rate limiting off (unlimited)
	Custom        bool   `json:"custom_value"` // use entry's rate values instead of global default
}

type tenantInfoResponse struct {
	TenantID   uint16            `json:"tenant_id"`
	Aliases    []string          `json:"aliases"`
	Properties map[string]string `json:"properties"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// UpsertTenantHandler handles POST /tenants
//
// Creates or updates a tenant including all URL endpoints and metadata.
func (s *TenantServer) UpsertTenantHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req upsertTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Aliases) == 0 {
		http.Error(w, "aliases must not be empty", http.StatusBadRequest)
		return
	}
	s.mgr.UpsertTenantState(req.Aliases, req.ServiceURLs, req.Identifiers, req.Metadata)
	jsonOK(w, map[string]string{"status": "ok"})
}

// DeleteTenantHandler handles DELETE /tenants/{alias}
func (s *TenantServer) DeleteTenantHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	alias := aliasFromPath(r.URL.Path, "/tenants/")
	if alias == "" {
		http.Error(w, "alias required in path", http.StatusBadRequest)
		return
	}
	s.mgr.DeleteTenant(alias)
	jsonOK(w, map[string]string{"status": "ok"})
}

// AddAliasHandler handles POST /tenants/{alias}/aliases
//
// Links a new hostname / identifier to the same tenant identified by {alias}.
func (s *TenantServer) AddAliasHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	existing := aliasFromPath(r.URL.Path, "/tenants/")
	existing = strings.TrimSuffix(existing, "/aliases")
	if existing == "" {
		http.Error(w, "existing alias required in path", http.StatusBadRequest)
		return
	}
	var req addAliasRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Alias == "" {
		http.Error(w, "alias must not be empty", http.StatusBadRequest)
		return
	}
	s.mgr.AddAlias(existing, req.Alias)
	jsonOK(w, map[string]string{"status": "ok"})
}

// GetTenantHandler handles GET /tenants/{alias}
//
// Returns the TenantID and all stored properties for the tenant.
// Uses the atomic registry snapshot — zero locks, zero allocation on hot path.
func (s *TenantServer) GetTenantHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	alias := aliasFromPath(r.URL.Path, "/tenants/")
	if alias == "" {
		http.Error(w, "alias required in path", http.StatusBadRequest)
		return
	}

	reg := State.Active.Load()
	if reg == nil {
		http.Error(w, "registry not initialised", http.StatusServiceUnavailable)
		return
	}

	tID, found := reg.Aliases.Lookup(alias)
	if !found {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	// Collect all properties for this tenant by scanning all three store rows.
	props := make(map[string]string)
	collectStoreRow := func(store *PropStore, prefix string) {
		if store.Stride == 0 {
			return
		}
		rowStart := uint32(tID) * store.Stride
		if rowStart+store.Stride > uint32(len(store.Matrix)) {
			return
		}
		for kID := uint32(0); kID < store.Stride; kID++ {
			vID := store.Matrix[rowStart+kID]
			if vID == 0 || int(vID) >= len(reg.ValuePool) {
				continue
			}
			key := storeKeyNameForID(store, uint16(kID))
			if key != "" {
				props[prefix+key] = string(reg.ValuePool[vID])
			}
		}
	}
	collectStoreRow(&reg.URLs, "url:")
	collectStoreRow(&reg.IDs, "id:")
	collectStoreRow(&reg.Meta, "meta:")

	// Collect all aliases for this tenant from the tenantData mirror.
	var aliases []string
	if rec := s.mgr.GetTenantRecord(tID); rec != nil {
		aliases = rec.Aliases
	}

	jsonOK(w, tenantInfoResponse{TenantID: tID, Aliases: aliases, Properties: props})
}

// SetRateLimitConfigHandler handles POST /rate-limit-configs
//
// Creates or updates a system-wide rate limit config by name.
func (s *TenantServer) SetRateLimitConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req setRateLimitConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	bf := req.BurstFactor
	if bf == 0 {
		bf = 100 // default: 1x
	}
	id := s.mgr.UpsertNamedRateLimitConfig(req.Name, RateLimitConfig{
		PerSec:      req.PerSec,
		PerMin:      req.PerMin,
		BurstFactor: bf,
	})
	jsonOK(w, map[string]any{"status": "ok", "id": id})
}

// SetTenantModifierHandler handles POST /tenants/{alias}/modifier
//
// Sets the tenant-wide scale percentage and block/disable flags.
func (s *TenantServer) SetTenantModifierHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	alias := aliasFromPath(r.URL.Path, "/tenants/")
	alias = strings.TrimSuffix(alias, "/modifier")
	if alias == "" {
		http.Error(w, "alias required in path", http.StatusBadRequest)
		return
	}

	var req setModifierRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Resolve alias → TenantID from current snapshot (read-only).
	reg := State.Active.Load()
	if reg == nil {
		http.Error(w, "registry not initialised", http.StatusServiceUnavailable)
		return
	}
	tID, found := reg.Aliases.Lookup(alias)
	if !found {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	var flags TenantRLFlags
	if req.Blocked {
		flags |= TenantBlocked
	}
	if req.RLDisabled {
		flags |= TenantRLDisabled
	}
	s.mgr.SetTenantRateLimitModifier(tID, TenantRateLimitModifier{ScalePct: req.ScalePct, Flags: flags})
	jsonOK(w, map[string]string{"status": "ok"})
}

// UpsertTenantRateLimitOverrideHandler handles POST /tenants/{alias}/rate-limit-overrides
//
// Inserts or updates a per-tenant rate limit override for one RateLimitConfigId.
func (s *TenantServer) UpsertTenantRateLimitOverrideHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	alias := aliasFromPath(r.URL.Path, "/tenants/")
	alias = strings.TrimSuffix(alias, "/rate-limit-overrides")
	if alias == "" {
		http.Error(w, "alias required in path", http.StatusBadRequest)
		return
	}

	var req upsertRateLimitOverrideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.RateLimitName == "" {
		http.Error(w, "rate_limit name must not be empty", http.StatusBadRequest)
		return
	}

	policyID, ok := s.mgr.GetRateLimitConfigId(req.RateLimitName)
	if !ok {
		http.Error(w, "rate limit config not found: "+req.RateLimitName, http.StatusBadRequest)
		return
	}

	reg := State.Active.Load()
	if reg == nil {
		http.Error(w, "registry not initialised", http.StatusServiceUnavailable)
		return
	}
	tID, found := reg.Aliases.Lookup(alias)
	if !found {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	var flags RateLimitFlags
	if req.Blocked {
		flags |= RLBlocked
	}
	if req.RLDisabled {
		flags |= RLDisabled
	}
	if req.Custom {
		flags |= RLCustomValue
	}
	s.mgr.UpsertTenantRateLimitOverride(tID, policyID, TenantRateLimitEntry{
		PerSec: req.RatePerSec,
		PerMin: req.RatePerMin,
		Flags:  flags,
	})
	jsonOK(w, map[string]string{"status": "ok"})
}

// ListTenantsHandler handles GET /tenants?cursor=<id>&limit=<n>
//
// Returns a paginated list of tenant summaries ordered by TenantID.
// cursor is the last TenantID from the previous page (omit or 0 for first page).
// limit defaults to 100, max 1000.
func (s *TenantServer) ListTenantsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var cursor uint16
	if v := q.Get("cursor"); v != "" {
		n, err := strconv.ParseUint(v, 10, 16)
		if err != nil {
			http.Error(w, "cursor must be a non-negative integer", http.StatusBadRequest)
			return
		}
		cursor = uint16(n)
	}
	limit := 100
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
			return
		}
		limit = n
	}

	items, nextCursor := s.mgr.ListTenants(cursor, limit)
	type listResponse struct {
		Items      []TenantSummary `json:"items"`
		NextCursor uint16          `json:"next_cursor"` // 0 = no more pages
		Count      int             `json:"count"`
	}
	jsonOK(w, listResponse{Items: items, NextCursor: nextCursor, Count: len(items)})
}

// GetRateLimitConfigsHandler handles GET /rate-limit-configs
//
// Returns all named rate limit configs.
func (s *TenantServer) GetRateLimitConfigsHandler(w http.ResponseWriter, r *http.Request) {
	configs := s.mgr.GetRateLimitConfigs()
	type listResponse struct {
		Items []RateLimitRecord `json:"items"`
		Count int               `json:"count"`
	}
	jsonOK(w, listResponse{Items: configs, Count: len(configs)})
}

// GetRateLimitConfigHandler handles GET /rate-limit-configs/{name}
//
// Returns a single named rate limit config.
func (s *TenantServer) GetRateLimitConfigHandler(w http.ResponseWriter, r *http.Request) {
	name := aliasFromPath(r.URL.Path, "/rate-limit-configs/")
	if name == "" {
		http.Error(w, "name required in path", http.StatusBadRequest)
		return
	}
	rec, ok := s.mgr.GetRateLimitConfig(name)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	jsonOK(w, rec)
}

// ─── RegisterHandlers wires all tenant/rate-limit endpoints onto the given mux ───

// RegisterHandlers registers all tenant management endpoints on mux:
//
//	POST   /tenants                                    — create / update tenant
//	GET    /tenants                                    — list tenants (cursor-paginated)
//	GET    /tenants/{alias}                            — read tenant properties
//	DELETE /tenants/{alias}                            — remove tenant
//	POST   /tenants/{alias}/aliases                    — add alias to existing tenant
//	POST   /tenants/{alias}/modifier                   — set rate-limit scale / block flags
//	POST   /tenants/{alias}/rate-limit-overrides       — upsert per-tenant rate limit override
//	POST   /rate-limit-configs                         — create / update global rate limit config
//	GET    /rate-limit-configs                         — list all rate limit configs
//	GET    /rate-limit-configs/{name}                  — get specific rate limit config
func (s *TenantServer) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/tenants", s.tenantsRootHandler)
	mux.HandleFunc("/tenants/", s.tenantSubHandler)
	mux.HandleFunc("/rate-limit-configs", s.rateLimitConfigsRootHandler)
	mux.HandleFunc("/rate-limit-configs/", s.GetRateLimitConfigHandler)
}

// tenantsRootHandler dispatches /tenants (no trailing sub-path).
func (s *TenantServer) tenantsRootHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.UpsertTenantHandler(w, r)
	case http.MethodGet:
		s.ListTenantsHandler(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// rateLimitConfigsRootHandler dispatches /rate-limit-configs (no trailing sub-path).
func (s *TenantServer) rateLimitConfigsRootHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.SetRateLimitConfigHandler(w, r)
	case http.MethodGet:
		s.GetRateLimitConfigsHandler(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// tenantSubHandler dispatches /tenants/{alias} and /tenants/{alias}/...
func (s *TenantServer) tenantSubHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path // e.g. /tenants/pepsi.api.com/modifier
	switch {
	case strings.HasSuffix(path, "/aliases") && r.Method == http.MethodPost:
		s.AddAliasHandler(w, r)
	case strings.HasSuffix(path, "/modifier") && r.Method == http.MethodPost:
		s.SetTenantModifierHandler(w, r)
	case strings.HasSuffix(path, "/rate-limit-overrides") && r.Method == http.MethodPost:
		s.UpsertTenantRateLimitOverrideHandler(w, r)
	case isCredentialsSubPath(path) && (r.Method == http.MethodGet || r.Method == http.MethodPut || r.Method == http.MethodDelete):
		if s.ExtraSubHandler != nil {
			s.ExtraSubHandler.ServeHTTP(w, r)
		} else {
			http.Error(w, "not found", http.StatusNotFound)
		}
	case r.Method == http.MethodGet:
		s.GetTenantHandler(w, r)
	case r.Method == http.MethodDelete:
		s.DeleteTenantHandler(w, r)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// isCredentialsSubPath reports whether path is a /tenants/{alias}/credentials[/...]
// sub-path. It requires the "/credentials" segment to appear after the alias
// (i.e. not as a prefix of the alias itself) so that tenant aliases that happen
// to contain "credentials" in their name are not mistakenly dispatched.
func isCredentialsSubPath(path string) bool {
	// Strip /tenants/ prefix.
	rest := strings.TrimPrefix(path, "/tenants/")
	if rest == path {
		return false // no /tenants/ prefix
	}
	// rest = "{alias}/credentials[/...]"
	// Find the slash that follows the alias.
	idx := strings.Index(rest, "/")
	if idx < 0 {
		return false // no sub-path at all
	}
	subPath := rest[idx:] // "/credentials[/...]"
	return subPath == "/credentials" ||
		strings.HasPrefix(subPath, "/credentials/")
}

// aliasFromPath strips prefix from path and returns the remainder.
// e.g. aliasFromPath("/tenants/pepsi.api.com/modifier", "/tenants/") → "pepsi.api.com/modifier"
func aliasFromPath(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	return path[len(prefix):]
}

// storeKeyNameForID walks a PropStore's Keys radix to reverse-lookup a KeyID → name.
// O(n) over the node list; management plane only — not called on hot path.
func storeKeyNameForID(store *PropStore, keyID uint16) string {
	for _, node := range store.Keys {
		if node.Value == keyID && node.ChildCount == 0 {
			end := node.PrefixOffset + uint32(node.PrefixLen)
			if end <= uint32(len(store.StringPool)) {
				return string(store.StringPool[node.PrefixOffset:end])
			}
		}
	}
	return ""
}
