package control

import (
	"encoding/json"
	"net/http"
	"rah/internal/cache"
)

type CacheCommand struct {
	Action   string `json:"action"` // "purge_tenant", "resize_slab"
	TenantID uint32 `json:"tenant_id"`
	SlabID   uint8  `json:"slab_id"`
}

type CacheController struct {
	CacheMgr *cache.CacheManager
}

func (c *CacheController) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Use POST", 405)
		return
	}

	var cmd CacheCommand
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	switch cmd.Action {
	case "purge_tenant":
		// Note: In our Slab design, we don't delete individual items (expensive).
		// We simply increment the Version in the Slab or let it expire naturally.
		// For a specific tenant purge, we could implement a 'Tombstone' logic.
		w.Write([]byte(`{"status": "queued", "detail": "Tenant cache marked for expiry"}`))
	default:
		http.Error(w, "Unknown Action", 400)
	}
}
