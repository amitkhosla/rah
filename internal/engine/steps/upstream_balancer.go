package steps

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

// DistributionStrategy defines the request distribution mode for a multi-upstream target.
type DistributionStrategy string

const (
	// RoundRobinStrategy routes requests evenly across healthy instances.
	RoundRobinStrategy DistributionStrategy = "round_robin"
	// WeightedRoundRobinStrategy routes requests proportionally by weight.
	WeightedRoundRobinStrategy DistributionStrategy = "weighted_round_robin"
)

// UpstreamInstance contains one routable endpoint.
// It can be represented directly by customers in registry payloads.
type UpstreamInstance struct {
	URL     string `json:"url"`
	Weight  int    `json:"weight,omitempty"`
	Healthy *bool  `json:"healthy,omitempty"`
}

// UpstreamStats provides lightweight observability for upstream selection.
type UpstreamStats struct {
	Selections uint64
	Errors     uint64
}

type upstreamBalancer struct {
	strategy  DistributionStrategy
	instances []UpstreamInstance
	counter   atomic.Uint64

	selectionCount atomic.Uint64
	errorCount     atomic.Uint64
}

func newUpstreamBalancer(strategy DistributionStrategy, instances []UpstreamInstance) (*upstreamBalancer, error) {
	if len(instances) == 0 {
		return nil, errors.New("at least one upstream instance is required")
	}
	if strategy == "" {
		strategy = RoundRobinStrategy
	}

	pool := make([]UpstreamInstance, 0, len(instances))
	for _, instance := range instances {
		if _, err := url.ParseRequestURI(instance.URL); err != nil {
			return nil, errors.New("invalid upstream url: " + instance.URL)
		}

		if strategy == WeightedRoundRobinStrategy {
			if instance.Weight <= 0 {
				instance.Weight = 1
			}
			for i := 0; i < instance.Weight; i++ {
				pool = append(pool, instance)
			}
			continue
		}

		pool = append(pool, instance)
	}

	return &upstreamBalancer{strategy: strategy, instances: pool}, nil
}

func (b *upstreamBalancer) Next() (string, error) {
	n := uint64(len(b.instances))
	if n == 0 {
		b.errorCount.Add(1)
		return "", errors.New("no upstream instances available")
	}

	start := b.counter.Add(1)
	for i := range n {
		idx := (start - 1 + i) % n
		candidate := b.instances[idx]
		if candidate.Healthy == nil || *candidate.Healthy {
			b.selectionCount.Add(1)
			return candidate.URL, nil
		}
	}

	b.errorCount.Add(1)
	return "", errors.New("no healthy upstream instances available")
}

func (b *upstreamBalancer) Stats() UpstreamStats {
	return UpstreamStats{
		Selections: b.selectionCount.Load(),
		Errors:     b.errorCount.Load(),
	}
}

var balancerRegistry sync.Map

type balancerKey struct {
	upstreamSet string
	strategy    DistributionStrategy
}

// selectUpstreamURL resolves one endpoint from either a single URL or a multi-instance payload.
// Supported multi-instance payload formats:
//   - JSON array of strings: ["https://a", "https://b"]
//   - JSON array of objects: [{"url":"https://a","weight":2}, {"url":"https://b"}]
//   - Comma-separated string: https://a,https://b
//
// Strategy can be controlled via flowInput["http.upstream_strategy"].
func selectUpstreamURL(raw string, flowInput map[string]string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty upstream")
	}

	instances, multi, err := parseUpstreamInstances(raw)
	if err != nil {
		return "", err
	}
	if !multi {
		return instances[0].URL, nil
	}

	strategy := DistributionStrategy(strings.TrimSpace(flowInput["http.upstream_strategy"]))
	if strategy == "" {
		strategy = RoundRobinStrategy
	}

	key := balancerKey{upstreamSet: raw, strategy: strategy}
	if existing, ok := balancerRegistry.Load(key); ok {
		return existing.(*upstreamBalancer).Next()
	}

	created, err := newUpstreamBalancer(strategy, instances)
	if err != nil {
		return "", err
	}
	actual, _ := balancerRegistry.LoadOrStore(key, created)
	return actual.(*upstreamBalancer).Next()
}

func parseUpstreamInstances(raw string) ([]UpstreamInstance, bool, error) {
	if strings.HasPrefix(raw, "[") {
		if instances, ok, err := parseJSONArrayInstances(raw); ok || err != nil {
			return instances, true, err
		}
	}

	if strings.Contains(raw, ",") {
		parts := strings.Split(raw, ",")
		instances := make([]UpstreamInstance, 0, len(parts))
		for _, part := range parts {
			u := strings.TrimSpace(part)
			if u == "" {
				continue
			}
			instances = append(instances, UpstreamInstance{URL: u})
		}
		if len(instances) == 0 {
			return nil, true, errors.New("empty upstream instance list")
		}
		return instances, true, nil
	}

	if _, err := url.ParseRequestURI(raw); err != nil {
		return nil, false, errors.New("invalid upstream url: " + raw)
	}

	return []UpstreamInstance{{URL: raw}}, false, nil
}

func parseJSONArrayInstances(raw string) ([]UpstreamInstance, bool, error) {
	var objectInstances []UpstreamInstance
	if err := json.Unmarshal([]byte(raw), &objectInstances); err == nil {
		if len(objectInstances) == 0 {
			return nil, true, errors.New("empty upstream instance list")
		}
		return objectInstances, true, nil
	}

	var stringInstances []string
	if err := json.Unmarshal([]byte(raw), &stringInstances); err == nil {
		if len(stringInstances) == 0 {
			return nil, true, errors.New("empty upstream instance list")
		}
		instances := make([]UpstreamInstance, 0, len(stringInstances))
		for _, u := range stringInstances {
			u = strings.TrimSpace(u)
			if u == "" {
				continue
			}
			instances = append(instances, UpstreamInstance{URL: u})
		}
		if len(instances) == 0 {
			return nil, true, errors.New("empty upstream instance list")
		}
		return instances, true, nil
	}

	return nil, false, errors.New("invalid upstream JSON array")
}

func readBalancerStats() map[string]UpstreamStats {
	out := make(map[string]UpstreamStats)
	balancerRegistry.Range(func(k, v any) bool {
		key := k.(balancerKey)
		out[key.upstreamSet+"|"+string(key.strategy)] = v.(*upstreamBalancer).Stats()
		return true
	})
	return out
}

func resetUpstreamBalancersForTest() {
	balancerRegistry = sync.Map{}
}
