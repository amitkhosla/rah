package sync

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/control"
	"github.com/amitkhosla/rah/internal/mcpreg"
	registrypkg "github.com/amitkhosla/rah/internal/registry"
)

// LoadResult is the output of Load: the merged bundle plus source map and lint issues.
type LoadResult struct {
	Bundle    control.UnifiedSyncRequest
	SourceMap SourceMap
	Issues    []LintIssue
}

// Load recursively walks the given directory, collects *.yaml and *.json files,
// parses them into partial UnifiedSyncRequest objects, and merges them with
// deduplication. Returns a LoadResult containing the merged bundle, source map,
// and any lint issues discovered during the process.
func Load(dir string) (LoadResult, error) {
	var result LoadResult
	result.SourceMap = make(SourceMap)
	result.Issues = []LintIssue{}
	result.Bundle = control.UnifiedSyncRequest{
		Flows:              []control.FlowUpdate{},
		Apis:               []control.ApiUpdate{},
		RateLimitConfigsV2: []registrypkg.RateLimitConfigV2{},
		Tiers:              []registrypkg.TierDef{},
		UpstreamServices:   []registrypkg.UpstreamServiceDef{},
		Tenants:            []control.TenantSyncDef{},
		CacheSeeds:         []control.CacheSeedDef{},
		APIKeys:            []control.APIKeySyncDef{},
		LLMModels:          []config.LLMModelConfig{},
		MCPServers:          []config.MCPServerConfig{},
		VirtualMCPServers:   []mcpreg.VirtualMCPServerDef{},
		APITools:            []mcpreg.APIToolDef{},
		Schedules:           []control.ScheduleConfig{},
	}

	// Collect all .yaml and .json files from the directory tree
	fileList, err := collectFiles(dir)
	if err != nil {
		return result, fmt.Errorf("failed to collect files: %w", err)
	}

	// Sort files for deterministic merge: by path depth (fewer separators first),
	// then alphabetically within same depth
	sort.Slice(fileList, func(i, j int) bool {
		depthI := strings.Count(fileList[i], string(filepath.Separator))
		depthJ := strings.Count(fileList[j], string(filepath.Separator))
		if depthI != depthJ {
			return depthI < depthJ
		}
		return fileList[i] < fileList[j]
	})

	// Parse and merge each file
	for _, absPath := range fileList {
		relPath := getRelativePath(dir, absPath)
		rawBytes, err := readFile(absPath)
		if err != nil {
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityError,
				Rule:       "file_read_error",
				File:       relPath,
				Line:       0,
				Message:    fmt.Sprintf("Failed to read file: %v", err),
				Suggestion: "Check file permissions and path",
			})
			continue
		}

		// Determine file type and parse accordingly
		isJSON := strings.HasSuffix(strings.ToLower(absPath), ".json")
		if isJSON {
			// Parse as single JSON document
			if err := parseAndMergeJSON(rawBytes, relPath, &result); err != nil {
				result.Issues = append(result.Issues, LintIssue{
					Severity:   SeverityError,
					Rule:       "json_parse_error",
					File:       relPath,
					Line:       0,
					Message:    fmt.Sprintf("Failed to parse JSON: %v", err),
					Suggestion: "Verify JSON syntax",
				})
			}
		} else {
			// Parse as YAML (may contain multiple documents separated by ---)
			if err := parseAndMergeYAML(rawBytes, relPath, &result); err != nil {
				result.Issues = append(result.Issues, LintIssue{
					Severity:   SeverityError,
					Rule:       "yaml_parse_error",
					File:       relPath,
					Line:       0,
					Message:    fmt.Sprintf("Failed to parse YAML: %v", err),
					Suggestion: "Verify YAML syntax",
				})
			}
		}
	}

	return result, nil
}

// collectFiles recursively walks dir and returns a slice of absolute paths
// to all *.yaml and *.json files found.
func collectFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".json") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// readFile reads the entire contents of a file.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// getRelativePath returns path relative to dir.
func getRelativePath(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return path
	}
	return rel
}

