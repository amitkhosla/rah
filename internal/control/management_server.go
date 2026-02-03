package control

import (
	"encoding/json"
	"net/http"
	"rah/internal/engine"
	"rah/internal/router"
	"strconv"
)

type ManagementServer struct {
	Router      *router.RahRouter
	FlowManager *engine.FlowManager
	Compiler    *Compiler
}

func (s *ManagementServer) RegisterAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var cfg ApiConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 1. Convert string ID to uint32 for our internal engine
	id, _ := strconv.ParseUint(cfg.ApiID, 10, 32)
	apiId := uint32(id)

	// 2. Compile JSON steps into executable instructions
	// Note: You'll want to extend your Compiler to handle fragments too!
	plan := s.Compiler.Compile(cfg.Flow)

	// 3. Update the Data Plane (Atomic Updates)
	s.FlowManager.Plans[apiId] = plan // Direct index write is safe if size is pre-allocated
	s.Router.Add(cfg.Path, apiId)     // Re-bakes the radix tree and swaps atomically

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte("API Registered Successfully"))
}
