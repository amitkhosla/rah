package sync

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/amitkhosla/rah/internal/control"
	"github.com/amitkhosla/rah/internal/datasource"
)

// Lint runs Level 0—4 checks on the loaded bundle.
// Issues already present in result.Issues (from the loader/parser) are included
// unchanged at the front of the returned slice — the linter appends to, never
// replaces them.
func Lint(result LoadResult) []LintIssue {
	issues := make([]LintIssue, len(result.Issues), len(result.Issues)+256)
	copy(issues, result.Issues)
	issues = append(issues, lintLevel0(result)...)
	issues = append(issues, lintLevel1(result)...)
	issues = append(issues, lintLevel2(result)...)
	issues = append(issues, lintLevel3(result)...)
	issues = append(issues, lintLevel4(result)...)
	// Wave 5B: Redis, Named Queries, Migrations, and Tenant Isolation validation
	issues = append(issues, lintRedisSteps(result)...)
	issues = append(issues, lintNamedQueryRefs(result)...)
	issues = append(issues, lintMigrationVersions(result)...)
	issues = append(issues, lintRedisMultiKeySteps(result)...)
	issues = append(issues, lintNamedQueryBatchBy(result)...)
	issues = append(issues, lintRedisZAddScore(result)...)
	return issues
}

// â"€â"€â"€ Level 0: Structural checks â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func lintLevel0(result LoadResult) []LintIssue {
	var issues []LintIssue
	b := result.Bundle

	// At least one of flows/apis must be present
	if len(b.Flows) == 0 && len(b.Apis) == 0 {
		issues = append(issues, LintIssue{
			Severity:   SeverityError,
			Rule:       "empty_bundle",
			Message:    "bundle must contain at least one flow or API definition",
			Suggestion: "add at least one entry under 'flows' or 'apis'",
		})
	}

	for _, flow := range b.Flows {
		loc := result.SourceMap[flow.Name]

		if flow.Name == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "flow_missing_name",
				File:       loc.File,
				Line:       loc.Line,
				Message:    "flow definition is missing a 'name' field",
				Suggestion: "set the 'name' field to a unique identifier for this flow",
			})
		}

		if len(flow.Instructions) == 0 {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "flow_empty_instructions",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %q has no instructions", flow.Name),
				Suggestion: "add at least one step to the 'instructions' list",
			})
		}

		for i, step := range flow.Instructions {
			issues = append(issues, checkStepHasAction(step, flow.Name, i+1, loc)...)
		}
	}

	for _, api := range b.Apis {
		key := apiKey(api.Method, api.Path)
		loc := result.SourceMap[key]

		if api.Path == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "api_missing_path",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("API %q is missing a 'path' field", api.Name),
				Suggestion: "set the 'path' field (e.g. /v1/orders)",
			})
		}

		if api.FlowName == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "api_missing_flow_name",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("API %s %q is missing a 'flow_name' field", methodOrAny(api.Method), api.Path),
				Suggestion: "set 'flow_name' to the name of the flow this API should execute",
			})
		}

		if api.Action == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "api_missing_action",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("API %s %q is missing an 'action' field", methodOrAny(api.Method), api.Path),
				Suggestion: "set 'action' to 'upsert' or 'delete'",
			})
		}

		// WebSocket configuration validation
		if api.WebSocket != nil && api.WebSocket.Enabled {
			wsCfg := api.WebSocket
			if wsCfg.InboundFlow == "" {
				issues = append(issues, LintIssue{
					Severity:   SeverityError,
					Rule:       "websocket_missing_inbound_flow",
					File:       loc.File,
					Line:       loc.Line,
					Message:    fmt.Sprintf("API %s %q websocket: inbound_flow is required when enabled", methodOrAny(api.Method), api.Path),
					Suggestion: "set 'inbound_flow' to the name of the flow to handle inbound messages",
				})
			}
			if wsCfg.PingIntervalSec < 0 {
				issues = append(issues, LintIssue{
					Severity:   SeverityError,
					Rule:       "websocket_invalid_ping_interval",
					File:       loc.File,
					Line:       loc.Line,
					Message:    fmt.Sprintf("API %s %q websocket: ping_interval_sec must be >= 0, got %d", methodOrAny(api.Method), api.Path, wsCfg.PingIntervalSec),
					Suggestion: "set 'ping_interval_sec' to a non-negative integer",
				})
			}
			if wsCfg.PongTimeoutSec < 0 {
				issues = append(issues, LintIssue{
					Severity:   SeverityError,
					Rule:       "websocket_invalid_pong_timeout",
					File:       loc.File,
					Line:       loc.Line,
					Message:    fmt.Sprintf("API %s %q websocket: pong_timeout_sec must be >= 0, got %d", methodOrAny(api.Method), api.Path, wsCfg.PongTimeoutSec),
					Suggestion: "set 'pong_timeout_sec' to a non-negative integer",
				})
			}
		}
	}

	// Tenant structural checks
	for i, t := range b.Tenants {
		primaryAlias := ""
		if len(t.Aliases) > 0 {
			primaryAlias = t.Aliases[0]
		}
		loc := result.SourceMap["tenant:"+primaryAlias]
		if len(t.Aliases) == 0 {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "tenant_no_aliases",
				File: loc.File, Line: loc.Line,
				Message: fmt.Sprintf("tenants[%d]: aliases must not be empty", i),
			})
		}
		if t.Action != "upsert" && t.Action != "delete" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "tenant_invalid_action",
				File: loc.File, Line: loc.Line,
				Message: fmt.Sprintf("tenants[%d] %v: action must be \"upsert\" or \"delete\", got %q", i, t.Aliases, t.Action),
			})
		}
	}

	// Cache seed structural checks
	for i, seed := range b.CacheSeeds {
		loc := result.SourceMap["cacheseed:"+seed.Key]
		if seed.Key == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "cacheseed_no_key",
				File: loc.File, Line: loc.Line,
				Message: fmt.Sprintf("cache_seeds[%d]: key must not be empty", i),
			})
		}
		if len(seed.Tenants) == 0 {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "cacheseed_no_tenants",
				File: loc.File, Line: loc.Line,
				Message: fmt.Sprintf("cache_seeds[%d] %q: tenants must not be empty (use [\"*\"] for global)", i, seed.Key),
			})
		}
		if seed.Action != "upsert" && seed.Action != "delete" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "cacheseed_invalid_action",
				File: loc.File, Line: loc.Line,
				Message: fmt.Sprintf("cache_seeds[%d] %q: action must be \"upsert\" or \"delete\", got %q", i, seed.Key, seed.Action),
			})
		}
	}

	// API key structural checks
	for i, k := range b.APIKeys {
		loc := result.SourceMap["apikey:"+k.Alias]
		if k.App == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "apikey_no_app",
				File: loc.File, Line: loc.Line,
				Message: fmt.Sprintf("api_keys[%d]: app must not be empty", i),
			})
		}
		if k.Alias == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "apikey_no_alias",
				File: loc.File, Line: loc.Line,
				Message: fmt.Sprintf("api_keys[%d]: alias must not be empty", i),
			})
		}
		if k.KeyRef == "" && k.Action != "delete" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "apikey_no_key_ref",
				File: loc.File, Line: loc.Line,
				Message: fmt.Sprintf("api_keys[%d] %q: key_ref required for action %q", i, k.Alias, k.Action),
			})
		}
		if k.Action != "upsert" && k.Action != "delete" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "apikey_invalid_action",
				File: loc.File, Line: loc.Line,
				Message: fmt.Sprintf("api_keys[%d] %q: action must be \"upsert\" or \"delete\", got %q", i, k.Alias, k.Action),
			})
		}
	}

	// ── LLM Models ───────────────────────────────────────────────────────────────
	for i, m := range b.LLMModels {
		loc := fmt.Sprintf("llm_models[%d]", i)
		if m.Alias == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "llm_model_missing_alias",
				Message:    fmt.Sprintf("%s: alias is required", loc),
				Suggestion: "Add an alias field to identify this LLM model",
			})
		}
		if m.Provider == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "llm_model_missing_provider",
				Message:    fmt.Sprintf("%s: provider is required", loc),
				Suggestion: "Set provider to the model provider (e.g. anthropic, openai)",
			})
		}
		validAdapters := map[string]bool{
			"anthropic": true, "openai": true, "gemini": true,
			"ollama": true, "deepseek": true, "custom": true, "bedrock": true,
		}
		if string(m.Adapter) != "" && !validAdapters[string(m.Adapter)] {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "llm_model_invalid_adapter",
				Message:    fmt.Sprintf("%s: unknown adapter %q", loc, m.Adapter),
				Suggestion: "Valid adapters: anthropic, openai, gemini, ollama, deepseek, custom, bedrock",
			})
		}
	}

	// ── MCP Servers ──────────────────────────────────────────────────────────────
	for i, srv := range b.MCPServers {
		loc := fmt.Sprintf("mcp_servers[%d]", i)
		if srv.Alias == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "mcp_server_missing_alias",
				Message:    fmt.Sprintf("%s: alias is required", loc),
				Suggestion: "Add an alias field to identify this MCP server",
			})
		}
		validTransports := map[string]bool{"http": true, "sse": true, "stdio": true}
		if string(srv.Transport) != "" && !validTransports[string(srv.Transport)] {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "mcp_server_invalid_transport",
				Message:    fmt.Sprintf("%s: unknown transport %q", loc, srv.Transport),
				Suggestion: "Valid transports: http, sse, stdio",
			})
		}
		if (srv.Transport == "http" || srv.Transport == "sse") && srv.URL == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "mcp_server_missing_url",
				Message:    fmt.Sprintf("%s: url is required for transport %q", loc, srv.Transport),
				Suggestion: "Add a url field pointing to the MCP server endpoint",
			})
		}
	}

	// ── Virtual MCP Servers ──────────────────────────────────────────────────────
	for i, def := range b.VirtualMCPServers {
		loc := fmt.Sprintf("virtual_mcp_servers[%d]", i)
		if def.Name == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "virtual_mcp_server_missing_name",
				Message:    fmt.Sprintf("%s: name is required", loc),
				Suggestion: "Add a name field to identify this virtual MCP server",
			})
		}
		if len(def.Sources) == 0 {
			issues = append(issues, LintIssue{
				Severity: SeverityWarning, Rule: "virtual_mcp_server_no_sources",
				Message:    fmt.Sprintf("%s %q: has no tool sources defined", loc, def.Name),
				Suggestion: "Add at least one source entry to expose tools from this virtual server",
			})
		}
		validKinds := map[string]bool{"api_tool": true, "mcp_tool": true, "mcp_all": true}
		for j, src := range def.Sources {
			if !validKinds[string(src.Kind)] {
				issues = append(issues, LintIssue{
					Severity: SeverityError, Rule: "virtual_mcp_server_invalid_source_kind",
					Message:    fmt.Sprintf("%s %q sources[%d]: unknown kind %q", loc, def.Name, j, src.Kind),
					Suggestion: "Valid source kinds: api_tool, mcp_tool, mcp_all",
				})
			}
		}
	}

	// ── API Tools ────────────────────────────────────────────────────────────────
	for i, tool := range b.APITools {
		loc := fmt.Sprintf("api_tools[%d]", i)
		if tool.Name == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "api_tool_missing_name",
				Message:    fmt.Sprintf("%s: name is required", loc),
				Suggestion: "Add a name field to identify this API tool",
			})
		}
		if tool.Path == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "api_tool_missing_path",
				Message:    fmt.Sprintf("%s %q: path is required", loc, tool.Name),
				Suggestion: "Add a path field pointing to the API endpoint (e.g. /v1/search)",
			})
		}
		validMethods := map[string]bool{
			"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true,
		}
		if tool.Method != "" && !validMethods[tool.Method] {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "api_tool_invalid_method",
				Message:    fmt.Sprintf("%s %q: unknown HTTP method %q", loc, tool.Name, tool.Method),
				Suggestion: "Valid methods: GET, POST, PUT, DELETE, PATCH",
			})
		}
	}

	// ── Schedules ────────────────────────────────────────────────────────────────
	for i, s := range b.Schedules {
		loc := fmt.Sprintf("schedules[%d]", i)
		if s.Name == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "schedule_missing_name",
				Message:    fmt.Sprintf("%s: name is required", loc),
				Suggestion: "Add a name field to identify this schedule",
			})
		}
		if s.Cron == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "schedule_missing_cron",
				Message:    fmt.Sprintf("%s %q: cron is required", loc, s.Name),
				Suggestion: "Add a cron field with a cron expression (e.g. '0 * * * *')",
			})
		}
		if s.FlowName == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "schedule_missing_flow_name",
				Message:    fmt.Sprintf("%s %q: flow_name is required", loc, s.Name),
				Suggestion: "Add a flow_name field pointing to the flow to execute",
			})
		}
		if s.TenantAlias == "" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "schedule_missing_tenant_alias",
				Message:    fmt.Sprintf("%s %q: tenant_alias is required", loc, s.Name),
				Suggestion: "Add a tenant_alias field to specify which tenant to use",
			})
		}
		if s.Action != "upsert" && s.Action != "delete" {
			issues = append(issues, LintIssue{
				Severity: SeverityError, Rule: "schedule_invalid_action",
				Message:    fmt.Sprintf("%s %q: action must be \"upsert\" or \"delete\", got %q", loc, s.Name, s.Action),
				Suggestion: "Set action to \"upsert\" or \"delete\"",
			})
		}
	}

	return issues
}