// parseAndMergeJSON parses a single JSON document and merges it into the result.
func parseAndMergeJSON(rawBytes []byte, filePath string, result *LoadResult) error {
	var partial control.UnifiedSyncRequest
	if err := json.Unmarshal(rawBytes, &partial); err != nil {
		return err
	}

	compileDSLFlowsInPartial(&partial)
	trackSourcesAndMerge(filePath, partial, result)
	return nil
}

// parseAndMergeYAML parses YAML (which may contain multiple documents) and merges each into result.
func parseAndMergeYAML(rawBytes []byte, filePath string, result *LoadResult) error {
	decoder := yaml.NewDecoder(strings.NewReader(string(rawBytes)))

	for {
		// Decode to interface{} first, then re-encode as JSON so that the
		// existing json struct tags on control.UnifiedSyncRequest are applied.
		// This handles snake_case keys (e.g. flow_name â†’ FlowName) that yaml.v3
		// would not map correctly using its own lowercase-only field resolution.
		var rawDoc interface{}
		err := decoder.Decode(&rawDoc)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return err
		}
		if rawDoc == nil {
			continue
		}

		jsonBytes, err := json.Marshal(rawDoc)
		if err != nil {
			return fmt.Errorf("yaml-to-json conversion: %w", err)
		}

		var partial control.UnifiedSyncRequest
		if err := json.Unmarshal(jsonBytes, &partial); err != nil {
			return err
		}

		compileDSLFlowsInPartial(&partial)
		trackSourcesAndMerge(filePath, partial, result)
	}

	return nil
}

// compileDSLFlowsInPartial compiles the Code field of every flow in partial
// that has code but no Instructions, populating Instructions with the result.
// Any anonymous flows auto-generated for inline if/else blocks are appended to
// partial.Flows so they appear in the bundle and source map.
func compileDSLFlowsInPartial(partial *control.UnifiedSyncRequest) {
	var extraFlows []control.FlowUpdate
	for i := range partial.Flows {
		flow := &partial.Flows[i]
		if flow.Code == "" || len(flow.Instructions) > 0 {
			continue
		}
		dslResult, err := control.ParseDSL(flow.Code)
		if err != nil {
			// Leave Instructions empty; the linter will report it.
			continue
		}
		flow.Instructions = dslResult.Steps
		for name, steps := range dslResult.ExtraFlows {
			extraFlows = append(extraFlows, control.FlowUpdate{
				Name:         name,
				Instructions: steps,
				Action:       flow.Action,
			})
		}
	}
	partial.Flows = append(partial.Flows, extraFlows...)
}

