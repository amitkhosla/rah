package control

// CircuitControlStepDescriptors returns step descriptors for the circuit control group:
// trip_circuit, check_circuit, reset_circuit.
//
// These steps provide direct control over named circuit breakers:
//
//	trip_circuit   → immediately open a circuit (reject requests)
//	check_circuit  → query the current state of a circuit
//	reset_circuit  → force-close a circuit and clear counters
func CircuitControlStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "trip_circuit",
			Title:       "Trip Circuit",
			Description: "Immediately open a named circuit breaker, transitioning it to Open state and rejecting requests for the specified duration. If the circuit does not exist, it is created with default thresholds and the given override duration.",
			Category:    "resilience",
			Capability:  "circuit-control",
			Defaults: map[string]string{
				"name":   "",
				"for_ms": "60000",
			},
			Fields: []StepField{
				sf("name", "Circuit name", "Name of the circuit breaker to trip (required; uses key_identifier if name is empty)", "payment_api"),
				sf("for_ms", "Duration (ms)", "How long to hold the circuit open in milliseconds (default: 60000)", "60000"),
			},
		},
		{
			Type:        "check_circuit",
			Title:       "Check Circuit",
			Description: "Query the current state of a named circuit breaker and write the result to a slot. Returns one of: 'closed' (normal operation), 'open' (tripped, requests blocked), or 'half_open' (recovery probe phase). Unknown circuits are treated as 'closed'.",
			Category:    "resilience",
			Capability:  "circuit-control",
			Defaults: map[string]string{
				"name": "",
				"as":   "circuit_state",
			},
			Fields: []StepField{
				sf("name", "Circuit name", "Name of the circuit breaker to check (required; uses key_identifier if name is empty)", "payment_api"),
				sf("as", "Store state in", "ByteSlot to write the state string into ('closed', 'open', or 'half_open')", "circuit_state"),
			},
		},
		{
			Type:        "reset_circuit",
			Title:       "Reset Circuit",
			Description: "Force-close a named circuit breaker and reset all counters (failure and success counts). The circuit transitions to Closed state. If the circuit does not exist, the step succeeds as a no-op.",
			Category:    "resilience",
			Capability:  "circuit-control",
			Defaults: map[string]string{
				"name": "",
			},
			Fields: []StepField{
				sf("name", "Circuit name", "Name of the circuit breaker to reset (required; uses key_identifier if name is empty)", "payment_api"),
			},
		},
	}
}
