package observability

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

var detailLogState struct {
	once    sync.Once
	enabled bool
	mu      sync.Mutex
	f       *os.File
	path    string
}

func initDetailLog() {
	path := strings.TrimSpace(os.Getenv("RAH_OBS_DETAIL_LOG_FILE"))
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("[obs] detail log disabled; open %q failed: %v", path, err)
		return
	}
	detailLogState.f = f
	detailLogState.enabled = true
	detailLogState.path = path
	log.Printf("[obs] detail log enabled: %s", path)
}

// WriteDetailLog appends a single JSONL record to the optional observability
// detail log file (configured via RAH_OBS_DETAIL_LOG_FILE).
// This is intended for full prompt/response troubleshooting when the UI
// truncates output or only shows summaries.
func WriteDetailLog(record map[string]any) {
	detailLogState.once.Do(initDetailLog)
	if !detailLogState.enabled || detailLogState.f == nil {
		return
	}
	if _, ok := record["ts"]; !ok {
		record["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	b, err := json.Marshal(record)
	if err != nil {
		return
	}

	detailLogState.mu.Lock()
	defer detailLogState.mu.Unlock()
	_, _ = detailLogState.f.Write(append(b, '\n'))
}

type DetailLogConfig struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path"`
}

func GetDetailLogConfig() DetailLogConfig {
	detailLogState.once.Do(initDetailLog)
	detailLogState.mu.Lock()
	defer detailLogState.mu.Unlock()
	return DetailLogConfig{
		Enabled: detailLogState.enabled && detailLogState.f != nil,
		Path:    detailLogState.path,
	}
}

func SetDetailLogConfig(enabled bool, path string) error {
	detailLogState.once.Do(initDetailLog)
	detailLogState.mu.Lock()
	defer detailLogState.mu.Unlock()

	path = strings.TrimSpace(path)
	if enabled && path == "" {
		return errors.New("detail log path is required when enabled=true")
	}

	if detailLogState.f != nil && (path != detailLogState.path || !enabled) {
		_ = detailLogState.f.Close()
		detailLogState.f = nil
	}

	if !enabled {
		detailLogState.enabled = false
		detailLogState.path = ""
		return nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	detailLogState.f = f
	detailLogState.enabled = true
	detailLogState.path = path
	return nil
}
