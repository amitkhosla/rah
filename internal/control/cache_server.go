package control

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/amitkhosla/rah/internal/engine/steps"
	"github.com/amitkhosla/rah/internal/registry"
)

// CacheServer exposes cache lookup and invalidation over HTTP for management-plane use.
type CacheServer struct {
	store steps.CacheStore
}

// RegisterCacheRoutes wires cache management endpoints onto the given mux.
// store must be non-nil (only called when cache is enabled).
func RegisterCacheRoutes(mux *http.ServeMux, store steps.CacheStore) {
	s := &CacheServer{store: store}
	mux.HandleFunc("/cache/", s.cacheHandler)
}

func (s *CacheServer) cacheHandler(w http.ResponseWriter, r *http.Request) {
	// Parse /cache/{alias}/{key} â€” key may contain slashes.
	rest := strings.TrimPrefix(r.URL.Path, "/cache/")
	alias, key, ok2 := strings.Cut(rest, "/")
	if !ok2 {
		http.Error(w, `{"error":"bad path"}`, http.StatusBadRequest)
		return
	}

	if alias == "" || key == "" {
		http.Error(w, `{"error":"alias and key are required"}`, http.StatusBadRequest)
		return
	}

	var tenantID uint16
	if alias == "*" {
		tenantID = 0
	} else {
		reg := registry.State.Active.Load()
		if reg == nil {
			http.Error(w, `{"error":"registry not initialised"}`, http.StatusServiceUnavailable)
			return
		}
		var ok bool
		tenantID, ok = registry.GetTenantID(reg, alias)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"tenant not found"}`))
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		val, found := s.store.Get(tenantID, []byte(key))
		if !found {
			_ = json.NewEncoder(w).Encode(map[string]any{"found": false})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"found": true,
			"value": string(val),
		})

	case http.MethodPut:
		var req struct {
			Value string `json:"value"`
			TTL   uint32 `json:"ttl,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		s.store.Put(tenantID, []byte(key), []byte(req.Value), req.TTL)
		_, _ = w.Write([]byte(`{"status":"ok"}`))

	case http.MethodDelete:
		_ = s.store.Invalidate(tenantID, []byte(key))
		_, _ = w.Write([]byte(`{"status":"ok"}`))

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":"method not allowed"}`))
	}
}
