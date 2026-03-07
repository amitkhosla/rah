package config

import "testing"

func TestDataStoreConfigValidate(t *testing.T) {
	cfg := DataStoreConfig{
		Stores: map[string]StoreConfig{
			"redis_primary": {
				Name:    "redis_primary",
				Kind:    StoreRedis,
				Enabled: true,
			},
		},
		Bindings: map[DataDomain]string{
			DomainAPIDefinitions: "redis_primary",
			DomainFlows:          "redis_primary",
			DomainTenantRegistry: "redis_primary",
			DomainCache:          "redis_primary",
			DomainCustomerData:   "redis_primary",
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestDataStoreConfigValidateMissingRequiredBinding(t *testing.T) {
	cfg := DataStoreConfig{
		Stores: map[string]StoreConfig{
			"disk": {Name: "disk", Kind: StoreDisk, Enabled: true},
		},
		Bindings: map[DataDomain]string{
			DomainAPIDefinitions: "disk",
			DomainFlows:          "disk",
			DomainCache:          "disk",
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error")
	}
}
