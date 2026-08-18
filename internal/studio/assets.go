package studio

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	defer dst.Close()
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

// detectContentType sniffs the content type from the first 512 bytes.
func detectContentType(filename string, data []byte) string {
	ct := mime.TypeByExtension(filepath.Ext(filename))
	if ct != "" {
		return ct
	}
	ct = http.DetectContentType(data)
	return strings.Split(ct, ";")[0]
}
