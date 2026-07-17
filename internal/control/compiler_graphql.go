package control

import (
	"fmt"

	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileGraphQLCall handles the "graphql_call" step type.
//
// Step input keys:
//
//	url              â€” static upstream GraphQL endpoint URL
//	url_slot         â€” var name whose slot holds the dynamic URL (use if URL is dynamic)
//	query            â€” static GraphQL query string (baked into StaticPrefix at compile time)
//	variables_slot   â€” var name whose slot holds the JSON variables object (optional)
//	data_slot        â€” var name whose slot will store the response "data" field
//	errors_slot      â€” var name whose slot will store the response "errors" field
//	timeout_ms       â€” request timeout in milliseconds (default: 10000)
//	fail_on_errors   â€” "true" to set ctx.Failed when errors are present in the response
//
// At bake time, the query string is JSON-escaped and baked into StaticPrefix.
// The instruction POSTs {"query":"<query>","variables":<vars>} to the upstream endpoint.
func (c *Compiler) compileGraphQLCall(step StepConfig) error {
	// Resolve URL (static or dynamic slot)
	staticURL := step.Input["url"]
	urlSlotStr := step.Input["url_slot"]
	urlSlot := -1
	if urlSlotStr != "" {
		slot, err := c.getSlot(urlSlotStr)
		if err != nil {
			return fmt.Errorf("graphql_call url_slot: %w", err)
		}
		urlSlot = slot
	}

	if staticURL == "" && urlSlot < 0 {
		return fmt.Errorf("graphql_call: either 'url' or 'url_slot' must be set")
	}

	// Build the StaticPrefix: {"query":"<escaped-query>","variables":
	queryStr := step.Input["query"]
	prefix := append([]byte(`{"query":"`), steps.AppendJSONEscapedString(nil, queryStr)...)
	prefix = append(prefix, `","variables":`...)

	// Resolve optional variables slot
	variablesSlot := -1
	if varSlotStr := step.Input["variables_slot"]; varSlotStr != "" {
		slot, err := c.getSlot(varSlotStr)
		if err != nil {
			return fmt.Errorf("graphql_call variables_slot: %w", err)
		}
		variablesSlot = slot
	}

	// Resolve optional data slot
	dataSlot := -1
	if dataSlotStr := step.Input["data_slot"]; dataSlotStr != "" {
		slot, err := c.getSlot(dataSlotStr)
		if err != nil {
			return fmt.Errorf("graphql_call data_slot: %w", err)
		}
		dataSlot = slot
	}

	// Resolve optional errors slot
	errorsSlot := -1
	if errorsSlotStr := step.Input["errors_slot"]; errorsSlotStr != "" {
		slot, err := c.getSlot(errorsSlotStr)
		if err != nil {
			return fmt.Errorf("graphql_call errors_slot: %w", err)
		}
		errorsSlot = slot
	}

	// Parse timeout (default 10000 ms)
	timeoutMs := 10000
	if timeoutStr := step.Input["timeout_ms"]; timeoutStr != "" {
		_, err := fmt.Sscanf(timeoutStr, "%d", &timeoutMs)
		if err != nil {
			return fmt.Errorf("graphql_call: invalid timeout_ms %q: %w", timeoutStr, err)
		}
	}

	// Parse fail_on_errors flag
	failOnErrors := step.Input["fail_on_errors"] == "true"

	c.GlobalTable = append(c.GlobalTable, steps.GraphQLCallFromConfig(steps.GraphQLCallConfig{
		StaticURL:     staticURL,
		URLSlot:       urlSlot,
		StaticPrefix:  prefix,
		StaticSuffix:  []byte("}"),
		QuerySlot:     -1, // query is baked into StaticPrefix
		VariablesSlot: variablesSlot,
		DataSlot:      dataSlot,
		ErrorsSlot:    errorsSlot,
		TimeoutMs:     timeoutMs,
		FailOnErrors:  failOnErrors,
	}))

	return nil
}

// compileGraphQLGet handles the "graphql_get" step type.
//
// Step input keys (via StepConfig fields):
//
//	step.Source       â€” var name whose slot holds the GraphQL response "data" JSON
//	step.Input["path"] â€” static gjson path to extract from the data JSON
//	step.As           â€” var name whose slot will store the extracted value
func (c *Compiler) compileGraphQLGet(step StepConfig) error {
	if step.Source == "" {
		return fmt.Errorf("graphql_get: 'source' is required")
	}
	dataSlot, err := c.getSlot(step.Source)
	if err != nil {
		return fmt.Errorf("graphql_get source: %w", err)
	}

	staticPath := step.Input["path"]
	if staticPath == "" {
		return fmt.Errorf("graphql_get: 'path' (in input) is required")
	}

	if step.As == "" {
		return fmt.Errorf("graphql_get: 'as' (destination slot) is required")
	}
	destSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("graphql_get as: %w", err)
	}

	c.GlobalTable = append(c.GlobalTable, steps.GraphQLGetFromConfig(steps.GraphQLGetConfig{
		DataSlot:   dataSlot,
		StaticPath: staticPath,
		DestSlot:   destSlot,
	}))

	return nil
}

// compileParseGraphQLError handles the "parse_graphql_error" step type.
//
// Step input keys (via StepConfig fields):
//
//	step.Source â€” var name whose slot holds the GraphQL errors array JSON
//	step.As     â€” var name whose slot will store the extracted error message string
func (c *Compiler) compileParseGraphQLError(step StepConfig) error {
	if step.Source == "" {
		return fmt.Errorf("parse_graphql_error: 'source' is required")
	}
	sourceSlot, err := c.getSlot(step.Source)
	if err != nil {
		return fmt.Errorf("parse_graphql_error source: %w", err)
	}

	if step.As == "" {
		return fmt.Errorf("parse_graphql_error: 'as' (destination slot) is required")
	}
	destSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("parse_graphql_error as: %w", err)
	}

	c.GlobalTable = append(c.GlobalTable, steps.ParseGraphQLError(sourceSlot, destSlot))

	return nil
}
