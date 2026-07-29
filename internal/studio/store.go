package studio

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type ReleaseStore interface {
	Put(context.Context, ReleaseRecord) error
	Get(context.Context, string) (ReleaseRecord, error)
	List(context.Context) ([]ReleaseRecord, error)
}

type InMemoryReleaseStore struct {
	mu       sync.RWMutex
	releases map[string]ReleaseRecord
}

func NewInMemoryReleaseStore() *InMemoryReleaseStore {
	return &InMemoryReleaseStore{releases: map[string]ReleaseRecord{}}
}

func (s *InMemoryReleaseStore) Put(_ context.Context, rec ReleaseRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases[rec.ReleaseID] = rec
	return nil
}

func (s *InMemoryReleaseStore) Get(_ context.Context, id string) (ReleaseRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.releases[id]
	if !ok {
		return ReleaseRecord{}, errors.New("release not found")
	}
	return rec, nil
}

func (s *InMemoryReleaseStore) List(_ context.Context) ([]ReleaseRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ReleaseRecord, 0, len(s.releases))
	for _, r := range s.releases {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.T > out[j].CreatedAt.T })
	return out, nil
}

type FileReleaseStore struct {
	mu   sync.Mutex
	path string
}

func NewFileReleaseStore(path string) *FileReleaseStore { return &FileReleaseStore{path: path} }

func (s *FileReleaseStore) Put(_ context.Context, rec ReleaseRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return err
	}
	all[rec.ReleaseID] = rec
	return s.writeAllLocked(all)
}

func (s *FileReleaseStore) Get(_ context.Context, id string) (ReleaseRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return ReleaseRecord{}, err
	}
	rec, ok := all[id]
	if !ok {
		return ReleaseRecord{}, errors.New("release not found")
	}
	return rec, nil
}

func (s *FileReleaseStore) List(_ context.Context) ([]ReleaseRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return nil, err
	}
	out := make([]ReleaseRecord, 0, len(all))
	for _, r := range all {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.T > out[j].CreatedAt.T })
	return out, nil
}

func (s *FileReleaseStore) readAllLocked() (map[string]ReleaseRecord, error) {
	all := map[string]ReleaseRecord{}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return all, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return all, nil
	}
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, err
	}
	return all, nil
}

func (s *FileReleaseStore) writeAllLocked(all map[string]ReleaseRecord) error {
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

func releaseStoreFromConfig(kind, path string) ReleaseStore {
	if kind == "file" && path != "" {
		return NewFileReleaseStore(path)
	}
	return NewInMemoryReleaseStore()
}

func nowUTC() time.Time { return time.Now().UTC() }

// ── Chat history ──────────────────────────────────────────────────────────────

type ChatAuditRecord struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	Summary     string    `json:"summary"`
	Artifacts   []string  `json:"artifacts"`
	GatewayName string    `json:"gateway_name"`
}

type ProjectContextRecord struct {
	ID        string    `json:"id"`
	UpdatedAt time.Time `json:"updated_at"`
	Content   string    `json:"content"`
}

type ChatHistoryStore interface {
	AppendAudit(ctx context.Context, r ChatAuditRecord) error
	ListAudits(ctx context.Context) ([]ChatAuditRecord, error)
	GetProjectContext(ctx context.Context) (ProjectContextRecord, error)
	PutProjectContext(ctx context.Context, r ProjectContextRecord) error
}

type InMemoryChatHistoryStore struct {
	mu      sync.RWMutex
	audits  []ChatAuditRecord
	context ProjectContextRecord
}

func NewInMemoryChatHistoryStore() *InMemoryChatHistoryStore {
	return &InMemoryChatHistoryStore{}
}

func (s *InMemoryChatHistoryStore) AppendAudit(_ context.Context, r ChatAuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = append(s.audits, r)
	return nil
}

func (s *InMemoryChatHistoryStore) ListAudits(_ context.Context) ([]ChatAuditRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ChatAuditRecord, len(s.audits))
	copy(out, s.audits)
	return out, nil
}

func (s *InMemoryChatHistoryStore) GetProjectContext(_ context.Context) (ProjectContextRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.context, nil
}

func (s *InMemoryChatHistoryStore) PutProjectContext(_ context.Context, r ProjectContextRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.context = r
	return nil
}

type FileChatHistoryStore struct {
	mu          sync.Mutex
	auditPath   string
	contextPath string
}

func NewFileChatHistoryStore(dir string) *FileChatHistoryStore {
	return &FileChatHistoryStore{
		auditPath:   dir + "/chat_audit.ndjson",
		contextPath: dir + "/project_context.json",
	}
}

