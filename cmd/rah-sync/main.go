package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/amitkhosla/rah/internal/sync"
	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcommand := os.Args[1]

	switch subcommand {
	case "lint":
		exitCode := lintCmd(os.Args[2:])
		os.Exit(exitCode)
	case "publish":
		exitCode := publishCmd(os.Args[2:])
		os.Exit(exitCode)
	case "promote":
		exitCode := promoteCmd(os.Args[2:])
		os.Exit(exitCode)
	case "diff":
		exitCode := diffCmd(os.Args[2:])
		os.Exit(exitCode)
	case "export":
		exitCode := exportCmd(os.Args[2:])
		os.Exit(exitCode)
	case "-h", "--help", "help":
		printUsage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `rah-sync - RAH bundle management CLI

Usage:
  rah-sync lint <dir> [options]
  rah-sync publish <dir> --studio <URL> [options]
  rah-sync promote <release-id> --env <env> --studio <URL> [options]
  rah-sync diff <release-A> <release-B> --studio <URL>
  rah-sync export --release <id> --studio <URL> [options]

Subcommands:
  lint       Lint a bundle directory for configuration errors
  publish    Upload a bundle to studio and optionally deploy it
  promote    Deploy an existing release to an environment
  diff       Show differences between two releases
  export     Export a release as YAML or JSON

Common Options (all subcommands):
  --token <token>     Bearer token for authentication (or env RAH_SYNC_TOKEN)

Lint Options:
  --studio <URL>      Studio server URL for server-side validation (optional)
  --output format     Output format: text or json (default: text)
  --strict            Treat warnings as errors for exit code (default: false)

Publish Options:
  --studio <URL>      Studio server URL (required)
  --tag <tag>         Release tag (optional, e.g. v1.2.3)
  --auto-deploy <env> Automatically deploy to environment after publish (optional)
  --git-commit <sha>  Git commit SHA (optional)
  --git-branch <name> Git branch name (optional)
  --include <spec>    Include filter: type:name (repeatable, comma-separated, optional)
  --exclude <spec>    Exclude filter: type:name (repeatable, comma-separated, optional)
  --output format     Output format: text or json (default: text)

Promote Options:
  --env <env>         Target environment (required, e.g. uat, prd)
  --studio <URL>      Studio server URL (required)
  --by <user>         User email performing the deployment (optional)
  --include <spec>    Include filter: type:name (repeatable, comma-separated, optional)
  --exclude <spec>    Exclude filter: type:name (repeatable, comma-separated, optional)
  --output format     Output format: text or json (default: text)

Diff Options:
  --studio <URL>      Studio server URL (required)
  --output format     Output format: text or json (default: text)

Export Options:
  --release <id>      Release ID to export (required)
  --studio <URL>      Studio server URL (required)
  --format <format>   Output format: yaml or json (default: yaml)
  --out <path>        Output file path (optional, defaults to <id>.yaml or <id>.json)

Exit Codes:
  0  Success
  1  Errors found or operation failed
  2  (Reserved for future use)

Examples:
  rah-sync lint ./definitions
  rah-sync lint ./definitions --strict --output json
  rah-sync publish ./definitions --studio https://studio.example.com --tag v1.0.0
  rah-sync publish ./definitions --studio https://studio.example.com --auto-deploy uat
  rah-sync promote release-123 --env prd --studio https://studio.example.com
  rah-sync diff release-123 release-456 --studio https://studio.example.com
  rah-sync export --release release-123 --studio https://studio.example.com --format yaml
  rah-sync export --release release-123 --studio https://studio.example.com --format json --out my-bundle.json
`)
}

