package observability

import (
	"encoding/json"
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
