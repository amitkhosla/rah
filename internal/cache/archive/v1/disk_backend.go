package v1

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// diskBackend is a CacheBackend that stores each cache entry as an individual
// file on disk. This is the default backend when no external store is configured.
//
// File layout:
//
//	{base}/{tenantID}/{hash[0]:02x}/{hash_hex}.bin
//	   └─ [0:4]  expiry uint32 little-endian (unix seconds; 0 = no expiry)
//	      [4:N]  value bytes
//
// The two-level directory ({tenantID}/{hash[0]}) caps each directory to ≤256
// entries on the first level and spreads hot entries across 256 second-level
// directories, avoiding filesystem limits on large caches.
//
// Writes are atomic: value is written to a temp file then renamed into place.
type diskBackend struct {
	base string // root directory, e.g. "./cache"
}

// DefaultDiskCachePath is used when no explicit path is provided.
const DefaultDiskCachePath = "./icache"

// NewDiskBackend creates a disk-based CacheBackend rooted at base.
// The directory is created if it does not exist.
func NewDiskBackend(base string) (CacheBackend, error) {
	if base == "" {
		base = DefaultDiskCachePath
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}
	return &diskBackend{base: base}, nil
}

// entryPath derives the file path for (tenantID, key).
// Uses Hash128 to produce a stable 128-bit fingerprint → hex filename.
func (d *diskBackend) entryPath(tenantID uint16, key []byte) string {
	fp := Hash128(tenantID, key)
	// hex-encode the 16-byte fingerprint as the filename
	const hex = "0123456789abcdef"
	name := make([]byte, 32)
	for i, b := range fp {
		name[i*2] = hex[b>>4]
		name[i*2+1] = hex[b&0xf]
	}
	// first byte of fp → sub-directory (256 buckets)
	sub := string(name[:2])
	return filepath.Join(d.base, strconv.FormatUint(uint64(tenantID), 10), sub, string(name)+".bin")
}

func (d *diskBackend) Get(tenantID uint16, key []byte) ([]byte, uint32, bool) {
	path := d.entryPath(tenantID, key)
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 4 {
		return nil, 0, false
	}
	expiry := binary.LittleEndian.Uint32(data[:4])
	if expiry > 0 && expiry < uint32(time.Now().Unix()) {
		// Lazy delete: expired entry.
		os.Remove(path)
		return nil, 0, false
	}
	return data[4:], expiry, true
}

func (d *diskBackend) Set(tenantID uint16, key []byte, value []byte, expiry uint32) error {
	path := d.entryPath(tenantID, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	buf := make([]byte, 4+len(value))
	binary.LittleEndian.PutUint32(buf[:4], expiry)
	copy(buf[4:], value)

	// Atomic write: temp file + rename.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (d *diskBackend) Delete(tenantID uint16, key []byte) error {
	err := os.Remove(d.entryPath(tenantID, key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Sweep walks the cache directory and removes expired entries.
// Designed to be called from a low-frequency background goroutine (e.g. every 5 min).
func (d *diskBackend) Sweep() int {
	now := uint32(time.Now().Unix())
	deleted := 0

	_ = filepath.WalkDir(d.base, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".bin" {
			return nil
		}
		// Read only the 4-byte expiry header.
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		var expBuf [4]byte
		n, err := f.Read(expBuf[:])
		_ = f.Close()
		if err != nil || n < 4 {
			return nil
		}
		expiry := binary.LittleEndian.Uint32(expBuf[:])
		if expiry > 0 && expiry < now {
			os.Remove(path)
			deleted++
		}
		return nil
	})

	return deleted
}

func (d *diskBackend) Close() error { return nil }
