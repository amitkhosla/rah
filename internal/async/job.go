package async

import "time"

// AsyncMode controls how an endpoint handles async execution.
// Values mirror what is stored in ApiConfig.Async (string).
type AsyncMode uint8

const (
	AsyncDisabled AsyncMode = 0 // default: always synchronous
	AsyncAllowed  AsyncMode = 1 // client opts in per-request via header/query
	AsyncForced   AsyncMode = 2 // always async regardless of client preference
)

// ParseAsyncMode converts the string from ApiConfig.Async to AsyncMode.
func ParseAsyncMode(s string) AsyncMode {
	switch s {
	case "allowed":
		return AsyncAllowed
	case "forced":
		return AsyncForced
	default:
		return AsyncDisabled
	}
}

// JobStatus represents the lifecycle state of an async job.
type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"
)

// Job is the unit of async work. It carries both the captured request input
// and the eventual execution result. It must be JSON-serialisable so any
// KV backend can persist it.
type Job struct {
	// Identity
	ID       string `json:"id"`
	TenantID uint16 `json:"tenant_id"`
	APIID    uint32 `json:"api_id"`

	// FlowEntryPC is the absolute PC into the compiled GlobalTable.
	// Set at enqueue time from the matched Endpoint.
	FlowEntryPC int16 `json:"flow_entry_pc"`

	// Lifecycle
	Status      JobStatus `json:"status"`
	CreatedAt   int64     `json:"created_at"`   // unix ns
	StartedAt   int64     `json:"started_at"`   // unix ns; 0 until picked up
	CompletedAt int64     `json:"completed_at"` // unix ns; 0 until done
	TTLSeconds  int64     `json:"ttl_seconds"`  // store uses this for expiry; default 3600

	// Captured request input — no net/http types, fully serialisable.
	Method   []byte      `json:"method"`
	Path     []byte      `json:"path"`
	RawQuery []byte      `json:"raw_query,omitempty"`
	Headers  [][2]string `json:"headers,omitempty"` // whitelisted k/v pairs
	Body     []byte      `json:"body,omitempty"`

	// Execution result — populated after job completes.
	ResponseStatus  int         `json:"response_status,omitempty"`
	ResponseHeaders [][2]string `json:"response_headers,omitempty"`
	ResponseBody    []byte      `json:"response_body,omitempty"`
	ErrorMsg        string      `json:"error_msg,omitempty"`
}

// IsTerminal returns true when the job has reached a final state
// and its result can be read.
func (j *Job) IsTerminal() bool {
	return j.Status == JobCompleted || j.Status == JobFailed
}

// Age returns how long ago the job was created.
func (j *Job) Age() time.Duration {
	return time.Duration(time.Now().UnixNano() - j.CreatedAt)
}
