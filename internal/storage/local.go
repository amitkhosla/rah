package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type localProvider struct {
	rootDir string
}

func newLocalProvider(cfg StorageProviderConfig) (*localProvider, error) {
	root := cfg.RootDir
	if root == "" {
		return nil, fmt.Errorf("storage %q: local provider requires root_dir", cfg.Name)
	}
	// Ensure root exists
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("storage %q: create root dir: %w", cfg.Name, err)
	}
	return &localProvider{rootDir: root}, nil
}

// safePath joins rootDir + key and rejects path traversal attempts.
func (p *localProvider) safePath(key string) (string, error) {
	clean := filepath.Join(p.rootDir, filepath.FromSlash(key))
	if !strings.HasPrefix(clean, filepath.Clean(p.rootDir)+string(filepath.Separator)) &&
		clean != filepath.Clean(p.rootDir) {
		return "", fmt.Errorf("local storage: key %q escapes root directory", key)
	}
	return clean, nil
}

func (p *localProvider) Get(ctx context.Context, key string) ([]byte, error) {
	path, err := p.safePath(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("local get %q: %w", key, err)
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (p *localProvider) Put(ctx context.Context, key string, content []byte, contentType string) error {
	path, err := p.safePath(key)
	if err != nil {
		return err
	}
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("local put %q: mkdir: %w", key, err)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		return fmt.Errorf("local put %q: %w", key, err)
	}
	return nil
}

func (p *localProvider) Delete(ctx context.Context, key string) error {
	path, err := p.safePath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("local delete %q: %w", key, err)
	}
	return nil
}