// trackSourcesAndMerge records source locations for definitions, tracks duplicates as warnings,
// and merges the partial bundle.
func trackSourcesAndMerge(filePath string, partial control.UnifiedSyncRequest, result *LoadResult) {
	// Track flow sources and detect duplicates.
	// A flow name must be unique across the entire bundle â€” even with action:upsert,
	// defining the same flow in multiple files leads to load-order-dependent behaviour
	// and makes it impossible to know which definition is authoritative.
	for _, flow := range partial.Flows {
		key := flow.Name
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning, // escalated to Error by lintLevel2
				Rule:       "duplicate_flow",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("flow %q is already defined in %s â€” each flow must appear in exactly one file", key, existing.File),
				Suggestion: fmt.Sprintf("Remove the duplicate definition from %s, or rename it if you need a distinct variation", filePath),
			})
		}
		result.SourceMap[key] = SourceLocation{File: filePath, Line: 1}
	}

	// Track API sources and detect duplicates
	for _, api := range partial.Apis {
		// Use method+path as the key for SourceMap
		key := apiKey(api.Method, api.Path)
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			// Duplicate detected
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "duplicate_api",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("API '%s' already defined in %s:%d", key, existing.File, existing.Line),
				Suggestion: "Remove the duplicate or modify the method/path",
			})
		}
		result.SourceMap[key] = SourceLocation{File: filePath, Line: 1}
	}

	// Track rate limit config sources and detect duplicates
	for _, rl := range partial.RateLimitConfigsV2 {
		key := "rl:" + rl.Name
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			// Duplicate detected
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "duplicate_rate_limit_config",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("Rate limit config '%s' already defined in %s:%d", rl.Name, existing.File, existing.Line),
				Suggestion: "Remove the duplicate or rename the config",
			})
		}
		result.SourceMap[key] = SourceLocation{File: filePath, Line: 1}
	}

	// Track tier sources
	for _, tier := range partial.Tiers {
		key := "tier:" + tier.Name
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "duplicate_tier",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("Tier '%s' already defined in %s:%d", tier.Name, existing.File, existing.Line),
				Suggestion: "Remove the duplicate or rename the tier",
			})
		}
		result.SourceMap[key] = SourceLocation{File: filePath, Line: 1}
	}

	// Track upstream service sources
	for _, svc := range partial.UpstreamServices {
		key := "upstream:" + svc.Name
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "duplicate_upstream_service",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("Upstream service '%s' already defined in %s:%d", svc.Name, existing.File, existing.Line),
				Suggestion: "Remove the duplicate or rename the service",
			})
		}
		result.SourceMap[key] = SourceLocation{File: filePath, Line: 1}
	}

	// Track tenant sources and detect duplicates
	for _, t := range partial.Tenants {
		if len(t.Aliases) == 0 {
			continue
		}
		key := "tenant:" + t.Aliases[0]
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "duplicate_tenant",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("tenant %q already defined in %s â€” last definition wins", t.Aliases[0], existing.File),
				Suggestion: "Remove the duplicate definition or consolidate into one file",
			})
		}
		result.SourceMap[key] = SourceLocation{File: filePath, Line: 1}
	}

	// Track cache seed sources and detect duplicates
	for _, seed := range partial.CacheSeeds {
		if seed.Key == "" {
			continue
		}
		key := "cacheseed:" + seed.Key
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "duplicate_cache_seed",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("cache seed %q already defined in %s â€” last definition wins", seed.Key, existing.File),
				Suggestion: "Remove the duplicate or consolidate into one file",
			})
		}
		result.SourceMap[key] = SourceLocation{File: filePath, Line: 1}
	}

	// Track API key sources and detect duplicates
	for _, k := range partial.APIKeys {
		if k.Alias == "" {
			continue
		}
		key := "apikey:" + k.Alias
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "duplicate_api_key",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("api_key %q already defined in %s â€” last definition wins", k.Alias, existing.File),
				Suggestion: "Remove the duplicate or consolidate into one file",
			})
		}
		result.SourceMap[key] = SourceLocation{File: filePath, Line: 1}
	}

	// Track LLM model sources and detect duplicates.
	for _, m := range partial.LLMModels {
		if m.Alias == "" {
			continue
		}
		key := "llm_model:" + m.Alias
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "duplicate_llm_model",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("LLM model %q already defined in %s — last definition wins", m.Alias, existing.File),
				Suggestion: "Remove the duplicate or consolidate into one file",
			})
		}
		result.SourceMap[key] = SourceLocation{File: filePath, Line: 1}
	}

	// Track MCP server sources and detect duplicates.
	for _, srv := range partial.MCPServers {
		if srv.Alias == "" {
			continue
		}
		key := "mcp_server:" + srv.Alias
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "duplicate_mcp_server",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("MCP server %q already defined in %s — last definition wins", srv.Alias, existing.File),
				Suggestion: "Remove the duplicate or consolidate into one file",
			})
		}
		result.SourceMap[key] = SourceLocation{File: filePath, Line: 1}
	}

	// Track virtual MCP server sources and detect duplicates.
	for _, def := range partial.VirtualMCPServers {
		if def.Name == "" {
			continue
		}
		key := "virtual_mcp:" + def.Name
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "duplicate_virtual_mcp_server",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("virtual MCP server %q already defined in %s — last definition wins", def.Name, existing.File),
				Suggestion: "Remove the duplicate or consolidate into one file",
			})
		}
		result.SourceMap[key] = SourceLocation{File: filePath, Line: 1}
	}

	// Track API tool sources and detect duplicates.
	for _, tool := range partial.APITools {
		if tool.Name == "" {
			continue
		}
		key := "api_tool:" + tool.Name
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "duplicate_api_tool",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("API tool %q already defined in %s — last definition wins", tool.Name, existing.File),
				Suggestion: "Remove the duplicate or consolidate into one file",
			})
		}
		result.SourceMap[key] = SourceLocation{File: filePath, Line: 1}
	}

	// Track schedule sources and detect duplicates.
	for _, sched := range partial.Schedules {
		if sched.Name == "" {
			continue
		}
		key := "schedule:" + sched.Name
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "duplicate_schedule",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("schedule %q already defined in %s — last definition wins", sched.Name, existing.File),
				Suggestion: "Remove the duplicate or consolidate into one file",
			})
		}
		result.SourceMap[key] = SourceLocation{File: filePath, Line: 1}
	}

	// Merge bundles (concatenate and deduplicate)
	result.Bundle = mergeBundles(result.Bundle, partial)
}

