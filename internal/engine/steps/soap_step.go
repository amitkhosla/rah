package steps

import (
	"bytes"
	"context"
	"net/http"
	"github.com/amitkhosla/rah/internal/egress"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"github.com/amitkhosla/rah/internal/soap"
	"time"
)

// SOAPCallConfig holds the bake-time configuration for a soap_call instruction.
type SOAPCallConfig struct {
	// Static configuration
	StaticURL        string
	StaticMethod     string           // "POST" or other
	StaticContentType string           // typically "application/soap+xml" or "text/xml"
	URLSlot          int              // -1 = use StaticURL
	MethodSlot       int              // -1 = use StaticMethod
	BodySlot         int              // slot containing XML request body
	ContentTypeSlot  int              // -1 = use StaticContentType
	Timeout          uint32           // milliseconds; 0 = no timeout
	MaxRetries       int              // -1 = default, >= 0 = explicit retry count
	RetryCondFunc    ConditionFunc    // nil = no condition-based retry

	// Response configuration
	ResponseBodySlot int              // -1 = not captured; slot to store unwrapped response
	ResponseStatusSlot int            // -1 = not captured; slot to store HTTP status
	ForwardIncomingHeaders  bool      // copy headers from incoming request
	ForwardResponseHeaders  bool      // copy response headers to context
	BlockHeadersMap map[string]struct{} // nil = no blocking

	// Runtime tuning
	FlowInput map[string]string       // flow-level http.* tuning keys
	EgressProfile *egress.EgressProfile // nil = Auto
	TLSClientCert []byte              // mTLS certificate
	TLSClientKey  []byte              // mTLS key
	URLPolicy URLPolicy               // URL validation policy

	// Computed at bake time
	envSizeHint int                   // estimated envelope size for ctx.Alloc
	soapVersion uint8                 // 1 or 2 for envelope builder selection
}

// BuildSOAPEnvelopeStepConfig holds bake-time config for a standalone build_soap_envelope step.
type BuildSOAPEnvelopeStepConfig struct {
	BodySlot    int   // slot holding XML body to wrap (required)
	OutputSlot  int   // slot to store resulting SOAP envelope (required)
	SoapVersion uint8 // 1 = SOAP 1.1 (default), 2 = SOAP 1.2
}

// BuildSOAPEnvelopeFromConfig creates an instruction that wraps an XML body in a SOAP envelope
// and writes it to OutputSlot. The envelope bytes are arena-allocated.
func BuildSOAPEnvelopeFromConfig(cfg BuildSOAPEnvelopeStepConfig) engine.Instruction {
	const envSizeHint = 150 + 256 // typical SOAP overhead + padding
	return engine.Instruction{
		Name: "BUILD_SOAP_ENVELOPE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.BodySlot < 0 || cfg.BodySlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}
			bodyXML := ctx.ByteSlots[cfg.BodySlot]
			if len(bodyXML) == 0 {
				return state.PC + 1
			}

			envelopeBuf := ctx.Alloc(envSizeHint + len(bodyXML))
			var envelope []byte
			if cfg.SoapVersion == 2 {
				envelope = soap.AppendSOAP12Envelope(envelopeBuf[:0], bodyXML)
			} else {
				envelope = soap.AppendSOAP11Envelope(envelopeBuf[:0], bodyXML)
			}

			if cfg.OutputSlot >= 0 && cfg.OutputSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[cfg.OutputSlot] = envelope
			}
			return state.PC + 1
		},
	}
}

// buildSOAPEnvelopeConfig is an internal helper kept for bake-time envSizeHint computation
// on SOAPCallConfig (not a step â€” used only within compileSOAPCall).
func buildSOAPEnvelopeHint(cfg SOAPCallConfig) SOAPCallConfig {
	cfg.envSizeHint = 150 + 256
	return cfg
}

// ParseSOAPFaultConfig creates a soap_fault instruction factory.
// Extracts faultcode/faultstring (SOAP 1.1) or Code/Reason (SOAP 1.2) from response.
type ParseSOAPFaultConfig struct {
	FaultSlot       int  // slot to store fault details (combined code:string)
	ResponseSlot    int  // slot containing SOAP fault response
	Version         uint8 // 1 or 2
}

