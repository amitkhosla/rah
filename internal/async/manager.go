package async

import (
	"context"
	"fmt"
	"log"
	"time"
)

const (
	defaultMaxWorkers = 64
	defaultTTLSeconds = 3600
)

// AsyncManager owns the JobStore and coordinates job submission and polling.
// It does NOT execute flows — that is the caller's responsibility via ExecuteFn.
type AsyncManager struct {
	Store      JobStore
	maxWorkers int
	semaphore  chan struct{}
	ttlSeconds int64
}

// NewAsyncManager creates an AsyncManager backed by the given store.
// maxWorkers bounds in-process goroutine concurrency; 0 uses the default (64).
// ttlSeconds is the job TTL written to every new job; 0 uses the default (3600).
func NewAsyncManager(store JobStore, maxWorkers int, ttlSeconds int64) *AsyncManager {
	if maxWorkers <= 0 {
		maxWorkers = defaultMaxWorkers
	}
	if ttlSeconds <= 0 {
		ttlSeconds = defaultTTLSeconds
	}
	return &AsyncManager{
		Store:      store,
		maxWorkers: maxWorkers,
		semaphore:  make(chan struct{}, maxWorkers),
		ttlSeconds: ttlSeconds,
	}
}

// Submit persists a new job, enqueues it, and — in in-process mode —
// immediately launches a goroutine to execute it.
// executeFn is the callback that runs the flow and writes results back to job.
// Returns the job ID to include in the 202 response.
func (am *AsyncManager) Submit(job *Job, executeFn func(*Job)) (string, error) {
	if job.ID == "" {
		return "", fmt.Errorf("async: job ID must be set before Submit")
	}
	job.Status = JobPending
	job.CreatedAt = time.Now().UnixNano()
	job.TTLSeconds = am.ttlSeconds

	if err := am.Store.Put(job); err != nil {
		return "", fmt.Errorf("async: persist job: %w", err)
	}
	if err := am.Store.Enqueue(job.ID); err != nil {
		return "", fmt.Errorf("async: enqueue job: %w", err)
	}

	// In-process execution: acquire semaphore slot, run in background goroutine.
	am.semaphore <- struct{}{}
	go func() {
		defer func() { <-am.semaphore }()
		am.runJob(job.ID, executeFn)
	}()

	return job.ID, nil
}

// GetStatus returns the current state of a job by ID.
func (am *AsyncManager) GetStatus(jobID string) (*Job, bool) {
	return am.Store.Get(jobID)
}

// ListForTenant returns up to limit jobs for the given tenant.
// tenantID=0 returns jobs for all tenants.
func (am *AsyncManager) ListForTenant(tenantID uint16, limit int) ([]*Job, error) {
	if limit <= 0 {
		limit = 20
	}
	return am.Store.List(tenantID, limit)
}

// DeleteJob removes a completed or failed job from the store.
func (am *AsyncManager) DeleteJob(jobID string) error {
	return am.Store.Delete(jobID)
}

// runJob is called from the in-process goroutine. It marks the job running,
// calls executeFn (which should write ResponseStatus/Body/Headers into job),
// then marks it completed or failed.
func (am *AsyncManager) runJob(jobID string, executeFn func(*Job)) {
	job, ok := am.Store.Get(jobID)
	if !ok {
		log.Printf("[async] runJob: job %s not found in store", jobID)
		return
	}

	// Mark running.
	job.Status = JobRunning
	job.StartedAt = time.Now().UnixNano()
	if err := am.Store.Put(job); err != nil {
		log.Printf("[async] runJob: failed to mark job %s running: %v", jobID, err)
		return
	}

	// Execute — any panic from the flow must not crash the worker goroutine.
	func() {
		defer func() {
			if r := recover(); r != nil {
				job.Status = JobFailed
				job.ErrorMsg = fmt.Sprintf("panic: %v", r)
				log.Printf("[async] runJob: panic in job %s: %v", jobID, r)
			}
		}()
		executeFn(job)
	}()

	// Mark terminal state.
	job.CompletedAt = time.Now().UnixNano()
	if job.Status != JobFailed {
		if job.ErrorMsg != "" {
			job.Status = JobFailed
		} else {
			job.Status = JobCompleted
		}
	}
	if err := am.Store.Put(job); err != nil {
		log.Printf("[async] runJob: failed to persist result for job %s: %v", jobID, err)
	}
}

// WorkerPoll is used by the rah-worker binary. It blocks until a job is
// available or ctx is cancelled, then calls executeFn.
// Returns false when ctx is done (graceful shutdown).
func (am *AsyncManager) WorkerPoll(ctx context.Context, executeFn func(*Job)) bool {
	// Non-blocking dequeue with backoff.
	jobID, ok := am.Store.Dequeue(ctx)
	if !ok {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
			return true // continue polling
		}
	}
	am.runJob(jobID, executeFn)
	return true
}
