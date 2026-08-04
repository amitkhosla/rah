package studio

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookieName = "rah_session"
const sessionTTL = 8 * time.Hour

// sessionEntry holds an active Studio session.
type sessionEntry struct {
	Username  string
	Role      string
	ExpiresAt time.Time
}

// SessionStore is a thread-safe in-memory session map.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*sessionEntry
}

func newSessionStore() *SessionStore {
	s := &SessionStore{sessions: make(map[string]*sessionEntry)}
	go s.sweepLoop()
	return s
}

func (s *SessionStore) create(username, role string) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	s.sessions[token] = &sessionEntry{
		Username:  username,
		Role:      role,
		ExpiresAt: time.Now().Add(sessionTTL),
	}
	s.mu.Unlock()
	return token
}

// deleteByUsername removes all sessions for the given username.
// Used when a user is deprovisioned via SCIM or their role/envs are changed.
func (s *SessionStore) deleteByUsername(username string) {
	lower := strings.ToLower(username)
	s.mu.Lock()
	for k, e := range s.sessions {
		if strings.ToLower(e.Username) == lower {
			delete(s.sessions, k)
		}
	}
	s.mu.Unlock()
}

func (s *SessionStore) get(token string) (*sessionEntry, bool) {
	if token == "" {
		return nil, false
	}
	s.mu.RLock()
	e, ok := s.sessions[token]
	s.mu.RUnlock()
	if !ok || time.Now().After(e.ExpiresAt) {
		return nil, false
	}
	return e, true
}

func (s *SessionStore) delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (s *SessionStore) sweepLoop() {
	ticker := time.NewTicker(30 * time.Minute)
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for k, e := range s.sessions {
			if now.After(e.ExpiresAt) {
				delete(s.sessions, k)
			}
		}
		s.mu.Unlock()
	}
}

// ctxKeyStudioSession is the context key for the authenticated session.
type ctxKeyStudioSession struct{}

func sessionFromContext(ctx context.Context) (*sessionEntry, bool) {
	e, ok := ctx.Value(ctxKeyStudioSession{}).(*sessionEntry)
	return e, ok && e != nil
}

// ── Request / response types ──────────────────────────────────────────────────

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type meResponse struct {
	Username           string   `json:"username"`
	Role               string   `json:"role"`
	AuthEnabled        bool     `json:"auth_enabled"`
	MustChangePassword bool     `json:"must_change_password,omitempty"`
	SSOProvider        string   `json:"sso_provider,omitempty"`
	AllowedEnvs        []string `json:"allowed_envs,omitempty"`
}

// ── Auth handlers ─────────────────────────────────────────────────────────────

// loginHandler validates credentials against the Studio user store and creates a session.
func (s *Server) loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Username) == "" {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"username required"}`, http.StatusBadRequest)
		return
	}

	user, ok := s.userStore.Authenticate(req.Username, req.Password)
	if !ok {
		go func() { _ = s.auditStore.Append(context.Background(), AuditRecord{
			ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(),
			Actor: req.Username, Action: "login", ResourceType: "session",
			Status: "failure", Summary: req.Username + " login failed",
		}) }()
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	token := s.sessions.create(user.Username, user.Role)
	setSessionCookie(w, token)
	go func() { _ = s.auditStore.Append(context.Background(), AuditRecord{
		ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(),
		Actor: user.Username, Action: "login", ResourceType: "session",
		Status: "success", Summary: user.Username + " logged in",
	}) }()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meResponse{
		Username:           user.Username,
		Role:               user.Role,
		AuthEnabled:        true,
		MustChangePassword: user.MustChangePassword,
		AllowedEnvs:        user.AllowedEnvs,
	})
}

// logoutHandler deletes the session and clears the cookie.
func (s *Server) logoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c, err := r.Cookie(sessionCookieName); err == nil {
		actor := "unknown"
		if entry, ok := s.sessions.get(c.Value); ok {
			actor = entry.Username
		}
		s.sessions.delete(c.Value)
		go func() { _ = s.auditStore.Append(context.Background(), AuditRecord{
			ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(),
			Actor: actor, Action: "logout", ResourceType: "session",
			Status: "success", Summary: actor + " logged out",
		}) }()
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// meHandler returns the current authenticated user.
// When auth is disabled it returns an open-access "admin" so the UI never shows the login form.
func (s *Server) meHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	if s.sessions == nil {
		// Auth disabled — studio is open.
		_ = json.NewEncoder(w).Encode(meResponse{Username: "admin", Role: "admin", AuthEnabled: false})
		return
	}

	entry, ok := sessionFromContext(r.Context())
	if !ok {
		http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
		return
	}
	user, exists := s.userStore.Get(entry.Username)
	if !exists {
		// User deleted after session was created.
		clearSessionCookie(w)
		http.Error(w, `{"error":"user no longer exists"}`, http.StatusUnauthorized)
		return
	}
	_ = json.NewEncoder(w).Encode(meResponse{
		Username:           user.Username,
		Role:               user.Role,
		AuthEnabled:        true,
		MustChangePassword: user.MustChangePassword,
		SSOProvider:        user.SSOProvider,
		AllowedEnvs:        user.AllowedEnvs,
	})
}

// changePasswordHandler validates the current password, sets the new one, and
// clears MustChangePassword. The flag is cleared exactly once and persisted.
func (s *Server) changePasswordHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	entry, ok := sessionFromContext(r.Context())
	if !ok {
		http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
		return
	}

	// SSO-provisioned users have no local password — they must authenticate via their IDP.
	if user, exists := s.userStore.Get(entry.Username); exists && user.SSOProvisioned {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"password change not available for SSO-provisioned users"}`, http.StatusBadRequest)
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if err := s.userStore.ChangePassword(entry.Username, req.CurrentPassword, req.NewPassword); err != nil {
		go func() {
			_ = s.auditStore.Append(context.Background(), AuditRecord{
				ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(),
				Actor: entry.Username, Action: "password.change", ResourceType: "user",
				ResourceID: entry.Username, Status: "failure", Summary: entry.Username + " password change failed",
			})
		}()
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "password changed"})
	go func() {
		_ = s.auditStore.Append(context.Background(), AuditRecord{
			ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(),
			Actor: entry.Username, Action: "password.change", ResourceType: "user",
			ResourceID: entry.Username, Status: "success", Summary: entry.Username + " changed password",
		})
	}()
}

