// Command rah-schema generates a JSON Schema (draft-07) for the RAH unified sync bundle format.
//
// Usage:
//   go run ./cmd/rah-schema
//   go run ./cmd/rah-schema bundle-schema.json    # output to specific file
//   ./rah-schema > my-schema.json                 # redirect to file
//
// The generated schema validates UnifiedSyncRequest structures containing:
//   - flows: array of flow definitions with instructions
//   - apis: array of API route definitions
//   - rate_limit_configs_v2: array of rate limit configurations
//
// Each step instruction includes:
//   - action: enum of all known step types (registry_lookup, http_call, etc.)
//   - input: object with step-specific parameters (validated against step descriptor fields)
//
// Field type mapping:
//   - Keys containing "timeout", "ttl", "max", "count", "retries", "port" → integer
//   - Keys containing "enabled", "require", "generate", "skip", "include", "forward", "match" → boolean
//   - All other fields → string
//
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"rah/internal/control"
	"rah/internal/sync"
)

// JSONSchema represents a JSON Schema draft-07 document
type JSONSchema struct {
	Schema              string                 `json:"$schema"`
	Type                string                 `json:"type"`
	Title               string                 `json:"title"`
	Description         string                 `json:"description"`
	AdditionalProperties bool                  `json:"additionalProperties"`
	Properties          map[string]interface{} `json:"properties"`
	Required            []string               `json:"required"`
}

// SchemaProperty represents a single property definition
type SchemaProperty struct {
	Type                 string                 `json:"type"`
	Title                string                 `json:"title,omitempty"`
	Description          string                 `json:"description,omitempty"`
	Items                interface{}            `json:"items,omitempty"`
	Properties           map[string]interface{} `json:"properties,omitempty"`
	Enum                 []string               `json:"enum,omitempty"`
	Default              interface{}            `json:"default,omitempty"`
	Required             []string               `json:"required,omitempty"`
	AdditionalProperties interface{}            `json:"additionalProperties,omitempty"`
}

func main() {
	flag.Parse()
	args := flag.Args()
	outputPath := "bundle-schema.json"
	if len(args) > 0 {
		outputPath = args[0]
	}

	schema := generateSchema()

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling schema: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing schema: %v\n", err)
		os.Exit(1)
	}

	absPath, _ := filepath.Abs(outputPath)
	fmt.Println(absPath)
}

