package scheduler

import "time"

// Schedule is the persisted definition of a recurring or one-shot task.
type Schedule struct {
	Name        string
	Cron        string            // standard 5-field or 6-field (with seconds) cron expression
	FlowName    string
	TenantAlias string
	Enabled     bool
	TimeoutSec  int
	Constants   map[string]string
	NextRunAt   time.Time // computed next execution time
	LastRunAt   time.Time
	LastStatus  string // "ok", "failed", "claimed_elsewhere", ""
	OnFailure   struct {
		RetryCount       int    `json:"retry_count"`
		RetryIntervalSec int    `json:"retry_interval_sec"`
		DeadLetterFlow   string `json:"dead_letter_flow"`
	} `json:"on_failure,omitempty"`
	MaxConcurrent int  `json:"max_concurrent,omitempty"`
	RuntimeOnly   bool // true for tenant-created runtime schedules; false for bundle-deployed schedules
}

// ScheduledEvent is what the wheel fires — a snapshot of what to execute.
type ScheduledEvent struct {
	Name             string
	FlowName         string
	TenantAlias      string
	TimeoutSec       int
	Constants        map[string]string
	ScheduledAt      time.Time
	Cron             string
	Epoch            uint32
	RetryCount       int
	RetryIntervalSec int
	DeadLetterFlow   string
}

// ClaimResult indicates whether this instance claimed the event.
type ClaimResult uint8

const (
	ClaimUnset ClaimResult = 0
	ClaimWon   ClaimResult = 1
	ClaimLost  ClaimResult = 2
	ClaimError ClaimResult = 3
)

func (e *ScheduledEvent) Reset() {
	e.Name = ""
	e.FlowName = ""
	e.TenantAlias = ""
	e.TimeoutSec = 0
	e.Constants = nil
	e.ScheduledAt = time.Time{}
	e.Cron = ""
	e.Epoch = 0
	e.RetryCount = 0
	e.RetryIntervalSec = 0
	e.DeadLetterFlow = ""
}

// ExecutionRecord is written after each execution attempt.
type ExecutionRecord struct {
	Name       string
	StartedAt  time.Time
	FinishedAt time.Time
	Status     string // "ok", "failed", "timeout"
	Error      string
}
