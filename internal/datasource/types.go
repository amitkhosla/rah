package datasource

// NamedQueryConfig defines a named query with batching configuration.
type NamedQueryConfig struct {
	SQL         string `yaml:"sql"`
	BatchBy     string `yaml:"batch_by"`
	BatchWindow string `yaml:"batch_window"`
	BatchMax    int    `yaml:"batch_max"`
}

// DataSourceConfig defines a data source with tenant isolation settings.
type DataSourceConfig struct {
	Name             string   `yaml:"name" json:"name"`
	Driver           string   `yaml:"driver" json:"driver"`
	DSNRef           string   `yaml:"dsn_ref" json:"dsn_ref,omitempty"`
	MaxConnections   int      `yaml:"max_connections" json:"max_connections,omitempty"`
	QueryTimeoutSec  int      `yaml:"query_timeout_sec" json:"query_timeout_sec,omitempty"`
	TenantIsolation  string   `yaml:"tenant_isolation" json:"tenant_isolation,omitempty"`
	TenantKey        string   `yaml:"tenant_key" json:"tenant_key,omitempty"`
	SharedSchemas    []string `yaml:"shared_schemas" json:"shared_schemas,omitempty"`
	RLSVariable      string   `yaml:"rls_variable" json:"rls_variable,omitempty"`
	Action           string   `json:"action,omitempty" yaml:"action,omitempty"` // "upsert" or "delete"
}
