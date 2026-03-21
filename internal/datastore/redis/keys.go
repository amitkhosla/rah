package redis

import (
	"fmt"
	"strings"
)

// scopedKey builds a Redis key with a CRC16 hash tag.
//
// Format: {tenant:<tenant>:<domain>}:<key>
//
// The curly-brace hash tag forces all keys for the same tenant+domain to the
// same CRC16 slot in Redis Cluster, making MGET and pipeline batching safe
// without cross-slot errors.
func scopedKey(tenant, domain, key string) (string, error) {
	tenant = strings.TrimSpace(tenant)
	domain = strings.TrimSpace(domain)
	key = strings.TrimSpace(key)
	if tenant == "" {
		return "", fmt.Errorf("tenant is required")
	}
	if domain == "" {
		return "", fmt.Errorf("domain is required")
	}
	if key == "" {
		return "", fmt.Errorf("key is required")
	}
	return fmt.Sprintf("{tenant:%s:%s}:%s", tenant, domain, key), nil
}

// scopedPrefix builds the scan prefix for SCAN pattern matching.
// Returns "{tenant:<tenant>:<domain>}:<prefix>" where prefix may be empty.
func scopedPrefix(tenant, domain, prefix string) (string, error) {
	tenant = strings.TrimSpace(tenant)
	domain = strings.TrimSpace(domain)
	if tenant == "" {
		return "", fmt.Errorf("tenant is required")
	}
	if domain == "" {
		return "", fmt.Errorf("domain is required")
	}
	base := fmt.Sprintf("{tenant:%s:%s}:", tenant, domain)
	return base + strings.TrimSpace(prefix), nil
}
