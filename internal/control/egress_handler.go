package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/egress"
)

// EgressHandler exposes CRUD REST endpoints for egress profiles and rules.
//
//	GET    /egress/profiles              â€” list all profiles (JSON array)
//	POST   /egress/profiles              â€” create/update profile (body: EgressProfileConfig)
//	DELETE /egress/profiles/{name}       â€” delete profile by name
//
//	GET    /egress/rules/codes           â€” list code rules (JSON array)
//	POST   /egress/rules/codes           â€” upsert code rule (body: EgressCodeRuleConfig)
//	DELETE /egress/rules/codes/{code}    â€” delete code rule by service code
//
//	GET    /egress/rules/patterns        â€” list pattern rules (JSON array, sorted by idx)
//	POST   /egress/rules/patterns        â€” append pattern rule (body: EgressPatternRuleConfig)
//	DELETE /egress/rules/patterns/{idx}  â€” delete pattern rule by idx string
type EgressHandler struct {
	dsm    *DataStoreManager
	egress *egress.EgressManager
}

// NewEgressHandler creates an EgressHandler backed by dsm and em.
func NewEgressHandler(dsm *DataStoreManager, em *egress.EgressManager) *EgressHandler {
	return &EgressHandler{dsm: dsm, egress: em}
}

// RegisterHandlers mounts the egress endpoints on mux.
func (h *EgressHandler) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/egress/profiles", h.profilesCollectionHandler)
	mux.HandleFunc("/egress/profiles/", h.profilesItemHandler)
	mux.HandleFunc("/egress/rules/codes", h.codesCollectionHandler)
	mux.HandleFunc("/egress/rules/codes/", h.codesItemHandler)
	mux.HandleFunc("/egress/rules/patterns", h.patternsCollectionHandler)
	mux.HandleFunc("/egress/rules/patterns/", h.patternsItemHandler)
}

