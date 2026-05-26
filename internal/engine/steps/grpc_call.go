package steps

import (
	"context"
	"crypto/tls"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip" // registers gzip compressor
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"rah/internal/egress"
	"rah/internal/engine"
	grpcutil "rah/internal/grpc"
	"rah/internal/rctx"
)

// GlobalGrpcConnPool is the gateway-wide gRPC connection pool.
// Set by main.go at startup before any flows are executed.
var GlobalGrpcConnPool *grpcutil.ConnPool

// GlobalGrpcRegistry is the gateway-wide gRPC descriptor registry.
// Set by main.go at startup; referenced by compileGrpcCall at bake time.
var GlobalGrpcRegistry *grpcutil.DescriptorRegistry

// MetadataLiteral is a static key=value pair injected into every gRPC call's metadata.
type MetadataLiteral struct {
	Key   string
	Value string
}

// MetadataSlotBind injects the value of a ByteSlot into gRPC metadata under the given key.
type MetadataSlotBind struct {
	Key  string
	Slot int // index into ctx.ByteSlots; -1 = skip
}

// GrpcCallConfig holds the bake-time configuration for a grpc_call instruction.
// All slot indices use -1 to indicate "not configured".
type GrpcCallConfig struct {
	// Descriptor / method identity — resolved at bake time.
	DescriptorSetName string
	ServiceName       string
	MethodName        string
	FullMethod        string                       // "/pkg.Service/Method" — precomputed
	MethodDesc        protoreflect.MethodDescriptor // nil until bake resolves it

	// URL resolution — slot takes precedence over static.
	StaticURL string
	URLSlot   int // -1 = use StaticURL

	// Payload slots.
	BodySlot     int // -1 = send empty message
	ResponseSlot int // -1 = discard response body
	StatusSlot   int // -1 = discard HTTP status string

	// Per-call behaviour.
	TimeoutMs    int
	Compress     bool // enable gzip compression on the request
	WaitForReady bool // wait for connection to be ready before sending
	MaxRetries   int  // 0 = no retry
	RetryOnCodes []codes.Code

	// Header / metadata forwarding.
	ForwardHeaders  bool
	BlockHeadersMap map[string]struct{} // pre-built lowercase set

	// Static and slot-driven metadata to inject into every call.
	MetadataLiterals []MetadataLiteral
	MetadataSlots    []MetadataSlotBind

	// Response metadata capture (trailing and initial headers).
	RespHeaderSlots  []grpcutil.MetadataSlotBinding
	RespTrailerSlots []grpcutil.MetadataSlotBinding

	// EgressProfile governs TLS and keepalive for the connection.
	// nil = infer from URL scheme (grpc:// = insecure, grpcs:// = system CA TLS).
	EgressProfile *egress.EgressProfile

	// TLSClientCert and TLSClientKey are PEM-encoded bytes resolved at bake time.
	// Both must be set together to enable mTLS. nil = no client certificate.
	// These are consumed by NewGrpcCallInstruction to build a runtime-ready
	// tlsClientCert that is stored in the closure — zero per-request cost.
	TLSClientCert []byte
	TLSClientKey  []byte
}

// NewGrpcCallInstruction wraps a GrpcCallConfig as an engine.Instruction.
//
// If cfg.TLSClientCert and cfg.TLSClientKey are both non-nil the key pair is
// parsed once here (bake time).  A synthetic EgressProfile carrying the cert
// is stored in the closure and passed to the connection pool on each call — the
// pool deduplicates connections by (addr, profileID, useTLS) so the mTLS
// connection is kept alive and reused across requests at zero extra cost.
func NewGrpcCallInstruction(cfg GrpcCallConfig) engine.Instruction {
	// ── Parse mTLS client certificate at bake time ────────────────────────────
	if len(cfg.TLSClientCert) > 0 && len(cfg.TLSClientKey) > 0 {
		cert, err := tls.X509KeyPair(cfg.TLSClientCert, cfg.TLSClientKey)
		if err != nil {
			// Return a fail-fast instruction so operators see the error immediately.
			return engine.Instruction{
				Name: "grpc_call",
				Action: func(ctx *rctx.Context, _ *engine.ExecutionState) int16 {
					return cfg.failWith(ctx, codes.Internal, "grpc_call: invalid tls_client_cert/key: "+err.Error())
				},
			}
		}
		// Build a synthetic EgressProfile that carries the client cert.
		// ID=0 is reserved for "no profile"; use 255 as a sentinel for mTLS-only
		// calls.  If a real EgressProfile is already set, clone its TLSConfig and
		// add the cert rather than replacing it.
		var baseTLS *tls.Config
		if cfg.EgressProfile != nil && cfg.EgressProfile.TLSConfig != nil {
			baseTLS = cfg.EgressProfile.TLSConfig.Clone()
		} else {
			baseTLS = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		baseTLS.Certificates = append(baseTLS.Certificates, cert)

		var baseProfile egress.EgressProfile
		if cfg.EgressProfile != nil {
			baseProfile = *cfg.EgressProfile
		}
		baseProfile.TLSConfig = baseTLS
		// Use a stable non-zero ID so the pool creates a distinct connection.
		// 255 is safe: real profiles use IDs assigned by the EgressManager (1-254).
		if baseProfile.ID == 0 {
			baseProfile.ID = 255
		}
		cfg.EgressProfile = &baseProfile
		// Clear raw PEM bytes — no longer needed.
		cfg.TLSClientCert = nil
		cfg.TLSClientKey = nil
	}

	return engine.Instruction{
		Name: "grpc_call",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			return cfg.Execute(ctx, state)
		},
	}
}

