package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// roleRank maps role names to numeric rank. Higher = more permissions.
var roleRank = map[string]int{
	"viewer":    0,
	"reviewer":  1,
	"publisher": 2,
	"deployer":  3,
	"admin":     4,
}

// ctxKeyStudioToken is the context key for an authenticated machine token.
type ctxKeyStudioToken struct{}

// tokenFromContext returns the StudioToken injected by studioAuthMiddleware.
func tokenFromContext(ctx context.Context) (*StudioToken, bool) {
	tok, ok := ctx.Value(ctxKeyStudioToken{}).(*StudioToken)
	return tok, ok && tok != nil
}

// callerRole returns the effective role for the current request.
// Checks token context first, then session context, then falls back to userStore.
// Returns "admin" when auth is disabled (s.sessions == nil).
func callerRole(ctx context.Context, s *Server) string {
	if s.sessions == nil {
		return "admin"
	}
	// Token-authenticated request.
	if tok, ok := tokenFromContext(ctx); ok {
		return tok.Role
	}
	// Session-authenticated request.
	if entry, ok := sessionFromContext(ctx); ok {
		if entry.Role != "" {
			return entry.Role
		}
		// Fallback for sessions created before Role was added to sessionEntry.
		if s.userStore != nil {
			if user, exists := s.userStore.Get(entry.Username); exists {
				return user.Role
			}
		}
	}
	return ""
}

// callerAllowedEnvs returns the environment allowlist for the current caller.
// nil means no restriction (all environments permitted).
// Admin role always returns nil regardless of stored AllowedEnvs.
func callerAllowedEnvs(ctx context.Context, s *Server) []string {
	if callerRole(ctx, s) == "admin" {
		return nil
	}
	if tok, ok := tokenFromContext(ctx); ok {
		return tok.AllowedEnvs
	}
	if entry, ok := sessionFromContext(ctx); ok {
		if s.userStore != nil {
			if user, exists := s.userStore.Get(entry.Username); exists {
				return user.AllowedEnvs
			}
		}
	}
	return nil
}

// requireRole checks that the caller holds at least the minimum role.
// Writes a 403 JSON response and returns false if the check fails.
// Always returns true when auth is disabled (s.sessions == nil).
func requireRole(ctx context.Context, w http.ResponseWriter, s *Server, minimum string) bool {
	if s.sessions == nil {
		return true
	}
	role := callerRole(ctx, s)
	if roleRank[role] < roleRank[minimum] {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "insufficient role: requires " + minimum + ", caller has " + role,
		})
		return false
	}
	return true
}

// requireAdmin is shorthand for requireRole with "admin".
func requireAdmin(ctx context.Context, w http.ResponseWriter, s *Server) bool {
	return requireRole(ctx, w, s, "admin")
}

// envAllowed checks whether the caller is permitted to deploy to the given environment.
// Returns true if allowed. Admin callers always return true.
// env comparison is case-insensitive.
func envAllowed(ctx context.Context, s *Server, env string) bool {
	allowed := callerAllowedEnvs(ctx, s)
	if allowed == nil {
		return true
	}
	env = strings.ToLower(strings.TrimSpace(env))
	for _, e := range allowed {
		if strings.ToLower(strings.TrimSpace(e)) == env {
			return true
		}
	}
	return false
}
