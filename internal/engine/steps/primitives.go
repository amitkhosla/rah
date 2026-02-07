package steps

import (
	"io"
	"net/http"
	"rah/internal/engine"
	"rah/internal/rctx"
	"sync"
)

type HttpCallConfig struct {
	Method     string
	URL        string
	TargetSlot int
	Timeout    int
}

var client = &http.Client{
	Transport: &http.Transport{
		MaxIdleConnsPerHost: 100,
	},
}

// ProxyStep: High-performance streaming proxy
func ProxyStep(targetUrl string) engine.Instruction {
	return engine.Instruction{
		Name: "Proxy",
		Action: func(ctx *rctx.Context) int16 {
			req, _ := http.NewRequest(ctx.MethodString(), targetUrl, ctx.GetBodyReader())

			// 1. Re-inject Pass-through headers from original request
			req.Header = ctx.Request.Header

			// 2. Apply Mutations (Overrides) from our internal log
			for i := 0; i < ctx.MutationCount; i++ {
				m := ctx.MutationLog[i]
				req.Header.Set(string(m.Key), string(m.Value))
			}

			resp, err := client.Do(req)
			if err != nil {
				ctx.ResponseStatus = 502
				return engine.StopPlan
			}
			defer resp.Body.Close()

			// 3. Sync response back to context
			ctx.ResponseStatus = resp.StatusCode
			for k, v := range resp.Header {
				ctx.SetResponseHeader([]byte(k), []byte(v[0]))
			}

			// 4. Stream body directly to client
			ctx.FinalizeHeaders()
			io.Copy(ctx.GetWriter(), resp.Body)

			return engine.StopPlan
		},
	}
}

// HttpCallStep: Side-car calls (e.g., Auth)
func HttpCallStep(method string, url string, resultSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "HttpCall",
		Action: func(ctx *rctx.Context) int16 {
			req, err := http.NewRequest(method, url, nil)
			if err != nil {
				return engine.StopPlan
			}

			resp, err := client.Do(req)
			if err != nil {
				return engine.StopPlan
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			ctx.ByteSlots[resultSlot] = body
			return 1
		},
	}
}

// DynamicProxyStep: Discovered URL proxying
func DynamicProxyStep(urlSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "DynamicProxy",
		Action: func(ctx *rctx.Context) int16 {
			targetUrl := string(ctx.ByteSlots[urlSlot])
			if targetUrl == "" {
				ctx.ResponseStatus = 404
				return engine.StopPlan
			}

			// Reuse the ProxyStep logic internally or duplicate for specific needs
			req, _ := http.NewRequest(ctx.MethodString(), targetUrl, ctx.GetBodyReader())
			req.Header = ctx.Request.Header

			resp, err := client.Do(req)
			if err != nil {
				ctx.ResponseStatus = 502
				return engine.StopPlan
			}
			defer resp.Body.Close()

			ctx.ResponseStatus = resp.StatusCode
			for k, v := range resp.Header {
				ctx.SetResponseHeader([]byte(k), []byte(v[0]))
			}

			ctx.FinalizeHeaders()
			io.Copy(ctx.GetWriter(), resp.Body)
			return engine.StopPlan
		},
	}
}

// AsyncHttpCall: Fan-out pattern
func AsyncHttpCall(calls []HttpCallConfig) engine.Instruction {
	return engine.Instruction{
		Name: "AsyncMultiCall",
		Action: func(ctx *rctx.Context) int16 {
			var wg sync.WaitGroup
			for _, conf := range calls {
				wg.Add(1)
				go func(c HttpCallConfig) {
					defer wg.Done()
					req, _ := http.NewRequest(c.Method, c.URL, nil)
					resp, err := client.Do(req)
					if err != nil {
						return
					}
					defer resp.Body.Close()
					body, _ := io.ReadAll(resp.Body)
					// Note: ByteSlots access in goroutines needs caution
					// but is safe if slots are unique per config
					ctx.ByteSlots[c.TargetSlot] = body
				}(conf)
			}
			wg.Wait()
			return 1
		},
	}
}

// MergeStep: Aggregator pattern
func MergeStep(slotA, slotB int) engine.Instruction {
	return engine.Instruction{
		Name: "MergeData",
		Action: func(ctx *rctx.Context) int16 {
			dataA := ctx.ByteSlots[slotA]
			dataB := ctx.ByteSlots[slotB]

			// Simple merge logic
			combined := make([]byte, len(dataA)+len(dataB))
			copy(combined, dataA)
			copy(combined[len(dataA):], dataB)

			ctx.ResponseBuffer = combined
			ctx.IsBuffered = true
			ctx.ResponseStatus = 200
			return 1
		},
	}
}

// PrimitiveAddResponseHeader: Direct header manipulation
func PrimitiveAddResponseHeader(key, value string) engine.Instruction {
	return engine.Instruction{
		Name: "AddResponseHeader",
		Action: func(ctx *rctx.Context) int16 {
			ctx.SetResponseHeader([]byte(key), []byte(value))
			return 1
		},
	}
}

func PrimitiveExtractHeader(headerKey string, targetSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "ExtractHeader",
		Action: func(ctx *rctx.Context) int16 {
			// Access original request headers without allocation
			val := ctx.Request.Header.Get(headerKey)
			if val != "" {
				ctx.ByteSlots[targetSlot] = []byte(val)
			}
			return 1 // Move to next instruction
		},
	}
}
