package sync

// BundleFile represents a single definition file collected from the bundle directory.
type BundleFile struct {
	Path    string // path relative to bundle root
	Content []byte // raw file bytes
}

// LintSeverity classifies the importance of a lint issue.
type LintSeverity string

const (
	SeverityError   LintSeverity = "error"
	SeverityWarning LintSeverity = "warning"
	SeverityInfo    LintSeverity = "info"
)

// LintIssue is one diagnostic produced by the linter.
type LintIssue struct {
	Severity   LintSeverity `json:"severity"`
	Rule       string       `json:"rule"`                 // machine-readable rule identifier
	File       string       `json:"file"`                 // source file path (relative to bundle root)
	Line       int          `json:"line"`                 // 1-based line number; 0 when unknown
	Message    string       `json:"message"`              // human-readable description
	Suggestion string       `json:"suggestion,omitempty"` // optional fix hint
}

// SourceLocation records where a named definition was found within the bundle.
type SourceLocation struct {
	File string // relative path of the file containing this definition
	Line int    // 1-based line number of the definition
}

// SourceMap maps definition names to their source locations.
// Keys are flow names, API paths (method+path), or rate-limit config names.
type SourceMap map[string]SourceLocation

// LintResult is the complete output of a linting run.
type LintResult struct {
	Issues       []LintIssue `json:"issues"`
	ErrorCount   int         `json:"error_count"`
	WarningCount int         `json:"warning_count"`
	InfoCount    int         `json:"info_count"`
}