func generateSchema() JSONSchema {
	allSteps := control.AllStepDescriptors()

	// Build step action enum and input schemas
	stepActions := make([]string, len(allSteps))
	stepInputSchemas := make(map[string]interface{})

	for i, step := range allSteps {
		stepActions[i] = step.Type

		// Get user-facing fields (excludes _slot suffixed fields)
		fields := sync.UserFacingFields(step)

		// Build properties for this step's input
		inputProps := make(map[string]interface{})
		for _, field := range fields {
			fieldSchema := buildFieldSchema(field)
			inputProps[field.Key] = fieldSchema
		}

		// Step input schema
		stepInputSchemas[step.Type] = SchemaProperty{
			Type:                 "object",
			AdditionalProperties: false,
			Properties:           inputProps,
		}
	}

	// Sort step actions for consistent output
	sort.Strings(stepActions)

	// Step schema: action + common fields
	stepProps := map[string]interface{}{
		"action": SchemaProperty{
			Type:        "string",
			Enum:        stepActions,
			Description: "Instruction type",
		},
		// Common fields from StepConfig
		"key": SchemaProperty{Type: "string"},
		"as": SchemaProperty{Type: "string"},
		"flow_name": SchemaProperty{Type: "string"},
		"url": SchemaProperty{Type: "string"},
		"url_var": SchemaProperty{Type: "string"},
		"method": SchemaProperty{Type: "string"},
		"timeout": SchemaProperty{Type: "integer"},
		"retry_condition": SchemaProperty{Type: "string"},
		"max_retries": SchemaProperty{Type: "integer"},
		"key_identifier": SchemaProperty{Type: "string"},
		"input": SchemaProperty{Type: "object"},
		"body_var": SchemaProperty{Type: "string"},
		"content_type": SchemaProperty{Type: "string"},
		"response_body_var": SchemaProperty{Type: "string"},
		"response_status_var": SchemaProperty{Type: "string"},
		"response_header_vars": SchemaProperty{Type: "object"},
		"forward_incoming_headers": SchemaProperty{Type: "boolean"},
		"forward_response_headers": SchemaProperty{Type: "boolean"},
		"block_headers": SchemaProperty{Type: "array"},
		"condition": SchemaProperty{Type: "string"},
		"then": SchemaProperty{Type: "string"},
		"else": SchemaProperty{Type: "string"},
		"cases": SchemaProperty{Type: "object"},
		"do": SchemaProperty{Type: "array"},
		"source": SchemaProperty{Type: "string"},
		"scope": SchemaProperty{Type: "string"},
		"value": SchemaProperty{Type: "string"},
		"path": SchemaProperty{Type: "string"},
		"ttl": SchemaProperty{Type: "integer"},
		"on_miss": SchemaProperty{Type: "string"},
		"delta": SchemaProperty{Type: "integer"},
		"branches": SchemaProperty{Type: "array"},
		"timeout_ms": SchemaProperty{Type: "integer"},
		"error_policy": SchemaProperty{Type: "string"},
		"generate_if_missing": SchemaProperty{Type: "boolean"},
		"tls_client_cert_ref": SchemaProperty{Type: "string"},
		"tls_client_key_ref": SchemaProperty{Type: "string"},
		"include_query": SchemaProperty{Type: "boolean"},
		"variable": SchemaProperty{Type: "string"},
		"params": SchemaProperty{Type: "array"},
		"destination": SchemaProperty{Type: "string"},
		"log_as": SchemaProperty{Type: "string"},
		"trace_capture": SchemaProperty{Type: "boolean"},
		"trace_vars": SchemaProperty{Type: "array"},
		"rules": SchemaProperty{Type: "array"},
		"on_error": SchemaProperty{Type: "string"},
		"on_error_status": SchemaProperty{Type: "integer"},
		"on_error_body": SchemaProperty{Type: "string"},
	}

	stepSchema := SchemaProperty{
		Type:       "object",
		Required:   []string{"action"},
		Properties: stepProps,
	}

	// Flow update schema
	flowSchema := SchemaProperty{
		Type:     "object",
		Required: []string{"name", "instructions"},
		Properties: map[string]interface{}{
			"name": SchemaProperty{
				Type:        "string",
				Description: "Unique flow name",
			},
			"instructions": SchemaProperty{
				Type:        "array",
				Description: "Sequence of steps in this flow",
				Items:       stepSchema,
			},
			"action": SchemaProperty{
				Type:        "string",
				Enum:        []string{"upsert", "delete"},
				Description: "Action type: upsert or delete",
			},
		},
	}

	// API update schema
	apiSchema := SchemaProperty{
		Type:     "object",
		Required: []string{"name", "path", "flow_name"},
		Properties: map[string]interface{}{
			"name": SchemaProperty{
				Type:        "string",
				Description: "API definition name",
			},
			"path": SchemaProperty{
				Type:        "string",
				Description: "URL path pattern",
			},
			"method": SchemaProperty{
				Type:        "string",
				Description: "HTTP method (GET, POST, etc.) or empty for all methods",
			},
			"flow_name": SchemaProperty{
				Type:        "string",
				Description: "Reference to a flow name",
			},
			"rate_limit": SchemaProperty{
				Type:        "string",
				Description: "Rate limit configuration name",
			},
			"action": SchemaProperty{
				Type:        "string",
				Enum:        []string{"upsert", "delete"},
				Description: "Action type: upsert or delete",
			},
			"alias_paths": SchemaProperty{
				Type:        "array",
				Description: "Additional base paths that map to this API",
				Items:       SchemaProperty{Type: "string"},
			},
		},
	}

	// Rate limit config schema (simplified)
	rateLimitSchema := SchemaProperty{
		Type:     "object",
		Required: []string{"name"},
		Properties: map[string]interface{}{
			"name": SchemaProperty{
				Type:        "string",
				Description: "Rate limit configuration name",
			},
			"type": SchemaProperty{
				Type:        "string",
				Description: "Rate limit type",
			},
		},
	}

	// Top-level schema
	return JSONSchema{
		Schema:               "http://json-schema.org/draft-07/schema#",
		Type:                 "object",
		Title:                "RAH Bundle Schema",
		Description:          "JSON Schema for RAH unified sync bundle format (flows, APIs, rate limits)",
		AdditionalProperties: false,
		Properties: map[string]interface{}{
			"flows": SchemaProperty{
				Type:        "array",
				Title:       "Flows",
				Description: "Array of flow definitions",
				Items:       flowSchema,
			},
			"apis": SchemaProperty{
				Type:        "array",
				Title:       "APIs",
				Description: "Array of API route definitions",
				Items:       apiSchema,
			},
			"rate_limit_configs_v2": SchemaProperty{
				Type:        "array",
				Title:       "Rate Limit Configs",
				Description: "Array of rate limit configurations",
				Items:       rateLimitSchema,
			},
		},
		Required: []string{},
	}
}

// buildFieldSchema creates a schema property for a step field based on naming conventions
func buildFieldSchema(field control.StepField) SchemaProperty {
	fieldType := "string" // default

	key := strings.ToLower(field.Key)

	// Detect integer fields by key naming patterns
	intPatterns := []string{"timeout", "ttl", "max", "count", "retries", "port", "delay", "retry", "limit", "size", "length", "expires", "seconds", "ms", "duration"}
	for _, pattern := range intPatterns {
		if strings.Contains(key, pattern) {
			fieldType = "integer"
			break
		}
	}

	// Detect boolean fields by key naming patterns
	if fieldType == "string" {
		boolPatterns := []string{"enabled", "require", "generate", "skip", "include", "forward", "match", "validate", "check", "prefetch"}
		for _, pattern := range boolPatterns {
			if strings.Contains(key, pattern) {
				fieldType = "boolean"
				break
			}
		}
	}

	return SchemaProperty{
		Type:        fieldType,
		Title:       field.Label,
		Description: field.Description,
	}
}