func lintCmd(args []string) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	fs.Usage = func() {}

	studioURL := fs.String("studio", "", "Studio server URL (optional)")
	outputFormat := fs.String("output", "text", "Output format: text or json")
	strict := fs.Bool("strict", false, "Treat warnings as errors")
	token := fs.String("token", "", "Bearer token (or env RAH_SYNC_TOKEN)")

	// Suppress default usage output
	fs.SetOutput(io.Discard)

	// Parse flags
	err := fs.Parse(hoistFlags(args))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		return 1
	}

	// Get token from flag or environment
	authToken := *token
	if authToken == "" {
		authToken = os.Getenv("RAH_SYNC_TOKEN")
	}

	// Get positional argument: directory
	posArgs := fs.Args()
	if len(posArgs) == 0 {
		fmt.Fprintf(os.Stderr, "Error: directory argument required\n")
		fmt.Fprintf(os.Stderr, "Usage: rah-sync lint <dir> [options]\n")
		return 1
	}

	bundleDir := posArgs[0]

	// Validate directory exists
	info, err := os.Stat(bundleDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot access directory %q: %v\n", bundleDir, err)
		return 1
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: %q is not a directory\n", bundleDir)
		return 1
	}

	// Run local linting (Levels 0-3)
	opts := sync.LintOptions{
		StrictMode:   *strict,
		OutputFormat: *outputFormat,
	}

	result, err := sync.Run(bundleDir, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running linter: %v\n", err)
		return 1
	}

	// If --studio URL provided: post to server for additional checks
	if *studioURL != "" {
		serverIssues, err := lintWithStudio(bundleDir, *studioURL, authToken)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Studio validation failed: %v\n", err)
			// Don't exit - continue with local results only
		} else {
			// Merge server-side issues into result
			result.Issues = append(result.Issues, serverIssues...)

			// Recount severities
			errorCount := 0
			warningCount := 0
			infoCount := 0
			for _, issue := range result.Issues {
				switch issue.Severity {
				case sync.SeverityError:
					errorCount++
				case sync.SeverityWarning:
					warningCount++
				case sync.SeverityInfo:
					infoCount++
				}
			}
			result.ErrorCount = errorCount
			result.WarningCount = warningCount
			result.InfoCount = infoCount

			// Re-format with merged results
			if opts.OutputFormat == "json" {
				sync.FormatJSON(result, os.Stdout)
			} else {
				sync.FormatText(result, os.Stdout)
			}
		}
	}

	// Determine exit code
	if result.ErrorCount > 0 {
		return 1
	}
	if *strict && result.WarningCount > 0 {
		return 1
	}
	return 0
}

// lintWithStudio posts the bundle to the studio server's dry-run endpoint
// and collects server-side lint issues.
func lintWithStudio(bundleDir string, studioURL string, token string) ([]sync.LintIssue, error) {
	// Load the bundle from disk
	loadResult, err := sync.Load(bundleDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load bundle: %w", err)
	}

	// Convert the bundle to YAML
	bundleYAML, err := yaml.Marshal(loadResult.Bundle)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal bundle: %w", err)
	}

	// POST to /api/releases?dry_run=true
	endpoint := studioURL + "/api/releases?dry_run=true"

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(bundleYAML))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/yaml")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to post to studio: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("studio returned error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Parse the response
	// Expected structure: {"lint_summary": {...}, "issues": [...], ...}
	var respData struct {
		Issues []sync.LintIssue `json:"issues"`
	}

	err = json.Unmarshal(respBody, &respData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse studio response: %w", err)
	}

	return respData.Issues, nil
}

