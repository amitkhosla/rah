package studio

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// flowBaselinesHandler handles GET (list) and POST (create) for flow baselines.
func (s *Server) flowBaselinesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listBaselines(w, r)
	case http.MethodPost:
		s.createBaseline(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// listBaselines retrieves all baselines and returns them as a JSON array.
func (s *Server) listBaselines(w http.ResponseWriter, r *http.Request) {
	baselines, err := s.baselineStore.ListBaselines()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(baselines)
}

// createBaseline creates a new baseline from the request body.
func (s *Server) createBaseline(w http.ResponseWriter, r *http.Request) {
	var rec FlowBaselineRecord
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	// Validate Tag is not empty
	if rec.Tag == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "tag is required"})
		return
	}

	// Set CreatedAt and Status if not provided
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	if rec.Status == "" {
		rec.Status = "active"
	}

	// Save baseline
	if err := s.baselineStore.SaveBaseline(&rec); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(rec)
}

// flowBaselineByTagHandler handles GET, DELETE, and POST /promote for a specific baseline.
func (s *Server) flowBaselineByTagHandler(w http.ResponseWriter, r *http.Request) {
	// Parse tag from URL path
	tag := strings.TrimPrefix(r.URL.Path, "/api/flow-baselines/")

	// Check for sub-paths (like /promote)
	parts := strings.Split(tag, "/")
	if len(parts) > 1 {
		// Has a sub-path
		tag = parts[0]
		subpath := parts[1]

		if subpath == "promote" && r.Method == http.MethodPost {
			s.promoteBaseline(w, r, tag)
			return
		}

		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	// Handle standard operations on the baseline by tag
	switch r.Method {
	case http.MethodGet:
		s.getBaseline(w, r, tag)
	case http.MethodDelete:
		s.deleteBaseline(w, r, tag)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// getBaseline retrieves a specific baseline by tag.
func (s *Server) getBaseline(w http.ResponseWriter, r *http.Request, tag string) {
	rec, err := s.baselineStore.GetBaseline(tag)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "baseline not found"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(rec)
}

// deleteBaseline removes a baseline by tag.
func (s *Server) deleteBaseline(w http.ResponseWriter, r *http.Request, tag string) {
	if err := s.baselineStore.DeleteBaseline(tag); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// promoteBaseline promotes a baseline from one tag to another.
func (s *Server) promoteBaseline(w http.ResponseWriter, r *http.Request, fromTag string) {
	var body struct {
		ToTag string `json:"to_tag"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	// Validate ToTag is not empty
	if body.ToTag == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "to_tag is required"})
		return
	}

	// Promote baseline
	if err := s.baselineStore.PromoteBaseline(fromTag, body.ToTag); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"promoted_to": body.ToTag})
}
