package redis

import (
	"context"
	"sync"
)

// MultiGet fetches multiple keys in a single pipeline round-trip.
// Missing keys are absent from the returned map — not an error.
//
// Duplicate keys within the call are deduplicated before sending to Redis.
// If IODedupWindow is configured, keys already in the dedup layer are returned
// immediately without a Redis round-trip; only the remaining keys are fetched.
// Concurrent fetches for the same missing key are coalesced via singleflight.
//
// Cluster safety: pipeline.Get() per key (not MGET) lets go-redis route each
// command to the correct node; MGET would require all keys on the same slot.
func (s *Store) MultiGet(ctx context.Context, tenant string, keys []string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return nil, nil
	}

	// Deduplicate keys and build scoped → original mapping.
	type entry struct {
		scoped    string
		originals []string
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

	out := make(map[string][]byte, len(keys))

	// Fast path: serve hits from the dedup layer without touching Redis.
	var missing []*entry
	if s.dedup != nil {
		for _, e := range order {
			if val, ok := s.dedup.Get(e.scoped); ok && val != nil {
				for _, orig := range e.originals {
					out[orig] = val
				}
			} else {
				missing = append(missing, e)
			}
		}
	} else {
		missing = order
	}

	if len(missing) == 0 {
		return out, nil
	}

	// Fetch remaining keys via a single pipeline (one round-trip).
	type sfResult struct {
		val   []byte
		found bool
	}
	results := make([]any, len(missing))
	var wg sync.WaitGroup
	wg.Add(len(missing))
	for i, e := range missing {
		i, e := i, e
		go func() {
			defer wg.Done()
			v, err, _ := s.sf.Do(e.scoped, func() (any, error) {
				val, ferr := s.client.Get(ctx, e.scoped).Bytes()
				if ferr != nil {
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

	for i, e := range missing {
		if results[i] == nil {
			continue
		}
		r := results[i].(sfResult)
		if s.dedup != nil {
			s.dedup.Store(e.scoped, r.val, 0)
		}
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
// If IODedupWindow is configured, keys whose current value already matches the
// new value are filtered out before the pipeline is built. Only changed or
// unknown keys are written to Redis.
//
// In cluster mode, go-redis automatically groups pipeline commands by slot and
// fans them out to the correct nodes — no manual sharding needed.
func (s *Store) MultiPut(ctx context.Context, tenant string, kvs map[string][]byte) error {
	if len(kvs) == 0 {
		return nil
	}

	// Scope all keys first; filter unchanged entries if dedup is active.
	type scopedKV struct {
		scoped string
		orig   string
		val    []byte
	}
	writes := make([]scopedKV, 0, len(kvs))
	for k, v := range kvs {
		sk, err := scopedKey(tenant, s.domain, k)
		if err != nil {
			return err
		}
		if s.dedup != nil && s.dedup.ShouldSkipWrite(sk, v, 0) {
			continue
		}
		writes = append(writes, scopedKV{scoped: sk, orig: k, val: v})
	}

	if len(writes) == 0 {
		return nil
	}

	pipe := s.client.Pipeline()
	for _, w := range writes {
		pipe.Set(ctx, w.scoped, w.val, 0)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	if s.dedup != nil {
		for _, w := range writes {
			s.dedup.Store(w.scoped, w.val, 0)
		}
	}
	return nil
}
