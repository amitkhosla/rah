package control

// StepConfig represents a single step in the JSON flow
type StepConfig struct {
	Action    string `json:"action"`
	Key       string `json:"key,omitempty"` // For extract_header
	As        string `json:"as,omitempty"`  // For variable naming
	Status    bool   `json:"status,false"`
	URL       string `json:"url,omitempty"`     // For http_call
	Method    string `json:"method,omitempty"`  // For http_call
	SaveAs    string `json:"save_as,omitempty"` // For http_call output
	Condition string `json:"if,omitempty"`      // For logic branching
	Then      string `json:"then,omitempty"`    // Fragment name
	Else      string `json:"else,omitempty"`    // Fragment name
	Target    string `json:"target,omitempty"`  // For proxy
	Value     string `json:"value,omitempty"`   // For static writes
	Path      string `json:"path,omitempty"`    // For JSON path (e.g., user.id)
	From      string `json:"from,omitempty"`    // Source slot name for JSON extract
	TTL       uint32 `json:"ttl,omitempty"`     // TTL
}

// ApiConfig is the top-level structure for the JSON input
type ApiConfig struct {
	ApiID     string                  `json:"api_id"`
	Path      string                  `json:"path"`
	Fragments map[string][]StepConfig `json:"fragments"`
	FlowID    string                  `json:"flow_id"`
	Flow      []StepConfig            `json:"flow"`
}

type FlowUpdate struct {
	Name         string       `json:"name"`
	Instructions []StepConfig `json:"instructions"`
	Action       string       `json:"action"` // "upsert" or "delete"
}

type ApiUpdate struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	FlowName string `json:"flow_name"` // Reference to a Flow name
	Action   string `json:"action"`    // "upsert" or "delete"
}

type UnifiedSyncRequest struct {
	SyncUUID string       `json:"sync_uuid"`
	Flows    []FlowUpdate `json:"flows"`
	Apis     []ApiUpdate  `json:"apis"`
}

type Step struct {
	Type          string            `json:"type"`    // e.g., "read_body", "extract_json", "proxy"
	As            string            `json:"as"`      // Slot name/alias
	Source        string            `json:"source"`  // Where to get data (e.g., "header.X-User")
	IsHugePayload bool              `json:"is_huge"` // Hint for Streaming vs Buffering
	Parameters    map[string]string `json:"params"`  // Custom logic params
}