// Execute is the hot-path implementation of a grpc_call step.
// It is exported so the compiler closure can call it directly without reflection.
func (g *GrpcCallConfig) Execute(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	// ── Pre-flight: client disconnect check ───────────────────────────────────
	if pc, stop := StopIfCancelled(ctx); stop {
		return pc
	}

	// ── 1. Resolve URL ────────────────────────────────────────────────────────
	rawURL := g.StaticURL
	if g.URLSlot >= 0 && g.URLSlot < len(ctx.ByteSlots) && len(ctx.ByteSlots[g.URLSlot]) > 0 {
		rawURL = string(ctx.ByteSlots[g.URLSlot])
	}
	if rawURL == "" {
		return g.failWith(ctx, codes.Internal, "grpc_call: no URL configured")
	}

	// ── 2. Get connection from pool ───────────────────────────────────────────
	pool := GlobalGrpcConnPool
	if pool == nil {
		return g.failWith(ctx, codes.Internal, "grpc_call: connection pool not initialized")
	}
	conn, err := pool.Get(rawURL, g.EgressProfile)
	if err != nil {
		return g.failWith(ctx, codes.Unavailable, "grpc_call: "+err.Error())
	}

	// ── 3. Build call context ─────────────────────────────────────────────────
	callCtx := ctx.Request.Context()
	cancel := func() {}
	if g.TimeoutMs > 0 {
		callCtx, cancel = context.WithTimeout(callCtx, time.Duration(g.TimeoutMs)*time.Millisecond)
	}
	defer cancel()

	// ── 4. Build gRPC outgoing metadata ───────────────────────────────────────
	md := metadata.MD{}
	if g.ForwardHeaders && ctx.Request != nil {
		md = grpcutil.HeadersToMetadata(ctx.Request.Header, g.BlockHeadersMap)
	}
	for _, lit := range g.MetadataLiterals {
		md[lit.Key] = append(md[lit.Key], lit.Value)
	}
	for _, bind := range g.MetadataSlots {
		if bind.Slot >= 0 && bind.Slot < len(ctx.ByteSlots) && len(ctx.ByteSlots[bind.Slot]) > 0 {
			md[bind.Key] = append(md[bind.Key], string(ctx.ByteSlots[bind.Slot]))
		}
	}
	if len(md) > 0 {
		callCtx = metadata.NewOutgoingContext(callCtx, md)
	}

	// ── 5. Transcode JSON → proto request ─────────────────────────────────────
	if g.MethodDesc == nil {
		return g.failWith(ctx, codes.Internal, "grpc_call: method descriptor not resolved (check descriptor_set name)")
	}
	var bodyJSON []byte
	if g.BodySlot >= 0 && g.BodySlot < len(ctx.ByteSlots) {
		bodyJSON = ctx.ByteSlots[g.BodySlot]
	}
	reqMsg, err := grpcutil.JSONToProto(g.MethodDesc.Input(), bodyJSON)
	if err != nil {
		return g.failWith(ctx, codes.InvalidArgument, "grpc_call: "+err.Error())
	}

	// ── 6. Build call options ─────────────────────────────────────────────────
	baseOpts := make([]grpc.CallOption, 0, 4)
	if g.WaitForReady {
		baseOpts = append(baseOpts, grpc.WaitForReady(true))
	}
	if g.Compress {
		baseOpts = append(baseOpts, grpc.UseCompressor("gzip"))
	}

	// ── 7. Retry loop ─────────────────────────────────────────────────────────
	maxAttempts := g.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if pc, stop := StopIfCancelled(ctx); stop {
				return pc
			}
			// Exponential backoff: 50ms, 100ms, 200ms … capped at 500ms.
			backoff := time.Duration(50<<uint(attempt-2)) * time.Millisecond
			if backoff > 500*time.Millisecond {
				backoff = 500 * time.Millisecond
			}
			time.Sleep(backoff)
		}

		// Allocate a fresh response message for each attempt.
		respMsg := dynamicpb.NewMessage(g.MethodDesc.Output())

		var respHeader, respTrailer metadata.MD
		callOpts := append(baseOpts, //nolint:gocritic
			grpc.Header(&respHeader),
			grpc.Trailer(&respTrailer),
		)

		upstreamStart := time.Now()
		invokeErr := conn.Invoke(callCtx, g.FullMethod, reqMsg, respMsg, callOpts...)
		upstreamDur := time.Since(upstreamStart)

		atomic.AddInt64(&ctx.Timing.UpstreamTimeNs, int64(upstreamDur))
		atomic.AddInt32(&ctx.Timing.UpstreamCalls, 1)

		if invokeErr == nil {
			// ── Success ───────────────────────────────────────────────────────
			jsonOut, jerr := grpcutil.ProtoToJSON(respMsg)
			if jerr != nil {
				return g.failWith(ctx, codes.Internal, "grpc_call: response transcoding: "+jerr.Error())
			}
			if g.ResponseSlot >= 0 && g.ResponseSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[g.ResponseSlot] = jsonOut
			}
			if g.StatusSlot >= 0 && g.StatusSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[g.StatusSlot] = []byte("200")
			}
			// Capture response metadata into slots.
			if len(g.RespHeaderSlots) > 0 {
				grpcutil.MetadataToSlots(respHeader, g.RespHeaderSlots, ctx.ByteSlots)
			}
			if len(g.RespTrailerSlots) > 0 {
				grpcutil.MetadataToSlots(respTrailer, g.RespTrailerSlots, ctx.ByteSlots)
			}
			return state.PC + 1
		}

		lastErr = invokeErr

		// ── Client disconnected ───────────────────────────────────────────────
		if invokeErr == context.Canceled || status.Code(invokeErr) == codes.Canceled {
			atomic.StoreInt32(&ctx.Cancelled, 1)
			return engine.StopCancelled
		}

		// ── Check retryability ────────────────────────────────────────────────
		if attempt < maxAttempts && len(g.RetryOnCodes) > 0 {
			code := status.Code(invokeErr)
			retryable := false
			for _, rc := range g.RetryOnCodes {
				if rc == code {
					retryable = true
					break
				}
			}
			if !retryable {
				break
			}
		} else if attempt < maxAttempts && len(g.RetryOnCodes) == 0 {
			// No explicit retry codes — stop on first failure.
			break
		}
	}

	// ── All attempts exhausted — write error response ─────────────────────────
	httpStatus := grpcutil.GRPCStatusToHTTP(status.Code(lastErr))
	errJSON := grpcutil.ErrorToJSON(lastErr)
	if g.ResponseSlot >= 0 && g.ResponseSlot < len(ctx.ByteSlots) {
		ctx.ByteSlots[g.ResponseSlot] = errJSON
	}
	if g.StatusSlot >= 0 && g.StatusSlot < len(ctx.ByteSlots) {
		ctx.ByteSlots[g.StatusSlot] = []byte(strconv.Itoa(httpStatus))
	}
	ctx.ResponseStatus = httpStatus
	ctx.Failed = true
	return engine.StopPlan
}