// apiKey creates a composite key for API deduplication (method + path).
func apiKey(method, path string) string {
	if method == "" {
		method = "ANY"
	}
	return method + " " + path
}

// mergeBundles merges newBundle into existing, deduplicating by name (last-file-wins).
// This maintains maps of seen items and updates the result bundle to only include
// the last occurrence of each definition.
func mergeBundles(existing, newBundle control.UnifiedSyncRequest) control.UnifiedSyncRequest {
	// Merge flows: keep last occurrence by name
	existingFlows := make(map[string]control.FlowUpdate)
	for _, flow := range existing.Flows {
		existingFlows[flow.Name] = flow
	}
	for _, flow := range newBundle.Flows {
		existingFlows[flow.Name] = flow
	}

	result := existing
	result.Flows = make([]control.FlowUpdate, 0, len(existingFlows))
	for _, flow := range existingFlows {
		result.Flows = append(result.Flows, flow)
	}

	// Merge APIs: keep last occurrence by (method, path) pair
	existingAPIs := make(map[string]control.ApiUpdate)
	for _, api := range existing.Apis {
		key := apiKey(api.Method, api.Path)
		existingAPIs[key] = api
	}
	for _, api := range newBundle.Apis {
		key := apiKey(api.Method, api.Path)
		existingAPIs[key] = api
	}

	result.Apis = make([]control.ApiUpdate, 0, len(existingAPIs))
	for _, api := range existingAPIs {
		result.Apis = append(result.Apis, api)
	}

	// Merge rate limit configs: keep last occurrence by name
	existingRLs := make(map[string]registrypkg.RateLimitConfigV2)
	for _, rl := range existing.RateLimitConfigsV2 {
		existingRLs[rl.Name] = rl
	}
	for _, rl := range newBundle.RateLimitConfigsV2 {
		existingRLs[rl.Name] = rl
	}

	result.RateLimitConfigsV2 = make([]registrypkg.RateLimitConfigV2, 0, len(existingRLs))
	for _, rl := range existingRLs {
		result.RateLimitConfigsV2 = append(result.RateLimitConfigsV2, rl)
	}

	// Merge tiers: keep last occurrence by name
	existingTiers := make(map[string]registrypkg.TierDef)
	for _, tier := range existing.Tiers {
		existingTiers[tier.Name] = tier
	}
	for _, tier := range newBundle.Tiers {
		existingTiers[tier.Name] = tier
	}

	result.Tiers = make([]registrypkg.TierDef, 0, len(existingTiers))
	for _, tier := range existingTiers {
		result.Tiers = append(result.Tiers, tier)
	}

	// Merge upstream services: keep last occurrence by name
	existingUpstream := make(map[string]registrypkg.UpstreamServiceDef)
	for _, svc := range existing.UpstreamServices {
		existingUpstream[svc.Name] = svc
	}
	for _, svc := range newBundle.UpstreamServices {
		existingUpstream[svc.Name] = svc
	}

	result.UpstreamServices = make([]registrypkg.UpstreamServiceDef, 0, len(existingUpstream))
	for _, svc := range existingUpstream {
		result.UpstreamServices = append(result.UpstreamServices, svc)
	}

	// Merge tenants: keep last occurrence by primary alias
	existingTenants := make(map[string]control.TenantSyncDef)
	for _, t := range existing.Tenants {
		if len(t.Aliases) > 0 {
			existingTenants[t.Aliases[0]] = t
		}
	}
	for _, t := range newBundle.Tenants {
		if len(t.Aliases) > 0 {
			existingTenants[t.Aliases[0]] = t
		}
	}
	result.Tenants = make([]control.TenantSyncDef, 0, len(existingTenants))
	for _, t := range existingTenants {
		result.Tenants = append(result.Tenants, t)
	}

	// Merge cache seeds: keep last occurrence by key+tenants composite
	existingSeeds := make(map[string]control.CacheSeedDef)
	for _, seed := range existing.CacheSeeds {
		k := seed.Key + "|" + strings.Join(seed.Tenants, ",")
		existingSeeds[k] = seed
	}
	for _, seed := range newBundle.CacheSeeds {
		k := seed.Key + "|" + strings.Join(seed.Tenants, ",")
		existingSeeds[k] = seed
	}
	result.CacheSeeds = make([]control.CacheSeedDef, 0, len(existingSeeds))
	for _, seed := range existingSeeds {
		result.CacheSeeds = append(result.CacheSeeds, seed)
	}

	// Merge API keys: keep last occurrence by alias
	existingAPIKeys := make(map[string]control.APIKeySyncDef)
	for _, k := range existing.APIKeys {
		existingAPIKeys[k.Alias] = k
	}
	for _, k := range newBundle.APIKeys {
		existingAPIKeys[k.Alias] = k
	}
	result.APIKeys = make([]control.APIKeySyncDef, 0, len(existingAPIKeys))
	for _, k := range existingAPIKeys {
		result.APIKeys = append(result.APIKeys, k)
	}

	// Merge LLM models: keep last occurrence by alias.
	existingLLMModels := make(map[string]config.LLMModelConfig)
	for _, m := range existing.LLMModels {
		existingLLMModels[m.Alias] = m
	}
	for _, m := range newBundle.LLMModels {
		existingLLMModels[m.Alias] = m
	}
	result.LLMModels = make([]config.LLMModelConfig, 0, len(existingLLMModels))
	for _, m := range existingLLMModels {
		result.LLMModels = append(result.LLMModels, m)
	}

	// Merge MCP servers: keep last occurrence by alias.
	existingMCPServers := make(map[string]config.MCPServerConfig)
	for _, srv := range existing.MCPServers {
		existingMCPServers[srv.Alias] = srv
	}
	for _, srv := range newBundle.MCPServers {
		existingMCPServers[srv.Alias] = srv
	}
	result.MCPServers = make([]config.MCPServerConfig, 0, len(existingMCPServers))
	for _, srv := range existingMCPServers {
		result.MCPServers = append(result.MCPServers, srv)
	}

	// Merge virtual MCP servers: keep last occurrence by name.
	existingVirtualMCP := make(map[string]mcpreg.VirtualMCPServerDef)
	for _, def := range existing.VirtualMCPServers {
		existingVirtualMCP[def.Name] = def
	}
	for _, def := range newBundle.VirtualMCPServers {
		existingVirtualMCP[def.Name] = def
	}
	result.VirtualMCPServers = make([]mcpreg.VirtualMCPServerDef, 0, len(existingVirtualMCP))
	for _, def := range existingVirtualMCP {
		result.VirtualMCPServers = append(result.VirtualMCPServers, def)
	}

	// Merge API tools: keep last occurrence by name.
	existingAPITools := make(map[string]mcpreg.APIToolDef)
	for _, tool := range existing.APITools {
		existingAPITools[tool.Name] = tool
	}
	for _, tool := range newBundle.APITools {
		existingAPITools[tool.Name] = tool
	}
	result.APITools = make([]mcpreg.APIToolDef, 0, len(existingAPITools))
	for _, tool := range existingAPITools {
		result.APITools = append(result.APITools, tool)
	}

	// Merge schedules: keep last occurrence by name.
	existingSchedules := make(map[string]control.ScheduleConfig)
	for _, sched := range existing.Schedules {
		existingSchedules[sched.Name] = sched
	}
	for _, sched := range newBundle.Schedules {
		existingSchedules[sched.Name] = sched
	}
	result.Schedules = make([]control.ScheduleConfig, 0, len(existingSchedules))
	for _, sched := range existingSchedules {
		result.Schedules = append(result.Schedules, sched)
	}

	return result
}
