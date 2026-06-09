package control

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"sync/atomic"
	"time"

	"rah/internal/config"
)

// InstanceRecord is written to DomainGatewayInstances on every heartbeat.
type InstanceRecord struct {
	InstanceID     string `json:"instance_id"`
	EnvironmentID  string `json:"environment_id"`
	Hostname       string `json:"hostname"`
	CurrentVersion uint64 `json:"current_version"`
	LastHeartbeat  int64  `json:"last_heartbeat"` // Unix seconds
	Status         string `json:"status"`         // "alive" | "draining"
}

// ConfigVersionRecord is written to DomainConfigVersions by Studio/deploy endpoints.
// Gateways poll this domain and apply newer published versions.
type ConfigVersionRecord struct {
	ID            uint64          `json:"id"`
	EnvironmentID string          `json:"environment_id"`
	ReleaseID     string          `json:"release_id,omitempty"`
	Payload       json.RawMessage `json:"payload"`             // UnifiedSyncRequest JSON
	Status        string          `json:"status"`              // "published" | "rolled_back"
	CreatedAt     int64           `json:"created_at"`
	Comment       string          `json:"comment,omitempty"`
}

// InstanceSync manages the heartbeat goroutine and config-poll goroutine
// for this gateway instance.
type InstanceSync struct {
	instanceID     string
	cfg            config.InstanceConfig
	dsm            *DataStoreManager
	ms             *ManagementServer
	currentVersion uint64
	instanceCount  atomic.Int32 // cached live instance count; updated every heartbeat
}

// NewInstanceSync creates an InstanceSync. instanceID should be the instance fingerprint.
func NewInstanceSync(instanceID string, cfg config.InstanceConfig, dsm *DataStoreManager, ms *ManagementServer) *InstanceSync {
	if cfg.PollIntervalS <= 0 {
		cfg.PollIntervalS = 10
	}
	if cfg.HeartbeatIntervalS <= 0 {
		cfg.HeartbeatIntervalS = 10
	}
	if cfg.EnvironmentID == "" {
		cfg.EnvironmentID = "default"
	}
	return &InstanceSync{
		instanceID: instanceID,
		cfg:        cfg,
		dsm:        dsm,
		ms:         ms,
	}
}

// Start launches the heartbeat and config-poll goroutines.
// They run until ctx is cancelled.
func (s *InstanceSync) Start(ctx context.Context) {
	go s.heartbeatLoop(ctx)
	go s.configPollLoop(ctx)
	log.Printf("[InstanceSync] started | env=%s poll=%ds heartbeat=%ds",
		s.cfg.EnvironmentID, s.cfg.PollIntervalS, s.cfg.HeartbeatIntervalS)
}

func (s *InstanceSync) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.cfg.HeartbeatIntervalS) * time.Second)
	defer ticker.Stop()
	s.writeHeartbeat(ctx) // write immediately on start
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.writeHeartbeat(ctx)
		}
	}
}

func (s *InstanceSync) writeHeartbeat(ctx context.Context) {
	if !s.dsm.IsConfigured(config.DomainGatewayInstances) {
		return
	}
	hostname, _ := os.Hostname()
	rec := InstanceRecord{
		InstanceID:     s.instanceID,
		EnvironmentID:  s.cfg.EnvironmentID,
		Hostname:       hostname,
		CurrentVersion: s.currentVersion,
		LastHeartbeat:  time.Now().Unix(),
		Status:         "alive",
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	if err := s.dsm.PutGlobal(ctx, config.DomainGatewayInstances, s.instanceID, data); err != nil {
		log.Printf("[InstanceSync] heartbeat write failed: %v", err)
	}
	s.refreshInstanceCount(ctx)
}

func (s *InstanceSync) refreshInstanceCount(ctx context.Context) {
	if !s.dsm.IsConfigured(config.DomainGatewayInstances) {
		return
	}
	keys, err := s.dsm.ListGlobalKeys(ctx, config.DomainGatewayInstances, s.cfg.EnvironmentID+"/")
	if err != nil {
		return
	}
	now := time.Now().Unix()
	ttl := int64(s.cfg.HeartbeatIntervalS * 3) // 3× heartbeat = TTL
	alive := 0
	for _, k := range keys {
		data, ok, err := s.dsm.GetGlobal(ctx, config.DomainGatewayInstances, k)
		if err != nil || !ok {
			continue
		}
		var rec InstanceRecord
		if json.Unmarshal(data, &rec) == nil && (now-rec.LastHeartbeat) < ttl {
			alive++
		}
	}
	if alive > 0 {
		s.instanceCount.Store(int32(alive))
	}
}

// InstanceCount returns the number of alive gateway instances in this environment.
// Returns 1 as a safe default when no datastore is configured or no records exist.
func (s *InstanceSync) InstanceCount() int {
	if n := s.instanceCount.Load(); n > 0 {
		return int(n)
	}
	return 1
}

func (s *InstanceSync) configPollLoop(ctx context.Context) {
	if s.cfg.PollIntervalS == 0 {
		return // polling disabled
	}
	ticker := time.NewTicker(time.Duration(s.cfg.PollIntervalS) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollConfigVersion(ctx)
		}
	}
}

func (s *InstanceSync) pollConfigVersion(ctx context.Context) {
	if !s.dsm.IsConfigured(config.DomainConfigVersions) {
		return
	}
	keys, err := s.dsm.ListGlobalKeys(ctx, config.DomainConfigVersions, s.cfg.EnvironmentID+"/")
	if err != nil || len(keys) == 0 {
		return
	}
	// Find the highest version key for our environment.
	// Keys are stored as "{environmentID}/{versionID}" so lexicographic sort gives latest.
	latestKey := keys[0]
	for _, k := range keys[1:] {
		if k > latestKey {
			latestKey = k
		}
	}
	data, ok, err := s.dsm.GetGlobal(ctx, config.DomainConfigVersions, latestKey)
	if err != nil || !ok {
		return
	}
	var rec ConfigVersionRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return
	}
	if rec.Status != "published" {
		return
	}
	if rec.ID <= s.currentVersion {
		return // already running this version or newer
	}
	log.Printf("[InstanceSync] new config version %d available (was %d), applying...", rec.ID, s.currentVersion)
	if err := s.ms.Bootstrap(ctx, s.dsm); err != nil {
		log.Printf("[InstanceSync] failed to apply config version %d: %v", rec.ID, err)
		return
	}
	s.currentVersion = rec.ID
	s.ms.configVersion.Store(uint32(rec.ID))
	log.Printf("[InstanceSync] applied config version %d", rec.ID)
	s.writeHeartbeat(ctx) // update heartbeat immediately with new version
}

// CurrentVersion returns the config version this instance is currently running.
func (s *InstanceSync) CurrentVersion() uint64 {
	return s.currentVersion
}

// SetVersion is called by the deploy handler after a direct /sync call
// so the instance knows it's already on the latest version without waiting for a poll cycle.
func (s *InstanceSync) SetVersion(v uint64) {
	s.currentVersion = v
}
