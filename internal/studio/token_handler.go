package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// tokenListCreateHandler serves GET and POST /api/tokens.
// GET  — list tokens for the current user (admins see all).
// POST — create a new machine token.
func (s *Server) tokenListCreateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.tokenStore == nil {
		http.Error(w, `{"error":"token store not initialised (auth not enabled)"}`, http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		caller := callerUsername(r.Context())
		role := callerRole(r.Context(), s)
		all := s.tokenStore.List()
		if role != "admin" {
			// Non-admins only see their own tokens.
			filtered := all[:0]
			for _, t := range all {
				if strings.EqualFold(t.CreatedBy, caller) {
					filtered = append(filtered, t)
				}
			}
			all = filtered
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tokens": all})

	case http.MethodPost:
		if !requireRole(r.Context(), w, s, "admin") {
			return
		}
		var req struct {
			Name        string   `json:"name"`
			Role        string   `json:"role"`
			AllowedEnvs []string `json:"allowed_envs,omitempty"`
			Scope       string   `json:"scope,omitempty"`
			ExpiresInDays int    `json:"expires_in_days,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
			http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
			return
		}
		if req.Role == "" {
			req.Role = "publisher"
		}
		if _, ok := roleRank[req.Role]; !ok {
			http.Error(w, `{"error":"invalid role"}`, http.StatusBadRequest)
			return
		}

		caller := callerUsername(r.Context())

		rawToken, tok, err := s.tokenStore.Create(req.Name, req.Scope, req.Role, req.AllowedEnvs, caller, req.ExpiresInDays)
		if err != nil {
			http.Error(w, `{"error":"failed to create token"}`, http.StatusInternalServerError)
			return
		}

		go s.auditStore.Append(context.Background(), AuditRecord{
			ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(),
			Actor: caller, Action: "token.create", ResourceType: "token",
			ResourceID: tok.ID, Status: "success",
			Summary: fmt.Sprintf("%s created token %q (role=%s)", caller, req.Name, req.Role),
		})

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           tok.ID,
			"name":         tok.Name,
			"role":         tok.Role,
			"allowed_envs": tok.AllowedEnvs,
			"scope":        tok.Scope,
			"created_at":   tok.CreatedAt,
			"expires_at":   tok.ExpiresAt,
			"token":        rawToken, // only returned once
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// tokenRevokeHandler serves DELETE /api/tokens/{id}.
func (s *Server) tokenRevokeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.tokenStore == nil {
		http.Error(w, `{"error":"token store not initialised"}`, http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireRole(r.Context(), w, s, "admin") {
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/tokens/")
	id = strings.Trim(id, "/")
	if id == "" {
		http.Error(w, `{"error":"token id required"}`, http.StatusBadRequest)
		return
	}

	if err := s.tokenStore.Revoke(id); err != nil {
		http.Error(w, `{"error":"token not found"}`, http.StatusNotFound)
		return
	}

	caller := callerUsername(r.Context())
	go s.auditStore.Append(context.Background(), AuditRecord{
		ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(),
		Actor: caller, Action: "token.revoke", ResourceType: "token",
		ResourceID: id, Status: "success",
		Summary: fmt.Sprintf("%s revoked token %s", caller, id),
	})

	w.WriteHeader(http.StatusNoContent)
}

// callerUsername extracts the username from the current session or token context.
func callerUsername(ctx context.Context) string {
	if tok, ok := tokenFromContext(ctx); ok {
		return "token:" + tok.Name
	}
	if entry, ok := sessionFromContext(ctx); ok {
		return entry.Username
	}
	return "unknown"
}
