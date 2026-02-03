package steps

import (
	//	"bytes"
	"io"
	"net/http"
	"rah/internal/engine"
	"rah/internal/rctx"
	"sync"
)

type HttpCallConfig struct {
	Method     string // GET, POST, etc.
	URL        string // The destination endpoint
	TargetSlot int    // The specific Slot index where the result will be stored
	Timeout    int    // (Optional) Max time to wait in milliseconds
}

var client = &http.Client{
	Transport: &http.Transport{
		MaxIdleConnsPerHost: 100,
	},
}

func ProxyStep(targetUrl string) engine.Instruction {
	return engine.Instruction{
		Name: "Proxy",
		Action: func(ctx *rctx.Context) int16 {
			// Build the new request using ONLY internal rctx data
			req, _ := http.NewRequest(string(ctx.Request.Method), targetUrl, ctx.GetBodyReader())

			// Re-inject the internal headers
			for _, h := range ctx.Request.Headers {
				req.Header.Add(string(h.Key), string(h.Value))
			}

			resp, err := client.Do(req)
			if err != nil {
				return 99
			}

			// Vacuum response headers back into our internal rctx.Response.Headers
			ctx.Response.StatusCode = resp.StatusCode
			for k, values := range resp.Header {
				for _, v := range values {
					ctx.Response.AddHeader(k, v)
				}
			}

			ctx.Response.Stream = resp.Body
			return 99
		},
	}
}

// HttpCallStep: Used for side-cars (Auth/Discovery)
func HttpCallStep(method string, url string, resultSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "HttpCall",
		Action: func(ctx *rctx.Context) int16 {
			// FIX: Using ctx.GetBodyReader() instead of wrapping Stream
			req, err := http.NewRequest(method, url, ctx.GetBodyReader())
			if err != nil {
				return 99
			}

			resp, err := client.Do(req)
			if err != nil {
				return 99
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			ctx.Slots[resultSlot] = body
			return 1
		},
	}
}
func ConditionStep(predicate func(ctx *rctx.Context) bool, onSuccess, onFail int16) engine.Instruction {
	return engine.Instruction{
		Name: "Branch",
		Action: func(ctx *rctx.Context) int16 {
			if predicate(ctx) {
				return onSuccess // Jump to the instruction index for "Then"
			}
			return onFail // Jump to the instruction index for "Else"
		},
	}
}
func DynamicProxyStep(urlSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "DynamicProxy",
		Action: func(ctx *rctx.Context) int16 {
			// Get the URL we discovered in a previous step (like from Consul or Etcd)
			targetUrl, _ := ctx.Slots[urlSlot].(string)

			// 1. Create Request from Internal Headers/Body
			req, _ := http.NewRequest(string(ctx.Request.Method), targetUrl, ctx.GetBodyReader())
			for _, h := range ctx.Request.Headers {
				req.Header.Add(string(h.Key), string(h.Value))
			}

			// 2. Execute
			resp, err := client.Do(req)
			if err != nil {
				return 99
			}

			// 3. Populate Response Buffer for Entry Layer
			ctx.Response.StatusCode = resp.StatusCode
			ctx.Response.Stream = resp.Body // Streaming back to client

			// Vacuum headers...
			return 99
		},
	}
}
func AsyncHttpCall(calls []HttpCallConfig) engine.Instruction {
	return engine.Instruction{
		Name: "AsyncMultiCall",
		Action: func(ctx *rctx.Context) int16 {
			var wg sync.WaitGroup

			for _, config := range calls {
				wg.Add(1)
				go func(conf HttpCallConfig) {
					defer wg.Done()

					// Create request from internal rctx data
					req, _ := http.NewRequest(conf.Method, conf.URL, nil)

					// Execute
					resp, err := client.Do(req)
					if err != nil {
						return
					}
					defer resp.Body.Close()

					// Materialize result into the designated slot
					body, _ := io.ReadAll(resp.Body)
					ctx.Slots[conf.TargetSlot] = body
				}(config)
			}

			wg.Wait()
			return 1 // Move to the next layer (Processing/Merging)
		},
	}
}

func MergeStep(targetSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "MergeData",
		Action: func(ctx *rctx.Context) int16 {
			dataA := ctx.Slots[1].([]byte)
			dataB := ctx.Slots[2].([]byte)

			// Perform your "Next Layer" logic - e.g., JSON merging
			combined := append(dataA, dataB...)

			// Set this as the final response body
			ctx.Response.Body = combined
			ctx.Response.StatusCode = 200
			return 1
		},
	}
}

// PrimitiveExtractHeader pulls a value from the internal Request.Headers slice
func PrimitiveExtractHeader(headerKey string, targetSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "ExtractHeader",
		Action: func(ctx *rctx.Context) int16 {
			// Find the header in our agnostic internal slice
			for _, h := range ctx.Request.Headers {
				if string(h.Key) == headerKey {
					ctx.Slots[targetSlot] = string(h.Value)
					return 1
				}
			}
			return 1
		},
	}
}

// PrimitiveAddResponseHeader modifies the internal ResponseBuffer
func PrimitiveAddResponseHeader(key, value string) engine.Instruction {
	return engine.Instruction{
		Name: "AddResponseHeader",
		Action: func(ctx *rctx.Context) int16 {
			ctx.Response.AddHeader(key, value)
			return 1
		},
	}
}
