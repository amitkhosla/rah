package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"github.com/amitkhosla/rah/internal/config"
)

// â”€â”€ Role permission engine â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// compiledRole holds the parsed permission rules for one role.
type compiledRole struct {
	name  string
	rules []permRule
}

// permRule is one parsed permission entry.
type permRule struct {
	method string // "*" or uppercase HTTP verb
	prefix string // "" means exact match; otherwise match path prefix
	exact  string // non-empty when no wildcard
}

// allows returns true if the rule permits method+path.
func (p permRule) allows(method, path string) bool {
	if p.method != "*" && p.method != strings.ToUpper(method) {
		return false
	}
	if p.prefix != "" {
		return strings.HasPrefix(path, p.prefix)
	}
	return p.exact == path || p.exact == "*"
}

// compileRole parses a RoleConfig into a compiledRole.
func compileRole(rc config.RoleConfig) compiledRole {
	cr := compiledRole{name: rc.Name}
	for _, perm := range rc.Permissions {
		if perm == "*" {
			cr.rules = append(cr.rules, permRule{method: "*", exact: "*"})
			continue
		}
		method := "*"
		pathPart := perm
		if idx := strings.Index(perm, ":"); idx >= 0 {
			method = strings.ToUpper(perm[:idx])
			pathPart = perm[idx+1:]
		}
		if strings.HasSuffix(pathPart, "/*") {
			cr.rules = append(cr.rules, permRule{method: method, prefix: strings.TrimSuffix(pathPart, "*")})
		} else {
			cr.rules = append(cr.rules, permRule{method: method, exact: pathPart})
		}
	}
	return cr
}

func (cr compiledRole) allows(method, path string) bool {
	for _, r := range cr.rules {
		if r.allows(method, path) {
			return true
		}
	}
	return false
}

