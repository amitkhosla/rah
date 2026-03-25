package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// fileProvider resolves "file:///path/to/file" references.
// The file contents are returned with leading/trailing whitespace trimmed —
// this handles the common case of mounted K8s Secret files that end with a
// newline.
type fileProvider struct{}

func newFileProvider() *fileProvider { return &fileProvider{} }

func (p *fileProvider) Scheme() string { return "file" }

func (p *fileProvider) Resolve(_ context.Context, ref string) ([]byte, error) {
	path := filePath(ref)
	if path == "" {
		return nil, fmt.Errorf("secrets/file: invalid file reference %q (expected file:///path)", ref)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("secrets/file: reading %q: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	return []byte(trimmed), nil
}

// filePath strips the "file://" prefix and returns the absolute path.
// Supports both "file:///absolute/path" and "file://relative/path".
func filePath(ref string) string {
	after, ok := strings.CutPrefix(ref, "file://")
	if !ok {
		return ""
	}
	return after
}