// ParseSOAPFaultFromConfig creates an instruction that extracts SOAP fault details.
func ParseSOAPFaultFromConfig(cfg ParseSOAPFaultConfig) engine.Instruction {
	return engine.Instruction{
		Name: "SOAP_PARSE_FAULT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Get response from slot
			respSlot := cfg.ResponseSlot
			if respSlot < 0 || respSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}

			respBody := ctx.ByteSlots[respSlot]
			if len(respBody) == 0 {
				return state.PC + 1
			}

			// Parse fault
			codeBuf := make([]byte, 0, 64)
			msgBuf := make([]byte, 0, 256)

			var code, msg []byte
			if cfg.Version == 2 {
				code, msg, _ = soap.AppendParseFault12(codeBuf, msgBuf, respBody)
			} else {
				code, msg, _ = soap.AppendParseFault11(codeBuf, msgBuf, respBody)
			}

			// Store combined fault details in slot
			if cfg.FaultSlot >= 0 && cfg.FaultSlot < len(ctx.ByteSlots) {
				// Combine code and message with separator
				totalLen := len(code) + len(msg) + 1 // +1 for colon separator
				faultBuf := ctx.Alloc(totalLen)
				copy(faultBuf, code)
				faultBuf[len(code)] = ':'
				copy(faultBuf[len(code)+1:], msg)
				ctx.ByteSlots[cfg.FaultSlot] = faultBuf
			}

			return state.PC + 1
		},
	}
}

