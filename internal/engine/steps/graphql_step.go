package steps

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/tidwall/gjson"
	"github.com/amitkhosla/rah/internal/egress"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/gatewaylog"
	"github.com/amitkhosla/rah/internal/observability"
	"github.com/amitkhosla/rah/internal/rctx"
)

// GraphQLCallConfig holds the bake-time configuration for a graphql_call instruction.
// All slot indices use -1 to indicate "not set / use static value".
type GraphQLCallConfig struct {
	StaticURL     string                // Static upstream URL for the GraphQL endpoint
	URLSlot       int                   // -1 = use StaticURL
	StaticPrefix  []byte                // `{"query":"<escaped>","operationName":"<name>","variables":`
	StaticSuffix  []byte                // `}` or final closing chars
	QuerySlot     int                   // -1 if query baked into StaticPrefix
	VariablesSlot int                   // -1 if no variables
	DataSlot      int                   // Destination slot for response "data" field
	ErrorsSlot    int                   // Destination slot for response "errors" field
	TimeoutMs     int                   // Request timeout in milliseconds
	EgressProfile *egress.EgressProfile // Transport profile for TLS/timeout
	FailOnErrors  bool                  // If true, set ctx.Failed when errors present in response
}

// GraphQLGetConfig holds the bake-time configuration for a graphql_get instruction.
// Extracts a single field from data already in a slot using gjson path.
type GraphQLGetConfig struct {
	DataSlot   int    // Source slot holding JSON response data
	StaticPath string // gjson path to extract (baked at compile time)
	DestSlot   int    // Destination slot for extracted value
}

// AppendJSONEscapedString appends the JSON-escaped string value to dst.
// Escapes quotes, backslashes, and control characters per JSON spec.
func AppendJSONEscapedString(dst []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			if c < 0x20 {
				// Control character: emit \uXXXX
				dst = append(dst, '\\', 'u', '0', '0')
				dst = append(dst, "0123456789abcdef"[c>>4], "0123456789abcdef"[c&0xf])
			} else {
				dst = append(dst, c)
			}
		}
	}
	return dst
}