// ── Studio user management handlers (admin only) ──────────────────────────────

// studioUsersHandler handles GET (list) and POST (create/update) on /api/studio/users.
func (s *Server) studioUsersHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"users": s.userStore.List()})

	case http.MethodPost:
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
			strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"username and password required"}`, http.StatusBadRequest)
			return
		}
		role := strings.TrimSpace(req.Role)
		if role == "" {
			role = "admin"
		}
		hash, err := bcryptHash(req.Password)
		if err != nil {
			http.Error(w, `{"error":"failed to hash password"}`, http.StatusInternalServerError)
			return
		}
		u := StudioUser{
			Username:     req.Username,
			PasswordHash: hash,
			Role:         role,
		}
		if err := s.userStore.Upsert(u); err != nil {
			http.Error(w, `{"error":"failed to save user"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"username": u.Username, "role": u.Role})
		actor := "system"
		if sess, ok := sessionFromContext(r.Context()); ok {
			actor = sess.Username
		}
		go func() {
			_ = s.auditStore.Append(context.Background(), AuditRecord{
				ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(),
				Actor: actor, Action: "user.create", ResourceType: "user",
				Status: "success", Summary: fmt.Sprintf("%s created user", actor),
			})
		}()

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// studioUserDeleteHandler handles DELETE /api/studio/users/{username}.
func (s *Server) studioUserDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Extract username from path: /api/studio/users/{username}
	target := strings.TrimPrefix(r.URL.Path, "/api/studio/users/")
	target = strings.Trim(target, "/")
	if target == "" {
		http.Error(w, `{"error":"username required"}`, http.StatusBadRequest)
		return
	}
	caller, _ := sessionFromContext(r.Context())
	if caller != nil && strings.EqualFold(target, caller.Username) {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"cannot delete your own account"}`, http.StatusBadRequest)
		return
	}
	user, exists := s.userStore.Get(target)
	if exists && strings.ToLower(user.Role) == "admin" && s.userStore.AdminCount() <= 1 {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"cannot delete the last admin user"}`, http.StatusBadRequest)
		return
	}
	if err := s.userStore.Delete(target); err != nil {
		http.Error(w, `{"error":"failed to delete user"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"deleted": target})
	actor := "system"
	if sess, ok := sessionFromContext(r.Context()); ok {
		actor = sess.Username
	}
	go func() {
		_ = s.auditStore.Append(context.Background(), AuditRecord{
			ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(),
			Actor: actor, Action: "user.delete", ResourceType: "user",
			Status: "success", Summary: fmt.Sprintf("%s deleted user", actor),
		})
	}()
}

// studioUsersHashHandler is always public — lets operators generate bcrypt hashes
// before any credentials exist (bootstrap helper).
func studioUsersHashHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Password) == "" {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"password required"}`, http.StatusBadRequest)
		return
	}
	hash, err := bcryptHash(req.Password)
	if err != nil {
		http.Error(w, `{"error":"failed to hash"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"hash": hash})
}

// ── Auth middleware ────────────────────────────────────────────────────────────

// studioAuthMiddleware rejects requests without a valid session cookie or machine token.
// When sessions is nil (auth disabled) it is a no-op pass-through.
func (s *Server) studioAuthMiddleware(next http.Handler) http.Handler {
	if s.sessions == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Machine token via Bearer header — checked before cookie so CI/CD scripts work.
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
			raw := strings.TrimPrefix(authHeader, "Bearer ")
			if s.tokenStore != nil {
				if tok, ok := s.tokenStore.Lookup(raw); ok {
					ctx := context.WithValue(r.Context(), ctxKeyStudioToken{}, tok)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		// 2. Session cookie.
		c, err := r.Cookie(sessionCookieName)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
			return
		}
		entry, ok := s.sessions.get(c.Value)
		if !ok {
			clearSessionCookie(w)
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"session expired, please log in again"}`, http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyStudioSession{}, entry)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ── Cookie helpers ────────────────────────────────────────────────────────────

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// bcryptHash returns a bcrypt hash of password suitable for storage.
func bcryptHash(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}
