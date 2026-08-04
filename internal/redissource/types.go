package redissource

const RahReservedPrefix = "_rah:"

type TenantPrefixMode string

const (
	PrefixNone  TenantPrefixMode = "none"
	PrefixID    TenantPrefixMode = "id"
	PrefixAlias TenantPrefixMode = "alias"
)

// RedisSourceConfig holds the configuration for a single customer Redis source.
type RedisSourceConfig struct {
	Name         string           `yaml:"name"`
	Addr         string           `yaml:"addr"`
	Addrs        []string         `yaml:"addrs"`
	Password     string           `yaml:"password"`
	DB           int              `yaml:"db"`
	TLS          bool             `yaml:"tls"`
	TenantPrefix TenantPrefixMode `yaml:"tenant_prefix"`
	KeySep       string           `yaml:"key_separator"`
}
