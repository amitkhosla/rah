package async

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// MemoryJobStore keeps all job state in process memory.
// The queue is a buffered channel (capacity 10 000).
// Suitable for single-instance gateways and development.
// All state is lost on process restart.
type MemoryJobStore struct {
	jobs   sync.Map   // jobID → *Job
	queue  chan string // jobID FIFO
	stopGC chan struct{}
	once   sync.Once
}

func NewMemoryJobStore() *MemoryJobStore {
	s := &MemoryJobStore{
		queue:  make(chan string, 10_000),
		stopGC: make(chan struct{}),
	}
	go s.gcLoop()
	return s
}

func (s *MemoryJobStore) Put(job *Job) error {
	// Store a JSON copy so callers cannot mutate stored state.
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	var copy Job
	if err := json.Unmarshal(data, &copy); err != nil {
		return err
	}
	s.jobs.Store(job.ID, &copy)
	return nil
}

func (s *MemoryJobStore) Get(jobID string) (*Job, bool) {
	v, ok := s.jobs.Load(jobID)
	if !ok {
		return nil, false
	}
	src := v.(*Job)
	// Return a copy so callers cannot corrupt stored state.
	data, _ := json.Marshal(src)
	var copy Job
	_ = json.Unmarshal(data, &copy)
	return &copy, true
}

func (s *MemoryJobStore) List(tenantID uint16, limit int) ([]*Job, error) {
	var result []*Job
	s.jobs.Range(func(_, v any) bool {
		job := v.(*Job)
		if tenantID == 0 || job.TenantID == tenantID {
			data, _ := json.Marshal(job)
			var copy Job
			_ = json.Unmarshal(data, &copy)
			result = append(result, &copy)
		}
		return len(result) < limit
	})
	return result, nil
}

func (s *MemoryJobStore) Delete(jobID string) error {
	s.jobs.Delete(jobID)
	return nil
}

func (s *MemoryJobStore) Enqueue(jobID string) error {
	select {
	case s.queue <- jobID:
		return nil
	default:
		return ErrQueueFull
	}
}

func (s *MemoryJobStore) Dequeue(ctx context.Context) (string, bool) {
	select {
	case id := <-s.queue:
		return id, true
	default:
		return "", false
	}
}

func (s *MemoryJobStore) Close() error {
	s.once.Do(func() { close(s.stopGC) })
	return nil
}

func (s *MemoryJobStore) gcLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now().UnixNano()
			s.jobs.Range(func(k, v any) bool {
				job := v.(*Job)
				if job.TTLSeconds > 0 {
					expiry := job.CreatedAt + job.TTLSeconds*int64(time.Second)
					if now > expiry {
						s.jobs.Delete(k)
					}
				}
				return true
			})
		case <-s.stopGC:
			return
		}
	}
}
