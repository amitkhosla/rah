package async

import "errors"

var (
	// ErrQueueFull is returned when Enqueue cannot accept a new job because
	// the in-memory channel is at capacity.
	ErrQueueFull = errors.New("async: job queue is full")

	// ErrJobNotFound is returned when Get cannot locate a job by ID.
	ErrJobNotFound = errors.New("async: job not found")
)