func (s *FileChatHistoryStore) AppendAudit(_ context.Context, r ChatAuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

func (s *FileChatHistoryStore) ListAudits(_ context.Context) ([]ChatAuditRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.auditPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []ChatAuditRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var r ChatAuditRecord
		if err := json.Unmarshal([]byte(line), &r); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *FileChatHistoryStore) GetProjectContext(_ context.Context) (ProjectContextRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.contextPath)
	if os.IsNotExist(err) {
		return ProjectContextRecord{ID: "project_context"}, nil
	}
	if err != nil {
		return ProjectContextRecord{}, err
	}
	var r ProjectContextRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return ProjectContextRecord{}, err
	}
	return r, nil
}

func (s *FileChatHistoryStore) PutProjectContext(_ context.Context, r ProjectContextRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.contextPath, data, 0644)
}

func newChatHistoryStore(kind, path string) ChatHistoryStore {
	if kind == "file" && path != "" {
		return NewFileChatHistoryStore(path)
	}
	return NewInMemoryChatHistoryStore()
}

// ── FlowBaselineStore ─────────────────────────────────────────────────────────

// FlowBaselineStore persists tagged flow baseline snapshots.
type FlowBaselineStore interface {
	GetBaseline(tag string) (*FlowBaselineRecord, error)
	ListBaselines() ([]*FlowBaselineRecord, error)
	SaveBaseline(rec *FlowBaselineRecord) error
	DeleteBaseline(tag string) error
	PromoteBaseline(fromTag, toTag string) error
}

type memFlowBaselineStore struct {
	mu        sync.RWMutex
	baselines map[string]*FlowBaselineRecord
}

func newMemFlowBaselineStore() *memFlowBaselineStore {
	return &memFlowBaselineStore{baselines: make(map[string]*FlowBaselineRecord)}
}

func (s *memFlowBaselineStore) GetBaseline(tag string) (*FlowBaselineRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.baselines[tag]
	if !ok {
		return nil, errors.New("baseline not found: " + tag)
	}
	cp := *rec
	return &cp, nil
}

func (s *memFlowBaselineStore) ListBaselines() ([]*FlowBaselineRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*FlowBaselineRecord, 0, len(s.baselines))
	for _, rec := range s.baselines {
		cp := *rec
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out, nil
}

func (s *memFlowBaselineStore) SaveBaseline(rec *FlowBaselineRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *rec
	s.baselines[rec.Tag] = &cp
	return nil
}

func (s *memFlowBaselineStore) DeleteBaseline(tag string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.baselines, tag)
	return nil
}

func (s *memFlowBaselineStore) PromoteBaseline(fromTag, toTag string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.baselines[fromTag]
	if !ok {
		return errors.New("baseline not found: " + fromTag)
	}
	cp := *src
	cp.Tag = toTag
	cp.CreatedAt = nowUTC()
	s.baselines[toTag] = &cp
	return nil
}

// ── VersionHistoryStore ───────────────────────────────────────────────────────

// VersionHistoryStore persists the deployment version history per environment.
type VersionHistoryStore interface {
	AppendVersion(envID string, rec *VersionRecord) error
	ListVersions(envID string) ([]*VersionRecord, error)
	GetVersion(envID, versionID string) (*VersionRecord, error)
	VoidVersion(envID, versionID, voidedBy, reason string) error
}

type memVersionHistoryStore struct {
	mu       sync.RWMutex
	versions map[string][]*VersionRecord // envID → ordered list
}

func newMemVersionHistoryStore() *memVersionHistoryStore {
	return &memVersionHistoryStore{versions: make(map[string][]*VersionRecord)}
}

func (s *memVersionHistoryStore) AppendVersion(envID string, rec *VersionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *rec
	s.versions[envID] = append(s.versions[envID], &cp)
	return nil
}

func (s *memVersionHistoryStore) ListVersions(envID string) ([]*VersionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.versions[envID]
	out := make([]*VersionRecord, len(src))
	for i, r := range src {
		cp := *r
		out[i] = &cp
	}
	// Newest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *memVersionHistoryStore) GetVersion(envID, versionID string) (*VersionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.versions[envID] {
		if r.VersionID == versionID {
			cp := *r
			return &cp, nil
		}
	}
	return nil, errors.New("version not found: " + versionID)
}

func (s *memVersionHistoryStore) VoidVersion(envID, versionID, voidedBy, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.versions[envID] {
		if r.VersionID == versionID {
			r.Status = "voided"
			r.VoidedBy = voidedBy
			r.VoidedAt = time.Now().Unix()
			r.VoidReason = reason
			return nil
		}
	}
	return errors.New("version not found: " + versionID)
}