// publishCmd handles: publish <dir> --studio URL [--tag TAG] [--auto-deploy ENV] [--git-commit SHA] [--git-branch BRANCH] [--include SPEC] [--exclude SPEC] [--token TOKEN]
func publishCmd(args []string) int {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.Usage = func() {}

	studioURL := fs.String("studio", "", "Studio server URL (required)")
	tag := fs.String("tag", "", "Release tag (optional)")
	autoDeploy := fs.String("auto-deploy", "", "Auto-deploy to environment (optional)")
	gitCommit := fs.String("git-commit", "", "Git commit SHA (optional)")
	gitBranch := fs.String("git-branch", "", "Git branch name (optional)")
	outputFormat := fs.String("output", "text", "Output format: text or json")
	token := fs.String("token", "", "Bearer token (or env RAH_SYNC_TOKEN)")
	include := fs.String("include", "", "Include filters: type:name (repeatable, comma-separated)")
	exclude := fs.String("exclude", "", "Exclude filters: type:name (repeatable, comma-separated)")

	fs.SetOutput(io.Discard)

	err := fs.Parse(hoistFlags(args))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		return 1
	}

	// Get token from flag or environment
	authToken := *token
	if authToken == "" {
		authToken = os.Getenv("RAH_SYNC_TOKEN")
	}

	posArgs := fs.Args()
	if len(posArgs) == 0 {
		fmt.Fprintf(os.Stderr, "Error: directory argument required\n")
		fmt.Fprintf(os.Stderr, "Usage: rah-sync publish <dir> --studio <URL> [options]\n")
		return 1
	}

	if *studioURL == "" {
		fmt.Fprintf(os.Stderr, "Error: --studio URL is required\n")
		return 1
	}

	bundleDir := posArgs[0]

	// Validate directory exists
	info, err := os.Stat(bundleDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot access directory %q: %v\n", bundleDir, err)
		return 1
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: %q is not a directory\n", bundleDir)
		return 1
	}

	// Run full lint (Levels 0-4)
	opts := sync.LintOptions{
		StrictMode:   false, // Don't fail on warnings for publish
		OutputFormat: *outputFormat,
	}

	result, err := sync.Run(bundleDir, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running linter: %v\n", err)
		return 1
	}

	// Abort on lint errors
	if result.ErrorCount > 0 {
		return 1
	}

	// Load bundle and publish
	loadResult, err := sync.Load(bundleDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading bundle: %v\n", err)
		return 1
	}

	releaseID, err := publishBundle(*studioURL, loadResult.Bundle, *tag, *gitCommit, *gitBranch, authToken, *include, *exclude)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error publishing bundle: %v\n", err)
		return 1
	}

	fmt.Printf("Published release: %s\n", releaseID)

	// If --auto-deploy is set, immediately promote to that environment
	if *autoDeploy != "" {
		fmt.Printf("Deploying to %s...\n", *autoDeploy)
		deployErr := deployRelease(*studioURL, releaseID, *autoDeploy, "", authToken, *include, *exclude)
		if deployErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: deployment failed: %v\n", deployErr)
			return 1
		}
		fmt.Printf("Successfully deployed to %s\n", *autoDeploy)
	}

	return 0
}

// promoteCmd handles: promote <release-id> --env ENV --studio URL [--by USER] [--include SPEC] [--exclude SPEC] [--token TOKEN]
func promoteCmd(args []string) int {
	fs := flag.NewFlagSet("promote", flag.ContinueOnError)
	fs.Usage = func() {}

	env := fs.String("env", "", "Target environment (required)")
	studioURL := fs.String("studio", "", "Studio server URL (required)")
	byUser := fs.String("by", "", "User email (optional)")
	outputFormat := fs.String("output", "text", "Output format: text or json")
	token := fs.String("token", "", "Bearer token (or env RAH_SYNC_TOKEN)")
	include := fs.String("include", "", "Include filters: type:name (repeatable, comma-separated)")
	exclude := fs.String("exclude", "", "Exclude filters: type:name (repeatable, comma-separated)")

	fs.SetOutput(io.Discard)

	err := fs.Parse(hoistFlags(args))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		return 1
	}

	// Get token from flag or environment
	authToken := *token
	if authToken == "" {
		authToken = os.Getenv("RAH_SYNC_TOKEN")
	}

	posArgs := fs.Args()
	if len(posArgs) == 0 {
		fmt.Fprintf(os.Stderr, "Error: release-id argument required\n")
		fmt.Fprintf(os.Stderr, "Usage: rah-sync promote <release-id> --env <env> --studio <URL> [options]\n")
		return 1
	}

	if *env == "" {
		fmt.Fprintf(os.Stderr, "Error: --env is required\n")
		return 1
	}

	if *studioURL == "" {
		fmt.Fprintf(os.Stderr, "Error: --studio URL is required\n")
		return 1
	}

	releaseID := posArgs[0]

	err = deployRelease(*studioURL, releaseID, *env, *byUser, authToken, *include, *exclude)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error deploying release: %v\n", err)
		return 1
	}

	if *outputFormat == "json" {
		// Print JSON response
		fmt.Printf(`{"release_id": %q, "env": %q, "status": "success"}`, releaseID, *env)
	} else {
		fmt.Printf("Successfully promoted %s to %s\n", releaseID, *env)
	}

	return 0
}

