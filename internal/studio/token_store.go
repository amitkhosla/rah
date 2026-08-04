package studio

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// StudioToken is a long-lived machine token for CI/CD pipelines.
// The raw token value is only returned on creation and never stored.
// Only the SHA-256 hash is persisted.
type StudioToken struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Scope       string   `json:"scope"` // "read_only"|"publish_only"|"promote_only"|"full"|"promote:<env>"
	Role        string   `json:"role"`
	AllowedEnvs []string `json:"allowed_envs,omitempty"`
	HashSHA256  string   `json:"hash_sha256"`
	CreatedAt   int64    `json:"created_at"`
	CreatedBy   string   `json:"created_by"`
	LastUsedAt  int64    `json:"last_used_at,omitempty"`
	ExpiresAt   int64    `json:"expires_at,omitempty"` // 0 = never
	Revoked     bool     `json:"revoked,omitempty"`
}

// StudioTokenStore manages CI/CD machine tokens with encrypted file persistence.
type StudioTokenStore struct {
	mu       sync.RWMutex
	byHash   map[string]*StudioToken // key: HashSHA256
	byID     map[string]*StudioToken // key: ID
	encKey   []byte
	filePath string
}

type tokenFileEnvelope struct {
	V      int           `json:"v"`
	Tokens []StudioToken `json:"tokens"`
}

// newTokenStore creates a token store. dir may be empty for memory-only mode.
func newTokenStore(dir string, encKey []byte) *StudioTokenStore {
	s := &StudioTokenStore{
		byHash: make(map[string]*StudioToken),
		byID:   make(map[string]*StudioToken),
		encKey: encKey,
	}
	if dir != "" {
		s.filePath = filepath.Join(dir, "tokens.json")
		if err := s.load(); err != nil && !os.IsNotExist(err) {
			fmt.Printf("[Studio] warning: could not load tokens from %s: %v\n", s.filePath, err)
		}
	}
	return s
}

// generateToken returns a new raw token and its SHA-256 hash.
// Raw token format: rahst_<base64url(32 random bytes)>
func generateToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	raw = "rahst_" + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(sum[:])
	return
}

// tokenHash computes the SHA-256 hash of a raw token without any locks.
func tokenHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Create generates a new token, stores it, and returns the raw value (shown once).
func (s *StudioTokenStore) Create(name, scope, role string, allowedEnvs []string, createdBy string, expiresInDays int) (rawToken string, tok *StudioToken, err error) {
	raw, hash, err := generateToken()
	if err != nil {
		return "", nil, fmt.Errorf("token generation: %w", err)
	}

	t := &StudioToken{
		ID:          "tok_" + hash[:8],
		Name:        name,
		Scope:       scope,
		Role:        role,
		AllowedEnvs: allowedEnvs,
		HashSHA256:  hash,
		CreatedAt:   time.Now().Unix(),
		CreatedBy:   createdBy,
	}
	if expiresInDays > 0 {
		t.ExpiresAt = time.Now().AddDate(0, 0, expiresInDays).Unix()
	}

	s.mu.Lock()
	s.byHash[hash] = t
	s.byID[t.ID] = t
	err = s.save()
	s.mu.Unlock()

	if err != nil {
		return "", nil, fmt.Errorf("save token: %w", err)
	}
	return raw, t, nil
}

// Lookup finds a token by raw value. Returns nil if not found, revoked, or expired.
// The SHA-256 hash is computed before acquiring any lock.
func (s *StudioTokenStore) Lookup(rawToken string) (*StudioToken, bool) {
	hash := tokenHash(rawToken) // compute outside lock
	s.mu.RLock()
	t, ok := s.byHash[hash]
	s.mu.RUnlock()
	if !ok || t.Revoked {
		return nil, false
	}
	if t.ExpiresAt > 0 && time.Now().Unix() > t.ExpiresAt {
		return nil, false
	}
	// Update last used asynchronously to avoid lock contention on hot path.
	go func() {
		s.mu.Lock()
		if cur, exists := s.byHash[hash]; exists {
			cur.LastUsedAt = time.Now().Unix()
			_ = s.save()
		}
		s.mu.Unlock()
	}()
	// Return a copy so callers cannot mutate stored state.
	copy := *t
	return &copy, true
}

// Revoke marks a token as revoked by ID.
func (s *StudioTokenStore) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("token %q not found", id)
	}
	t.Revoked = true
	return s.save()
}

// List returns all non-revoked tokens with HashSHA256 cleared for security.
func (s *StudioTokenStore) List() []StudioToken {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]StudioToken, 0, len(s.byID))
	for _, t := range s.byID {
		if t.Revoked {
			continue
		}
		safe := *t
		safe.HashSHA256 = ""
		out = append(out, safe)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out
}

// load reads tokens from disk. Must be called without holding mu.
func (s *StudioTokenStore) load() error {
	if s.filePath == "" {
		return nil
	}
	raw, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	if s.encKey != nil {
		raw, err = decryptGCM(s.encKey, raw)
		if err != nil {
			return fmt.Errorf("decrypt token file: %w", err)
		}
	}
	var env tokenFileEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("parse token file: %w", err)
	}
	for _, t := range env.Tokens {
		t2 := t
		s.byHash[t.HashSHA256] = &t2
		s.byID[t.ID] = &t2
	}
	return nil
}

// save persists tokens to disk. Caller must hold mu (write lock).
func (s *StudioTokenStore) save() error {
	if s.filePath == "" {
		return nil
	}
	tokens := make([]StudioToken, 0, len(s.byID))
	for _, t := range s.byID {
		tokens = append(tokens, *t)
	}
	payload, err := json.Marshal(tokenFileEnvelope{V: 1, Tokens: tokens})
	if err != nil {
		return err
	}
	if s.encKey != nil {
		payload, err = encryptGCM(s.encKey, payload)
		if err != nil {
			return fmt.Errorf("encrypt token file: %w", err)
		}
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, payload, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath)
}