// checkStepHasAction recursively verifies every step (including nested) has a
// non-empty action field.
func checkStepHasAction(step control.StepConfig, flowName string, stepNum int, loc SourceLocation) []LintIssue {
	var issues []LintIssue

	if step.Action == "" {
		issues = append(issues, LintIssue{
			Severity:   SeverityError,
			Rule:       "step_missing_action",
			File:       loc.File,
			Line:       loc.Line,
			Message:    fmt.Sprintf("flow %q step %d is missing an 'action' field", flowName, stepNum),
			Suggestion: "add an 'action' field to the step (e.g. action: http_call)",
		})
	}

	for i, nested := range step.Do {
		issues = append(issues, checkStepHasAction(nested, flowName, i+1, loc)...)
	}
	for _, branch := range step.Branches {
		for i, nested := range branch.Flow {
			issues = append(issues, checkStepHasAction(nested, flowName, i+1, loc)...)
		}
	}

	return issues
}

// â"€â"€â"€ Level 1: Schema validation â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func lintLevel1(result LoadResult) []LintIssue {
	descriptors := control.AllStepDescriptors()
	idx := make(map[string]control.StepDescriptor, len(descriptors))
	types := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		idx[d.Type] = d
		types = append(types, d.Type)
	}

	var issues []LintIssue
	for _, flow := range result.Bundle.Flows {
		loc := result.SourceMap[flow.Name]
		for i, step := range flow.Instructions {
			issues = append(issues, lintStepSchema(step, flow.Name, i+1, loc, idx, types)...)
		}
	}
	return issues
}

func lintStepSchema(
	step control.StepConfig,
	flowName string, stepNum int,
	loc SourceLocation,
	idx map[string]control.StepDescriptor,
	types []string,
) []LintIssue {
	var issues []LintIssue

	if step.Action != "" {
		issues = append(issues, validateStepSchema(step, flowName, stepNum, loc, idx, types)...)
	}

	for i, nested := range step.Do {
		issues = append(issues, lintStepSchema(nested, flowName, i+1, loc, idx, types)...)
	}
	for _, branch := range step.Branches {
		for i, nested := range branch.Flow {
			issues = append(issues, lintStepSchema(nested, flowName, i+1, loc, idx, types)...)
		}
	}
	return issues
}

// validateStepSchema validates a single step's action type and input map keys.
func validateStepSchema(
	step control.StepConfig,
	flowName string, stepNum int,
	loc SourceLocation,
	idx map[string]control.StepDescriptor,
	types []string,
) []LintIssue {
	var issues []LintIssue

	descriptor, known := idx[step.Action]
	if !known {
		issue := LintIssue{
			Severity: SeverityError,
			Rule:     "unknown_action",
			File:     loc.File,
			Line:     loc.Line,
			Message:  fmt.Sprintf("flow %q step %d: unknown action %q", flowName, stepNum, step.Action),
		}
		if closest := fuzzyMatchString(strings.ToLower(step.Action), types, strings.ToLower); closest != "" {
			issue.Suggestion = fmt.Sprintf("did you mean %q?", closest)
		}
		return append(issues, issue)
	}

	if len(step.Input) == 0 {
		return issues
	}

	// Build the set of valid input-map keys.
	// UserFacingFields returns fields with keys in two forms:
	//   "input.xxx"  — these correspond to Input["xxx"] in the parsed StepConfig
	//   "input"      — the whole Input map is free-form (e.g. assign_quota_group)
	//
	// We also keep the full descriptor key for type inference via Defaults.
	// validInputKeys: short key ("xxx") â†’ full descriptor field key ("input.xxx")
	validInputKeys := make(map[string]string)
	freeFormInput := false

	for _, f := range UserFacingFields(descriptor) {
		switch {
		case f.Key == "input":
			freeFormInput = true
		case strings.HasPrefix(f.Key, "input."):
			stripped := strings.TrimPrefix(f.Key, "input.")
			validInputKeys[stripped] = f.Key
		}
	}

	if freeFormInput {
		// Input map is intentionally open-ended; only check for forbidden slot keys.
		for key := range step.Input {
			if IsSlotKey(key) {
				issues = append(issues, slotKeyError(key, step.Action, flowName, stepNum, loc))
			}
		}
		return issues
	}

	for key, val := range step.Input {
		// Reject any _slot-suffixed key — these are internal compiler details.
		if IsSlotKey(key) {
			issues = append(issues, slotKeyError(key, step.Action, flowName, stepNum, loc))
			continue
		}

		// Unknown key check (only when the descriptor declares input.* fields).
		if len(validInputKeys) > 0 {
			fullKey, ok := validInputKeys[key]
			if !ok {
				issue := LintIssue{
					Severity: SeverityWarning,
					Rule:     "unknown_input_key",
					File:     loc.File,
					Line:     loc.Line,
					Message:  fmt.Sprintf("flow %q step %d (%s): unknown input key %q", flowName, stepNum, step.Action, key),
				}
				if closest := fuzzyMatchString(strings.ToLower(key), mapKeys(validInputKeys), strings.ToLower); closest != "" {
					issue.Suggestion = fmt.Sprintf("did you mean %q?", closest)
				}
				issues = append(issues, issue)
				continue
			}

			// Value type check for known keys.
			if typeIssue := checkFieldType(fullKey, key, val, descriptor, flowName, stepNum, step.Action, loc); typeIssue != nil {
				issues = append(issues, *typeIssue)
			}
		}
	}

	return issues
}

// slotKeyError builds the standardised error for a forbidden _slot key in input.
func slotKeyError(key, action, flowName string, stepNum int, loc SourceLocation) LintIssue {
	issue := LintIssue{
		Severity: SeverityError,
		Rule:     "slot_key_in_input",
		File:     loc.File,
		Line:     loc.Line,
		Message:  fmt.Sprintf("flow %q step %d (%s): input key %q is an internal slot index and must not appear in user YAML", flowName, stepNum, action, key),
	}
	if alt := SlotKeyAlternative(key); alt != "" {
		issue.Suggestion = fmt.Sprintf("replace %q with the named variable field %q", key, alt)
	} else {
		issue.Suggestion = "use named variable fields instead of internal _slot indices"
	}
	return issue
}

// checkFieldType infers the expected type from the descriptor's Defaults map and
// validates the user-supplied value. Only boolean and integer fields are checked;
// all other field types are left unconstrained.
//
// Type inference rules:
//   - Defaults[fullKey] == "true" or "false" â†’ boolean; value must be "true"/"false"
//   - Defaults[fullKey] is a pure decimal integer â†’ integer; value must parse as int64
func checkFieldType(fullKey, displayKey, value string, descriptor control.StepDescriptor,
	flowName string, stepNum int, action string, loc SourceLocation,
) *LintIssue {
	defaultVal, ok := descriptor.Defaults[fullKey]
	if !ok || defaultVal == "" {
		return nil
	}

	// Boolean
	if defaultVal == "true" || defaultVal == "false" {
		if value != "true" && value != "false" {
			return &LintIssue{
				Severity:   SeverityError,
				Rule:       "invalid_boolean_value",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %q step %d (%s): field %q must be \"true\" or \"false\", got %q", flowName, stepNum, action, displayKey, value),
				Suggestion: fmt.Sprintf("set %q to \"true\" or \"false\"", displayKey),
			}
		}
		return nil
	}

	// Integer
	if isIntegerString(defaultVal) {
		if !isIntegerString(value) {
			return &LintIssue{
				Severity:   SeverityError,
				Rule:       "invalid_integer_value",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %q step %d (%s): field %q must be an integer, got %q", flowName, stepNum, action, displayKey, value),
				Suggestion: fmt.Sprintf("set %q to an integer value (e.g. \"%s\")", displayKey, defaultVal),
			}
		}
	}

	return nil
}