// diffCmd handles: diff <release-A> <release-B> --studio URL [--token TOKEN]
func diffCmd(args []string) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.Usage = func() {}

	studioURL := fs.String("studio", "", "Studio server URL (required)")
	outputFormat := fs.String("output", "text", "Output format: text or json")
	token := fs.String("token", "", "Bearer token (or env RAH_SYNC_TOKEN)")

	fs.SetOutput(io.Discard)

	err := fs.Parse(hoistFlags(args))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		return 1
	}

	// Get token from flag or environment
	authToken := *token
	if authToken == "" {
		authToken = os.Getenv("RAH_SYNC_TOKEN")
	}

	posArgs := fs.Args()
	if len(posArgs) < 2 {
		fmt.Fprintf(os.Stderr, "Error: two release-id arguments required\n")
		fmt.Fprintf(os.Stderr, "Usage: rah-sync diff <release-A> <release-B> --studio <URL>\n")
		return 1
	}

	if *studioURL == "" {
		fmt.Fprintf(os.Stderr, "Error: --studio URL is required\n")
		return 1
	}

	releaseA := posArgs[0]
	releaseB := posArgs[1]

	diff, err := getDiff(*studioURL, releaseA, releaseB, authToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting diff: %v\n", err)
		return 1
	}

	if *outputFormat == "json" {
		diffJSON, _ := json.MarshalIndent(diff, "", "  ")
		fmt.Println(string(diffJSON))
	} else {
		formatDiffText(releaseA, releaseB, diff)
	}

	return 0
}

// exportCmd handles: export --release <id> --studio URL [--format yaml|json] [--out PATH] [--token TOKEN]
func exportCmd(args []string) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.Usage = func() {}

	releaseID := fs.String("release", "", "Release ID to export (required)")
	studioURL := fs.String("studio", "", "Studio server URL (required)")
	format := fs.String("format", "yaml", "Output format: yaml or json")
	outPath := fs.String("out", "", "Output file path (optional)")
	token := fs.String("token", "", "Bearer token (or env RAH_SYNC_TOKEN)")

	fs.SetOutput(io.Discard)

	err := fs.Parse(hoistFlags(args))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		return 1
	}

	// Validate required flags
	if *releaseID == "" {
		fmt.Fprintf(os.Stderr, "Error: --release is required\n")
		fmt.Fprintf(os.Stderr, "Usage: rah-sync export --release <id> --studio <URL> [options]\n")
		return 1
	}

	if *studioURL == "" {
		fmt.Fprintf(os.Stderr, "Error: --studio URL is required\n")
		return 1
	}

	// Validate format
	if *format != "yaml" && *format != "json" {
		fmt.Fprintf(os.Stderr, "Error: --format must be 'yaml' or 'json'\n")
		return 1
	}

	// Determine output path
	outFile := *outPath
	if outFile == "" {
		if *format == "json" {
			outFile = *releaseID + ".json"
		} else {
			outFile = *releaseID + ".yaml"
		}
	}

	// Get token from flag or environment
	authToken := *token
	if authToken == "" {
		authToken = os.Getenv("RAH_SYNC_TOKEN")
	}

	// Export the release
	err = exportRelease(*studioURL, *releaseID, *format, outFile, authToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error exporting release: %v\n", err)
		return 1
	}

	fmt.Printf("Exported release %s to %s\n", *releaseID, outFile)
	return 0
}

