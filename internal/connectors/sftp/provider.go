package sftp

import (
	"context"
	"time"
)

// FileInfo represents information about a file in SFTP.
type FileInfo struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"is_dir"`
	ModTime time.Time `json:"mod_time"`
}

// SFTPProvider defines operations for interacting with SFTP servers.
type SFTPProvider interface {
	// Get retrieves the content of a file at the given path.
	Get(ctx context.Context, path string) ([]byte, error)

	// Put writes content to a file at the given path, creating parent directories as needed.
	Put(ctx context.Context, path string, content []byte) error

	// Delete removes the file at the given path.
	Delete(ctx context.Context, path string) error

	// List returns the contents of a directory.
	List(ctx context.Context, dir string) ([]FileInfo, error)

	// Close closes the SFTP and SSH connections.
	Close() error
}

// SecretResolver avoids circular import with the secrets package.
// It resolves credential references to their plaintext values.
type SecretResolver interface {
	// Resolve returns the plaintext secret for a credential reference.
	Resolve(ctx context.Context, ref string) ([]byte, error)
}
