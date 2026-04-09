package pricing

import (
	"sync"
	"time"
)

// TokenRatio tracks the relationship between estimated and actual tokens
type TokenRatio struct {
	ModelID              string
	EstimatedTokens     int64
	ActualTokens        int64
	SampleCount         int64
	LastUpdated         time.Time
	AdjustmentRatio     float64 // actual / estimated (for next day's estimates)
}

// MetricsCollector tracks token estimation accuracy across all requests
type MetricsCollector struct {
	mu              sync.RWMutex
	tokenRatios     map[string]*TokenRatio // keyed by model ID
	requestCount    int64
	totalCostError  float64 // cumulative cost error ($)
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		tokenRatios: make(map[string]*TokenRatio),
	}
}

// RecordTokenEstimate records an estimated vs actual token count for learning
func (mc *MetricsCollector) RecordTokenEstimate(modelID string, estimatedTokens, actualTokens int64) {
	if estimatedTokens == 0 {
		// Can't compute ratio with zero estimated tokens
		return
	}

	mc.mu.Lock()
	defer mc.mu.Unlock()

	ratio, exists := mc.tokenRatios[modelID]
	if !exists {
		ratio = &TokenRatio{
			ModelID: modelID,
		}
		mc.tokenRatios[modelID] = ratio
	}

	// Update running average
	ratio.SampleCount++
	ratio.EstimatedTokens += estimatedTokens
	ratio.ActualTokens += actualTokens
	ratio.LastUpdated = time.Now()

	// Compute adjustment ratio: actual / estimated
	if ratio.EstimatedTokens > 0 {
		ratio.AdjustmentRatio = float64(ratio.ActualTokens) / float64(ratio.EstimatedTokens)
	}

	mc.requestCount++
}

// GetAdjustmentRatio returns the learned adjustment ratio for a model
// If no history exists, returns 1.0 (assume 100% accuracy)
func (mc *MetricsCollector) GetAdjustmentRatio(modelID string) float64 {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	if ratio, exists := mc.tokenRatios[modelID]; exists && ratio.SampleCount > 0 {
		return ratio.AdjustmentRatio
	}

	// No history, assume perfect estimation
	return 1.0
}

// GetAllRatios returns all collected token ratios
func (mc *MetricsCollector) GetAllRatios() map[string]*TokenRatio {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	// Return a copy
	result := make(map[string]*TokenRatio)
	for k, v := range mc.tokenRatios {
		result[k] = v
	}
	return result
}

// ResetDaily should be called once per day to preserve daily metrics
// and potentially archive them for analysis
func (mc *MetricsCollector) ResetDaily() map[string]*TokenRatio {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Snapshot current ratios
	snapshot := make(map[string]*TokenRatio)
	for k, v := range mc.tokenRatios {
		snapshot[k] = v
	}

	// Reset for new day
	mc.tokenRatios = make(map[string]*TokenRatio)
	mc.requestCount = 0
	mc.totalCostError = 0

	return snapshot
}

// GetMetrics returns current metrics summary
func (mc *MetricsCollector) GetMetrics() map[string]interface{} {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	totalActual := int64(0)
	totalEstimated := int64(0)
	totalSamples := int64(0)

	ratios := make([]map[string]interface{}, 0, len(mc.tokenRatios))
	for _, ratio := range mc.tokenRatios {
		totalActual += ratio.ActualTokens
		totalEstimated += ratio.EstimatedTokens
		totalSamples += ratio.SampleCount

		ratios = append(ratios, map[string]interface{}{
			"model_id":          ratio.ModelID,
			"sample_count":      ratio.SampleCount,
			"estimated_tokens":  ratio.EstimatedTokens,
			"actual_tokens":     ratio.ActualTokens,
			"adjustment_ratio":  ratio.AdjustmentRatio,
			"last_updated":      ratio.LastUpdated,
		})
	}

	overallRatio := 1.0
	if totalEstimated > 0 {
		overallRatio = float64(totalActual) / float64(totalEstimated)
	}

	return map[string]interface{}{
		"request_count":    mc.requestCount,
		"total_samples":    totalSamples,
		"estimated_tokens": totalEstimated,
		"actual_tokens":    totalActual,
		"overall_ratio":    overallRatio,
		"cost_error":       mc.totalCostError,
		"models":           ratios,
	}
}

// DailyLearningJob is a background job that runs once per day to:
// 1. Collect token ratio metrics
// 2. Compute adjustment factors for next day
// 3. Archive historical metrics
type DailyLearningJob struct {
	collector *MetricsCollector
	ticker    *time.Ticker
	done      chan bool
}

// NewDailyLearningJob creates a new daily learning job
func NewDailyLearningJob(collector *MetricsCollector, runAt time.Time) *DailyLearningJob {
	job := &DailyLearningJob{
		collector: collector,
		done:      make(chan bool),
	}

	// Calculate duration until next run time
	now := time.Now()
	nextRun := calculateNextRun(now, runAt)
	duration := time.Until(nextRun)

	// Start the job in a goroutine
	go job.run(duration)

	return job
}

// calculateNextRun calculates the next run time based on preferred hour
func calculateNextRun(now time.Time, preferredTime time.Time) time.Time {
	// Get the preferred hour (e.g., 2 AM)
	hour, min, sec := preferredTime.Clock()

	// Create a time for today at the preferred time
	nextRun := time.Date(
		now.Year(), now.Month(), now.Day(),
		hour, min, sec, 0,
		now.Location(),
	)

	// If it's already past that time today, schedule for tomorrow
	if now.After(nextRun) {
		nextRun = nextRun.AddDate(0, 0, 1)
	}

	return nextRun
}

// run executes the learning job periodically
func (j *DailyLearningJob) run(initialDuration time.Duration) {
	// Wait for initial duration
	timer := time.NewTimer(initialDuration)
	defer timer.Stop()

	<-timer.C

	// Now run daily
	j.ticker = time.NewTicker(24 * time.Hour)
	defer j.ticker.Stop()

	for {
		select {
		case <-j.done:
			return
		case <-j.ticker.C:
			j.executeJob()
		}
	}
}

// executeJob runs the actual learning computation
func (j *DailyLearningJob) executeJob() {
	// Get snapshot of current metrics
	snapshot := j.collector.ResetDaily()

	// Log the snapshot (in production, would store to database)
	// For now, just compute and store the adjustment ratios
	for _, ratio := range snapshot {
		if ratio.SampleCount > 0 {
			// This ratio is now available for tomorrow's estimates
			// In production, you would store this and look it up at estimate time
			_ = ratio // Would be persisted to a store for next day's use
		}
	}
}

// Stop stops the daily learning job
func (j *DailyLearningJob) Stop() {
	j.done <- true
}
