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
	Name             string   `yaml:"name"`
	Driver           string   `yaml:"driver"`
	DSNRef           string   `yaml:"dsn_ref"`
	MaxConnections   int      `yaml:"max_connections"`
	QueryTimeoutSec  int      `yaml:"query_timeout_sec"`
	TenantIsolation  string   `yaml:"tenant_isolation"`
	TenantKey        string   `yaml:"tenant_key"`
	SharedSchemas    []string `yaml:"shared_schemas"`
	RLSVariable      string   `yaml:"rls_variable"`
}
