package storage

import (
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	defer func() { _ = f.Close() }()
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

// List returns all objects whose keys start with prefix (using forward slashes).
func (p *localProvider) List(ctx context.Context, prefix string) ([]ListItem, error) {
	searchDir := filepath.Join(p.rootDir, filepath.FromSlash(prefix))
	var items []ListItem
	err := filepath.WalkDir(searchDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(p.rootDir, path)
		key := filepath.ToSlash(rel)
		ct := mime.TypeByExtension(filepath.Ext(path))
		if ct == "" {
			ct = "application/octet-stream"
		}
		items = append(items, ListItem{Key: key, Size: info.Size(), ContentType: ct, UpdatedAt: info.ModTime()})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("local list %q: %w", prefix, err)
	}
	_ = time.Now() // keep time import used
	return items, nil
}