// SOAPCallFromConfig creates an instruction that wraps request XML in SOAP envelope,
// calls the upstream, unwraps response, and stores it in ResponseBodySlot.
// On SOAP fault response, ctx.Failed = true and fault details are parsed.
func SOAPCallFromConfig(cfg SOAPCallConfig) engine.Instruction {
	// Reuse HTTP infrastructure: build transport pool just like http_call
	bakedCfg := resolveHTTPConfigForTarget("", cfg.FlowInput)

	bakedAttempts := bakedCfg.RetryMaxAttempts + 1
	if cfg.MaxRetries >= 0 {
		bakedAttempts = cfg.MaxRetries + 1
	}
	if bakedAttempts < 1 {
		bakedAttempts = 1
	}

	// Validate static URL at bake time
	if cfg.StaticURL != "" && cfg.URLSlot < 0 && cfg.URLPolicy != URLPolicyPassthrough {
		if _, errMsg := validateURL(cfg.StaticURL, cfg.URLPolicy); errMsg != "" {
			return engine.Instruction{
				Name: "SOAP_CALL",
				Action: func(ctx *rctx.Context, _ *engine.ExecutionState) int16 {
					msg := "soap_call: invalid static URL: " + errMsg
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				},
			}
		}
	}

	// Build transport pool for static URL
	var staticPool *upstreamTransportPool
	hasStaticURL := cfg.StaticURL != "" && cfg.URLSlot < 0
	if hasStaticURL {
		staticPool = getClientForProfile(cfg.EgressProfile, extractUpstreamHost(cfg.StaticURL), cfg.FlowInput).Pool
	}

	// Timeout configuration
	bakedTotalTimeoutMs := cfg.Timeout
	if bakedTotalTimeoutMs == 0 && bakedCfg.RequestTimeout > 0 {
		bakedTotalTimeoutMs = uint32(bakedCfg.RequestTimeout / time.Millisecond)
	}
	hasTotalTimeout := bakedTotalTimeoutMs > 0
	bakedTotalDur := time.Duration(bakedTotalTimeoutMs) * time.Millisecond

	// Fixed context variable for request creation
	var requestCtx context.Context
	if hasTotalTimeout {
		requestCtx = context.Background()
	}

	return engine.Instruction{
		Name: "SOAP_CALL",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Get request body from slot
			bodySlot := cfg.BodySlot
			if bodySlot < 0 || bodySlot >= len(ctx.ByteSlots) {
				ctx.ResponseStatus = 400
				ctx.Failed = true
				ctx.ErrorCode = 400
				ctx.ErrorMsg = ctx.Alloc(len("soap_call: missing body"))
				copy(ctx.ErrorMsg, "soap_call: missing body")
				return state.PC + 1
			}

			bodyXML := ctx.ByteSlots[bodySlot]
			if len(bodyXML) == 0 {
				ctx.ResponseStatus = 400
				ctx.Failed = true
				ctx.ErrorCode = 400
				ctx.ErrorMsg = ctx.Alloc(len("soap_call: empty body"))
				copy(ctx.ErrorMsg, "soap_call: empty body")
				return engine.StopPlan
			}

			// Build SOAP envelope
			envelopeBuf := ctx.Alloc(cfg.envSizeHint + len(bodyXML))
			var envelope []byte
			if cfg.soapVersion == 2 {
				envelope = soap.AppendSOAP12Envelope(envelopeBuf[:0], bodyXML)
			} else {
				envelope = soap.AppendSOAP11Envelope(envelopeBuf[:0], bodyXML)
			}

			// Resolve URL
			url := cfg.StaticURL
			if cfg.URLSlot >= 0 && cfg.URLSlot < len(ctx.ByteSlots) {
				if urlBytes := ctx.ByteSlots[cfg.URLSlot]; len(urlBytes) > 0 {
					url = string(urlBytes)
				}
			}
			if url == "" {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				ctx.ErrorMsg = ctx.Alloc(len("soap_call: no URL"))
				copy(ctx.ErrorMsg, "soap_call: no URL")
				return engine.StopPlan
			}

			// Resolve content type
			contentType := cfg.StaticContentType
			if cfg.StaticContentType == "" {
				if cfg.soapVersion == 2 {
					contentType = "application/soap+xml"
				} else {
					contentType = "text/xml"
				}
			}
			if cfg.ContentTypeSlot >= 0 && cfg.ContentTypeSlot < len(ctx.ByteSlots) {
				if ctBytes := ctx.ByteSlots[cfg.ContentTypeSlot]; len(ctBytes) > 0 {
					contentType = string(ctBytes)
				}
			}

			// Create HTTP request
			req, err := http.NewRequest("POST", url, bytes.NewReader(envelope))
			if err != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "soap_call: " + err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("SOAPAction", "")

			// Set timeout
			if hasTotalTimeout {
				reqCtx, cancel := context.WithTimeout(requestCtx, bakedTotalDur)
				defer cancel()
				req = req.WithContext(reqCtx)
			}

			// Execute request with retries
			var resp *http.Response
			var execErr error
			for attempt := 0; attempt < bakedAttempts; attempt++ {
				var client *http.Client
				if hasStaticURL {
					client = staticPool.get()
				} else {
					client = getClientForProfile(cfg.EgressProfile, extractUpstreamHost(url), cfg.FlowInput).Pool.get()
				}

				resp, execErr = client.Do(req)
				// Success: break
				if execErr == nil {
					break
				}
				// Error: close and retry
				if resp != nil && resp.Body != nil {
					resp.Body.Close()
				}
			}

			if execErr != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "soap_call: " + execErr.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// Capture response status
			if cfg.ResponseStatusSlot >= 0 && cfg.ResponseStatusSlot < len(ctx.IntSlots) {
				ctx.IntSlots[cfg.ResponseStatusSlot] = int64(resp.StatusCode)
			}

			// Read response body
			respBuf := responseBodyPool.Get().(*bytes.Buffer)
			defer responseBodyPool.Put(respBuf)
			respBuf.Reset()

			_, err = respBuf.ReadFrom(resp.Body)
			resp.Body.Close()
			if err != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "soap_call: " + err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			respBody := respBuf.Bytes()

			// Check for SOAP fault
			if resp.StatusCode >= 400 {
				// Try to parse SOAP fault
				codeBuf := make([]byte, 0, 64)
				msgBuf := make([]byte, 0, 256)

				var code, msg []byte
				if cfg.soapVersion == 2 {
					code, msg, _ = soap.AppendParseFault12(codeBuf, msgBuf, respBody)
				} else {
					code, msg, _ = soap.AppendParseFault11(codeBuf, msgBuf, respBody)
				}

				// Populate fault details if extraction succeeded
				if len(code) > 0 || len(msg) > 0 {
					ctx.Failed = true
					ctx.ResponseStatus = resp.StatusCode
					ctx.ErrorCode = int16(resp.StatusCode)
					if len(msg) > 0 {
						ctx.ErrorMsg = ctx.Alloc(len(msg))
						copy(ctx.ErrorMsg, msg)
					}
				}
				return engine.StopPlan
			}

			// Unwrap SOAP body
			if cfg.ResponseBodySlot >= 0 && cfg.ResponseBodySlot < len(ctx.ByteSlots) {
				unwrappedBuf := make([]byte, 0, len(respBody))
				unwrapped, err := soap.AppendUnwrapBody(unwrappedBuf, respBody)
				if err == nil {
					slotBuf := ctx.Alloc(len(unwrapped))
					copy(slotBuf, unwrapped)
					ctx.ByteSlots[cfg.ResponseBodySlot] = slotBuf
				} else {
					// Couldn't unwrap; store raw response
					slotBuf := ctx.Alloc(len(respBody))
					copy(slotBuf, respBody)
					ctx.ByteSlots[cfg.ResponseBodySlot] = slotBuf
				}
			}

			return state.PC + 1
		},
	}
}
