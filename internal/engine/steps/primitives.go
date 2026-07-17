package steps

import (
	"io"
	"net/http"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"sync"
)

var client = GetClientFromPool()

// hopByHopHeaders is the set of HTTP/1.1 hop-by-hop headers that must NOT be
// forwarded to upstream services. Shared by ProxyStep and HttpAction.
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Transfer-Encoding":   {},
	"Te":                  {},
	"Trailer":             {},
	"Upgrade":             {},
	"Proxy-Authorization": {},
	"Proxy-Authenticate":  {},
}

func ProxyStep(targetUrl string) engine.Instruction {
	return engine.Instruction{
		Name: "Proxy",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			req, _ := http.NewRequest(ctx.MethodString(), targetUrl, ctx.GetBodyReader())
			req.Header = make(http.Header, len(ctx.Request.Header))
			for key, vals := range ctx.Request.Header {
				if _, skip := hopByHopHeaders[key]; skip {
					continue
				}
				req.Header[key] = vals
			}

			for i := 0; i < ctx.MutationCount; i++ {
				m := ctx.MutationLog[i]
				if m.Op == 1 {
					req.Header.Del(string(m.Key))
				} else {
					req.Header.Set(string(m.Key), string(m.Value))
				}
			}

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

func HttpCallStep(method string, url string, resultSlot int, nextID int16) engine.Instruction {
	return engine.Instruction{
		Name: "HttpCall",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
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
			return nextID
		},
	}
}

func AsyncHttpCall(calls []HttpCallConfig, nextID int16) engine.Instruction {
	return engine.Instruction{
		Name: "AsyncMultiCall",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
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
					ctx.ByteSlots[c.TargetSlot] = body
				}(conf)
			}
			wg.Wait()
			return nextID
		},
	}
}

func MergeStep(slotA, slotB int, nextID int16) engine.Instruction {
	return engine.Instruction{
		Name: "MergeData",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			dataA := ctx.ByteSlots[slotA]
			dataB := ctx.ByteSlots[slotB]

			combined := make([]byte, len(dataA)+len(dataB))
			copy(combined, dataA)
			copy(combined[len(dataA):], dataB)

			ctx.ResponseBuffer = combined
			ctx.IsBuffered = true
			ctx.ResponseStatus = 200
			return nextID
		},
	}
}

func PrimitiveAddResponseHeader(key, value string, nextID int16) engine.Instruction {
	return engine.Instruction{
		Name: "AddResponseHeader",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.SetResponseHeader([]byte(key), []byte(value))
			return nextID
		},
	}
}

func PrimitiveExtractHeader(headerKey string, targetSlot int, nextID int16) engine.Instruction {
	return engine.Instruction{
		Name: "ExtractHeader",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			val := ctx.Request.Header.Get(headerKey)
			if val != "" {
				ctx.ByteSlots[targetSlot] = []byte(val)
			}
			return nextID
		},
	}
}

// Support types
type HttpCallConfig struct {
	Method     string
	URL        string
	TargetSlot int
	Timeout    int
}