// isIntegerString reports whether s is a valid signed decimal integer.
func isIntegerString(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

// â"€â"€â"€ Level 2: Reference integrity â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// lintLevel2 checks that every flow name referenced by APIs and steps exists in
// the bundle, detects flows that are defined but never reachable from any API,
// and escalates loader-detected duplicate flow/API definitions to errors.
func lintLevel2(result LoadResult) []LintIssue {
	var issues []LintIssue
	b := result.Bundle

	// Build flow index: name â†’ source location.
	flowIndex := make(map[string]SourceLocation, len(b.Flows))
	for _, flow := range b.Flows {
		if flow.Name != "" {
			flowIndex[flow.Name] = result.SourceMap[flow.Name]
		}
	}

	// Build API index: key â†’ source location.
	apiIndex := make(map[string]SourceLocation, len(b.Apis))
	for _, api := range b.Apis {
		key := apiKey(api.Method, api.Path)
		apiIndex[key] = result.SourceMap[key]
	}

	// Escalate loader-detected duplicate flow/API names to errors.
	// A flow or API must be defined in exactly one file; duplicates make the
	// effective definition load-order-dependent and are never intentional.
	for _, issue := range result.Issues {
		if issue.Rule == "duplicate_flow" || issue.Rule == "duplicate_api" {
			escalated := issue
			escalated.Severity = SeverityError
			issues = append(issues, escalated)
		}
	}

	// Check API-level flow_name references.
	for _, api := range b.Apis {
		apiLoc := apiIndex[apiKey(api.Method, api.Path)]

		if api.FlowName != "" {
			if _, ok := flowIndex[api.FlowName]; !ok {
				issues = append(issues, LintIssue{
					Severity:   SeverityError,
					Rule:       "unresolved_flow_ref",
					File:       apiLoc.File,
					Line:       apiLoc.Line,
					Message:    fmt.Sprintf("API %s %q references flow %q which is not defined in the bundle", methodOrAny(api.Method), api.Path, api.FlowName),
					Suggestion: fmt.Sprintf("define a flow named %q or correct the flow_name field", api.FlowName),
				})
			}
		}

		// Check per-endpoint flow overrides.
		for _, ep := range api.EndpointConfigs {
			if ep.FlowName != "" {
				if _, ok := flowIndex[ep.FlowName]; !ok {
					issues = append(issues, LintIssue{
						Severity:   SeverityError,
						Rule:       "unresolved_flow_ref",
						File:       apiLoc.File,
						Line:       apiLoc.Line,
						Message:    fmt.Sprintf("API %s %q endpoint %s references flow %q which is not defined in the bundle", methodOrAny(api.Method), api.Path, ep.Path, ep.FlowName),
						Suggestion: fmt.Sprintf("define a flow named %q or correct the flow_name field", ep.FlowName),
					})
				}
			}
		}
	}

	// Check step-level flow references across all flows.
	for _, flow := range b.Flows {
		loc := result.SourceMap[flow.Name]
		for i, step := range flow.Instructions {
			issues = append(issues, lintStepRefs(step, flow.Name, i+1, loc, flowIndex)...)
		}
	}

	// Check schedule flow references.
	for _, sched := range b.Schedules {
		schedLoc := result.SourceMap["schedule:"+sched.Name]
		if sched.FlowName != "" {
			if _, ok := flowIndex[sched.FlowName]; !ok && sched.Action == "upsert" {
				issues = append(issues, LintIssue{
					Severity:   SeverityError,
					Rule:       "unresolved_flow_ref",
					File:       schedLoc.File,
					Line:       schedLoc.Line,
					Message:    fmt.Sprintf("schedule %q references flow %q which is not defined in the bundle", sched.Name, sched.FlowName),
					Suggestion: fmt.Sprintf("define a flow named %q or correct the flow_name field", sched.FlowName),
				})
			}
		}
		// Validate on_failure field (Section J).
		if (sched.OnFailure != control.ScheduleOnFailure{}) {
			if sched.OnFailure.RetryCount < 0 {
				issues = append(issues, LintIssue{
					Severity:   SeverityError,
					Rule:       "schedule_invalid_retry_count",
					File:       schedLoc.File,
					Line:       schedLoc.Line,
					Message:    fmt.Sprintf("schedule %q: on_failure.retry_count must be non-negative, got %d", sched.Name, sched.OnFailure.RetryCount),
					Suggestion: "set on_failure.retry_count to a non-negative integer",
				})
			}
			if sched.OnFailure.DeadLetterFlow != "" {
				if _, ok := flowIndex[sched.OnFailure.DeadLetterFlow]; !ok {
					issues = append(issues, LintIssue{
						Severity:   SeverityError,
						Rule:       "schedule_dead_letter_flow_not_found",
						File:       schedLoc.File,
						Line:       schedLoc.Line,
						Message:    fmt.Sprintf("schedule %q: on_failure.dead_letter_flow references unknown flow %q", sched.Name, sched.OnFailure.DeadLetterFlow),
						Suggestion: fmt.Sprintf("define a flow named %q or remove the dead_letter_flow reference", sched.OnFailure.DeadLetterFlow),
					})
				}
			}
		}
	}

	// Check WebSocket configuration flow references.
	for _, api := range b.Apis {
		if api.WebSocket != nil && api.WebSocket.Enabled {
			apiLoc := apiIndex[apiKey(api.Method, api.Path)]
			wsCfg := api.WebSocket
			if wsCfg.InboundFlow != "" {
				if _, ok := flowIndex[wsCfg.InboundFlow]; !ok {
					issues = append(issues, LintIssue{
						Severity:   SeverityError,
						Rule:       "unresolved_flow_ref",
						File:       apiLoc.File,
						Line:       apiLoc.Line,
						Message:    fmt.Sprintf("API %s %q websocket: inbound_flow %q is not defined in the bundle", methodOrAny(api.Method), api.Path, wsCfg.InboundFlow),
						Suggestion: fmt.Sprintf("define a flow named %q or correct the inbound_flow field", wsCfg.InboundFlow),
					})
				}
			}
			if wsCfg.ConnectFlow != "" {
				if _, ok := flowIndex[wsCfg.ConnectFlow]; !ok {
					issues = append(issues, LintIssue{
						Severity:   SeverityError,
						Rule:       "unresolved_flow_ref",
						File:       apiLoc.File,
						Line:       apiLoc.Line,
						Message:    fmt.Sprintf("API %s %q websocket: connect_flow %q is not defined in the bundle", methodOrAny(api.Method), api.Path, wsCfg.ConnectFlow),
						Suggestion: fmt.Sprintf("define a flow named %q or correct the connect_flow field", wsCfg.ConnectFlow),
					})
				}
			}
			if wsCfg.DisconnectFlow != "" {
				if _, ok := flowIndex[wsCfg.DisconnectFlow]; !ok {
					issues = append(issues, LintIssue{
						Severity:   SeverityError,
						Rule:       "unresolved_flow_ref",
						File:       apiLoc.File,
						Line:       apiLoc.Line,
						Message:    fmt.Sprintf("API %s %q websocket: disconnect_flow %q is not defined in the bundle", methodOrAny(api.Method), api.Path, wsCfg.DisconnectFlow),
						Suggestion: fmt.Sprintf("define a flow named %q or correct the disconnect_flow field", wsCfg.DisconnectFlow),
					})
				}
			}
		}
	}

	// Reachability: warn about flows never transitively reachable from any API.
	reachable := computeReachable(b)
	for name, loc := range flowIndex {
		if !reachable[name] {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "unreferenced_flow",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %q is defined but never reachable from any API or call step", name),
				Suggestion: fmt.Sprintf("reference %q from an API flow_name or a call step, or remove the flow definition", name),
			})
		}
	}

	// Cross-reference: warn if cache seeds reference tenant aliases not in this bundle.
	tenantAliasSet := make(map[string]bool)
	for _, t := range b.Tenants {
		for _, a := range t.Aliases {
			tenantAliasSet[a] = true
		}
	}
	for _, seed := range b.CacheSeeds {
		for _, alias := range seed.Tenants {
			if alias == "*" {
				continue
			}
			if !tenantAliasSet[alias] {
				loc := result.SourceMap["cacheseed:"+seed.Key]
				issues = append(issues, LintIssue{
					Severity:   SeverityWarning,
					Rule:       "cacheseed_unknown_tenant",
					File:       loc.File,
					Line:       loc.Line,
					Message:    fmt.Sprintf("cache_seeds %q: tenant alias %q not declared in this bundle (may be pre-existing on gateway)", seed.Key, alias),
					Suggestion: "Add a tenants: section declaring this alias, or verify it already exists on the gateway.",
				})
			}
		}
	}

	// Warn if API keys reference tenant aliases not declared in this bundle.
	for _, k := range result.Bundle.APIKeys {
		for _, alias := range k.AllowedTenants {
			if !tenantAliasSet[alias] {
				loc := result.SourceMap["apikey:"+k.Alias]
				issues = append(issues, LintIssue{
					Severity:   SeverityWarning,
					Rule:       "apikey_unknown_tenant",
					File:       loc.File,
					Line:       loc.Line,
					Message:    fmt.Sprintf("api_keys %q: allowed_tenant alias %q not declared in this bundle (may be pre-existing)", k.Alias, alias),
					Suggestion: "Declare the tenant in a tenants: section or verify it already exists on the gateway.",
				})
			}
		}
	}

	// Cross-check virtual MCP server source aliases against registered MCP servers.
	mcpServerAliases := make(map[string]bool)
	for _, srv := range b.MCPServers {
		if srv.Alias != "" {
			mcpServerAliases[srv.Alias] = true
		}
	}
	for _, def := range b.VirtualMCPServers {
		for _, src := range def.Sources {
			if src.ServerAlias == "" {
				continue
			}
			if (src.Kind == "mcp_tool" || src.Kind == "mcp_all") && !mcpServerAliases[src.ServerAlias] {
				issues = append(issues, LintIssue{
					Severity:   SeverityWarning,
					Rule:       "virtual_mcp_unresolved_server_alias",
					Message:    fmt.Sprintf("virtual MCP server %q references server_alias %q which is not defined in this bundle", def.Name, src.ServerAlias),
					Suggestion: "Add the MCP server definition to this bundle, or ensure it is pre-registered on the gateway",
				})
			}
		}
	}

	return issues
}

