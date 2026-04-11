package control

// FlowProfile describes what a compiled flow does — emitted by the compiler
// alongside the instruction table. Used by the test executor and Studio UI
// to show customers expected cache/registry usage and minimum latency.
type FlowProfile struct {
	FlowName              string   // name of the flow
	CacheReadPatterns     []string // cache keys/patterns this flow reads (variable names)
	CacheWritePatterns    []string // cache keys/patterns this flow writes
	RegistryReadKeys      []string // registry property names this flow reads
	RegistryWriteKeys     []string // registry property names this flow writes
	ExternalStepNames     []string // names of external call steps (llm_call, http_request, etc.)
	HasLLMCalls           bool     // true if flow contains any llm_call steps
	HasHTTPCalls          bool     // true if flow contains any http_request steps
	EstimatedMinLatencyNs int64    // sum of estimated baseline latency for each step type
	StepCount             int      // total number of steps in the flow
}

// stepBaseLatencyNs returns a rough baseline latency estimate for a step type.
// These are best-effort estimates for pre-release guidance, not guarantees.
func stepBaseLatencyNs(stepType string) int64 {
	switch stepType {
	case "cache_get", "cache_put":
		return 500 // 500ns cache op
	case "registry_get", "load_service_url", "load_identifier":
		return 100 // 100ns registry read
	case "llm_call":
		return 50_000_000 // 50ms minimum (network + inference)
	case "http_call":
		return 1_000_000 // 1ms minimum (network)
	case "set_variable", "bind_header", "bind_query_param":
		return 50 // 50ns assignment
	case "check_rate_limit":
		return 200 // 200ns
	default:
		return 200 // 200ns generic
	}
}

// buildFlowProfile iterates over the steps of a flow and constructs a FlowProfile
// describing cache/registry access patterns, external calls, and estimated latency.
func buildFlowProfile(flowName string, steps []StepConfig) FlowProfile {
	p := FlowProfile{
		FlowName: flowName,
	}

	for _, step := range steps {
		p.StepCount++
		p.EstimatedMinLatencyNs += stepBaseLatencyNs(step.Action)

		switch step.Action {
		case "llm_call", "route_llm":
			p.HasLLMCalls = true
			name := step.As
			if name == "" {
				name = step.Action
			}
			p.ExternalStepNames = append(p.ExternalStepNames, name)

		case "http_call":
			p.HasHTTPCalls = true
			name := step.As
			if name == "" {
				name = step.Action
			}
			p.ExternalStepNames = append(p.ExternalStepNames, name)

		case "cache_get":
			key := step.Key
			if key == "" {
				key = step.Variable
			}
			if key == "" {
				key = step.As
			}
			if key != "" {
				p.CacheReadPatterns = append(p.CacheReadPatterns, key)
			}

		case "cache_put":
			key := step.Key
			if key == "" {
				key = step.Variable
			}
			if key == "" {
				key = step.As
			}
			if key != "" {
				p.CacheWritePatterns = append(p.CacheWritePatterns, key)
			}

		case "load_service_url", "load_identifier", "registry_get":
			if step.Key != "" {
				p.RegistryReadKeys = append(p.RegistryReadKeys, step.Key)
			}

		case "set_service_url", "set_identifier", "set_meta":
			if step.Key != "" {
				p.RegistryWriteKeys = append(p.RegistryWriteKeys, step.Key)
			}
		}

		// Recurse into inline sub-flows (foreach, if/else do blocks)
		if len(step.Do) > 0 {
			sub := buildFlowProfile(flowName, step.Do)
			p.StepCount += sub.StepCount
			p.EstimatedMinLatencyNs += sub.EstimatedMinLatencyNs
			if sub.HasLLMCalls {
				p.HasLLMCalls = true
			}
			if sub.HasHTTPCalls {
				p.HasHTTPCalls = true
			}
			p.ExternalStepNames = append(p.ExternalStepNames, sub.ExternalStepNames...)
			p.CacheReadPatterns = append(p.CacheReadPatterns, sub.CacheReadPatterns...)
			p.CacheWritePatterns = append(p.CacheWritePatterns, sub.CacheWritePatterns...)
			p.RegistryReadKeys = append(p.RegistryReadKeys, sub.RegistryReadKeys...)
			p.RegistryWriteKeys = append(p.RegistryWriteKeys, sub.RegistryWriteKeys...)
		}
	}

	return p
}
