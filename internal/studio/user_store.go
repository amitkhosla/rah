package studio

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// StudioUser is one Studio operator account.
// PasswordHash is bcrypt — never stored in plaintext.
// The entire record is AES-256-GCM encrypted before writing to disk.
type StudioUser struct {
	Username           string `json:"username"`
	PasswordHash       string `json:"password_hash"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"must_change_password,omitempty"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
}

// StudioSeedUser is the config-file representation: bcrypt hash, no plaintext password.
type StudioSeedUser struct {
	Username     string `json:"username"      yaml:"username"`
	PasswordHash string `json:"password_hash" yaml:"password_hash"` // bcrypt
	Role         string `json:"role"          yaml:"role"`
}

// StudioUserStore manages Studio users in memory with optional encrypted file persistence.
// Thread-safe for concurrent reads and writes.
type StudioUserStore struct {
	mu       sync.RWMutex
	users    map[string]*StudioUser // key: lower(username)
	encKey   []byte                 // 32-byte AES-256 key; nil = store without encryption
	filePath string                 // empty = memory only
}

// newStudioUserStore creates a store, loads persisted users, seeds from config, then
// bootstraps a default admin if no users exist after all of the above.
func newStudioUserStore(filePath string, seedUsers []StudioSeedUser) *StudioUserStore {
	encKey := parseEncryptionKey(os.Getenv("RAH_STUDIO_ENCRYPTION_KEY"))

	s := &StudioUserStore{
		users:    make(map[string]*StudioUser),
		encKey:   encKey,
		filePath: filePath,
	}

	// 1. Load persisted users from disk.
	if filePath != "" {
		if err := s.loadFromFile(); err != nil && !os.IsNotExist(err) {
			log.Printf("[Studio] warning: could not load users from %s: %v", filePath, err)
		}
		if filePath != "" && encKey == nil {
			log.Printf("[Studio] warning: RAH_STUDIO_ENCRYPTION_KEY not set — user file stored without encryption")
		}
	}

	// 2. Seed from config (config hash wins — allows password reset via config).
	for _, cu := range seedUsers {
		if cu.Username == "" || cu.PasswordHash == "" {
			continue
		}
		key := strings.ToLower(cu.Username)
		existing, exists := s.users[key]
		if !exists || existing.PasswordHash != cu.PasswordHash {
			role := cu.Role
			if role == "" {
				role = "admin"
			}
			now := time.Now().Unix()
			createdAt := now
			if exists {
				createdAt = existing.CreatedAt
			}
			s.users[key] = &StudioUser{
				Username:     cu.Username,
				PasswordHash: cu.PasswordHash,
				Role:         role,
				CreatedAt:    createdAt,
				UpdatedAt:    now,
			}
		}
	}

	// 3. Bootstrap if still no users.
	if len(s.users) == 0 {
		s.bootstrap()
		// Persist the bootstrapped user immediately so restarts don't re-print the warning.
		if filePath != "" {
			if err := s.saveToFileLocked(); err != nil {
				log.Printf("[Studio] warning: could not persist bootstrap user: %v", err)
			}
		}
	}

	return s
}

// bootstrap creates the initial admin account using (in priority order):
//  1. RAH_STUDIO_ADMIN_PASSWORD env var  → no forced change (automation set it intentionally)
//  2. Hardcoded admin/admin              → forced change on first login
func (s *StudioUserStore) bootstrap() {
	var username, password string
	mustChange := false

	if p := os.Getenv("RAH_STUDIO_ADMIN_PASSWORD"); p != "" {
		u := strings.TrimSpace(os.Getenv("RAH_STUDIO_ADMIN_USERNAME"))
		if u == "" {
			u = "admin"
		}
		username, password = u, p
	} else {
		username, password = "admin", "admin"
		mustChange = true
		log.Printf("")
		log.Printf("╔══════════════════════════════════════════════════════════╗")
		log.Printf("║  RAH Studio — default credentials in use                ║")
		log.Printf("║  Username: admin   Password: admin                      ║")
		log.Printf("║  You will be required to change the password on login.  ║")
		log.Printf("║  Set RAH_STUDIO_ADMIN_PASSWORD to suppress this.        ║")
		log.Printf("╚══════════════════════════════════════════════════════════╝")
		log.Printf("")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("[Studio] failed to hash bootstrap password: %v", err)
		return
	}
	now := time.Now().Unix()
	s.users[strings.ToLower(username)] = &StudioUser{
		Username:           username,
		PasswordHash:       string(hash),
		Role:               "admin",
		MustChangePassword: mustChange,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

// ── Auth operations ───────────────────────────────────────────────────────────

// Authenticate validates username+password using constant-time bcrypt comparison.
// Returns the user on success.
func (s *StudioUserStore) Authenticate(username, password string) (*StudioUser, bool) {
	s.mu.RLock()
	u, ok := s.users[strings.ToLower(username)]
	s.mu.RUnlock()
	if !ok {
		// Constant-time dummy hash to prevent username enumeration via timing.
		_ = bcrypt.CompareHashAndPassword(
			[]byte("$2a$10$invalidhashpadding000000000000000000000000000000000000"),
			[]byte(password),
		)
		return nil, false
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, false
	}
	return u, true
}

// ChangePassword validates oldPassword, sets newPassword (bcrypt), and clears
// MustChangePassword. The flag is cleared exactly once — subsequent logins see false.
func (s *StudioUserStore) ChangePassword(username, oldPassword, newPassword string) error {
	if strings.TrimSpace(newPassword) == "" {
		return errors.New("new password cannot be empty")
	}
	if len(newPassword) < 8 {
		return errors.New("new password must be at least 8 characters")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := strings.ToLower(username)
	u, ok := s.users[key]
	if !ok {
		return errors.New("user not found")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(oldPassword)); err != nil {
		return errors.New("current password is incorrect")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	updated := *u
	updated.PasswordHash = string(hash)
	updated.MustChangePassword = false // cleared once, never set again by the system
	updated.UpdatedAt = time.Now().Unix()
	s.users[key] = &updated
	return s.saveToFileLocked()
}

// ── CRUD ──────────────────────────────────────────────────────────────────────

// Upsert adds or updates a user and persists. Preserves CreatedAt for existing users.
func (s *StudioUserStore) Upsert(u StudioUser) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(u.Username)
	if existing, ok := s.users[key]; ok {
		u.CreatedAt = existing.CreatedAt
	} else {
		u.CreatedAt = time.Now().Unix()
	}
	u.UpdatedAt = time.Now().Unix()
	s.users[key] = &u
	return s.saveToFileLocked()
}

// List returns all users with PasswordHash redacted.
func (s *StudioUserStore) List() []StudioUser {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]StudioUser, 0, len(s.users))
	for _, u := range s.users {
		safe := *u
		safe.PasswordHash = ""
		out = append(out, safe)
	}
	return out
}

// Delete removes a user and persists.
func (s *StudioUserStore) Delete(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.users, strings.ToLower(username))
	return s.saveToFileLocked()
}

// AdminCount returns how many users hold the "admin" role.
func (s *StudioUserStore) AdminCount() int {
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

// Get returns a single user (PasswordHash redacted).
func (s *StudioUserStore) Get(username string) (StudioUser, bool) {
	s.mu.RLock()
	u, ok := s.users[strings.ToLower(username)]
	s.mu.RUnlock()
	if !ok {
		return StudioUser{}, false
	}
	safe := *u
	safe.PasswordHash = ""
	return safe, true
}

// ── Encrypted file persistence ────────────────────────────────────────────────

type userFileEnvelope struct {
	V     int          `json:"v"`
	Users []StudioUser `json:"users"`
}

// saveToFileLocked writes all users to the encrypted file. Caller must hold mu.
func (s *StudioUserStore) saveToFileLocked() error {
	if s.filePath == "" {
		return nil
	}
	users := make([]StudioUser, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, *u)
	}
	payload, err := json.Marshal(userFileEnvelope{V: 1, Users: users})
	if err != nil {
		return err
	}
	if s.encKey != nil {
		payload, err = encryptGCM(s.encKey, payload)
		if err != nil {
			return fmt.Errorf("encrypt: %w", err)
		}
	}
	return os.WriteFile(s.filePath, payload, 0600)
}

// loadFromFile reads and decrypts the user file into memory. Caller must NOT hold mu.
func (s *StudioUserStore) loadFromFile() error {
	raw, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	if s.encKey != nil {
		raw, err = decryptGCM(s.encKey, raw)
		if err != nil {
			return fmt.Errorf("decrypt user file (wrong key?): %w", err)
		}
	}
	var env userFileEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("parse user file: %w", err)
	}
	for _, u := range env.Users {
		u2 := u
		s.users[strings.ToLower(u.Username)] = &u2
	}
	return nil
}

// ── AES-256-GCM helpers ───────────────────────────────────────────────────────

// encryptGCM encrypts plaintext with AES-256-GCM and returns base64(nonce||ciphertext).
// The output is text-safe so the file remains a plain text file.
func encryptGCM(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	enc := base64.StdEncoding.EncodeToString(sealed)
	return []byte(enc), nil
}

// decryptGCM decodes base64(nonce||ciphertext) and decrypts with AES-256-GCM.
func decryptGCM(key, encoded []byte) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("ciphertext too short")
	}
	return gcm.Open(nil, raw[:ns], raw[ns:], nil)
}

// parseEncryptionKey parses RAH_STUDIO_ENCRYPTION_KEY as hex (64 chars) or base64 (44 chars)
// into a 32-byte AES-256 key. Returns nil and logs a warning on any mismatch.
func parseEncryptionKey(raw string) []byte {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	// Hex: 64 hex characters = 32 bytes
	if b, err := hex.DecodeString(raw); err == nil {
		if len(b) == 32 {
			return b
		}
		log.Printf("[Studio] RAH_STUDIO_ENCRYPTION_KEY hex decoded to %d bytes, need 32 — ignoring", len(b))
		return nil
	}
	// Base64 (standard or URL-safe, with or without padding)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.URLEncoding,
		base64.RawStdEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(raw); err == nil {
			if len(b) == 32 {
				return b
			}
		}
	}
	log.Printf("[Studio] RAH_STUDIO_ENCRYPTION_KEY is not a valid 32-byte hex or base64 value — encryption disabled")
	return nil
}
