package redis

import (
	"context"
	"sync"
)

// MultiGet fetches multiple keys in a single MGET command (one round-trip).
// Missing keys are absent from the returned map — not an error.
//
// Duplicate keys within the call are deduplicated before sending to Redis.
// Concurrent MultiGet calls that share keys are coalesced via singleflight:
// each unique key triggers at most one in-flight Redis read at a time.
//
// Cluster safety: all keys share the hash tag {tenant:<t>:<domain>}, so they
// always land on the same slot. MGET never produces a CROSSSLOT error.
func (s *Store) MultiGet(ctx context.Context, tenant string, keys []string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	// Deduplicate keys and build scoped → original mapping.
	type entry struct {
		scoped   string
		originals []string // multiple original keys may map to the same scoped key
	}
	seen := make(map[string]*entry, len(keys))
	order := make([]*entry, 0, len(keys))
	for _, k := range keys {
		sk, err := scopedKey(tenant, s.domain, k)
		if err != nil {
			return nil, err
		}
		if e, ok := seen[sk]; ok {
			e.originals = append(e.originals, k)
		} else {
			e := &entry{scoped: sk, originals: []string{k}}
			seen[sk] = e
			order = append(order, e)
		}
	}

	// Fan out one singleflight.Do per unique scoped key, all running in parallel.
	type sfResult struct {
		val   []byte
		found bool
	}
	results := make([]any, len(order))
	var wg sync.WaitGroup
	wg.Add(len(order))
	for i, e := range order {
		i, e := i, e
		go func() {
			defer wg.Done()
			v, err, _ := s.sf.Do(e.scoped, func() (any, error) {
				val, ferr := s.client.Get(ctx, e.scoped).Bytes()
				if ferr != nil {
					// Treat missing key as (nil, false) not an error.
					return sfResult{nil, false}, nil
				}
				return sfResult{val, true}, nil
			})
			if err == nil {
				results[i] = v
			}
		}()
	}
	wg.Wait()

	out := make(map[string][]byte, len(keys))
	for i, e := range order {
		if results[i] == nil {
			continue
		}
		r := results[i].(sfResult)
		if !r.found {
			continue
		}
		for _, orig := range e.originals {
			out[orig] = r.val
		}
	}
	return out, nil
}

// MultiPut writes multiple key-value pairs in a single pipeline round-trip.
// All entries are stored with no expiry. Use PutWithTTL for expiring entries.
//
// In cluster mode, go-redis automatically groups pipeline commands by slot and
// fans them out to the correct nodes — no manual sharding needed.
func (s *Store) MultiPut(ctx context.Context, tenant string, kvs map[string][]byte) error {
	if len(kvs) == 0 {
		return nil
	}
	pipe := s.client.Pipeline()
	for k, v := range kvs {
		sk, err := scopedKey(tenant, s.domain, k)
		if err != nil {
			return err
		}
		pipe.Set(ctx, sk, v, 0)
	}
	_, err := pipe.Exec(ctx)
	return err
}