// publishBundle POSTs the bundle to studio and returns the release ID
func publishBundle(studioURL string, bundle interface{}, tag, gitCommit, gitBranch, token, include, exclude string) (string, error) {
	bundleJSON, err := json.Marshal(bundle)
	if err != nil {
		return "", fmt.Errorf("failed to marshal bundle: %w", err)
	}

	endpoint := studioURL + "/api/releases"

	// Add query parameters for include/exclude filters
	queryParams := []string{}
	if include != "" {
		// Parse comma-separated include values
		for _, inc := range strings.Split(include, ",") {
			if trimmed := strings.TrimSpace(inc); trimmed != "" {
				queryParams = append(queryParams, "include="+inc)
			}
		}
	}
	if exclude != "" {
		// Parse comma-separated exclude values
		for _, exc := range strings.Split(exclude, ",") {
			if trimmed := strings.TrimSpace(exc); trimmed != "" {
				queryParams = append(queryParams, "exclude="+exc)
			}
		}
	}

	if len(queryParams) > 0 {
		endpoint = endpoint + "?" + strings.Join(queryParams, "&")
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(bundleJSON))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if tag != "" {
		req.Header.Set("X-Release-Tag", tag)
	}
	if gitCommit != "" {
		req.Header.Set("X-Git-Commit", gitCommit)
	}
	if gitBranch != "" {
		req.Header.Set("X-Git-Branch", gitBranch)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to post to studio: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("studio returned error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var respData struct {
		ReleaseID string `json:"release_id"`
	}

	err = json.Unmarshal(respBody, &respData)
	if err != nil {
		return "", fmt.Errorf("failed to parse studio response: %w", err)
	}

	return respData.ReleaseID, nil
}

// deployRelease POSTs to /api/releases/:id/deploy to deploy a release
func deployRelease(studioURL, releaseID, env, byUser, token, include, exclude string) error {
	endpoint := fmt.Sprintf("%s/api/releases/%s/deploy", studioURL, releaseID)

	// Add query parameters for include/exclude filters
	queryParams := []string{}
	if include != "" {
		// Parse comma-separated include values
		for _, inc := range strings.Split(include, ",") {
			if trimmed := strings.TrimSpace(inc); trimmed != "" {
				queryParams = append(queryParams, "include="+inc)
			}
		}
	}
	if exclude != "" {
		// Parse comma-separated exclude values
		for _, exc := range strings.Split(exclude, ",") {
			if trimmed := strings.TrimSpace(exc); trimmed != "" {
				queryParams = append(queryParams, "exclude="+exc)
			}
		}
	}

	if len(queryParams) > 0 {
		endpoint = endpoint + "?" + strings.Join(queryParams, "&")
	}

	deployReq := map[string]string{
		"env": env,
	}
	if byUser != "" {
		deployReq["by_user"] = byUser
	}

	deployJSON, err := json.Marshal(deployReq)
	if err != nil {
		return fmt.Errorf("failed to marshal deploy request: %w", err)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(deployJSON))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to post to studio: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("studio returned error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// getDiff GETs the diff between two releases
func getDiff(studioURL, releaseA, releaseB, token string) (map[string]interface{}, error) {
	endpoint := fmt.Sprintf("%s/api/releases/%s/diff/%s", studioURL, releaseA, releaseB)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("studio returned error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var diff map[string]interface{}
	err = json.Unmarshal(respBody, &diff)
	if err != nil {
		return nil, fmt.Errorf("failed to parse diff response: %w", err)
	}

	return diff, nil
}

// formatDiffText prints the diff in human-readable text format
func formatDiffText(releaseA, releaseB string, diff map[string]interface{}) {
	fmt.Printf("Differences between %s and %s:\n", releaseA, releaseB)
	fmt.Println()

	if flowsAdded, ok := diff["flows_added"].([]interface{}); ok && len(flowsAdded) > 0 {
		fmt.Println("Flows added:")
		for _, f := range flowsAdded {
			fmt.Printf("  + %v\n", f)
		}
		fmt.Println()
	}

	if flowsRemoved, ok := diff["flows_removed"].([]interface{}); ok && len(flowsRemoved) > 0 {
		fmt.Println("Flows removed:")
		for _, f := range flowsRemoved {
			fmt.Printf("  - %v\n", f)
		}
		fmt.Println()
	}

	if apisAdded, ok := diff["apis_added"].([]interface{}); ok && len(apisAdded) > 0 {
		fmt.Println("APIs added:")
		for _, a := range apisAdded {
			fmt.Printf("  + %v\n", a)
		}
		fmt.Println()
	}

	if apisRemoved, ok := diff["apis_removed"].([]interface{}); ok && len(apisRemoved) > 0 {
		fmt.Println("APIs removed:")
		for _, a := range apisRemoved {
			fmt.Printf("  - %v\n", a)
		}
		fmt.Println()
	}
}

// exportRelease GETs a release bundle and saves it to a file
func exportRelease(studioURL, releaseID, format, outPath, token string) error {
	endpoint := fmt.Sprintf("%s/api/releases/%s/bundle?format=%s", studioURL, releaseID, format)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to get bundle: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("studio returned error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Write to file
	err = os.WriteFile(outPath, respBody, 0644)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// hoistFlags reorders args so that all --flag [value] pairs come before
// positional arguments. This lets callers mix flags and positionals in any
// order (e.g. "publish ./dir --studio URL" or "--studio URL publish ./dir").
//
// Go's flag.FlagSet stops parsing at the first non-flag argument, so without
// this reordering flags that appear after the directory would be silently ignored.
func hoistFlags(args []string) []string {
	var flags, positionals []string
	i := 0
	for i < len(args) {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			// If the flag uses --name=value form the value is embedded; don't consume next.
			// Otherwise, if the next token doesn't start with '-', treat it as the flag's value.
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flags = append(flags, args[i])
			}
		} else {
			positionals = append(positionals, a)
		}
		i++
	}
	return append(flags, positionals...)
}
