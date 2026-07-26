package sync

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// LintOptions configures the linting and formatting behavior.
type LintOptions struct {
	StrictMode   bool   // If true, warnings are treated as errors for exit code
	OutputFormat string // "text" (default) or "json"
}

// Run orchestrates the complete linting pipeline: load bundle → lint → format.
// Returns a LintResult summarizing all issues found.
func Run(dir string, opts LintOptions) (LintResult, error) {
	// 1. Load the bundle from the directory tree.
	loadResult, err := Load(dir)
	if err != nil {
		return LintResult{}, fmt.Errorf("failed to load bundle: %w", err)
	}

	// 2. Run all lint levels (0-4).
	issues := Lint(loadResult)

	// 3. Count and classify issues.
	errorCount := 0
	warningCount := 0
	infoCount := 0
	for _, issue := range issues {
		switch issue.Severity {
		case SeverityError:
			errorCount++
		case SeverityWarning:
			warningCount++
		case SeverityInfo:
			infoCount++
		}
	}

	result := LintResult{
		Issues:       issues,
		ErrorCount:   errorCount,
		WarningCount: warningCount,
		InfoCount:    infoCount,
	}

	// 4. Format and output.
	if opts.OutputFormat == "json" {
		FormatJSON(result, os.Stdout)
	} else {
		FormatText(result, os.Stdout)
	}

	return result, nil
}

// FormatText writes issues in a human-readable text format.
// Uses color output when writing to a TTY.
func FormatText(result LintResult, w io.Writer) {
	if len(result.Issues) == 0 {
		_, _ = fmt.Fprintf(w, "✓ No linting issues found\n")
		return
	}

	// Sort issues by file, then line number for consistent output.
	sorted := make([]LintIssue, len(result.Issues))
	copy(sorted, result.Issues)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].File != sorted[j].File {
			return sorted[i].File < sorted[j].File
		}
		return sorted[i].Line < sorted[j].Line
	})

	// Determine if output supports colors (basic TTY check).
	useColor := isTTY(w)

	for _, issue := range sorted {
		formatTextIssue(w, issue, useColor)
	}

	// Print summary.
	_, _ = fmt.Fprintf(w, "\n")
	if result.ErrorCount > 0 {
		_, _ = fmt.Fprintf(w, "Errors: %d  ", result.ErrorCount)
	}
	if result.WarningCount > 0 {
		_, _ = fmt.Fprintf(w, "Warnings: %d  ", result.WarningCount)
	}
	if result.InfoCount > 0 {
		_, _ = fmt.Fprintf(w, "Info: %d  ", result.InfoCount)
	}
	_, _ = fmt.Fprintf(w, "\n")
}

// formatTextIssue formats a single issue in text format.
func formatTextIssue(w io.Writer, issue LintIssue, useColor bool) {
	// Determine icon and color based on severity.
	var icon, color, reset string
	if useColor {
		reset = "\033[0m"
	}

	switch issue.Severity {
	case SeverityError:
		icon = "✖"
		if useColor {
			color = "\033[31m" // red
		}
	case SeverityWarning:
		icon = "⚠"
		if useColor {
			color = "\033[33m" // yellow
		}
	case SeverityInfo:
		icon = "ℹ"
		if useColor {
			color = "\033[36m" // cyan
		}
	default:
		icon = "•"
	}

	// Format: file:line icon [rule] message
	fmt.Fprintf(w, "%s:%d %s%s%s %s\n",
		issue.File, issue.Line,
		color, icon, reset,
		formatMessage(issue))

	// Include suggestion if present.
	if issue.Suggestion != "" {
		fmt.Fprintf(w, "  → %s\n", issue.Suggestion)
	}
}

// formatMessage creates a concise message string from the issue.
func formatMessage(issue LintIssue) string {
	// Format: [rule] message
	return fmt.Sprintf("[%s] %s", issue.Rule, issue.Message)
}

// isTTY checks if the writer is a terminal (basic check for os.Stdout).
func isTTY(w io.Writer) bool {
	// Simple heuristic: if it's os.Stdout, check if it's a TTY.
	if f, ok := w.(*os.File); ok {
		// Check if stdout is a TTY using a simple heuristic
		// (actual TTY detection would require sys/unix syscalls).
		// For now, we'll assume color output if writing to stdout.
		return f.Fd() == 1 // stdout
	}
	return false
}

// FormatJSON writes issues as a single JSON object.
func FormatJSON(result LintResult, w io.Writer) {
	data := map[string]interface{}{
		"issues":       result.Issues,
		"error_count":  result.ErrorCount,
		"warning_count": result.WarningCount,
		"info_count":   result.InfoCount,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(data)
}

// ExitCode returns the appropriate exit code for the linting result.
// With StrictMode, warnings are treated as errors.
func ExitCode(result LintResult, strictMode bool) int {
	if result.ErrorCount > 0 {
		return 1
	}
	if strictMode && result.WarningCount > 0 {
		return 1
	}
	return 0
}

// Summary returns a single-line summary of the lint result.
func Summary(result LintResult) string {
	parts := make([]string, 0, 4)

	if result.ErrorCount > 0 {
		parts = append(parts, fmt.Sprintf("%d error", result.ErrorCount))
		if result.ErrorCount > 1 {
			parts[len(parts)-1] += "s"
		}
	}
	if result.WarningCount > 0 {
		parts = append(parts, fmt.Sprintf("%d warning", result.WarningCount))
		if result.WarningCount > 1 {
			parts[len(parts)-1] += "s"
		}
	}
	if result.InfoCount > 0 {
		parts = append(parts, fmt.Sprintf("%d info", result.InfoCount))
		if result.InfoCount > 1 {
			parts[len(parts)-1] += "s"
		}
	}

	if len(parts) == 0 {
		return "no issues found"
	}
	return strings.Join(parts, ", ")
}
