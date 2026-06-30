package config

// XMLConfig holds gateway-wide XML processing limits.
type XMLConfig struct {
	MaxDocSizeBytes int `json:"max_doc_size_bytes,omitempty" yaml:"max_doc_size_bytes,omitempty"` // 0 = 4MB default
}
