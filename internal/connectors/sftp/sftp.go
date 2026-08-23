package sftp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// sftpProvider is the concrete implementation of SFTPProvider.
type sftpProvider struct {
	mu         sync.RWMutex
	sshClient  *ssh.Client
	sftpClient *sftp.Client
	cfg        SFTPConnectorConfig
	secrets    SecretResolver
}

// newSFTPProvider creates a new sftpProvider without connecting.
func newSFTPProvider(cfg SFTPConnectorConfig, secrets SecretResolver) *sftpProvider {
	return &sftpProvider{
		cfg:     cfg,
		secrets: secrets,
	}
}

// ensureConnected performs a double-check locking pattern to ensure the SFTP connection is established.
func (p *sftpProvider) ensureConnected(ctx context.Context) error {
	p.mu.RLock()
	connected := p.sftpClient != nil
	p.mu.RUnlock()
	if connected {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sftpClient != nil { // recheck under write lock
		return nil
	}
	return p.dial(ctx)
}

// dial establishes the SSH and SFTP connections. Must be called with write lock held.
func (p *sftpProvider) dial(ctx context.Context) error {
	timeoutMs := p.cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 10000
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	config := &ssh.ClientConfig{
		User:    p.cfg.Username,
		Timeout: timeout,
	}

	var authMethods []ssh.AuthMethod

	if p.cfg.PasswordRef != "" {
		passwordBytes, err := p.secrets.Resolve(ctx, p.cfg.PasswordRef)
		if err != nil {
			return fmt.Errorf("failed to resolve password: %w", err)
		}
		authMethods = append(authMethods, ssh.Password(string(passwordBytes)))
	}

	if p.cfg.PrivateKeyRef != "" {
		keyBytes, err := p.secrets.Resolve(ctx, p.cfg.PrivateKeyRef)
		if err != nil {
			return fmt.Errorf("failed to resolve private key: %w", err)
		}
		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			return fmt.Errorf("failed to parse private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	if len(authMethods) == 0 {
		return fmt.Errorf("no authentication methods configured (no password or private key)")
	}
	config.Auth = authMethods

	if p.cfg.KnownHostsRef != "" {
		knownHostsBytes, err := p.secrets.Resolve(ctx, p.cfg.KnownHostsRef)
		if err != nil {
			return fmt.Errorf("failed to resolve known_hosts: %w", err)
		}
		tmpFile, err := os.CreateTemp("", "known_hosts_*")
		if err != nil {
			return fmt.Errorf("failed to create temp file for known_hosts: %w", err)
		}
		tmpPath := tmpFile.Name()
		defer func() { _ = os.Remove(tmpPath) }()
		if _, err := tmpFile.Write(knownHostsBytes); err != nil {
			_ = tmpFile.Close()
			return fmt.Errorf("failed to write known_hosts to temp file: %w", err)
		}
		_ = tmpFile.Close()
		callback, err := knownhosts.New(tmpPath)
		if err != nil {
			return fmt.Errorf("failed to load known_hosts: %w", err)
		}
		config.HostKeyCallback = callback
	} else {
		config.HostKeyCallback = ssh.InsecureIgnoreHostKey() //nolint:gosec
	}

	port := p.cfg.Port
	if port == 0 {
		port = 22
	}
	addr := fmt.Sprintf("%s:%d", p.cfg.Host, port)
	sshClient, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("failed to connect to SSH server at %s: %w", addr, err)
	}
	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close()
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	p.sshClient = sshClient
	p.sftpClient = sftpClient
	return nil
}

// invalidate closes and clears the clients. Must be called with write lock held.
func (p *sftpProvider) invalidate() {
	if p.sftpClient != nil {
		_ = p.sftpClient.Close()
		p.sftpClient = nil
	}
	if p.sshClient != nil {
		_ = p.sshClient.Close()
		p.sshClient = nil
	}
}

// markConnError invalidates the connection if the error looks like a connection failure.
// Must be called WITHOUT any lock held — it acquires the write lock internally.
func (p *sftpProvider) markConnError(err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	if strings.Contains(s, "EOF") || strings.Contains(s, "connection") {
		p.mu.Lock()
		p.invalidate()
		p.mu.Unlock()
	}
	return err
}

// Get retrieves the content of a file.
// The RLock is held across Open + ReadAll so that a concurrent reconnect cannot
// close the underlying SFTP session while the file handle is in use.
func (p *sftpProvider) Get(ctx context.Context, path string) ([]byte, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return nil, err
	}

	p.mu.RLock()
	file, err := p.sftpClient.Open(path)
	if err != nil {
		p.mu.RUnlock()
		return nil, p.markConnError(err)
	}
	content, readErr := io.ReadAll(file)
	_ = file.Close()
	p.mu.RUnlock()

	if readErr != nil {
		return nil, p.markConnError(readErr)
	}
	return content, nil
}

// Put writes content to a file, creating parent directories as needed.
// The RLock is held across MkdirAll + Create + Write so that the session
// cannot be invalidated while the file handle is open.
func (p *sftpProvider) Put(ctx context.Context, path string, content []byte) error {
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}

	dir := filepath.Dir(path)

	p.mu.RLock()
	if err := p.sftpClient.MkdirAll(dir); err != nil {
		p.mu.RUnlock()
		return p.markConnError(err)
	}
	file, err := p.sftpClient.Create(path)
	if err != nil {
		p.mu.RUnlock()
		return p.markConnError(err)
	}
	_, writeErr := file.Write(content)
	if closeErr := file.Close(); closeErr != nil && writeErr == nil {
		writeErr = closeErr
	}
	p.mu.RUnlock()

	if writeErr != nil {
		return p.markConnError(writeErr)
	}
	return nil
}

// Delete removes a file.
func (p *sftpProvider) Delete(ctx context.Context, path string) error {
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}

	p.mu.RLock()
	err := p.sftpClient.Remove(path)
	p.mu.RUnlock()

	if err != nil {
		return p.markConnError(err)
	}
	return nil
}

// List returns the contents of a directory as a slice of FileInfo.
// ReadDir returns all entries atomically so no file handles are held after the call.
func (p *sftpProvider) List(ctx context.Context, dir string) ([]FileInfo, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return nil, err
	}

	p.mu.RLock()
	entries, err := p.sftpClient.ReadDir(dir)
	p.mu.RUnlock()

	if err != nil {
		return nil, p.markConnError(err)
	}

	result := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		result = append(result, FileInfo{
			Name:    entry.Name(),
			Size:    entry.Size(),
			IsDir:   entry.IsDir(),
			ModTime: entry.ModTime(),
		})
	}
	return result, nil
}

// Close closes the SFTP and SSH connections.
func (p *sftpProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.invalidate()
	return nil
}
