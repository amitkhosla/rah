package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitV2Datastore is the minimal interface the TenantServer needs to
// persist V2 rate limit configs, tiers, and upstream services. Implemented by
// control.DataStoreManager — passed in at wire-up time to avoid a circular import.
type RateLimitV2Datastore interface {
	PutRateLimitConfigV2(ctx context.Context, name string, raw []byte) error
	DeleteRateLimitConfigV2(ctx context.Context, name string) error
	PutTier(ctx context.Context, name string, raw []byte) error
	DeleteTier(ctx context.Context, name string) error
	PutUpstreamService(ctx context.Context, name string, raw []byte) error
	DeleteUpstreamService(ctx context.Context, name string) error
}

// RLCounterRegistrar wires a newly created rate-limit-v2 config into the
// engine's in-process counter arenas. Implemented by the gateway main package
// to avoid a circular import between registry and engine.
type RLCounterRegistrar interface {
	RegisterRateLimitV2(id uint16, numWindows int)
}

// CounterStatusProvider reads live window usage from the engine's counter arenas.
// Implemented by gateway main to avoid a circular registry→engine import.
type CounterStatusProvider interface {
	ReadTenantCurrent(configID uint16, tenantID uint16, windowIdx int, epoch uint32) uint32
	ReadSlotCurrent(configID uint16, keyBytes []byte, windowIdx int, epoch uint32) uint32
}

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

	// RLV2Store is optional. When set, V2 rate limit config, tier, and upstream
	// service mutations are written through to the backing datastore for durability.
	// Leave nil in test environments or when an external orchestrator owns persistence.
	RLV2Store RateLimitV2Datastore

	// RLCounterRegistrar is optional. When set, POST /rate-limit-configs-v2
	// wires the new config into the engine's counter arenas immediately, so the
	// limit is enforced without requiring a subsequent /sync call.
	RLRegistrar RLCounterRegistrar

	// CounterStatus is optional. When set, GET /tenants/{alias}/rate-limit-status
	// returns live counter values from the engine's counter arenas.
	// When nil, used=0 and remaining=limit for all windows (graceful degradation).
	CounterStatus CounterStatusProvider
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

