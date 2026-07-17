package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/config"
)

// RegisterAdminUserRoutes registers admin user and role management endpoints.
// The entire :8081 mux is already wrapped with AdminUserStore.Middleware, so
// individual routes do not need their own auth wrappers.
// POST /admin/users/hash is registered here but is always exempt from auth
// (declared in exemptPaths in auth_middleware.go).
func RegisterAdminUserRoutes(mux *http.ServeMux, store *AdminUserStore) {

	// â”€â”€ Public utility â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
	// Always exempt â€” lets operators generate hashes before credentials exist.
	mux.HandleFunc("POST /admin/users/hash", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
			writeError(w, http.StatusBadRequest, "password required")
			return
		}
		hash, err := HashPassword(req.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to hash")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"hash": hash})
	})

	// â”€â”€ User CRUD â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

	// GET /admin/users â€” list all users (no hashes).
	mux.HandleFunc("GET /admin/users", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"users": store.List()})
	})

	// POST /admin/users â€” create or update a user.
	// Body: {"username":"alice","password":"s3cr3t","role":"deployer"}
	// Role must match a configured role or built-in "admin"/"readonly".
	mux.HandleFunc("POST /admin/users", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" || req.Password == "" {
			writeError(w, http.StatusBadRequest, "username and password required")
			return
		}
		role := strings.TrimSpace(req.Role)
		if role == "" {
			role = "admin"
		}
		hash, err := HashPassword(req.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		u := AdminUser{
			Username:     req.Username,
			PasswordHash: hash,
			Role:         role,
			UpdatedAt:    time.Now().Unix(),
		}
		if err := store.Upsert(r.Context(), u); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save user")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"username": u.Username, "role": u.Role})
	})

	// DELETE /admin/users/{name} â€” remove a user.
	// Guards: cannot delete yourself; cannot delete the last admin-role user.
	mux.HandleFunc("DELETE /admin/users/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			name = strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/users/"), "/")
		}
		if name == "" {
			writeError(w, http.StatusBadRequest, "username required in path")
			return
		}
		caller := AdminUserFromContext(r.Context())
		if strings.EqualFold(name, caller) {
			writeError(w, http.StatusBadRequest, "cannot delete your own account")
			return
		}
		store.mu.RLock()
		target, exists := store.users[strings.ToLower(name)]
		store.mu.RUnlock()
		if exists && strings.ToLower(target.Role) == "admin" && store.adminCount() <= 1 {
			writeError(w, http.StatusBadRequest, "cannot delete the last admin user")
			return
		}
		if err := store.Delete(r.Context(), name); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete user")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
	})

	// â”€â”€ Role inspection â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

	// GET /admin/roles â€” list all roles (built-in + custom) with their permissions.
	mux.HandleFunc("GET /admin/roles", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"roles": store.ListRoles()})
	})

	// POST /admin/roles â€” create or update a role, persisted to DB if configured.
	// Body: {"name":"deployer","description":"CI/CD role","permissions":["POST:/sync","GET:/observability/*"]}
	mux.HandleFunc("POST /admin/roles", func(w http.ResponseWriter, r *http.Request) {
		var rc config.RoleConfig
		if err := json.NewDecoder(r.Body).Decode(&rc); err != nil || rc.Name == "" || len(rc.Permissions) == 0 {
			writeError(w, http.StatusBadRequest, "name and permissions required")
			return
		}
		if err := store.UpsertRole(r.Context(), rc); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save role")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": rc.Name, "permissions": rc.Permissions})
	})

	// DELETE /admin/roles/{name} â€” remove a role.
	// Returns 400 if any user is currently assigned to this role.
	mux.HandleFunc("DELETE /admin/roles/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			name = strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/roles/"), "/")
		}
		if name == "" {
			writeError(w, http.StatusBadRequest, "role name required in path")
			return
		}
		if err := store.DeleteRole(r.Context(), name); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
	})
}
