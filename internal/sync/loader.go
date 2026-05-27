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
	"rah/internal/control"
	registrypkg "rah/internal/registry"
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
		Flows:                  []control.FlowUpdate{},
		Apis:                   []control.ApiUpdate{},
		RateLimitConfigsV2:     []registrypkg.RateLimitConfigV2{},
		Tiers:                  []registrypkg.TierDef{},
		UpstreamServices:       []registrypkg.UpstreamServiceDef{},
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

	// Track source locations and merge
	trackSourcesAndMerge(filePath, partial, result)
	return nil
}

// parseAndMergeYAML parses YAML (which may contain multiple documents) and merges each into result.
func parseAndMergeYAML(rawBytes []byte, filePath string, result *LoadResult) error {
	decoder := yaml.NewDecoder(strings.NewReader(string(rawBytes)))

	for {
		var partial control.UnifiedSyncRequest
		err := decoder.Decode(&partial)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return err
		}

		// Track source locations and merge
		trackSourcesAndMerge(filePath, partial, result)
	}

	return nil
}

// trackSourcesAndMerge records source locations for definitions, tracks duplicates as warnings,
// and merges the partial bundle.
func trackSourcesAndMerge(filePath string, partial control.UnifiedSyncRequest, result *LoadResult) {
	// Track flow sources and detect duplicates
	for _, flow := range partial.Flows {
		key := flow.Name
		if existing, ok := result.SourceMap[key]; ok && existing.File != "" {
			// Duplicate detected
			result.Issues = append(result.Issues, LintIssue{
				Severity:   SeverityWarning,
				Rule:       "duplicate_flow",
				File:       filePath,
				Line:       1,
				Message:    fmt.Sprintf("Flow '%s' already defined in %s:%d", key, existing.File, existing.Line),
				Suggestion: "Remove the duplicate or rename the flow",
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

	return result
}
