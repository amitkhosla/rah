package control

// StepConfig represents a single step in the JSON flow
type StepConfig struct {
	Action    string `json:"action"`
	Key       string `json:"key,omitempty"`     // For extract_header
	As        string `json:"as,omitempty"`      // For variable naming
	URL       string `json:"url,omitempty"`     // For http_call
	Method    string `json:"method,omitempty"`  // For http_call
	SaveAs    string `json:"save_as,omitempty"` // For http_call output
	Condition string `json:"if,omitempty"`      // For logic branching
	Then      string `json:"then,omitempty"`    // Fragment name
	Else      string `json:"else,omitempty"`    // Fragment name
	Target    string `json:"target,omitempty"`  // For proxy
	Value     string `json:"value,omitempty"`
}

// ApiConfig is the top-level structure for the JSON input
type ApiConfig struct {
	ApiID     string                  `json:"api_id"`
	Path      string                  `json:"path"`
	Fragments map[string][]StepConfig `json:"fragments"`
	Flow      []StepConfig            `json:"flow"`
}
