package redissource

import "github.com/redis/go-redis/v9"

// GetUniversalClient returns the raw redis.UniversalClient for the named source.
// The returned client is shared across tenants — key scoping is the caller's responsibility.
// Returns nil, false if the source is not configured.
func (p *RedisSourcePool) GetUniversalClient(name string) (redis.UniversalClient, bool) {
	c, ok := p.coalescers[name]
	if !ok {
		return nil, false
	}
	return c.client, true
}