// lintStepRefs recursively checks that every flow-name reference in a step tree
// resolves to a flow defined in flowIndex.
func lintStepRefs(
	step control.StepConfig,
	flowName string, stepNum int,
	loc SourceLocation,
	flowIndex map[string]SourceLocation,
) []LintIssue {
	var issues []LintIssue

	checkRef := func(field, ref string) {
		if ref == "" {
			return
		}
		if _, ok := flowIndex[ref]; !ok {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "unresolved_flow_ref",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %q step %d (%s): %s references flow %q which is not defined in the bundle", flowName, stepNum, step.Action, field, ref),
				Suggestion: fmt.Sprintf("define a flow named %q or correct the %s field", ref, field),
			})
		}
	}

	// call action: FlowName must resolve.
	if step.Action == "call" {
		checkRef("flow_name", step.FlowName)
	}

	// then/else: flow-name references for if-style branching steps.
	checkRef("then", step.Then)
	checkRef("else", step.Else)

	// on_miss: used by cache_get, registry_lookup, etc.
	checkRef("on_miss", step.OnMiss)

	// cases: switch-style branching — each value is a flow name.
	for caseKey, caseFlow := range step.Cases {
		if caseFlow == "" {
			continue
		}
		if _, ok := flowIndex[caseFlow]; !ok {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "unresolved_flow_ref",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %q step %d (%s): case %q references flow %q which is not defined in the bundle", flowName, stepNum, step.Action, caseKey, caseFlow),
				Suggestion: fmt.Sprintf("define a flow named %q or correct the case value", caseFlow),
			})
		}
	}

	// on_error: "jump:<flow>" format.
	if strings.HasPrefix(step.OnError, "jump:") {
		target := strings.TrimPrefix(step.OnError, "jump:")
		checkRef("on_error (jump target)", target)
	}

	// Recurse into nested steps (do-block and parallel branches).
	for i, nested := range step.Do {
		issues = append(issues, lintStepRefs(nested, flowName, i+1, loc, flowIndex)...)
	}
	for _, branch := range step.Branches {
		for i, nested := range branch.Flow {
			issues = append(issues, lintStepRefs(nested, flowName, i+1, loc, flowIndex)...)
		}
	}

	return issues
}

// computeReachable returns the set of flow names transitively reachable from
// any API entry point (api.FlowName and per-endpoint FlowName overrides).
func computeReachable(b control.UnifiedSyncRequest) map[string]bool {
	reachable := make(map[string]bool)

	// Seed with every flow name mentioned by any API.
	for _, api := range b.Apis {
		if api.FlowName != "" {
			reachable[api.FlowName] = true
		}
		for _, ep := range api.EndpointConfigs {
			if ep.FlowName != "" {
				reachable[ep.FlowName] = true
			}
		}
	}

	// Build instruction index for DFS.
	flowSteps := make(map[string][]control.StepConfig, len(b.Flows))
	for _, flow := range b.Flows {
		flowSteps[flow.Name] = flow.Instructions
	}

	// DFS: transitively mark all referenced flows as reachable.
	var visit func(name string)
	visit = func(name string) {
		steps, ok := flowSteps[name]
		if !ok {
			return
		}
		collectAllFlowRefs(steps, func(ref string) {
			if !reachable[ref] {
				reachable[ref] = true
				visit(ref)
			}
		})
	}

	// Snapshot seed set before DFS modifies the map.
	seeds := make([]string, 0, len(reachable))
	for name := range reachable {
		seeds = append(seeds, name)
	}
	for _, name := range seeds {
		visit(name)
	}

	return reachable
}

// collectAllFlowRefs calls fn for every flow name referenced by any step in
// the given instruction list (recursively, including nested do/branches).
func collectAllFlowRefs(steps []control.StepConfig, fn func(string)) {
	for _, step := range steps {
		collectStepFlowRefs(step, fn)
	}
}

func collectStepFlowRefs(step control.StepConfig, fn func(string)) {
	if step.FlowName != "" {
		fn(step.FlowName)
	}
	if step.Then != "" {
		fn(step.Then)
	}
	if step.Else != "" {
		fn(step.Else)
	}
	if step.OnMiss != "" {
		fn(step.OnMiss)
	}
	for _, ref := range step.Cases {
		if ref != "" {
			fn(ref)
		}
	}
	if strings.HasPrefix(step.OnError, "jump:") {
		if target := strings.TrimPrefix(step.OnError, "jump:"); target != "" {
			fn(target)
		}
	}
	for _, nested := range step.Do {
		collectStepFlowRefs(nested, fn)
	}
	for _, branch := range step.Branches {
		for _, nested := range branch.Flow {
			collectStepFlowRefs(nested, fn)
		}
	}
}

