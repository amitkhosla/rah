package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AppDraft holds the draft APIs and flows for one app, pending deployment.
type AppDraft struct {
	AppName   string          `json:"app_name"`
	UpdatedAt time.Time       `json:"updated_at"`
	APIs      json.RawMessage `json:"apis"`  // []SyncAPIEntry JSON
	Flows     json.RawMessage `json:"flows"` // []SyncFlowEntry JSON
}

// DraftStore manages per-app draft state.
type DraftStore interface {
	GetDraft(ctx context.Context, appName string) (AppDraft, bool, error)
	PutDraft(ctx context.Context, draft AppDraft) error
	DeleteAPIfromDraft(ctx context.Context, appName, apiName string) error
	ListDrafts(ctx context.Context) ([]AppDraft, error)
}

// ---- InMemoryDraftStore ----

// InMemoryDraftStore stores drafts in memory (lost on restart).
type InMemoryDraftStore struct {
	mu     sync.RWMutex
	drafts map[string]AppDraft
}

func (s *InMemoryDraftStore) GetDraft(_ context.Context, appName string) (AppDraft, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.drafts[appName]
	return d, ok, nil
}

func (s *InMemoryDraftStore) PutDraft(_ context.Context, draft AppDraft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drafts[draft.AppName] = draft
	return nil
}

func (s *InMemoryDraftStore) DeleteAPIfromDraft(_ context.Context, appName, apiName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drafts[appName]
	if !ok {
		return nil
	}
	filtered, err := filterByName(d.APIs, apiName)
	if err != nil {
		return err
	}
	d.APIs = filtered
	d.UpdatedAt = time.Now()
	s.drafts[appName] = d
	return nil
}

func (s *InMemoryDraftStore) ListDrafts(_ context.Context) ([]AppDraft, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AppDraft, 0, len(s.drafts))
	for _, d := range s.drafts {
		out = append(out, d)
	}
	return out, nil
}

// ---- FileDraftStore ----

// FileDraftStore persists one JSON file per app under {dir}/{appName}.json.
type FileDraftStore struct {
	mu  sync.Mutex
	dir string
}

func (s *FileDraftStore) GetDraft(_ context.Context, appName string) (AppDraft, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.filePath(appName))
	if os.IsNotExist(err) {
		return AppDraft{}, false, nil
	}
	if err != nil {
		return AppDraft{}, false, err
	}
	var d AppDraft
	if err := json.Unmarshal(data, &d); err != nil {
		return AppDraft{}, false, err
	}
	return d, true, nil
}

func (s *FileDraftStore) PutDraft(_ context.Context, draft AppDraft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("draft store mkdir: %w", err)
	}
	data, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath(draft.AppName), data, 0o644)
}

func (s *FileDraftStore) DeleteAPIfromDraft(_ context.Context, appName, apiName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.filePath(appName))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var d AppDraft
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	filtered, err := filterByName(d.APIs, apiName)
	if err != nil {
		return err
	}
	d.APIs = filtered
	d.UpdatedAt = time.Now()
	out, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath(appName), out, 0o644)
}

func (s *FileDraftStore) ListDrafts(_ context.Context) ([]AppDraft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []AppDraft
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var d AppDraft
		if err := json.Unmarshal(data, &d); err != nil {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *FileDraftStore) filePath(appName string) string {
	return filepath.Join(s.dir, appName+".json")
}

// ---- Factory ----

// NewDraftStore creates a DraftStore backed by files (kind=="file") or memory.
func NewDraftStore(kind, path string) DraftStore {
	if kind == "file" && path != "" {
		return &FileDraftStore{dir: filepath.Join(path, "drafts")}
	}
	return &InMemoryDraftStore{drafts: make(map[string]AppDraft)}
}

// ---- Helpers ----

// filterByName removes the entry whose "name" field equals apiName from a JSON array.
func filterByName(raw json.RawMessage, apiName string) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("[]"), nil
	}
	var items []map[string]interface{}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	out := items[:0]
	for _, item := range items {
		if name, _ := item["name"].(string); name != apiName {
			out = append(out, item)
		}
	}
	result, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// mergeDraftPayload merges two JSON arrays of objects by their "name" field.
// Entries in newRaw replace same-named entries in oldRaw.
// Entries whose "action" field is "delete" are removed from the result.
func mergeDraftPayload(oldRaw, newRaw json.RawMessage) (json.RawMessage, error) {
	if len(oldRaw) == 0 {
		oldRaw = json.RawMessage("[]")
	}
	if len(newRaw) == 0 {
		newRaw = json.RawMessage("[]")
	}

	var oldItems []map[string]interface{}
	if err := json.Unmarshal(oldRaw, &oldItems); err != nil {
		return nil, fmt.Errorf("parse old: %w", err)
	}
	var newItems []map[string]interface{}
	if err := json.Unmarshal(newRaw, &newItems); err != nil {
		return nil, fmt.Errorf("parse new: %w", err)
	}

	// Build index from old items by name.
	index := make(map[string]int, len(oldItems))
	for i, item := range oldItems {
		if name, _ := item["name"].(string); name != "" {
			index[name] = i
		}
	}

	// Apply new items onto old.
	merged := make([]map[string]interface{}, len(oldItems))
	copy(merged, oldItems)

	for _, item := range newItems {
		name, _ := item["name"].(string)
		action, _ := item["action"].(string)
		if action == "delete" {
			if idx, ok := index[name]; ok {
				// Mark for removal by setting to nil placeholder.
				merged[idx] = nil
			}
			continue
		}
		if idx, ok := index[name]; ok {
			merged[idx] = item
		} else {
			merged = append(merged, item)
		}
	}

	// Compact (remove nils).
	result := merged[:0]
	for _, item := range merged {
		if item != nil {
			result = append(result, item)
		}
	}

	out, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return out, nil
}
