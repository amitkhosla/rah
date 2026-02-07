package steps

import (
	"rah/internal/rctx"
	"sync"
)

// ParallelStep manages its own goroutines and returns 1 when finished.
func ParallelStep(ctx *rctx.Context) int16 {
	var wg sync.WaitGroup
	wg.Add(2)

	// Branch 1: e.g., Validate JWT
	go func() {
		defer wg.Done()
		// Logic here...
	}()

	// Branch 2: e.g., Check Rate Limit
	go func() {
		defer wg.Done()
		// Logic here...
	}()

	wg.Wait() // The "Join" point
	return 1  // Tell engine to move to the next sequential instruction
}