// â"€â"€â"€ Fuzzy matching â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// fuzzyMatchString returns the element of candidates that is closest to target
// (after normalisation via norm), provided the Levenshtein distance is â‰¤ 2.
// Returns "" when no candidate qualifies.
func fuzzyMatchString(target string, candidates []string, norm func(string) string) string {
	best := ""
	bestDist := 3 // only accept distance â‰¤ 2
	for _, c := range candidates {
		if d := levenshtein(target, norm(c)); d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// levenshtein computes the edit distance between two strings (two-row DP).
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = minInt(curr[j-1]+1, minInt(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// â"€â"€â"€ Level 3: Named variable analysis + cycle detection â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// lintLevel3 adds two categories of checks:
//
//  1. Variable forward-use analysis: for each flow, walks steps in order and
//     reports an error when a step reads a variable that has not yet been assigned
//     by an earlier step.
//
//  2. Cycle detection: builds the cross-flow call graph and classifies cycles as
//     guaranteed infinite loops (error) or recursive patterns (warning).
func lintLevel3(result LoadResult) []LintIssue {
	var issues []LintIssue
	b := result.Bundle

	// Pre-populate variable sets from API/endpoint constants.
	apiConstants := l3BuildAPIConstants(b)

	// Build the set of flows that are direct entry points (referenced by api.FlowName).
	// Sub-flows (not directly referenced by any API) receive warnings rather than
	// errors for undefined variables, since their variable context is provided by
	// the calling flow at runtime.
	entryPoints := make(map[string]bool, len(b.Apis))
	for _, api := range b.Apis {
		if api.FlowName != "" {
			entryPoints[api.FlowName] = true
		}
		for _, ep := range api.EndpointConfigs {
			if ep.FlowName != "" {
				entryPoints[ep.FlowName] = true
			}
		}
	}

	// Variable forward-use analysis — one flow at a time.
	for _, flow := range b.Flows {
		loc := result.SourceMap[flow.Name]
		seed := apiConstants[flow.Name]
		isEntryPoint := entryPoints[flow.Name]
		issues = append(issues, l3AnalyzeFlowVars(flow.Name, flow.Instructions, seed, loc, isEntryPoint)...)
	}

	// Cycle detection across the full call graph.
	issues = append(issues, l3DetectCycles(b, result)...)

	return issues
}

// â"€â"€â"€ Variable forward-use analysis â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// l3BuildAPIConstants collects the variable names pre-populated via API and
// endpoint Constants maps, keyed by the flow name those constants are injected into.
func l3BuildAPIConstants(b control.UnifiedSyncRequest) map[string]map[string]bool {
	out := make(map[string]map[string]bool)
	addConsts := func(flowName string, consts map[string]string) {
		if flowName == "" || len(consts) == 0 {
			return
		}
		if out[flowName] == nil {
			out[flowName] = make(map[string]bool)
		}
		for k := range consts {
			out[flowName][k] = true
		}
	}
	for _, api := range b.Apis {
		addConsts(api.FlowName, api.Constants)
		for _, ep := range api.EndpointConfigs {
			fn := ep.FlowName
			if fn == "" {
				fn = api.FlowName
			}
			addConsts(fn, ep.Constants)
		}
	}
	return out
}

// l3AnalyzeFlowVars runs forward variable analysis for a single flow.
// isEntryPoint should be true for flows referenced directly by an API's flow_name.
// Sub-flows (not direct entry points) emit warnings instead of errors for
// undefined variables, since their variable context is provided by calling flows.
func l3AnalyzeFlowVars(
	flowName string,
	steps []control.StepConfig,
	seed map[string]bool,
	loc SourceLocation,
	isEntryPoint bool,
) []LintIssue {
	// knownVars: variable name â†’ step number that assigned it (0 = pre-seeded).
	knownVars := make(map[string]int)
	for v := range seed {
		knownVars[v] = 0
	}
	var issues []LintIssue
	l3WalkSteps(steps, flowName, 1, loc, knownVars, isEntryPoint, &issues)
	return issues
}

// l3WalkSteps processes a step list in order, checking variable references
// against knownVars and recording assignments. startIdx is the 1-based step
// number for the first element in steps.
func l3WalkSteps(
	steps []control.StepConfig,
	flowName string,
	startIdx int,
	loc SourceLocation,
	knownVars map[string]int,
	isEntryPoint bool,
	issues *[]LintIssue,
) {
	for i, step := range steps {
		stepNum := startIdx + i

		// 1. Check input variable references before processing assignments.
		//    Entry-point flows emit errors; sub-flows emit warnings (their variable
		//    context is provided at runtime by the calling flow).
		undefinedSeverity := SeverityError
		if !isEntryPoint {
			undefinedSeverity = SeverityWarning
		}
		for _, ref := range l3StepVarRefs(step) {
			if _, known := knownVars[ref]; !known {
				*issues = append(*issues, LintIssue{
					Severity: undefinedSeverity,
					Rule:     "undefined_variable",
					File:     loc.File,
					Line:     loc.Line,
					Message: fmt.Sprintf(
						"flow %q step %d (%s): variable %q used but not assigned in any preceding step",
						flowName, stepNum, step.Action, ref),
					Suggestion: fmt.Sprintf(
						"add a bind_* or extract step before step %d that assigns variable %q",
						stepNum, ref),
				})
			}
		}

		// 2. Recurse into inline do-block (foreach/retry body).
		//    The do-block shares knownVars so that variables assigned inside the
		//    loop body are visible to subsequent steps outside the loop.
		if len(step.Do) > 0 {
			l3WalkSteps(step.Do, flowName, 1, loc, knownVars, isEntryPoint, issues)
		}

		// 3. Recurse into parallel branches.
		//    Only variables assigned in ALL branches are guaranteed after the join.
		if len(step.Branches) > 0 {
			guaranteed, partial := l3ParallelBranchVars(step.Branches, flowName, loc, knownVars, isEntryPoint, issues)
			for v := range guaranteed {
				knownVars[v] = stepNum
			}
			for v := range partial {
				if _, ok := knownVars[v]; !ok {
					*issues = append(*issues, LintIssue{
						Severity: SeverityWarning,
						Rule:     "variable_in_some_branches",
						File:     loc.File,
						Line:     loc.Line,
						Message: fmt.Sprintf(
							"flow %q step %d: variable %q is assigned in some but not all parallel branches; it may be undefined after the parallel join",
							flowName, stepNum, v),
						Suggestion: fmt.Sprintf(
							"assign %q in every branch of the parallel step, or guard its use after step %d",
							v, stepNum),
					})
					knownVars[v] = stepNum // treat as potentially-known so later steps don't cascade
				}
			}
		}

		// 4. Record this step's own variable assignments.
		for _, v := range l3StepVarAssignments(step) {
			knownVars[v] = stepNum
		}
	}
}

// l3ParallelBranchVars processes parallel branches and returns:
//   - guaranteed: variable names assigned in ALL branches (safe to read after join)
//   - partial: variable names assigned in SOME but not all branches (uncertain after join)
func l3ParallelBranchVars(
	branches []control.BranchConfig,
	flowName string,
	loc SourceLocation,
	baseKnown map[string]int,
	isEntryPoint bool,
	issues *[]LintIssue,
) (guaranteed map[string]bool, partial map[string]bool) {
	if len(branches) == 0 {
		return nil, nil
	}

	branchNew := make([]map[string]bool, 0, len(branches))
	for _, branch := range branches {
		branchKnown := l3CopyVarMap(baseKnown)
		l3WalkSteps(branch.Flow, flowName, 1, loc, branchKnown, isEntryPoint, issues)
		newVars := make(map[string]bool)
		for v := range branchKnown {
			if _, inBase := baseKnown[v]; !inBase {
				newVars[v] = true
			}
		}
		branchNew = append(branchNew, newVars)
	}

	guaranteed = l3IntersectBoolMaps(branchNew)
	union := l3UnionBoolMaps(branchNew)
	partial = make(map[string]bool)
	for v := range union {
		if !guaranteed[v] {
			partial[v] = true
		}
	}
	return guaranteed, partial
}

// l3StepVarAssignments returns the variable names that this step writes to.
func l3StepVarAssignments(step control.StepConfig) []string {
	var vars []string
	if step.As != "" {
		vars = append(vars, step.As)
	}
	if step.ResponseBodyVar != "" {
		vars = append(vars, step.ResponseBodyVar)
	}
	if step.ResponseStatusVar != "" {
		vars = append(vars, step.ResponseStatusVar)
	}
	for _, v := range step.ResponseHeaderVars {
		if v != "" {
			vars = append(vars, v)
		}
	}
	if step.Destination != "" {
		vars = append(vars, step.Destination)
	}
	// Well-known output keys in the Input map (auth steps, etc.).
	for _, key := range l3InputMapOutputKeys {
		if v, ok := step.Input[key]; ok && v != "" {
			vars = append(vars, v)
		}
	}
	return vars
}

// l3StepVarRefs returns the variable names that this step reads as inputs.
func l3StepVarRefs(step control.StepConfig) []string {
	var vars []string
	if step.UrlVar != "" {
		vars = append(vars, step.UrlVar)
	}
	if step.BodyVar != "" {
		vars = append(vars, step.BodyVar)
	}
	if step.SourceVar != "" {
		vars = append(vars, step.SourceVar)
	}
	if step.Variable != "" {
		vars = append(vars, step.Variable)
	}
	// key_identifier "var.<name>" pattern (registry_lookup, load_service_url_var, etc.)
	if strings.HasPrefix(step.KeyIdentifier, "var.") {
		vars = append(vars, strings.TrimPrefix(step.KeyIdentifier, "var."))
	}
	// Well-known input-ref keys in the Input map.
	for _, key := range l3InputMapRefKeys {
		if v, ok := step.Input[key]; ok && v != "" {
			vars = append(vars, v)
		}
	}
	return vars
}

// l3InputMapOutputKeys lists Input map keys whose values are variable names that
// the step writes its output into.
var l3InputMapOutputKeys = []string{
	// token_validation (JWT) output variables
	"jwt.result_var", "jwt.claims_var", "jwt.subject_var",
	"jwt.client_id_var", "jwt.scopes_out_var",
	// token introspection output variables
	"introspect.result_var", "introspect.claims_var", "introspect.subject_var",
	"introspect.client_id_var", "introspect.scopes_out_var",
	// DPoP output variables
	"dpop.result_var", "dpop.cnf_jkt_var",
	// API key validation output variable
	"apikey.result_var",
}

// l3InputMapRefKeys lists Input map keys whose values are variable names that
// the step reads at runtime.
var l3InputMapRefKeys = []string{
	// token_validation (JWT) variable inputs
	"jwt.jwks_uri_var", "jwt.alg_var", "jwt.leeway_var",
	"jwt.validate_var", "jwt.issuer_var", "jwt.audience_var",
	"jwt.required_scopes_var", "jwt.scope_claims_var",
	"jwt.on_failure_var", "jwt.failure_status_var", "jwt.failure_body_var",
	"jwt.result_success_var", "jwt.result_failure_var",
	// token introspection variable inputs
	"introspect.bearer_token_var", "introspect.token_var",
	// DPoP variable inputs
	"dpop.access_token_var",
	// grpc_call dynamic URL variable
	"url_var",
	// AES crypto runtime key variable
	"key_var",
	// ingest pipeline variable inputs
	"payload_var", "model_var", "session_var",
}

// â"€â"€â"€ Cycle detection â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// l3CallEdge is a directed edge in the flow call graph.
type l3CallEdge struct {
	Target        string
	IsConditional bool // true when this edge is only taken under a condition
}

// l3DetectCycles builds the flow call graph and reports cyclic patterns.
func l3DetectCycles(b control.UnifiedSyncRequest, result LoadResult) []LintIssue {
	graph := l3BuildCallGraph(b)

	// Determine which flows contain state-mutation steps.
	flowMutates := make(map[string]bool, len(b.Flows))
	for _, flow := range b.Flows {
		if l3HasMutationStep(flow.Instructions) {
			flowMutates[flow.Name] = true
		}
	}

	var issues []LintIssue
	// 0=unvisited, 1=in DFS stack, 2=fully processed.
	color := make(map[string]int, len(b.Flows))
	path := make([]string, 0, 16) // current DFS stack
	reported := make(map[string]bool)

	var dfs func(node string)
	dfs = func(node string) {
		color[node] = 1
		path = append(path, node)

		for _, edge := range graph[node] {
			switch color[edge.Target] {
			case 1:
				// Back edge — cycle found. Report once per cycle root.
				if !reported[edge.Target] {
					reported[edge.Target] = true
					cycleNodes := l3ExtractCyclePath(path, edge.Target)
					loc := result.SourceMap[node]
					issues = append(issues, l3ClassifyCycle(cycleNodes, graph, flowMutates, loc)...)
				}
			case 0:
				dfs(edge.Target)
			}
		}

		path = path[:len(path)-1]
		color[node] = 2
	}

	for _, flow := range b.Flows {
		if color[flow.Name] == 0 {
			dfs(flow.Name)
		}
	}
	return issues
}

// l3BuildCallGraph returns a map from flow name to its outgoing call edges.
func l3BuildCallGraph(b control.UnifiedSyncRequest) map[string][]l3CallEdge {
	graph := make(map[string][]l3CallEdge, len(b.Flows))
	for _, flow := range b.Flows {
		var edges []l3CallEdge
		l3CollectEdges(flow.Instructions, false, &edges)
		if len(edges) > 0 {
			graph[flow.Name] = edges
		}
	}
	return graph
}

// l3CollectEdges recursively collects all cross-flow call edges from a step list.
// parentConditional is true when these steps are already inside a conditional block.
func l3CollectEdges(steps []control.StepConfig, parentConditional bool, edges *[]l3CallEdge) {
	for _, step := range steps {
		// Direct call step: conditionally unconditional, depending on nesting.
		if step.Action == "call" && step.FlowName != "" {
			*edges = append(*edges, l3CallEdge{Target: step.FlowName, IsConditional: parentConditional})
		}
		// then/else are always conditional (only one branch executes).
		if step.Then != "" {
			*edges = append(*edges, l3CallEdge{Target: step.Then, IsConditional: true})
		}
		if step.Else != "" {
			*edges = append(*edges, l3CallEdge{Target: step.Else, IsConditional: true})
		}
		// on_miss is conditional (only when lookup fails).
		if step.OnMiss != "" {
			*edges = append(*edges, l3CallEdge{Target: step.OnMiss, IsConditional: true})
		}
		// switch/cases: each case is conditional.
		for _, target := range step.Cases {
			if target != "" {
				*edges = append(*edges, l3CallEdge{Target: target, IsConditional: true})
			}
		}
		// on_error jump target.
		if strings.HasPrefix(step.OnError, "jump:") {
			if target := strings.TrimPrefix(step.OnError, "jump:"); target != "" {
				*edges = append(*edges, l3CallEdge{Target: target, IsConditional: true})
			}
		}
		// do-block (foreach/retry): inherit parent conditionality.
		l3CollectEdges(step.Do, parentConditional, edges)
		// Parallel branches are conditional (only some execute).
		for _, branch := range step.Branches {
			l3CollectEdges(branch.Flow, true, edges)
		}
	}
}

// l3ExtractCyclePath returns the cycle sub-path [root, ..., node, root].
func l3ExtractCyclePath(path []string, root string) []string {
	for i, n := range path {
		if n == root {
			cycle := make([]string, len(path)-i+1)
			copy(cycle, path[i:])
			cycle[len(cycle)-1] = root // close the loop
			return cycle
		}
	}
	return []string{root, root}
}

// l3ClassifyCycle produces one lint issue that describes the detected cycle.
//
// Classification rules:
//   - All edges unconditional                â†’ Error: guaranteed infinite loop
//   - â‰¥1 conditional edge + mutation in cycle â†’ Warning: bounded recursion
//   - â‰¥1 conditional edge, no mutation        â†’ Warning: recursive call
func l3ClassifyCycle(
	cycleNodes []string,
	graph map[string][]l3CallEdge,
	flowMutates map[string]bool,
	loc SourceLocation,
) []LintIssue {
	if len(cycleNodes) < 2 {
		return nil
	}

	allUnconditional := true
	anyMutates := false

	for i := 0; i < len(cycleNodes)-1; i++ {
		src, dst := cycleNodes[i], cycleNodes[i+1]
		for _, e := range graph[src] {
			if e.Target == dst {
				if e.IsConditional {
					allUnconditional = false
				}
				break
			}
		}
		if flowMutates[src] {
			anyMutates = true
		}
	}

	cyclePath := strings.Join(cycleNodes, " â†’ ")

	if allUnconditional {
		return []LintIssue{{
			Severity: SeverityError,
			Rule:     "infinite_loop",
			File:     loc.File,
			Line:     loc.Line,
			Message: fmt.Sprintf(
				"guaranteed infinite loop: %s — all call edges in this cycle are unconditional",
				cyclePath),
			Suggestion: "add an if/condition step to guard the recursive call so the flow eventually terminates",
		}}
	}

	if anyMutates {
		return []LintIssue{{
			Severity: SeverityWarning,
			Rule:     "bounded_recursion",
			File:     loc.File,
			Line:     loc.Line,
			Message: fmt.Sprintf(
				"bounded recursion pattern detected: %s — verify the exit condition is always eventually reached",
				cyclePath),
			Suggestion: "ensure the mutation step changes the value checked by the exit condition on every iteration",
		}}
	}

	return []LintIssue{{
		Severity: SeverityWarning,
		Rule:     "recursive_call",
		File:     loc.File,
		Line:     loc.Line,
		Message: fmt.Sprintf(
			"recursive call detected: %s — ensure the exit condition changes on each iteration",
			cyclePath),
		Suggestion: "add a state-mutation step (cache_put, set_identifier, etc.) to make progress toward the exit condition",
	}}
}

// l3HasMutationStep reports whether any step in the list (recursively) writes
// to shared state (cache, registry, etc.).
func l3HasMutationStep(steps []control.StepConfig) bool {
	for _, step := range steps {
		switch step.Action {
		case "cache_put", "cache_incr", "cache_del", "cache_set_ex",
			"cache_put_multi", "cache_del_multi",
			"set_service_url", "set_identifier", "set_meta":
			return true
		}
		if l3HasMutationStep(step.Do) {
			return true
		}
		for _, branch := range step.Branches {
			if l3HasMutationStep(branch.Flow) {
				return true
			}
		}
	}
	return false
}

// â"€â"€â"€ Level 3 utility helpers â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// l3CopyVarMap makes a shallow copy of a map[string]int.
func l3CopyVarMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// l3IntersectBoolMaps returns a map containing only keys present in ALL input maps.
func l3IntersectBoolMaps(maps []map[string]bool) map[string]bool {
	if len(maps) == 0 {
		return nil
	}
	result := make(map[string]bool, len(maps[0]))
	for k := range maps[0] {
		result[k] = true
	}
	for _, m := range maps[1:] {
		for k := range result {
			if !m[k] {
				delete(result, k)
			}
		}
	}
	return result
}

// l3UnionBoolMaps returns a map containing all keys present in ANY input map.
func l3UnionBoolMaps(maps []map[string]bool) map[string]bool {
	result := make(map[string]bool)
	for _, m := range maps {
		for k := range m {
			result[k] = true
		}
	}
	return result
}

// â"€â"€â"€ Level 4: Advisory warnings â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// lintLevel4 checks for common patterns that are not errors but worth warning about.
//
//  - http_call steps without timeout protection
//  - Literal secret-like values in input fields (use secret refs instead)
//  - Flows with more than 30 steps (suggests refactoring opportunity)
//  - APIs with no rate limit policies defined
func lintLevel4(result LoadResult) []LintIssue {
	var issues []LintIssue
	b := result.Bundle

	// Check flows for size and step warnings.
	for _, flow := range b.Flows {
		loc := result.SourceMap[flow.Name]
		issues = append(issues, l4CheckFlow(flow, loc)...)
		// Add step-specific required field validations.
		for i, step := range flow.Instructions {
			issues = append(issues, lintStepRequiredFields(step, flow, i+1, loc)...)
		}
	}

	// Check APIs for rate limit policies.
	for _, api := range b.Apis {
		key := apiKey(api.Method, api.Path)
		loc := result.SourceMap[key]
		issues = append(issues, l4CheckAPI(api, loc)...)
	}

	return issues
}

// l4CheckFlow emits advisory warnings for a single flow.
func l4CheckFlow(flow control.FlowUpdate, loc SourceLocation) []LintIssue {
	var issues []LintIssue

	// Check for excessively large flows (> 30 steps).
	stepCount := l4CountSteps(flow.Instructions)
	if stepCount > 30 {
		issues = append(issues, LintIssue{
			Severity: SeverityInfo,
			Rule:     "large_flow",
			File:     loc.File,
			Line:     loc.Line,
			Message: fmt.Sprintf(
				"flow %q has %d steps (> 30) and may be difficult to maintain",
				flow.Name, stepCount),
			Suggestion: "consider breaking the flow into smaller named flows and using call steps to compose them",
		})
	}

	// Check each step for advisory warnings.
	issues = append(issues, l4CheckSteps(flow.Instructions, flow.Name, loc)...)

	// Check security enforcement steps.
	issues = append(issues, l4CheckSecuritySteps(flow.Instructions, flow.Name, loc)...)

	return issues
}

// l4CheckSteps recursively checks steps for warnings.
func l4CheckSteps(steps []control.StepConfig, flowName string, loc SourceLocation) []LintIssue {
	var issues []LintIssue

	for _, step := range steps {
		// http_call timeout check.
		if step.Action == "http_call" || step.Action == "grpc_call" {
			if step.Timeout == 0 && step.MaxRetries == 0 {
				issues = append(issues, LintIssue{
					Severity: SeverityInfo,
					Rule:     "missing_timeout",
					File:     loc.File,
					Line:     loc.Line,
					Message: fmt.Sprintf(
						"flow %q step %s: no timeout or retry configuration; requests may hang indefinitely",
						flowName, step.Action),
					Suggestion: "set a timeout_ms or max_retries value to protect against slow/hung upstream",
				})
			}
		}

		// Check input for literal secrets (common patterns).
		issues = append(issues, l4CheckSecrets(step, flowName, loc)...)

		// Recurse into nested steps.
		issues = append(issues, l4CheckSteps(step.Do, flowName, loc)...)
		for _, branch := range step.Branches {
			issues = append(issues, l4CheckSteps(branch.Flow, flowName, loc)...)
		}
	}

	return issues
}

// l4CheckSecrets looks for secret-like values in step input fields that should
// use secret references instead (e.g., "env://VAR", "gsm://project/name").
func l4CheckSecrets(step control.StepConfig, flowName string, loc SourceLocation) []LintIssue {
	var issues []LintIssue

	// Only check auth/crypto steps where secrets are common.
	shouldCheck := false
	switch step.Action {
	case "token_validation", "validate_dpop", "validate_token_introspection",
		"aes_encrypt", "aes_decrypt", "load_secret", "validate_api_key":
		shouldCheck = true
	}
	if !shouldCheck {
		return issues
	}

	// Patterns that suggest literal secrets (alphanumeric with length > 20,
	// base64-like, or starting with common secret prefixes).
	for key, val := range step.Input {
		if val == "" || len(val) < 20 {
			continue
		}
		// Skip known variable refs or legitimate values.
		if strings.HasPrefix(val, "var.") || strings.HasPrefix(val, "header.") ||
			strings.HasPrefix(val, "query.") || strings.HasPrefix(val, "env://") ||
			strings.HasPrefix(val, "gsm://") || strings.HasPrefix(val, "vault://") ||
			strings.HasPrefix(val, "aws://") || strings.HasPrefix(val, "file://") {
			continue
		}

		// Check if it looks like a secret (length > 20, mostly alphanumeric/special).
		if looksLikeSecret(val) {
			issues = append(issues, LintIssue{
				Severity: SeverityInfo,
				Rule:     "literal_secret",
				File:     loc.File,
				Line:     loc.Line,
				Message: fmt.Sprintf(
					"flow %q step %s: input field %q appears to contain a literal secret value",
					flowName, step.Action, key),
				Suggestion: "load the secret using load_secret or use a secret reference (env://, gsm://, vault://, etc.) instead",
			})
		}
	}

	return issues
}

// looksLikeSecret heuristically detects if a string looks like a secret.
// Returns true if the string is long and contains mostly alphanumeric chars or
// common secret characters (-, _, ., =, +, /, etc.).
func looksLikeSecret(s string) bool {
	if len(s) < 20 {
		return false
	}
	// URLs are never secrets — they're endpoint addresses.
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return false
	}
	// Comma-separated word lists are enum/flag values, not secrets
	// (e.g. "signature,expiry,issuer,audience").
	if isWordList(s) {
		return false
	}
	// Values containing spaces are human-readable text, not secrets.
	if strings.ContainsRune(s, ' ') {
		return false
	}
	// Count alphanumeric and secret-like characters.
	count := 0
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '=' || c == '+' || c == '/' || c == ':' {
			count++
		}
	}
	// If > 70% alphanumeric/secret-like, consider it a secret.
	return float64(count)/float64(len(s)) > 0.7
}

// isWordList returns true if s is a comma-separated list of short lowercase words
// (e.g. "signature,expiry,issuer,audience"). These are option/enum values, not secrets.
func isWordList(s string) bool {
	parts := strings.Split(s, ",")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) == 0 || len(p) > 30 {
			return false
		}
		for _, c := range p {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
				return false
			}
		}
	}
	return true
}