// DeleteServiceURLHandler handles DELETE /tenants/{alias}/urls/{key}
//
// Clears a single service URL entry from the registry for the given tenant
// without touching its identity (aliases, identifiers, meta). Useful for
// resetting cached URL state in tests or after a tenant re-provisioning event.
func (s *TenantServer) DeleteServiceURLHandler(w http.ResponseWriter, r *http.Request) {
	// Path: /tenants/{alias}/urls/{key}
	rest := strings.TrimPrefix(r.URL.Path, "/tenants/")
	idx := strings.Index(rest, "/urls/")
	if idx < 0 {
		http.Error(w, "path must be /tenants/{alias}/urls/{key}", http.StatusBadRequest)
		return
	}
	alias := rest[:idx]
	key := rest[idx+len("/urls/"):]
	if alias == "" || key == "" {
		http.Error(w, "alias and key are required", http.StatusBadRequest)
		return
	}
	s.mgr.DeleteServiceURL(alias, key)
	jsonOK(w, map[string]string{"status": "ok", "alias": alias, "key": key})
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

// SetTenantDebugHandler handles PATCH /tenants/{alias}/debug
//
// Request body: {"enabled": true}
// Response 204 on success, 404 if tenant not found, 400 on bad JSON.
func (s *TenantServer) SetTenantDebugHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	alias := aliasFromPath(r.URL.Path, "/tenants/")
	alias = strings.TrimSuffix(alias, "/debug")
	if alias == "" {
		http.Error(w, "alias required in path", http.StatusBadRequest)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.SetTenantDebug(alias, req.Enabled); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetTenantLogLevelHandler handles PATCH /tenants/{alias}/log-level
//
// Request body: {"level": "debug"}
// Response 204 on success, 404 if tenant not found, 400 if level value is invalid or bad JSON.
// Valid level values: "debug", "info", "warn", "error", "".
func (s *TenantServer) SetTenantLogLevelHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	alias := aliasFromPath(r.URL.Path, "/tenants/")
	alias = strings.TrimSuffix(alias, "/log-level")
	if alias == "" {
		http.Error(w, "alias required in path", http.StatusBadRequest)
		return
	}
	var req struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.mgr.SetTenantLogLevel(alias, req.Level); err != nil {
		// SetTenantLogLevel returns 400-class errors for invalid level and 404-class for missing tenant.
		// Distinguish by checking for "invalid log level" prefix.
		if len(req.Level) > 0 && req.Level != "debug" && req.Level != "info" && req.Level != "warn" && req.Level != "error" {
			http.Error(w, err.Error(), http.StatusBadRequest)
		} else {
			http.Error(w, err.Error(), http.StatusNotFound)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
//	DELETE /tenants/{alias}/urls/{key}                 — clear a single service URL (keeps identity)
//	POST   /tenants/{alias}/aliases                    — add alias to existing tenant
//	POST   /tenants/{alias}/modifier                   — set rate-limit scale / block flags
//	POST   /tenants/{alias}/rate-limit-overrides       — upsert per-tenant rate limit override
//	POST   /rate-limit-configs                         — create / update global rate limit config (V1)
//	GET    /rate-limit-configs                         — list all rate limit configs (V1)
//	GET    /rate-limit-configs/{name}                  — get specific rate limit config (V1)
//	POST   /rate-limit-configs-v2                      — create / update V2 rate limit config
//	GET    /rate-limit-configs-v2                      — list all V2 rate limit configs
//	DELETE /rate-limit-configs-v2/{name}               — delete a V2 rate limit config
//	POST   /tiers                                      — create / update a tier definition
//	GET    /tiers                                      — list all tier definitions
//	DELETE /tiers/{name}                               — delete a tier definition
//	POST   /upstream-services                          — create / update an upstream service definition
//	GET    /upstream-services                          — list all upstream service definitions
//	DELETE /upstream-services/{name}                   — delete an upstream service definition
func (s *TenantServer) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/tenants", s.tenantsRootHandler)
	mux.HandleFunc("/tenants/", s.tenantSubHandler)
	mux.HandleFunc("/rate-limit-configs", s.rateLimitConfigsRootHandler)
	mux.HandleFunc("/rate-limit-configs/", s.GetRateLimitConfigHandler)
	mux.HandleFunc("/rate-limit-configs-v2", s.rateLimitConfigsV2RootHandler)
	mux.HandleFunc("/rate-limit-configs-v2/", s.rateLimitConfigsV2SubHandler)
	mux.HandleFunc("/tiers", s.tiersRootHandler)
	mux.HandleFunc("/tiers/", s.tiersSubHandler)
	mux.HandleFunc("/upstream-services", s.upstreamServicesRootHandler)
	mux.HandleFunc("/upstream-services/", s.upstreamServicesSubHandler)
}

// ─── V2 Rate Limit Config Handlers ───────────────────────────────────────────

// rateLimitConfigsV2RootHandler dispatches GET and POST /rate-limit-configs-v2.
func (s *TenantServer) rateLimitConfigsV2RootHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items := ListRateLimitConfigsV2()
		if items == nil {
			items = []RateLimitConfigV2{}
		}
		records := make([]RateLimitConfigV2Record, len(items))
		for i, cfg := range items {
			records[i] = RateLimitConfigV2Record{Name: cfg.Name, Config: cfg}
		}
		type listResponse struct {
			Items []RateLimitConfigV2Record `json:"items"`
			Count int                       `json:"count"`
		}
		jsonOK(w, listResponse{Items: records, Count: len(records)})
	case http.MethodPost:
		var cfg RateLimitConfigV2
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if cfg.Name == "" {
			http.Error(w, "name must not be empty", http.StatusBadRequest)
			return
		}
		UpsertRateLimitConfigV2(cfg)
		if s.RLRegistrar != nil {
			id := s.mgr.EnsureRateLimitV2ID(cfg.Name)
			numWindows := len(cfg.Windows)
			if numWindows == 0 {
				numWindows = 1
			}
			s.RLRegistrar.RegisterRateLimitV2(id, numWindows)
		}
		if s.RLV2Store != nil {
			if raw, merr := json.Marshal(cfg); merr == nil {
				if perr := s.RLV2Store.PutRateLimitConfigV2(r.Context(), cfg.Name, raw); perr != nil {
					// Log but don't fail — in-memory state is already updated.
					_ = perr
				}
			}
		}
		jsonOK(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// rateLimitConfigsV2SubHandler dispatches DELETE /rate-limit-configs-v2/{name}.
func (s *TenantServer) rateLimitConfigsV2SubHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := aliasFromPath(r.URL.Path, "/rate-limit-configs-v2/")
	if name == "" {
		http.Error(w, "name required in path", http.StatusBadRequest)
		return
	}
	DeleteRateLimitConfigV2(name)
	if s.RLV2Store != nil {
		_ = s.RLV2Store.DeleteRateLimitConfigV2(r.Context(), name)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// ─── Tier Handlers ────────────────────────────────────────────────────────────

// tiersRootHandler dispatches GET and POST /tiers.
func (s *TenantServer) tiersRootHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items := ListTiers()
		if items == nil {
			items = []TierDef{}
		}
		type listResponse struct {
			Items []TierDef `json:"items"`
			Count int       `json:"count"`
		}
		jsonOK(w, listResponse{Items: items, Count: len(items)})
	case http.MethodPost:
		var def TierDef
		if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if def.Name == "" {
			http.Error(w, "name must not be empty", http.StatusBadRequest)
			return
		}
		UpsertTier(def)
		if s.RLV2Store != nil {
			if raw, merr := json.Marshal(def); merr == nil {
				_ = s.RLV2Store.PutTier(r.Context(), def.Name, raw)
			}
		}
		jsonOK(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// tiersSubHandler dispatches DELETE /tiers/{name}.
func (s *TenantServer) tiersSubHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := aliasFromPath(r.URL.Path, "/tiers/")
	if name == "" {
		http.Error(w, "name required in path", http.StatusBadRequest)
		return
	}
	DeleteTier(name)
	if s.RLV2Store != nil {
		_ = s.RLV2Store.DeleteTier(r.Context(), name)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// ─── Upstream Service Handlers ────────────────────────────────────────────────

// upstreamServicesRootHandler dispatches GET and POST /upstream-services.
func (s *TenantServer) upstreamServicesRootHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items := ListUpstreamServices()
		if items == nil {
			items = []UpstreamServiceDef{}
		}
		type listResponse struct {
			Items []UpstreamServiceDef `json:"items"`
			Count int                  `json:"count"`
		}
		jsonOK(w, listResponse{Items: items, Count: len(items)})
	case http.MethodPost:
		var def UpstreamServiceDef
		if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if def.Name == "" {
			http.Error(w, "name must not be empty", http.StatusBadRequest)
			return
		}
		UpsertUpstreamService(def)
		if s.RLV2Store != nil {
			if raw, merr := json.Marshal(def); merr == nil {
				_ = s.RLV2Store.PutUpstreamService(r.Context(), def.Name, raw)
			}
		}
		jsonOK(w, map[string]string{"status": "ok"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// upstreamServicesSubHandler dispatches DELETE /upstream-services/{name}.
func (s *TenantServer) upstreamServicesSubHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := aliasFromPath(r.URL.Path, "/upstream-services/")
	if name == "" {
		http.Error(w, "name required in path", http.StatusBadRequest)
		return
	}
	DeleteUpstreamService(name)
	if s.RLV2Store != nil {
		_ = s.RLV2Store.DeleteUpstreamService(r.Context(), name)
	}
	jsonOK(w, map[string]string{"status": "ok"})
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
	case strings.Contains(path, "/urls/") && r.Method == http.MethodDelete:
		s.DeleteServiceURLHandler(w, r)
	case strings.HasSuffix(path, "/aliases") && r.Method == http.MethodPost:
		s.AddAliasHandler(w, r)
	case strings.HasSuffix(path, "/modifier") && r.Method == http.MethodPost:
		s.SetTenantModifierHandler(w, r)
	case strings.HasSuffix(path, "/rate-limit-overrides") && r.Method == http.MethodPost:
		s.UpsertTenantRateLimitOverrideHandler(w, r)
	case strings.HasSuffix(path, "/debug") && r.Method == http.MethodPatch:
		s.SetTenantDebugHandler(w, r)
	case strings.HasSuffix(path, "/log-level") && r.Method == http.MethodPatch:
		s.SetTenantLogLevelHandler(w, r)
	case strings.HasSuffix(path, "/rate-limit-status") && r.Method == http.MethodGet:
		s.rateLimitStatusHandler(w, r)
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

// rateLimitStatusHandler handles GET /tenants/{alias}/rate-limit-status.
// Returns live counter values per V2 rate limit policy for the given tenant.
// If CounterStatus is nil, used=0 and remaining=limit (graceful degradation).
func (s *TenantServer) rateLimitStatusHandler(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/tenants/")
	alias := strings.TrimSuffix(rest, "/rate-limit-status")
	if alias == "" || alias == rest {
		http.Error(w, "alias required in path", http.StatusBadRequest)
		return
	}

	reg := State.Active.Load()
	if reg == nil {
		http.Error(w, "registry not initialised", http.StatusServiceUnavailable)
		return
	}
	tenantID, found := reg.Aliases.Lookup(alias)
	if !found {
		jsonError(w, "tenant not found", http.StatusNotFound)
		return
	}

	cfgs := ListRateLimitConfigsV2()

	type windowStatus struct {
		EpochSec  uint32 `json:"epoch_sec"`
		Limit     uint32 `json:"limit"`
		Used      uint32 `json:"used"`
		Remaining uint32 `json:"remaining"`
		ResetsAt  int64  `json:"resets_at_unix"`
	}
	type policyStatus struct {
		ConfigName string         `json:"config_name"`
		Windows    []windowStatus `json:"windows"`
	}
	type statusResponse struct {
		Tenant   string         `json:"tenant"`
		TenantID uint16         `json:"tenant_id"`
		Policies []policyStatus `json:"policies"`
	}

	now := uint32(time.Now().Unix())
	policies := make([]policyStatus, 0, len(cfgs))
	for _, cfg := range cfgs {
		configID := s.mgr.EnsureRateLimitV2ID(cfg.Name)
		windows := make([]windowStatus, 0, len(cfg.Windows))
		for windowIdx, win := range cfg.Windows {
			epochSec := win.PeriodSecs
			if epochSec == 0 {
				epochSec = 1
			}
			epoch := now / epochSec
			var used uint32
			if s.CounterStatus != nil {
				used = s.CounterStatus.ReadTenantCurrent(configID, tenantID, windowIdx, epoch)
			}
			remaining := uint32(0)
			if used < win.Limit {
				remaining = win.Limit - used
			}
			windows = append(windows, windowStatus{
				EpochSec:  epochSec,
				Limit:     win.Limit,
				Used:      used,
				Remaining: remaining,
				ResetsAt:  int64((epoch + 1) * epochSec),
			})
		}
		policies = append(policies, policyStatus{ConfigName: cfg.Name, Windows: windows})
	}

	jsonOK(w, statusResponse{Tenant: alias, TenantID: tenantID, Policies: policies})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
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
