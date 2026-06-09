package steps

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

func newTestPool() *upstreamTransportPool {
	p := &upstreamTransportPool{
		shards: make([]transportShard, 1),
		mask:   0,
	}
	p.shards[0].client = &http.Client{}
	return p
}

func TestUpstreamPoolTable_GetOrCreate_NewHost(t *testing.T) {
	table := &upstreamPoolTable{}
	counter := atomic.Int64{}

	pool := table.getOrCreate("example.com", func() *upstreamTransportPool {
		counter.Add(1)
		return newTestPool()
	})

	if pool == nil {
		t.Fatal("getOrCreate returned nil for new host")
	}

	retrieved := table.get("example.com")
	if retrieved != pool {
		t.Fatal("get did not return the same pool as getOrCreate")
	}

	if counter.Load() != 1 {
		t.Fatalf("build called %d times, expected 1", counter.Load())
	}
}

func TestUpstreamPoolTable_GetOrCreate_SameHostTwice(t *testing.T) {
	table := &upstreamPoolTable{}
	counter := atomic.Int64{}

	pool1 := table.getOrCreate("example.com", func() *upstreamTransportPool {
		counter.Add(1)
		return newTestPool()
	})

	pool2 := table.getOrCreate("example.com", func() *upstreamTransportPool {
		counter.Add(1)
		return newTestPool()
	})

	if pool1 != pool2 {
		t.Fatal("getOrCreate returned different pointers for same host")
	}

	if counter.Load() != 1 {
		t.Fatalf("build called %d times, expected 1", counter.Load())
	}
}

func TestUpstreamPoolTable_MultipleHosts(t *testing.T) {
	table := &upstreamPoolTable{}
	hosts := []string{"host1.com", "host2.com", "host3.com"}

	pools := make(map[string]*upstreamTransportPool)
	for _, host := range hosts {
		p := table.getOrCreate(host, func() *upstreamTransportPool {
			return newTestPool()
		})
		pools[host] = p
	}

	// Verify all pools are different
	for i, h1 := range hosts {
		for j, h2 := range hosts {
			if i != j && pools[h1] == pools[h2] {
				t.Fatalf("host %s and %s returned same pool", h1, h2)
			}
		}
	}

	// Verify get returns correct pool for each host
	for _, host := range hosts {
		retrieved := table.get(host)
		if retrieved != pools[host] {
			t.Fatalf("get for %s returned wrong pool", host)
		}
	}
}

func TestUpstreamPoolTable_Concurrent(t *testing.T) {
	table := &upstreamPoolTable{}
	counter := atomic.Int64{}
	hosts := []string{"host1.com", "host2.com", "host3.com"}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		for _, host := range hosts {
			wg.Add(1)
			go func(h string) {
				defer wg.Done()
				table.getOrCreate(h, func() *upstreamTransportPool {
					counter.Add(1)
					return newTestPool()
				})
			}(host)
		}
	}

	wg.Wait()

	// build should be called at most 3 times (once per host)
	if counter.Load() > 3 {
		t.Fatalf("build called %d times, expected at most 3", counter.Load())
	}

	// Verify all pools are non-nil and retrievable
	for _, host := range hosts {
		p := table.get(host)
		if p == nil {
			t.Fatalf("get returned nil for host %s", host)
		}
	}
}