// â”€â”€ AdminUser â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// AdminUser is one user record, persisted to the datastore.
type AdminUser struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"` // bcrypt
	Role         string `json:"role"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// â”€â”€ AdminUserStore â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// AdminUserStore holds users and compiled roles in memory.
// Mutations persist to the datastore. Safe for concurrent use.
type AdminUserStore struct {
	mu      sync.RWMutex
	users   map[string]*AdminUser  // key: lower(username)
	roles   map[string]compiledRole // key: lower(role name)
	enabled bool
	realm   string
	dsm     *DataStoreManager
}

// builtinRoles are always available unless overridden in config.
var builtinRoles = []config.RoleConfig{
	{Name: "admin", Description: "Full access to all endpoints", Permissions: []string{"*"}},
	{Name: "readonly", Description: "Read-only access (GET requests only)", Permissions: []string{"GET:*"}},
}

// NewAdminUserStore creates a store seeded from config and the datastore.
func NewAdminUserStore(ctx context.Context, dsm *DataStoreManager, cfg config.AdminConfig) *AdminUserStore {
	realm := cfg.Realm
	if realm == "" {
		realm = "RAH"
	}
	s := &AdminUserStore{
		users:   make(map[string]*AdminUser),
		roles:   make(map[string]compiledRole),
		enabled: cfg.Enabled,
		realm:   realm,
		dsm:     dsm,
	}

	// Compile built-in roles first, then DB roles, then config roles (highest priority).
	for _, rc := range builtinRoles {
		s.roles[strings.ToLower(rc.Name)] = compileRole(rc)
	}
	// Load persisted roles from datastore.
	if dsm != nil && dsm.IsConfigured(config.DomainAdminRoles) {
		keys, _ := dsm.ListGlobalKeys(ctx, config.DomainAdminRoles, "")
		for _, k := range keys {
			data, ok, err := dsm.GetGlobal(ctx, config.DomainAdminRoles, k)
			if err != nil || !ok {
				continue
			}
			var rc config.RoleConfig
			if json.Unmarshal(data, &rc) == nil && rc.Name != "" {
				s.roles[strings.ToLower(rc.Name)] = compileRole(rc)
			}
		}
	}
	// Config roles win over DB roles (allows config-driven override/reset).
	for _, rc := range cfg.Roles {
		if rc.Name != "" {
			s.roles[strings.ToLower(rc.Name)] = compileRole(rc)
		}
	}

	// Load persisted users from datastore first.
	if dsm != nil && dsm.IsConfigured(config.DomainAdminUsers) {
		keys, _ := dsm.ListGlobalKeys(ctx, config.DomainAdminUsers, "")
		for _, k := range keys {
			data, ok, err := dsm.GetGlobal(ctx, config.DomainAdminUsers, k)
			if err != nil || !ok {
				continue
			}
			var u AdminUser
			if json.Unmarshal(data, &u) == nil {
				s.users[strings.ToLower(u.Username)] = &u
			}
		}
	}

	// Seed from config â€” config hash wins (enables password reset via config).
	for _, cu := range cfg.Users {
		if cu.Username == "" || cu.PasswordHash == "" {
			continue
		}
		role := cu.Role
		if role == "" {
			role = "admin"
		}
		key := strings.ToLower(cu.Username)
		existing, exists := s.users[key]
		if !exists || existing.PasswordHash != cu.PasswordHash {
			u := &AdminUser{
				Username:     cu.Username,
				PasswordHash: cu.PasswordHash,
				Role:         role,
				CreatedAt:    time.Now().Unix(),
				UpdatedAt:    time.Now().Unix(),
			}
			s.users[key] = u
			if dsm != nil && dsm.IsConfigured(config.DomainAdminUsers) {
				if b, err := json.Marshal(u); err == nil {
					_ = dsm.PutGlobal(ctx, config.DomainAdminUsers, key, b)
				}
			}
		}
	}

	return s
}

// Authenticate returns true if username+password match a stored user.
// Uses constant-time bcrypt comparison.
func (s *AdminUserStore) Authenticate(username, password string) bool {
	s.mu.RLock()
	u, ok := s.users[strings.ToLower(username)]
	s.mu.RUnlock()
	if !ok {
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidhashpadding000000000000000000000000000000000000"), []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
}

// Permitted returns true if the user's role allows method+path.
func (s *AdminUserStore) Permitted(username, method, path string) bool {
	s.mu.RLock()
	u, ok := s.users[strings.ToLower(username)]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	s.mu.RLock()
	role, hasRole := s.roles[strings.ToLower(u.Role)]
	s.mu.RUnlock()
	if !hasRole {
		return false
	}
	return role.allows(method, path)
}

// ListRoles returns all configured role definitions (built-in + custom).
func (s *AdminUserStore) ListRoles() []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]any, 0, len(s.roles))
	for _, cr := range s.roles {
		perms := make([]string, 0, len(cr.rules))
		for _, r := range cr.rules {
			if r.exact == "*" && r.method == "*" {
				perms = append(perms, "*")
			} else if r.prefix != "" {
				perms = append(perms, r.method+":"+r.prefix+"*")
			} else {
				perms = append(perms, r.method+":"+r.exact)
			}
		}
		out = append(out, map[string]any{"name": cr.name, "permissions": perms})
	}
	return out
}

// List returns all users without password hashes.
func (s *AdminUserStore) List() []AdminUser {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AdminUser, 0, len(s.users))
	for _, u := range s.users {
		safe := *u
		safe.PasswordHash = ""
		out = append(out, safe)
	}
	return out
}

// Upsert adds or updates a user and persists to datastore.
func (s *AdminUserStore) Upsert(ctx context.Context, u AdminUser) error {
	key := strings.ToLower(u.Username)
	s.mu.Lock()
	if existing, ok := s.users[key]; ok {
		u.CreatedAt = existing.CreatedAt
	} else {
		u.CreatedAt = time.Now().Unix()
	}
	u.UpdatedAt = time.Now().Unix()
	s.users[key] = &u
	s.mu.Unlock()

	if s.dsm != nil && s.dsm.IsConfigured(config.DomainAdminUsers) {
		b, err := json.Marshal(u)
		if err != nil {
			return err
		}
		return s.dsm.PutGlobal(ctx, config.DomainAdminUsers, key, b)
	}
	return nil
}

// UpsertRole adds or updates a role in memory and persists it to the datastore.
// Built-in roles (admin, readonly) can be overridden this way.
func (s *AdminUserStore) UpsertRole(ctx context.Context, rc config.RoleConfig) error {
	cr := compileRole(rc)
	key := strings.ToLower(rc.Name)
	s.mu.Lock()
	s.roles[key] = cr
	s.mu.Unlock()

	if s.dsm != nil && s.dsm.IsConfigured(config.DomainAdminRoles) {
		b, err := json.Marshal(rc)
		if err != nil {
			return err
		}
		return s.dsm.PutGlobal(ctx, config.DomainAdminRoles, key, b)
	}
	return nil
}

// DeleteRole removes a role from memory and datastore.
// Returns an error if any user is currently assigned this role.
func (s *AdminUserStore) DeleteRole(ctx context.Context, name string) error {
	key := strings.ToLower(name)
	// Prevent deletion if any user holds this role.
	s.mu.RLock()
	for _, u := range s.users {
		if strings.ToLower(u.Role) == key {
			s.mu.RUnlock()
			return fmt.Errorf("role %q is assigned to one or more users", name)
		}
	}
	s.mu.RUnlock()

	s.mu.Lock()
	delete(s.roles, key)
	s.mu.Unlock()

	if s.dsm != nil && s.dsm.IsConfigured(config.DomainAdminRoles) {
		return s.dsm.DeleteGlobal(ctx, config.DomainAdminRoles, key)
	}
	return nil
}

// Delete removes a user from memory and datastore.
func (s *AdminUserStore) Delete(ctx context.Context, username string) error {
	key := strings.ToLower(username)
	s.mu.Lock()
	delete(s.users, key)
	s.mu.Unlock()
	if s.dsm != nil && s.dsm.IsConfigured(config.DomainAdminUsers) {
		return s.dsm.DeleteGlobal(ctx, config.DomainAdminUsers, key)
	}
	return nil
}

// adminAdminCount returns how many users hold admin role.
func (s *AdminUserStore) adminCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, u := range s.users {
		if strings.ToLower(u.Role) == "admin" {
			n++
		}
	}
	return n
}

// â”€â”€ Middleware â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// exemptPaths are always accessible without credentials.
var exemptPaths = map[string]bool{
	"POST /admin/users/hash": true, // bootstrap: generate hash before credentials exist
}

// Middleware returns an http.Handler that enforces auth when enabled.
//
//   - If admin.enabled is false: all requests pass through unchanged.
//   - Exempt paths (e.g. POST /admin/users/hash) always pass through.
//   - Otherwise: requires valid Basic Auth credentials, then checks that the
//     user's role permits method+path. Returns 401 or 403 on failure.
func (s *AdminUserStore) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled {
			next.ServeHTTP(w, r)
			return
		}
		// Check exempt paths
		if exemptPaths[r.Method+" "+r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		username, password, ok := r.BasicAuth()
		if !ok || !s.Authenticate(username, password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+s.realm+`"`)
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if !s.Permitted(username, r.Method, r.URL.Path) {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"forbidden: your role does not permit this action"}`, http.StatusForbidden)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyAdminUser{}, username)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// â”€â”€ Context helpers â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

type ctxKeyAdminUser struct{}

// AdminUserFromContext returns the authenticated username, or "" if not set.
func AdminUserFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyAdminUser{}).(string)
	return v
}

// HashPassword returns a bcrypt hash of password suitable for storage.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}
