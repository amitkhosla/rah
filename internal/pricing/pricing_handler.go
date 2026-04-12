package pricing

import (
	"encoding/json"
	"log"
	"net/http"
)

// RegisterPricingRoutes registers pricing management endpoints on the given mux.
//
//	GET  /pricing         — full pricing catalog (all models, with last_updated)
//	POST /pricing/refresh — trigger immediate refresh from LiteLLM
func RegisterPricingRoutes(mux *http.ServeMux, pm *PricingManager) {
	mux.HandleFunc("/pricing", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(pm.GetPricing()); err != nil {
			log.Printf("pricing handler: encode error: %v", err)
		}
	})

	mux.HandleFunc("/pricing/refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		pm.ForceRefresh()
		go func() {
			all, err := pm.fetcher.FetchFromLiteLLM()
			if err != nil {
				log.Printf("pricing refresh: fetch failed: %v", err)
				return
			}
			pm.mergeFetchedPricing(all)
		}()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "refresh triggered"}); err != nil {
			log.Printf("pricing handler: encode error: %v", err)
		}
	})
}
