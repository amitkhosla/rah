package async

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"
)

// DataStoreJobStore persists job state in a KVBackend (any datastore domain)
// and uses an in-memory channel as the local work queue.
//
// Job state survives process restart; pending/running jobs found on startup
// are automatically re-enqueued by Recover().
type DataStoreJobStore struct {
	kv     KVBackend
	queue  chan string
	stopGC chan struct{}
	once   sync.Once
}

func NewDataStoreJobStore(kv KVBackend, queueCap int) *DataStoreJobStore {
	if queueCap <= 0 {
		queueCap = 10_000
	}
	s := &DataStoreJobStore{
		kv:     kv,
		queue:  make(chan string, queueCap),
		stopGC: make(chan struct{}),
	}
	go s.gcLoop()
	return s
}

func (s *DataStoreJobStore) Put(job *Job) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return s.kv.Put(context.Background(), job.ID, data)
}

func (s *DataStoreJobStore) Get(jobID string) (*Job, bool) {
	data, ok, err := s.kv.Get(context.Background(), jobID)
	if err != nil || !ok {
		return nil, false
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, false
	}
	return &job, true
}

func (s *DataStoreJobStore) List(tenantID uint16, limit int) ([]*Job, error) {
	keys, err := s.kv.ListKeys(context.Background(), "")
	if err != nil {
		return nil, err
	}
	var result []*Job
	for _, key := range keys {
		if len(result) >= limit {
			break
		}
		job, ok := s.Get(key)
		if !ok {
			continue
		}
		if tenantID == 0 || job.TenantID == tenantID {
			result = append(result, job)
		}
	}
	return result, nil
}

func (s *DataStoreJobStore) Delete(jobID string) error {
	return s.kv.Delete(context.Background(), jobID)
}

func (s *DataStoreJobStore) Enqueue(jobID string) error {
	select {
	case s.queue <- jobID:
		return nil
	default:
		return ErrQueueFull
	}
}

func (s *DataStoreJobStore) Dequeue(ctx context.Context) (string, bool) {
	select {
	case id := <-s.queue:
		return id, true
	default:
		return "", false
	}
}

// Recover scans the KV backend for jobs in pending or running state and
// re-enqueues them. Call once at startup before accepting traffic.
func (s *DataStoreJobStore) Recover(ctx context.Context) error {
	keys, err := s.kv.ListKeys(ctx, "")
	if err != nil {
		return err
	}
	recovered := 0
	for _, key := range keys {
		job, ok := s.Get(key)
		if !ok {
			continue
		}
		if job.Status == JobPending || job.Status == JobRunning {
			job.Status = JobPending
			job.StartedAt = 0
			if err := s.Put(job); err != nil {
				log.Printf("[async] recover: failed to reset job %s: %v", job.ID, err)
				continue
			}
			if err := s.Enqueue(job.ID); err != nil {
				log.Printf("[async] recover: queue full, skipping job %s", job.ID)
				continue
			}
			recovered++
		}
	}
	if recovered > 0 {
		log.Printf("[async] recovered %d unfinished job(s) from store", recovered)
	}
	return nil
}

func (s *DataStoreJobStore) Close() error {
	s.once.Do(func() { close(s.stopGC) })
	return nil
}

func (s *DataStoreJobStore) gcLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			keys, err := s.kv.ListKeys(context.Background(), "")
			if err != nil {
				continue
			}
			now := time.Now().UnixNano()
			for _, key := range keys {
				job, ok := s.Get(key)
				if !ok {
					continue
				}
				if job.TTLSeconds > 0 {
					expiry := job.CreatedAt + job.TTLSeconds*int64(time.Second)
					if now > expiry {
						s.kv.Delete(context.Background(), key)
					}
				}
			}
		case <-s.stopGC:
			return
		}
	}
}
