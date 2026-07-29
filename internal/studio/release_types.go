package studio

import (
	"encoding/json"
	"time"
)

// ReleaseItem is a reference to a named API or flow.
type ReleaseItem struct {
	Type string `json:"type"` // "api" or "flow"
	Name string `json:"name"`
}

// ExcludeItem specifies an API or flow to exclude from a release deployment.
type ExcludeItem struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Action string `json:"action,omitempty"` // "skip" (default), "delete", "hold"
	Reason string `json:"reason,omitempty"`
}

// EndpointChange records endpoint-level additions and removals within one API.
type EndpointChange struct {
	APIName string   `json:"api_name"`
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

// Changeset describes what changed between two deployed versions.
type Changeset struct {
	APIsAdded       []string         `json:"apis_added,omitempty"`
	APIsModified    []string         `json:"apis_modified,omitempty"`
	APIsDeleted     []string         `json:"apis_deleted,omitempty"`
	FlowsAdded      []string         `json:"flows_added,omitempty"`
	FlowsModified   []string         `json:"flows_modified,omitempty"`
	FlowsDeleted    []string         `json:"flows_deleted,omitempty"`
	EndpointChanges []EndpointChange `json:"endpoint_changes,omitempty"`
}

// Snapshot is the complete manifest of active APIs and flows at a specific version.
type Snapshot struct {
	APIs  []string `json:"apis"`
	Flows []string `json:"flows"`
}

// VersionRecord is one entry in the version history for an environment.
type VersionRecord struct {
	VersionID         string    `json:"version_id"`
	EnvironmentID     string    `json:"environment_id"`
	ReleaseID         string    `json:"release_id"`
	ReleaseName       string    `json:"release_name,omitempty"`
	DeployedAt        int64     `json:"deployed_at"` // Unix seconds
	DeployedBy        string    `json:"deployed_by,omitempty"`
	Message           string    `json:"message,omitempty"`
	Status            string    `json:"status"` // "active" | "voided"
	VoidedBy          string    `json:"voided_by,omitempty"`
	VoidedAt          int64     `json:"voided_at,omitempty"`
	VoidReason        string    `json:"void_reason,omitempty"`
	PreviousVersionID string    `json:"previous_version_id,omitempty"`
	Changeset         Changeset `json:"changeset"`
	Snapshot          Snapshot  `json:"snapshot"`
	PayloadRef        string    `json:"payload_ref,omitempty"` // release ID for payload lookup
}

// FlowBaselineRecord is a tagged, versioned snapshot of flow definitions.
// Tags follow the convention: "stable", "beta", "dev".
type FlowBaselineRecord struct {
	Tag         string                     `json:"tag"`
	Description string                     `json:"description,omitempty"`
	Flows       map[string]json.RawMessage `json:"flows"`
	CreatedAt   time.Time                  `json:"created_at"`
	CreatedBy   string                     `json:"created_by,omitempty"`
	Status      string                     `json:"status"` // "active" | "deprecated"
}