// failWith is a helper for bake-time or pre-call failures where we know the
// gRPC code and message without an actual gRPC status error.
func (g *GrpcCallConfig) failWith(ctx *rctx.Context, code codes.Code, msg string) int16 {
	httpStatus := grpcutil.GRPCStatusToHTTP(code)
	s := status.New(code, msg)
	errJSON := grpcutil.ErrorToJSON(s.Err())
	if g.ResponseSlot >= 0 && g.ResponseSlot < len(ctx.ByteSlots) {
		ctx.ByteSlots[g.ResponseSlot] = errJSON
	}
	if g.StatusSlot >= 0 && g.StatusSlot < len(ctx.ByteSlots) {
		ctx.ByteSlots[g.StatusSlot] = []byte(strconv.Itoa(httpStatus))
	}
	ctx.ResponseStatus = httpStatus
	ctx.Failed = true
	return engine.StopPlan
}

// ParseGRPCCode converts a case-insensitive status code name to codes.Code.
// Returns codes.Unknown for unrecognised names.
func ParseGRPCCode(s string) codes.Code {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "OK":
		return codes.OK
	case "CANCELLED", "CANCELED":
		return codes.Canceled
	case "UNKNOWN":
		return codes.Unknown
	case "INVALID_ARGUMENT":
		return codes.InvalidArgument
	case "DEADLINE_EXCEEDED":
		return codes.DeadlineExceeded
	case "NOT_FOUND":
		return codes.NotFound
	case "ALREADY_EXISTS":
		return codes.AlreadyExists
	case "PERMISSION_DENIED":
		return codes.PermissionDenied
	case "RESOURCE_EXHAUSTED":
		return codes.ResourceExhausted
	case "FAILED_PRECONDITION":
		return codes.FailedPrecondition
	case "ABORTED":
		return codes.Aborted
	case "OUT_OF_RANGE":
		return codes.OutOfRange
	case "UNIMPLEMENTED":
		return codes.Unimplemented
	case "INTERNAL":
		return codes.Internal
	case "UNAVAILABLE":
		return codes.Unavailable
	case "DATA_LOSS":
		return codes.DataLoss
	case "UNAUTHENTICATED":
		return codes.Unauthenticated
	default:
		return codes.Unknown
	}
}