// l4CheckSecuritySteps recursively checks security enforcement steps (validate_token,
// validate_api_key, validate_introspection) for risky on_failure: continue patterns.
// Bypassing failure halts on these steps is a critical security risk.
func l4CheckSecuritySteps(steps []control.StepConfig, flowName string, loc SourceLocation) []LintIssue {
	var issues []LintIssue

	for _, step := range steps {
		// Check if this is a security enforcement step.
		isSecurityStep := false
		switch step.Action {
		case "validate_token", "validate_api_key", "validate_introspection":
			isSecurityStep = true
		}

		if isSecurityStep {
			// Check if on_failure is set to "continue" in the Input map.
			if onFailure, ok := step.Input["on_failure"]; ok && onFailure == "continue" {
				issues = append(issues, LintIssue{
					Severity: SeverityWarning,
					Rule:     "security_step_on_failure_continue",
					File:     loc.File,
					Line:     loc.Line,
					Message: fmt.Sprintf(
						"on_failure:continue on security step %q bypasses auth enforcement",
						step.Action),
					Suggestion: "remove on_failure:continue or use on_failure:jump to an error handler instead to enforce security",
				})
			}
		}

		// Recurse into nested steps.
		issues = append(issues, l4CheckSecuritySteps(step.Do, flowName, loc)...)
		for _, branch := range step.Branches {
			issues = append(issues, l4CheckSecuritySteps(branch.Flow, flowName, loc)...)
		}
	}

	return issues
}

