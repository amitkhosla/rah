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
	Name        string
	FlowName    string
	TenantAlias string
	TimeoutSec  int
	Constants   map[string]string
	ScheduledAt time.Time
}

// ClaimResult indicates whether this instance claimed the event.
type ClaimResult uint8

const (
	ClaimWon   ClaimResult = 0
	ClaimLost  ClaimResult = 1
	ClaimError ClaimResult = 2
)

// ExecutionRecord is written after each execution attempt.
type ExecutionRecord struct {
	Name       string
	StartedAt  time.Time
	FinishedAt time.Time
	Status     string // "ok", "failed", "timeout"
	Error      string
}
