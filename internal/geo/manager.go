package geo

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oschwald/maxminddb-golang"
	"rah/internal/datastore"
)

const (
	keystoreVersion = "geo:db:version"
	keystoreData    = "geo:db:data"
	cacheShardsN    = 16
	cachePerShard   = 4096
)

// countryRecord is the minimal mmdb struct for country-level lookup.
type countryRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

type cacheShard struct {
	mu sync.RWMutex
	m  map[string]string // ip → country code
}

// Manager holds the mmdb reader and update state.
type Manager struct {
	reader         atomic.Pointer[maxminddb.Reader]
	cache          [cacheShardsN]cacheShard
	store          datastore.KeyValueStore // nil = no datastore sync
	licenseKey     string                  // MaxMind license key; "" = no auto-download
	updateInterval time.Duration           // 0 = never auto-update
	lastSyncTime   time.Time
	lastSyncMu     sync.Mutex
	syncing        atomic.Bool
	onMissing      string // "block" or "allow"
}

// Config holds startup config for a Manager.
type Config struct {
	DBPath         string                  // optional local file to load at startup
	Datastore      datastore.KeyValueStore // optional; nil = file/download only
	LicenseKey     string                  // MaxMind license key for auto-download
	UpdateInterval time.Duration           // 0 = disabled; default 168h (7 days)
	OnMissing      string                  // "block" (default) or "allow"
}

func New(cfg Config) (*Manager, error) {
	if cfg.UpdateInterval == 0 && cfg.LicenseKey != "" {
		cfg.UpdateInterval = 7 * 24 * time.Hour
	}
	if cfg.OnMissing == "" {
		cfg.OnMissing = "block"
	}
	m := &Manager{
		store:          cfg.Datastore,
		licenseKey:     cfg.LicenseKey,
		updateInterval: cfg.UpdateInterval,
		onMissing:      cfg.OnMissing,
	}
	for i := range m.cache {
		m.cache[i].m = make(map[string]string, cachePerShard)
	}

	// Try loading from datastore first.
	if m.store != nil {
		if data, found, err := m.store.Get(context.Background(), datastore.Tenant("_geo"), keystoreData); err == nil && found && len(data) > 0 {
			if r, err := maxminddb.FromBytes(data); err == nil {
				m.reader.Store(r)
				m.lastSyncTime = time.Now()
			}
		}
	}

	// Fall back to local file if reader not set.
	if m.reader.Load() == nil && cfg.DBPath != "" {
		r, err := maxminddb.Open(cfg.DBPath)
		if err != nil {
			return nil, fmt.Errorf("geo: open mmdb: %w", err)
		}
		m.reader.Store(r)
		m.lastSyncTime = time.Now()
	}

	return m, nil
}

// Lookup returns the ISO 3166-1 alpha-2 country code for ip.
// Returns "" if the IP is private/unroutable or the database is not loaded.
// Triggers an async sync if the database is stale.
func (m *Manager) Lookup(ip string) string {
	m.maybeSync()
	r := m.reader.Load()
	if r == nil {
		return ""
	}

	// Check cache first.
	sh := &m.cache[cacheHash(ip)%cacheShardsN]
	sh.mu.RLock()
	if cc, ok := sh.m[ip]; ok {
		sh.mu.RUnlock()
		return cc
	}
	sh.mu.RUnlock()

	// Parse IP.
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	if addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return "_private"
	}

	// mmdb lookup.
	var rec countryRecord
	if err := r.Lookup(addr.AsSlice(), &rec); err != nil {
		return ""
	}
	cc := rec.Country.ISOCode

	// Store in cache (evict oldest shard entry if full).
	sh.mu.Lock()
	if len(sh.m) >= cachePerShard {
		// Simple eviction: delete a random entry by iterating once.
		for k := range sh.m {
			delete(sh.m, k)
			break
		}
	}
	sh.m[ip] = cc
	sh.mu.Unlock()

	return cc
}

