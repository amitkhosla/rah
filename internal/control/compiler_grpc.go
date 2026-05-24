package control

import (
	"fmt"
	"strings"

	"rah/internal/engine"
	"rah/internal/engine/steps"
	"rah/internal/rctx"
)

// compileGrpcCall compiles a "grpc_call" step into a GrpcCallInstruction.
//
// Key bake-time work:
//   - Parses all step.Input fields into GrpcCallConfig.
//   - Resolves the MethodDescriptor via GrpcRegistry (zero runtime lookup).
//   - Pre-computes FullMethod string ("/pkg.Service/Method").
//   - Pre-builds BlockHeadersMap from comma-separated input.
func (c *Compiler) compileGrpcCall(step StepConfig) error {
	cfg := steps.GrpcCallConfig{
		DescriptorSetName: strings.TrimSpace(step.Input["descriptor_set"]),
		ServiceName:       strings.TrimSpace(step.Input["service"]),
		MethodName:        strings.TrimSpace(step.Input["method"]),
		StaticURL:         strings.TrimSpace(step.Input["static_url"]),
		URLSlot:           grpcSlotFromInput(step.Input, "url_slot"),
		BodySlot:          grpcSlotFromInput(step.Input, "body_slot"),
		ResponseSlot:      grpcSlotFromInput(step.Input, "response_slot"),
		StatusSlot:        grpcSlotFromInput(step.Input, "status_slot"),
		TimeoutMs:         grpcIntFromInput(step.Input, "timeout_ms", 0),
		Compress:          grpcBoolFromInput(step.Input, "compress"),
		WaitForReady:      grpcBoolFromInput(step.Input, "wait_for_ready"),
		ForwardHeaders:    grpcBoolFromInput(step.Input, "forward_headers"),
		MaxRetries:        grpcIntFromInput(step.Input, "max_retries", 0),
	}

	// Pre-compute the gRPC full method path.
	cfg.FullMethod = "/" + cfg.ServiceName + "/" + cfg.MethodName

	// Validate required fields.
	if cfg.ServiceName == "" {
		return fmt.Errorf("grpc_call: missing required field 'service'")
	}
	if cfg.MethodName == "" {
		return fmt.Errorf("grpc_call: missing required field 'method'")
	}
	if cfg.StaticURL == "" && cfg.URLSlot < 0 {
		return fmt.Errorf("grpc_call: one of 'static_url' or 'url_slot' is required")
	}

	// Pre-build block_headers map (lowercase, comma-separated).
	if bh := strings.TrimSpace(step.Input["block_headers"]); bh != "" {
		cfg.BlockHeadersMap = make(map[string]struct{})
		for _, h := range strings.Split(bh, ",") {
			if t := strings.ToLower(strings.TrimSpace(h)); t != "" {
				cfg.BlockHeadersMap[t] = struct{}{}
			}
		}
	}

	// Parse retry_on gRPC status code names.
	if ro := strings.TrimSpace(step.Input["retry_on"]); ro != "" {
		for _, codeStr := range strings.Split(ro, ",") {
			codeStr = strings.TrimSpace(codeStr)
			if codeStr == "" {
				continue
			}
			cfg.RetryOnCodes = append(cfg.RetryOnCodes, steps.ParseGRPCCode(codeStr))
		}
	}

	// Parse static metadata literals: metadata_0="key=value", metadata_1="key=value", …
	for i := 0; ; i++ {
		raw := strings.TrimSpace(step.Input[fmt.Sprintf("metadata_%d", i)])
		if raw == "" {
			break
		}
		eqIdx := strings.IndexByte(raw, '=')
		if eqIdx < 0 {
			return fmt.Errorf("grpc_call: metadata_%d %q must be 'key=value'", i, raw)
		}
		cfg.MetadataLiterals = append(cfg.MetadataLiterals, steps.MetadataLiteral{
			Key:   strings.ToLower(strings.TrimSpace(raw[:eqIdx])),
			Value: raw[eqIdx+1:],
		})
	}

	// Parse metadata_slot_N="key" (value from ByteSlots[N]).
	for i := 0; i < 16; i++ {
		key := strings.TrimSpace(step.Input[fmt.Sprintf("metadata_slot_%d", i)])
		if key == "" {
			continue
		}
		cfg.MetadataSlots = append(cfg.MetadataSlots, steps.MetadataSlotBind{
			Key:  strings.ToLower(key),
			Slot: i,
		})
	}

	// Bake-time gRPC method descriptor resolution.
	registry := c.GrpcRegistry
	if registry == nil {
		registry = steps.GlobalGrpcRegistry
	}
	if registry != nil && cfg.DescriptorSetName != "" {
		md, err := registry.FindMethod(cfg.DescriptorSetName, cfg.ServiceName, cfg.MethodName)
		if err != nil {
			return fmt.Errorf("grpc_call: %w", err)
		}
		cfg.MethodDesc = md
	}
	// If no registry or no descriptor_set: MethodDesc remains nil.
	// Execute() will return codes.Internal at runtime with a clear message.

	// Egress profile resolution: look up by profile name if EgressMgr is available.
	// Wired fully in S10 (main.go). For now, profile stays nil if not resolved.
	// URL-scheme-based TLS selection in pool.go handles the common case.

	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "grpc_call",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			return cfg.Execute(ctx, state)
		},
	})
	return nil
}

// grpcSlotFromInput parses a slot index from a step.Input string.
// Returns -1 if the key is absent, empty, or non-numeric.
func grpcSlotFromInput(input map[string]string, key string) int {
	raw := strings.TrimSpace(input[key])
	if raw == "" {
		return -1
	}
	n := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return -1
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

// grpcIntFromInput parses an integer from a step.Input string.
// Returns fallback if absent or non-numeric.
func grpcIntFromInput(input map[string]string, key string, fallback int) int {
	raw := strings.TrimSpace(input[key])
	if raw == "" {
		return fallback
	}
	neg := false
	if len(raw) > 0 && raw[0] == '-' {
		neg = true
		raw = raw[1:]
	}
	n := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return fallback
		}
		n = n*10 + int(ch-'0')
	}
	if neg {
		return -n
	}
	return n
}

// grpcBoolFromInput parses a boolean from a step.Input string.
func grpcBoolFromInput(input map[string]string, key string) bool {
	switch strings.ToLower(strings.TrimSpace(input[key])) {
	case "true", "1", "yes":
		return true
	}
	return false
}

