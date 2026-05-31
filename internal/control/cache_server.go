package control

import (
	"encoding/json"
	"net/http"
	"strings"

	"rah/internal/engine/steps"
	"rah/internal/registry"
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
	// Parse /cache/{alias}/{key} — key may contain slashes.
	rest := strings.TrimPrefix(r.URL.Path, "/cache/")
	slash := strings.Index(rest, "/")
	if slash < 0 {
		http.Error(w, `{"error":"bad path"}`, http.StatusBadRequest)
		return
	}
	alias := rest[:slash]
	key := rest[slash+1:]

	if alias == "" || key == "" {
		http.Error(w, `{"error":"alias and key are required"}`, http.StatusBadRequest)
		return
	}

	reg := registry.State.Active.Load()
	if reg == nil {
		http.Error(w, `{"error":"registry not initialised"}`, http.StatusServiceUnavailable)
		return
	}

	tenantID, ok := registry.GetTenantID(reg, alias)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"tenant not found"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		val, found := s.store.Get(tenantID, []byte(key))
		if !found {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"found": false})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"found": true,
			"value": string(val),
		})

	case http.MethodDelete:
		_ = s.store.Invalidate(tenantID, []byte(key))
		_, _ = w.Write([]byte(`{"status":"ok"}`))

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":"method not allowed"}`))
	}
}