// OnMissingAllow returns true if the config says to allow requests when the DB is unavailable.
func (m *Manager) OnMissingAllow() bool {
	return m.onMissing == "allow"
}

func (m *Manager) maybeSync() {
	if m.updateInterval <= 0 || m.licenseKey == "" {
		return
	}
	m.lastSyncMu.Lock()
	due := time.Since(m.lastSyncTime) > m.updateInterval
	m.lastSyncMu.Unlock()
	if !due {
		return
	}
	if m.syncing.CompareAndSwap(false, true) {
		go m.doSync()
	}
}

func (m *Manager) doSync() {
	defer m.syncing.Store(false)

	// Check if datastore has a fresher version.
	if m.store != nil {
		if verBytes, found, err := m.store.Get(context.Background(), datastore.Tenant("_geo"), keystoreVersion); err == nil && found {
			var storeTime time.Time
			if err := json.Unmarshal(verBytes, &storeTime); err == nil {
				m.lastSyncMu.Lock()
				isNewer := storeTime.After(m.lastSyncTime)
				m.lastSyncMu.Unlock()
				if isNewer {
					// Load from datastore.
					if data, found2, err2 := m.store.Get(context.Background(), datastore.Tenant("_geo"), keystoreData); err2 == nil && found2 && len(data) > 0 {
						if r, err3 := maxminddb.FromBytes(data); err3 == nil {
							if old := m.reader.Swap(r); old != nil {
								old.Close()
							}
							m.lastSyncMu.Lock()
							m.lastSyncTime = storeTime
							m.lastSyncMu.Unlock()
							return
						}
					}
				}
			}
		}
	}

	// Download from MaxMind.
	data, err := m.downloadFromMaxMind()
	if err != nil || len(data) == 0 {
		return
	}
	r, err := maxminddb.FromBytes(data)
	if err != nil {
		return
	}
	if old := m.reader.Swap(r); old != nil {
		old.Close()
	}
	now := time.Now()
	m.lastSyncMu.Lock()
	m.lastSyncTime = now
	m.lastSyncMu.Unlock()

	// Persist to datastore.
	if m.store != nil {
		verBytes, _ := json.Marshal(now)
		_ = m.store.Put(context.Background(), datastore.Tenant("_geo"), keystoreVersion, verBytes)
		_ = m.store.Put(context.Background(), datastore.Tenant("_geo"), keystoreData, data)
	}
}

func (m *Manager) downloadFromMaxMind() ([]byte, error) {
	url := fmt.Sprintf(
		"https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-Country&license_key=%s&suffix=tar.gz",
		m.licenseKey,
	)
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("maxmind download: HTTP %d", resp.StatusCode)
	}
	return extractMMDB(io.LimitReader(resp.Body, 32*1024*1024)) // 32MB max
}

// extractMMDB reads a .tar.gz archive from r and returns the raw bytes of the
// GeoLite2-Country.mmdb file contained inside.
func extractMMDB(r io.Reader) ([]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("geo: decompress tar.gz: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("geo: read tar: %w", err)
		}
		// The mmdb sits at e.g. GeoLite2-Country_20240101/GeoLite2-Country.mmdb
		if hdr.Typeflag == tar.TypeReg && len(hdr.Name) >= 20 {
			name := hdr.Name
			// match the basename
			for i := len(name) - 1; i >= 0; i-- {
				if name[i] == '/' {
					name = name[i+1:]
					break
				}
			}
			if name == "GeoLite2-Country.mmdb" {
				return io.ReadAll(io.LimitReader(tr, 32*1024*1024))
			}
		}
	}
	return nil, fmt.Errorf("geo: GeoLite2-Country.mmdb not found in archive")
}

func cacheHash(s string) int {
	h := 0
	for i := 0; i < len(s); i++ {
		h = h*31 + int(s[i])
	}
	if h < 0 {
		h = -h
	}
	return h
}
