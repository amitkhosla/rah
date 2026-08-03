package datasource

// DataSourceConfig configures a named database connection pool.
type DataSourceConfig struct {
	Name            string `json:"name"              yaml:"name"`
	DSNRef          string `json:"dsn_ref"           yaml:"dsn_ref"`            // env:VAR or plain DSN
	MaxConnections  int    `json:"max_connections"   yaml:"max_connections"`    // default 20
	QueryTimeoutSec int    `json:"query_timeout_sec" yaml:"query_timeout_sec"`  // default 10
}
