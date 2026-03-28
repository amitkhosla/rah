package async

import "context"

// KVBackend is the minimal key-value interface the DataStoreJobStore needs.
// control.DataStoreManager (scoped to DomainAsyncJobs via domainScopedKV) satisfies this.
// Defined here so the async package has no import dependency on control or config.
type KVBackend interface {
	Put(ctx context.Context, key string, value []byte) error
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Delete(ctx context.Context, key string) error
	ListKeys(ctx context.Context, prefix string) ([]string, error)
}

// JobStore is the single interface for all async job operations.
// State (put/get/list/delete) and queue (enqueue/dequeue) are unified
// so each implementation can use the best mechanism for each operation.
type JobStore interface {
	// State operations
	Put(job *Job) error
	Get(jobID string) (*Job, bool)
	List(tenantID uint16, limit int) ([]*Job, error)
	Delete(jobID string) error

	// Queue operations — FIFO work queue.
	// Enqueue adds the jobID of an already-Put job to the work queue.
	Enqueue(jobID string) error
	// Dequeue pops the next pending jobID. Returns ("", false) immediately if empty.
	// Implementations that support blocking (e.g. Redis BRPOP) should respect ctx.
	Dequeue(ctx context.Context) (string, bool)

	// Close releases any background resources.
	Close() error
}
