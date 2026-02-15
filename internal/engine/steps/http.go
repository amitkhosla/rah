package steps

import (
	"net"
	"net/http"
	"rah/internal/engine"
	"rah/internal/rctx"
	"time"
)

// Shared pooled client for all instructions.
// Optimized for a 2-core environment with high concurrency.
var GlobalTransport = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   2 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        2000,
		MaxIdleConnsPerHost: 100, // Important for high-throughput upstreams
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
	},
}

func GetClientFromPool() *http.Client {
	return GlobalTransport
}

var clientPool = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10, // Optimized for 2-core context
		IdleConnTimeout:     90 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	},
}

var poolClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        1000,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	},
}

func HttpAction(urlSlot int, timeout uint32) engine.Instruction {
	return engine.Instruction{
		Name: "HTTP_CALL",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			url := string(ctx.ByteSlots[urlSlot])
			req, _ := http.NewRequest("GET", url, nil)

			// Use the Pooled Client instead of http.DefaultClient
			resp, err := poolClient.Do(req)
			if err != nil {
				return -1 // Error state
			}
			defer resp.Body.Close()

			// Process response...
			return state.PC + 1
		},
	}
}