// GraphQLCallFromConfig builds a graphql_call instruction from configuration.
// The instruction executes an HTTP POST to the GraphQL endpoint with a JSON body.
func GraphQLCallFromConfig(cfg GraphQLCallConfig) engine.Instruction {
	// Bake-time URL and timeout resolution
	staticURL := cfg.StaticURL
	hasStaticURL := staticURL != "" && cfg.URLSlot < 0
	var staticPool *upstreamTransportPool
	if hasStaticURL {
		effectiveURL := staticURL
		effectiveProfile := cfg.EgressProfile
		if effectiveProfile == nil {
			effectiveProfile = &egress.EgressProfile{Type: egress.EgressTypeAuto}
		}
		bakedCfg := resolveHTTPConfigForTarget("", nil)
		bakedFingerprint := configFingerprint(bakedCfg)
		staticPool = getClientForBakedConfig(effectiveProfile, extractUpstreamHost(effectiveURL), bakedCfg, bakedFingerprint, 2048).Pool
	}

	// Bake-time timeout setup
	bakedTimeoutMs := uint32(cfg.TimeoutMs)
	if bakedTimeoutMs == 0 {
		bakedTimeoutMs = 10000 // 10s default
	}
	bakedTotalDur := time.Duration(bakedTimeoutMs) * time.Millisecond

	return engine.Instruction{
		Name: "GRAPHQL_CALL",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Resolve URL
			upstreamURL := staticURL
			if cfg.URLSlot >= 0 && cfg.URLSlot < len(ctx.ByteSlots) && len(ctx.ByteSlots[cfg.URLSlot]) > 0 {
				upstreamURL = string(ctx.ByteSlots[cfg.URLSlot])
			}
			if upstreamURL == "" {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "graphql_call: upstream URL is empty"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// Build request body: StaticPrefix + variables JSON + "}"
			vars := []byte("null") // default
			if cfg.VariablesSlot >= 0 && cfg.VariablesSlot < len(ctx.ByteSlots) {
				if v := ctx.ByteSlots[cfg.VariablesSlot]; len(v) > 0 {
					vars = v
				}
			}

			// Zero-alloc body assembly
			total := len(cfg.StaticPrefix) + len(vars) + 1
			body := ctx.Alloc(total)
			n := copy(body, cfg.StaticPrefix)
			n += copy(body[n:], vars)
			body[n] = '}'

			// Pick client
			var client *http.Client
			if staticPool != nil {
				client = staticPool.get()
			} else {
				pool := getClientForTarget(extractUpstreamHost(upstreamURL), nil)
				client = pool.Pool.get()
			}

			// Create HTTP request
			req, err := http.NewRequest("POST", upstreamURL, nil)
			if err != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "graphql_call: invalid URL: " + err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")

			// Create reader from body bytes
			reader := bytesReaderPool.Get().(*bytes.Reader)
			defer bytesReaderPool.Put(reader)
			reader.Reset(body)
			req.Body = io.NopCloser(reader)
			req.ContentLength = int64(len(body))

			// Execute with timeout
			ctx.SetUpstreamTimeout(bakedTotalDur)
			resp, err := client.Do(req)
			ctx.ClearUpstreamTimeout()

			if err != nil {
				ctx.ResponseStatus = 504
				ctx.Failed = true
				ctx.ErrorCode = 504
				msg := "graphql_call: upstream timeout or error: " + err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// Read response body
			respBodyBuf := responseBodyPool.Get().(*bytes.Buffer)
			defer responseBodyPool.Put(respBodyBuf)
			respBodyBuf.Reset()

			_, err = respBodyBuf.ReadFrom(resp.Body)
			_, _ = io.Copy(io.Discard, resp.Body)
			if closeErr := resp.Body.Close(); closeErr != nil {
				gatewaylog.Default.Error("graphql_call: failed to close upstream response body",
					gatewaylog.F("err", closeErr.Error()),
				)
				if ctx.Trace != nil && ctx.Obs != nil {
					ctx.Obs.AppendUpstreamEvent(ctx.Trace, observability.UpstreamEvent{
						Err: "graphql_call: body close: " + closeErr.Error(),
					})
				}
			}

			if err != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "graphql_call: failed to read response body: " + err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			respData := respBodyBuf.Bytes()

			// Extract "data" field
			if cfg.DataSlot >= 0 && cfg.DataSlot < len(ctx.ByteSlots) {
				dataResult := gjson.GetBytes(respData, "data")
				if dataResult.Exists() {
					dataBytes := []byte(dataResult.Raw)
					ctx.ByteSlots[cfg.DataSlot] = ctx.Alloc(len(dataBytes))
					copy(ctx.ByteSlots[cfg.DataSlot], dataBytes)
				}
			}

			// Extract "errors" field
			if cfg.ErrorsSlot >= 0 && cfg.ErrorsSlot < len(ctx.ByteSlots) {
				errResult := gjson.GetBytes(respData, "errors")
				if errResult.Exists() && errResult.IsArray() {
					errBytes := []byte(errResult.Raw)
					ctx.ByteSlots[cfg.ErrorsSlot] = ctx.Alloc(len(errBytes))
					copy(ctx.ByteSlots[cfg.ErrorsSlot], errBytes)

					if cfg.FailOnErrors {
						ctx.Failed = true
						ctx.ResponseStatus = 400
					}
				}
			}

			ctx.ResponseStatus = int(resp.StatusCode)
			if resp.StatusCode >= 400 {
				ctx.Failed = true
			}

			return state.PC + 1
		},
	}
}

// GraphQLGetFromConfig builds a graphql_get instruction from configuration.
// The instruction extracts a single field from JSON response data using gjson path.
func GraphQLGetFromConfig(cfg GraphQLGetConfig) engine.Instruction {
	return engine.Instruction{
		Name: "GRAPHQL_GET",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.DataSlot < 0 || cfg.DataSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}

			data := ctx.ByteSlots[cfg.DataSlot]
			if len(data) == 0 {
				// Source slot is empty, destination remains nil
				if cfg.DestSlot >= 0 && cfg.DestSlot < len(ctx.ByteSlots) {
					ctx.ByteSlots[cfg.DestSlot] = nil
				}
				return state.PC + 1
			}

			result := gjson.GetBytes(data, cfg.StaticPath)
			if !result.Exists() {
				// Path not found, destination slot becomes nil
				if cfg.DestSlot >= 0 && cfg.DestSlot < len(ctx.ByteSlots) {
					ctx.ByteSlots[cfg.DestSlot] = nil
				}
				return state.PC + 1
			}

			// Allocate and copy extracted value
			extracted := []byte(result.Raw)
			if cfg.DestSlot >= 0 && cfg.DestSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[cfg.DestSlot] = ctx.Alloc(len(extracted))
				copy(ctx.ByteSlots[cfg.DestSlot], extracted)
			}

			return state.PC + 1
		},
	}
}

// ParseGraphQLError extracts the first error message from a GraphQL errors array.
// sourceSlot: slot holding the errors array JSON
// destSlot: destination slot for the error message string
// If the array is empty or not present, destSlot is set to nil.
func ParseGraphQLError(sourceSlot, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "PARSE_GRAPHQL_ERROR",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if destSlot >= 0 && destSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[destSlot] = nil
			}

			if sourceSlot < 0 || sourceSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}

			errorsData := ctx.ByteSlots[sourceSlot]
			if len(errorsData) == 0 {
				return state.PC + 1
			}

			// Try to extract first error's message field
			// Assume errors is an array of objects with "message" field
			errResult := gjson.GetBytes(errorsData, "0.message")
			if errResult.Exists() && errResult.Str != "" {
				msg := errResult.Str
				msgBytes := []byte(msg)
				if destSlot >= 0 && destSlot < len(ctx.ByteSlots) {
					ctx.ByteSlots[destSlot] = ctx.Alloc(len(msgBytes))
					copy(ctx.ByteSlots[destSlot], msgBytes)
				}
				return state.PC + 1
			}

			// If no message field, try to get the entire first error
			firstErrResult := gjson.GetBytes(errorsData, "0")
			if firstErrResult.Exists() {
				errStr := firstErrResult.Raw
				if destSlot >= 0 && destSlot < len(ctx.ByteSlots) {
					ctx.ByteSlots[destSlot] = ctx.Alloc(len(errStr))
					copy(ctx.ByteSlots[destSlot], []byte(errStr))
				}
			}

			return state.PC + 1
		},
	}
}