// l4CountSteps recursively counts all steps (including nested in do/branches).
func l4CountSteps(steps []control.StepConfig) int {
	count := len(steps)
	for _, step := range steps {
		count += l4CountSteps(step.Do)
		for _, branch := range step.Branches {
			count += l4CountSteps(branch.Flow)
		}
	}
	return count
}

// l4CheckAPI checks an API definition for advisory warnings.
func l4CheckAPI(api control.ApiUpdate, loc SourceLocation) []LintIssue {
	var issues []LintIssue

	// Check if API has no rate limit policy.
	hasPolicy := api.RateLimitName != "" || api.RLConfig != "" || len(api.RateLimitPolicies) > 0
	for _, ep := range api.EndpointConfigs {
		if ep.RateLimitName != "" || ep.RLConfig != "" || len(ep.RateLimitPolicies) > 0 {
			hasPolicy = true
			break
		}
	}

	if !hasPolicy && !api.SkipRateLimit {
		issues = append(issues, LintIssue{
			Severity: SeverityInfo,
			Rule:     "no_rate_limit",
			File:     loc.File,
			Line:     loc.Line,
			Message: fmt.Sprintf(
				"API %s %q has no rate limit policy defined; API is unprotected against abuse",
				methodOrAny(api.Method), api.Path),
			Suggestion: "define a rate_limit_policies entry, or set skip_rate_limit: true if rate limiting is not needed",
		})
	}

	return issues
}

// lintStepRequiredFields validates step-specific required fields
func lintStepRequiredFields(
	step control.StepConfig,
	flow control.FlowUpdate,
	stepNum int,
	loc SourceLocation,
) []LintIssue {
	var issues []LintIssue
	for _, nested := range step.Do {
		issues = append(issues, lintStepRequiredFields(nested, flow, 1, loc)...)
	}
	for _, branch := range step.Branches {
		for _, nested := range branch.Flow {
			issues = append(issues, lintStepRequiredFields(nested, flow, 1, loc)...)
		}
	}
	if step.Action == "" {
		return issues
	}
	flowName := flow.Name
	flowType := flow.Type
	if flowType != "" && flowType != control.FlowTypeAny {
		incompatibleWS := []string{"set_response_status", "set_response_header", "bind_body"}
		incompatibleScheduled := []string{"ws_send", "ws_close", "ws_subscribe", "ws_unsubscribe", "bind_body", "bind_header"}
		isIncompatibleWS := false
		isIncompatibleScheduled := false
		for _, action := range incompatibleWS {
			if step.Action == action {
				isIncompatibleWS = true
				break
			}
		}
		for _, action := range incompatibleScheduled {
			if step.Action == action {
				isIncompatibleScheduled = true
				break
			}
		}
		if (flowType == control.FlowTypeWSMessage || flowType == control.FlowTypeWSConnect || flowType == control.FlowTypeWSDisconnect) && isIncompatibleWS {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "flow_type_step_incompatible",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s (type: %s) step %d (%s): action %s produces HTTP response, incompatible with WebSocket flows", flowName, flowType, stepNum, step.Action, step.Action),
				Suggestion: "remove this step or change flow type to http",
			})
		}
		if flowType == control.FlowTypeScheduled && isIncompatibleScheduled {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "flow_type_step_incompatible",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s (type: %s) step %d (%s): action %s requires HTTP/WebSocket session, incompatible with scheduled flows", flowName, flowType, stepNum, step.Action, step.Action),
				Suggestion: "remove this step or change flow type to http",
			})
		}
	}
	switch step.Action {
	case "db_query", "db_query_one":
		if step.Key == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "db_query_missing_key",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (%s): key is required", flowName, stepNum, step.Action),
				Suggestion: "set key to a database data source name",
			})
		}
		if step.Value == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "db_query_missing_value",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (%s): value is required", flowName, stepNum, step.Action),
				Suggestion: "set value to a SQL query string",
			})
		}
		if step.As == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "db_query_missing_as",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (%s): as is recommended for output", flowName, stepNum, step.Action),
				Suggestion: "set as to capture query results",
			})
		}
	case "db_exec":
		if step.Key == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "db_exec_missing_key",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (db_exec): key is required", flowName, stepNum),
				Suggestion: "set key to a database data source name",
			})
		}
		if step.Value == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "db_exec_missing_value",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (db_exec): value is required", flowName, stepNum),
				Suggestion: "set value to a SQL statement",
			})
		}
	case "ws_broadcast_channel":
		hasKeyStatic := step.Key != ""
		hasKeyDynamic := false
		if keyVar, ok := step.Input["key_var"]; ok && keyVar != "" {
			hasKeyDynamic = true
		}
		if !hasKeyStatic && !hasKeyDynamic {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "ws_broadcast_channel_missing_channel",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (ws_broadcast_channel): key or key_var is required", flowName, stepNum),
				Suggestion: "set key to channel name or input.key_var to dynamic channel",
			})
		}
		hasPayload := step.Value != "" || step.BodyVar != ""
		if _, ok := step.Input["message"]; ok {
			hasPayload = true
		}
		if _, ok := step.Input["message_var"]; ok {
			hasPayload = true
		}
		if _, ok := step.Input["payload_var"]; ok {
			hasPayload = true
		}
		if !hasPayload {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "ws_broadcast_channel_missing_payload",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (ws_broadcast_channel): no payload specified", flowName, stepNum),
				Suggestion: "set value, body_var, or input.message_var for payload",
			})
		}
	case "ws_push_session":
		hasSessionStatic := step.Key != ""
		hasSessionDynamic := false
		if sessionVar, ok := step.Input["key_var"]; ok && sessionVar != "" {
			hasSessionDynamic = true
		}
		if !hasSessionStatic && !hasSessionDynamic {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "ws_push_session_missing_session_id",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (ws_push_session): key or key_var is required", flowName, stepNum),
				Suggestion: "set key to session ID or input.key_var to dynamic session",
			})
		}
		hasPayload := step.Value != "" || step.BodyVar != ""
		if _, ok := step.Input["message"]; ok {
			hasPayload = true
		}
		if _, ok := step.Input["message_var"]; ok {
			hasPayload = true
		}
		if _, ok := step.Input["payload_var"]; ok {
			hasPayload = true
		}
		if !hasPayload {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "ws_push_session_missing_payload",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (ws_push_session): no payload specified", flowName, stepNum),
				Suggestion: "set value, body_var, or input.message_var for payload",
			})
		}
	case "ws_upstream_connect":
		if step.Key == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "ws_upstream_connect_missing_key",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (ws_upstream_connect): key is required", flowName, stepNum),
				Suggestion: "set key to upstream service name",
			})
		}
		if step.As == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "ws_upstream_connect_missing_as",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (ws_upstream_connect): as recommended for connection handle", flowName, stepNum),
				Suggestion: "set as to store connection handle",
			})
		}
	case "ws_upstream_disconnect":
		hasKeyStatic := step.Key != ""
		hasKeyDynamic := false
		if keyVar, ok := step.Input["key_var"]; ok && keyVar != "" {
			hasKeyDynamic = true
		}
		if !hasKeyStatic && !hasKeyDynamic {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "ws_upstream_disconnect_missing_key",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (ws_upstream_disconnect): key or key_var is required", flowName, stepNum),
				Suggestion: "set key to connection or input.key_var to dynamic reference",
			})
		}
	case "storage_get":
		if step.Key == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "storage_get_missing_key",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (storage_get): key is required", flowName, stepNum),
				Suggestion: "set key to a storage provider name",
			})
		}
		if step.Value == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "storage_get_missing_value",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (storage_get): value is required", flowName, stepNum),
				Suggestion: "set value to the object key (path) to retrieve",
			})
		}
		if step.As == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "storage_get_missing_as",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (storage_get): as is recommended to capture retrieved content", flowName, stepNum),
				Suggestion: "set as to a slot name to store the retrieved object bytes",
			})
		}
	case "storage_put":
		if step.Key == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "storage_put_missing_key",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (storage_put): key is required", flowName, stepNum),
				Suggestion: "set key to a storage provider name",
			})
		}
		if step.Value == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "storage_put_missing_value",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (storage_put): value is required", flowName, stepNum),
				Suggestion: "set value to the object key (path) to store",
			})
		}
		hasBody := step.BodyVar != "" || step.Variable != "" || step.As != ""
		if !hasBody {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "storage_put_missing_body",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (storage_put): no body source specified", flowName, stepNum),
				Suggestion: "set body_var or as to the slot containing content to store",
			})
		}
	case "storage_delete":
		if step.Key == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "storage_delete_missing_key",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (storage_delete): key is required", flowName, stepNum),
				Suggestion: "set key to a storage provider name",
			})
		}
		if step.Value == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "storage_delete_missing_value",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (storage_delete): value is required", flowName, stepNum),
				Suggestion: "set value to the object key (path) to delete",
			})
		}
	case "send_email":
		if step.Key == "" {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "send_email_missing_key",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (send_email): key is required", flowName, stepNum),
				Suggestion: "set key to email provider name",
			})
		}
		hasRecipient := false
		if _, ok := step.Input["to_var"]; ok {
			hasRecipient = true
		}
		if _, ok := step.Input["to_val"]; ok {
			hasRecipient = true
		}
		if _, ok := step.Input["to"]; ok {
			hasRecipient = true
		}
		if !hasRecipient {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "send_email_missing_recipient",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (send_email): recipient is required", flowName, stepNum),
				Suggestion: "set input.to_var or input.to_val for recipient",
			})
		}
		hasSubject := false
		if _, ok := step.Input["subject_var"]; ok {
			hasSubject = true
		}
		if _, ok := step.Input["subject_val"]; ok {
			hasSubject = true
		}
		if _, ok := step.Input["subject"]; ok {
			hasSubject = true
		}
		if !hasSubject {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "send_email_missing_subject",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (send_email): subject is required", flowName, stepNum),
				Suggestion: "set input.subject_var or input.subject_val",
			})
		}
		hasBody := false
		if _, ok := step.Input["body_var"]; ok {
			hasBody = true
		}
		if _, ok := step.Input["body_val"]; ok {
			hasBody = true
		}
		if _, ok := step.Input["body"]; ok {
			hasBody = true
		}
		if step.Body != "" || step.Value != "" {
			hasBody = true
		}
		if !hasBody {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "send_email_missing_body",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %s step %d (send_email): body is required", flowName, stepNum),
				Suggestion: "set input.body_var or input.body_val",
			})
		}
	}
	return issues
}

