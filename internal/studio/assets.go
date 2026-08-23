package studio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/storage"
)

// AssetMeta describes a stored asset file.
type AssetMeta struct {
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	UpdatedAt   time.Time `json:"updated_at"`
	URL         string    `json:"url,omitempty"`
}

// AssetStore persists and retrieves static asset files for apps.
type AssetStore interface {
	Upload(ctx context.Context, appName, filename string, r io.Reader, contentType string, size int64) error
	Download(ctx context.Context, appName, filename string) (io.ReadCloser, AssetMeta, error)
	List(ctx context.Context, appName string) ([]AssetMeta, error)
	Delete(ctx context.Context, appName, filename string) error
}

// LocalAssetStore stores assets on the local filesystem under baseDir/<appName>/<filename>.
type LocalAssetStore struct{ baseDir string }

func NewLocalAssetStore(baseDir string) (*LocalAssetStore, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create assets dir: %w", err)
	}
	return &LocalAssetStore{baseDir: baseDir}, nil
}

func (s *LocalAssetStore) appDir(appName string) string {
	// Sanitise: no path traversal
	return filepath.Join(s.baseDir, filepath.Base(appName))
}

func (s *LocalAssetStore) Upload(ctx context.Context, appName, filename string, r io.Reader, contentType string, size int64) error {
	dir := s.appDir(appName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	dst, err := os.Create(filepath.Join(dir, filepath.Base(filename)))
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	_, err = io.Copy(dst, r)
	return err
}

func (s *LocalAssetStore) Download(ctx context.Context, appName, filename string) (io.ReadCloser, AssetMeta, error) {
	path := filepath.Join(s.appDir(appName), filepath.Base(filename))
	f, err := os.Open(path)
	if err != nil {
		return nil, AssetMeta{}, err
	}
	stat, _ := f.Stat()
	ct := mime.TypeByExtension(filepath.Ext(filename))
	if ct == "" {
		ct = "application/octet-stream"
	}
	meta := AssetMeta{Filename: filename, ContentType: ct, Size: stat.Size(), UpdatedAt: stat.ModTime()}
	return f, meta, nil
}

func (s *LocalAssetStore) List(ctx context.Context, appName string) ([]AssetMeta, error) {
	dir := s.appDir(appName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]AssetMeta, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, _ := e.Info()
		ct := mime.TypeByExtension(filepath.Ext(e.Name()))
		if ct == "" {
			ct = "application/octet-stream"
		}
		out = append(out, AssetMeta{
			Filename:    e.Name(),
			ContentType: ct,
			Size:        info.Size(),
			UpdatedAt:   info.ModTime(),
		})
	}
	return out, nil
}

func (s *LocalAssetStore) Delete(ctx context.Context, appName, filename string) error {
	path := filepath.Join(s.appDir(appName), filepath.Base(filename))
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ── StorageManagerAssetStore ──────────────────────────────────────────────────
// Backs the AssetStore with any StorageManager provider (S3, GCS, local, etc.).
// Keys are stored as: {prefix}/{appName}/{filename}  (prefix may be empty).

type StorageManagerAssetStore struct {
	mgr      *storage.StorageManager
	provider string // named provider in the StorageManager
	prefix   string // optional key prefix, e.g. "assets"
}

func NewStorageManagerAssetStore(mgr *storage.StorageManager, provider, prefix string) *StorageManagerAssetStore {
	return &StorageManagerAssetStore{mgr: mgr, provider: provider, prefix: strings.TrimRight(prefix, "/")}
}

func (s *StorageManagerAssetStore) key(appName, filename string) string {
	appName = filepath.Base(appName)   // path traversal guard
	filename = filepath.Base(filename) // path traversal guard
	if s.prefix != "" {
		return s.prefix + "/" + appName + "/" + filename
	}
	return appName + "/" + filename
}

func (s *StorageManagerAssetStore) appPrefix(appName string) string {
	appName = filepath.Base(appName)
	if s.prefix != "" {
		return s.prefix + "/" + appName + "/"
	}
	return appName + "/"
}

func (s *StorageManagerAssetStore) Upload(ctx context.Context, appName, filename string, r io.Reader, contentType string, _ int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read upload: %w", err)
	}
	return s.mgr.Put(ctx, s.provider, s.key(appName, filename), data, contentType)
}

func (s *StorageManagerAssetStore) Download(ctx context.Context, appName, filename string) (io.ReadCloser, AssetMeta, error) {
	data, err := s.mgr.Get(ctx, s.provider, s.key(appName, filename))
	if err != nil {
		return nil, AssetMeta{}, err
	}
	ct := mime.TypeByExtension(filepath.Ext(filename))
	if ct == "" {
		ct = "application/octet-stream"
	}
	meta := AssetMeta{Filename: filename, ContentType: ct, Size: int64(len(data)), UpdatedAt: time.Now()}
	return io.NopCloser(bytes.NewReader(data)), meta, nil
}

func (s *StorageManagerAssetStore) List(ctx context.Context, appName string) ([]AssetMeta, error) {
	items, err := s.mgr.List(ctx, s.provider, s.appPrefix(appName))
	if err != nil {
		return nil, err
	}
	out := make([]AssetMeta, 0, len(items))
	for _, item := range items {
		// Key is "{prefix}/{appName}/{filename}" — extract just the filename.
		filename := item.Key[strings.LastIndex(item.Key, "/")+1:]
		if filename == "" {
			continue
		}
		out = append(out, AssetMeta{
			Filename:    filename,
			ContentType: item.ContentType,
			Size:        item.Size,
			UpdatedAt:   item.UpdatedAt,
		})
	}
	return out, nil
}

func (s *StorageManagerAssetStore) Delete(ctx context.Context, appName, filename string) error {
	return s.mgr.Delete(ctx, s.provider, s.key(appName, filename))
}