// â”€â”€â”€ Profile handlers â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func (h *EgressHandler) profilesCollectionHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listProfiles(w, r)
	case http.MethodPost:
		h.upsertProfile(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *EgressHandler) profilesItemHandler(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/egress/profiles/")
	if name == "" {
		http.Error(w, "profile name required in path", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		h.deleteProfile(w, r, name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *EgressHandler) listProfiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keys, err := h.dsm.ListKeysByDomain(ctx, config.DomainEgressProfiles, GlobalTenant, "profile/")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	profiles := make([]config.EgressProfileConfig, 0, len(keys))
	for _, k := range keys {
		raw, ok, err := h.dsm.GetGlobal(ctx, config.DomainEgressProfiles, k)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			continue
		}
		var p config.EgressProfileConfig
		if err := json.Unmarshal(raw, &p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		profiles = append(profiles, p)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(profiles)
}

func (h *EgressHandler) upsertProfile(w http.ResponseWriter, r *http.Request) {
	var p config.EgressProfileConfig
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if p.Name == "" {
		http.Error(w, `"name" is required`, http.StatusBadRequest)
		return
	}
	raw, err := json.Marshal(p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	if err := h.dsm.PutGlobal(ctx, config.DomainEgressProfiles, "profile/"+p.Name, raw); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.reload(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": p.Name})
}

func (h *EgressHandler) deleteProfile(w http.ResponseWriter, r *http.Request, name string) {
	ctx := r.Context()
	if err := h.dsm.DeleteGlobal(ctx, config.DomainEgressProfiles, "profile/"+name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.reload(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// â”€â”€â”€ Code rule handlers â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func (h *EgressHandler) codesCollectionHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listCodeRules(w, r)
	case http.MethodPost:
		h.upsertCodeRule(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *EgressHandler) codesItemHandler(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimPrefix(r.URL.Path, "/egress/rules/codes/")
	if code == "" {
		http.Error(w, "service code required in path", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		h.deleteCodeRule(w, r, code)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *EgressHandler) listCodeRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keys, err := h.dsm.ListKeysByDomain(ctx, config.DomainEgressProfiles, GlobalTenant, "rule/code/")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rules := make([]config.EgressCodeRuleConfig, 0, len(keys))
	for _, k := range keys {
		raw, ok, err := h.dsm.GetGlobal(ctx, config.DomainEgressProfiles, k)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			continue
		}
		var cr config.EgressCodeRuleConfig
		if err := json.Unmarshal(raw, &cr); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rules = append(rules, cr)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rules)
}

func (h *EgressHandler) upsertCodeRule(w http.ResponseWriter, r *http.Request) {
	var cr config.EgressCodeRuleConfig
	if err := json.NewDecoder(r.Body).Decode(&cr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if cr.ServiceCode == "" {
		http.Error(w, `"service_code" is required`, http.StatusBadRequest)
		return
	}
	if cr.Profile == "" {
		http.Error(w, `"profile" is required`, http.StatusBadRequest)
		return
	}
	raw, err := json.Marshal(cr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	if err := h.dsm.PutGlobal(ctx, config.DomainEgressProfiles, "rule/code/"+cr.ServiceCode, raw); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.reload(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service_code": cr.ServiceCode})
}

func (h *EgressHandler) deleteCodeRule(w http.ResponseWriter, r *http.Request, code string) {
	ctx := r.Context()
	if err := h.dsm.DeleteGlobal(ctx, config.DomainEgressProfiles, "rule/code/"+code); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.reload(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// â”€â”€â”€ Pattern rule handlers â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func (h *EgressHandler) patternsCollectionHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listPatternRules(w, r)
	case http.MethodPost:
		h.appendPatternRule(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *EgressHandler) patternsItemHandler(w http.ResponseWriter, r *http.Request) {
	idx := strings.TrimPrefix(r.URL.Path, "/egress/rules/patterns/")
	if idx == "" {
		http.Error(w, "pattern index required in path", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		h.deletePatternRule(w, r, idx)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *EgressHandler) listPatternRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keys, err := h.dsm.ListKeysByDomain(ctx, config.DomainEgressProfiles, GlobalTenant, "rule/pattern/")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sort.Strings(keys)
	rules := make([]config.EgressPatternRuleConfig, 0, len(keys))
	for _, k := range keys {
		raw, ok, err := h.dsm.GetGlobal(ctx, config.DomainEgressProfiles, k)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			continue
		}
		var pr config.EgressPatternRuleConfig
		if err := json.Unmarshal(raw, &pr); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rules = append(rules, pr)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rules)
}

func (h *EgressHandler) appendPatternRule(w http.ResponseWriter, r *http.Request) {
	var pr config.EgressPatternRuleConfig
	if err := json.NewDecoder(r.Body).Decode(&pr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if pr.Pattern == "" {
		http.Error(w, `"pattern" is required`, http.StatusBadRequest)
		return
	}
	if pr.Profile == "" {
		http.Error(w, `"profile" is required`, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	idx, err := h.nextPatternIdx(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	raw, err := json.Marshal(pr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.dsm.PutGlobal(ctx, config.DomainEgressProfiles, formatPatternKey(idx), raw); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.reload(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "idx": idx})
}

func (h *EgressHandler) deletePatternRule(w http.ResponseWriter, r *http.Request, idx string) {
	ctx := r.Context()
	if err := h.dsm.DeleteGlobal(ctx, config.DomainEgressProfiles, formatPatternKey(idx)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.reload(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// â”€â”€â”€ Helpers â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// formatPatternKey formats idx as "rule/pattern/000001".
func formatPatternKey(idx string) string { return "rule/pattern/" + idx }

// nextPatternIdx returns the next available 6-digit zero-padded index for a new
// pattern rule by scanning existing keys of the form "rule/pattern/NNNNNN".
func (h *EgressHandler) nextPatternIdx(ctx context.Context) (string, error) {
	keys, err := h.dsm.ListKeysByDomain(ctx, config.DomainEgressProfiles, GlobalTenant, "rule/pattern/")
	if err != nil {
		return "", err
	}
	max := -1
	for _, k := range keys {
		suffix := strings.TrimPrefix(k, "rule/pattern/")
		n, err := strconv.Atoi(suffix)
		if err != nil {
			continue
		}
		if n > max {
			max = n
		}
	}
	return fmt.Sprintf("%06d", max+1), nil
}

// reload reads all egress config from the datastore and calls Update on the EgressManager.
func (h *EgressHandler) reload(ctx context.Context) error {
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		return err
	}
	return h.egress.Update(cfg)
}

// loadConfig assembles a full *config.EgressConfig from the datastore.
func (h *EgressHandler) loadConfig(ctx context.Context) (*config.EgressConfig, error) {
	// â”€â”€ Profiles â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
	profileKeys, err := h.dsm.ListKeysByDomain(ctx, config.DomainEgressProfiles, GlobalTenant, "profile/")
	if err != nil {
		return nil, err
	}
	profiles := make([]config.EgressProfileConfig, 0, len(profileKeys))
	for _, k := range profileKeys {
		raw, ok, err := h.dsm.GetGlobal(ctx, config.DomainEgressProfiles, k)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var p config.EgressProfileConfig
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("decode profile %q: %w", k, err)
		}
		profiles = append(profiles, p)
	}

	// â”€â”€ Code rules â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
	codeKeys, err := h.dsm.ListKeysByDomain(ctx, config.DomainEgressProfiles, GlobalTenant, "rule/code/")
	if err != nil {
		return nil, err
	}
	codeRules := make([]config.EgressCodeRuleConfig, 0, len(codeKeys))
	for _, k := range codeKeys {
		raw, ok, err := h.dsm.GetGlobal(ctx, config.DomainEgressProfiles, k)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var cr config.EgressCodeRuleConfig
		if err := json.Unmarshal(raw, &cr); err != nil {
			return nil, fmt.Errorf("decode code rule %q: %w", k, err)
		}
		codeRules = append(codeRules, cr)
	}

	// â”€â”€ Pattern rules (sorted by key to preserve insertion order) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
	patternKeys, err := h.dsm.ListKeysByDomain(ctx, config.DomainEgressProfiles, GlobalTenant, "rule/pattern/")
	if err != nil {
		return nil, err
	}
	sort.Strings(patternKeys)
	patternRules := make([]config.EgressPatternRuleConfig, 0, len(patternKeys))
	for _, k := range patternKeys {
		raw, ok, err := h.dsm.GetGlobal(ctx, config.DomainEgressProfiles, k)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var pr config.EgressPatternRuleConfig
		if err := json.Unmarshal(raw, &pr); err != nil {
			return nil, fmt.Errorf("decode pattern rule %q: %w", k, err)
		}
		patternRules = append(patternRules, pr)
	}

	return &config.EgressConfig{
		Profiles:     profiles,
		CodeRules:    codeRules,
		PatternRules: patternRules,
	}, nil
}

// â”€â”€â”€ Bootstrap + Route registration â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// BootstrapEgress reads all egress config from the datastore and loads it into em.
// Returns nil if the domain is not configured (optional domain).
func BootstrapEgress(ctx context.Context, dsm *DataStoreManager, em *egress.EgressManager) error {
	if !dsm.IsConfigured(config.DomainEgressProfiles) {
		return nil
	}
	handler := &EgressHandler{dsm: dsm, egress: em}
	cfg, err := handler.loadConfig(ctx)
	if err != nil {
		return fmt.Errorf("egress bootstrap: %w", err)
	}
	return em.Update(cfg)
}

// RegisterEgressRoutes mounts the egress CRUD endpoints on mux.
// No-op if the egress_profiles domain is not configured.
func RegisterEgressRoutes(mux *http.ServeMux, dsm *DataStoreManager, em *egress.EgressManager) {
	if !dsm.IsConfigured(config.DomainEgressProfiles) {
		return
	}
	h := NewEgressHandler(dsm, em)
	h.RegisterHandlers(mux)
}