// â"€â"€â"€ Wave 5B: Redis, Named Queries, Migrations, and Tenant Isolation Validation â"€â"€â"€â"€â"€â"€

// lintRedisSteps checks that all redis_* steps reference a source name via the Key field.
// Redis steps without a Key (source name) will fail at runtime.
func lintRedisSteps(result LoadResult) []LintIssue {
	var issues []LintIssue
	b := result.Bundle

	for _, flow := range b.Flows {
		loc := result.SourceMap[flow.Name]
		for i, step := range flow.Instructions {
			issues = append(issues, checkRedisStepSource(step, flow.Name, i+1, loc)...)
		}
	}

	return issues
}

// checkRedisStepSource recursively checks a step and nested steps for redis source validation.
func checkRedisStepSource(step control.StepConfig, flowName string, stepNum int, loc SourceLocation) []LintIssue {
	var issues []LintIssue

	if isRedisStep(step.Action) && strings.TrimSpace(step.Key) == "" {
		issues = append(issues, LintIssue{
			Severity:   SeverityError,
			Rule:       "redis-source-required",
			File:       loc.File,
			Line:       loc.Line,
			Message:    fmt.Sprintf("flow %q step %d (type %s): key must specify a redis source name", flowName, stepNum, step.Action),
			Suggestion: "set key to the name of a configured Redis source",
		})
	}

	// Recurse into nested steps.
	for i, nested := range step.Do {
		issues = append(issues, checkRedisStepSource(nested, flowName, i+1, loc)...)
	}
	for _, branch := range step.Branches {
		for i, nested := range branch.Flow {
			issues = append(issues, checkRedisStepSource(nested, flowName, i+1, loc)...)
		}
	}

	return issues
}

// isRedisStep checks if an action type is a redis_* step.
func isRedisStep(t string) bool {
	return len(t) >= 6 && t[:6] == "redis_"
}

// lintNamedQueryRefs checks that db_* steps using "query:" prefix reference a known named query.
func lintNamedQueryRefs(result LoadResult) []LintIssue {
	var issues []LintIssue
	b := result.Bundle

	for _, flow := range b.Flows {
		loc := result.SourceMap[flow.Name]
		for i, step := range flow.Instructions {
			issues = append(issues, checkNamedQueryRef(step, flow.Name, i+1, loc, b.Queries)...)
		}
	}

	return issues
}

// checkNamedQueryRef recursively checks for named query references.
func checkNamedQueryRef(step control.StepConfig, flowName string, stepNum int, loc SourceLocation, queries map[string]datasource.NamedQueryConfig) []LintIssue {
	var issues []LintIssue

	if name, ok := extractQueryRef(step.Action, step.Value); ok {
		if _, found := queries[name]; !found {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "named-query-unknown",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %q step %d: references unknown named query %q", flowName, stepNum, name),
				Suggestion: fmt.Sprintf("add query %q to the queries section or inline the SQL in step value", name),
			})
		}
	}

	// Recurse into nested steps.
	for i, nested := range step.Do {
		issues = append(issues, checkNamedQueryRef(nested, flowName, i+1, loc, queries)...)
	}
	for _, branch := range step.Branches {
		for i, nested := range branch.Flow {
			issues = append(issues, checkNamedQueryRef(nested, flowName, i+1, loc, queries)...)
		}
	}

	return issues
}

// extractQueryRef extracts the named query name from a db_query/db_exec/db_query_one step
// that uses the "query:<name>" value prefix. Returns (name, true) if found, ("", false) otherwise.
func extractQueryRef(stepType, value string) (string, bool) {
	if stepType != "db_query" && stepType != "db_exec" && stepType != "db_query_one" {
		return "", false
	}
	const pfx = "query:"
	if !strings.HasPrefix(value, pfx) {
		return "", false
	}
	return value[len(pfx):], true
}

// lintMigrationVersions checks for duplicate or non-positive migration versions.
func lintMigrationVersions(result LoadResult) []LintIssue {
	var issues []LintIssue
	b := result.Bundle

	seen := make(map[int]bool, len(b.Migrations))
	for _, m := range b.Migrations {
		if m.Version <= 0 {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "migration-version-invalid",
				Message:    fmt.Sprintf("migration %q has invalid version %d (must be > 0)", m.Name, m.Version),
				Suggestion: "set version to a positive integer",
			})
			continue
		}
		if seen[m.Version] {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "migration-version-duplicate",
				Message:    fmt.Sprintf("duplicate migration version %d — each version must be unique", m.Version),
				Suggestion: "choose a unique version number for this migration",
			})
		}
		seen[m.Version] = true
	}
	return issues
}

// lintRedisMultiKeySteps checks that multi-key redis operations (mget, hmget) have Vars or Members set.
func lintRedisMultiKeySteps(result LoadResult) []LintIssue {
	var issues []LintIssue
	b := result.Bundle

	multiKeyTypes := map[string]bool{
		"redis_mget":  true,
		"redis_hmget": true,
	}

	for _, flow := range b.Flows {
		loc := result.SourceMap[flow.Name]
		for i, step := range flow.Instructions {
			issues = append(issues, checkRedisMultiKey(step, flow.Name, i+1, loc, multiKeyTypes)...)
		}
	}

	return issues
}

// checkRedisMultiKey recursively checks multi-key redis steps.
func checkRedisMultiKey(step control.StepConfig, flowName string, stepNum int, loc SourceLocation, multiKeyTypes map[string]bool) []LintIssue {
	var issues []LintIssue

	if multiKeyTypes[step.Action] {
		if len(step.Vars) == 0 && len(step.Members) == 0 {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "redis-multi-key-no-vars",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %q step %d (type %s): vars or members required for multi-key operation", flowName, stepNum, step.Action),
				Suggestion: "set vars to a list of key variable names, or members to a list of member names",
			})
		}
	}

	// Recurse into nested steps.
	for i, nested := range step.Do {
		issues = append(issues, checkRedisMultiKey(nested, flowName, i+1, loc, multiKeyTypes)...)
	}
	for _, branch := range step.Branches {
		for i, nested := range branch.Flow {
			issues = append(issues, checkRedisMultiKey(nested, flowName, i+1, loc, multiKeyTypes)...)
		}
	}

	return issues
}

// lintNamedQueryBatchBy checks that batch_by references a valid SQL parameter.
func lintNamedQueryBatchBy(result LoadResult) []LintIssue {
	var issues []LintIssue
	b := result.Bundle

	for name, q := range b.Queries {
		if q.BatchBy == "" {
			continue
		}
		// Check that the batch_by parameter is referenced in the SQL.
		// The parameter should appear as $<name> or @<name> or :<name> depending on dialect.
		if !strings.Contains(q.SQL, q.BatchBy) &&
			!strings.Contains(q.SQL, "$"+q.BatchBy) &&
			!strings.Contains(q.SQL, "@"+q.BatchBy) &&
			!strings.Contains(q.SQL, ":"+q.BatchBy) {
			issues = append(issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "named-query-batch-by-invalid",
				Message:    fmt.Sprintf("named query %q: batch_by %q not found in SQL", name, q.BatchBy),
				Suggestion: fmt.Sprintf("add parameter %q to the SQL statement or remove batch_by", q.BatchBy),
			})
		}
	}
	return issues
}

// lintRedisZAddScore warns when redis_zadd has no score configured.
func lintRedisZAddScore(result LoadResult) []LintIssue {
	var issues []LintIssue
	b := result.Bundle

	for _, flow := range b.Flows {
		loc := result.SourceMap[flow.Name]
		for i, step := range flow.Instructions {
			issues = append(issues, checkRedisZAddScore(step, flow.Name, i+1, loc)...)
		}
	}

	return issues
}

// checkRedisZAddScore recursively checks redis_zadd score configuration.
func checkRedisZAddScore(step control.StepConfig, flowName string, stepNum int, loc SourceLocation) []LintIssue {
	var issues []LintIssue

	if step.Action == "redis_zadd" {
		if step.Score == 0 {
			issues = append(issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "redis-zadd-no-score",
				File:       loc.File,
				Line:       loc.Line,
				Message:    fmt.Sprintf("flow %q step %d (redis_zadd): score=0 may be unintentional", flowName, stepNum),
				Suggestion: "set score to a non-zero numeric value, or ensure 0 is the intended score",
			})
		}
	}

	// Recurse into nested steps.
	for i, nested := range step.Do {
		issues = append(issues, checkRedisZAddScore(nested, flowName, i+1, loc)...)
	}
	for _, branch := range step.Branches {
		for i, nested := range branch.Flow {
			issues = append(issues, checkRedisZAddScore(nested, flowName, i+1, loc)...)
		}
	}

	return issues
}

// â"€â"€â"€ Utilities â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func methodOrAny(method string) string {
	if method == "" {
		return "ANY"
	}
	return method
}

// mapKeys returns the keys of m as a slice.
func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
