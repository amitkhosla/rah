package studio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/amitkhosla/rah/internal/control"
)

// hashDefinition returns a sha256 hex digest of the raw JSON bytes.
// Returns "" for nil or empty input.
func hashDefinition(def json.RawMessage) string {
	if len(def) == 0 {
		return ""
	}
	sum := sha256.Sum256(def)
	return hex.EncodeToString(sum[:])
}

// computeChangeset compares two UnifiedSyncRequest payloads and returns the diff.
// If oldPayload is nil or empty, all items in newPayload are treated as added.
func computeChangeset(oldPayload, newPayload json.RawMessage) (Changeset, error) {
	var cs Changeset

	var newReq control.UnifiedSyncRequest
	if len(newPayload) > 0 {
		if err := json.Unmarshal(newPayload, &newReq); err != nil {
			return cs, err
		}
	}

	// Build old maps: name → hash of marshalled definition.
	oldFlowHash := make(map[string]string)
	oldAPIHash := make(map[string]string)

	if len(oldPayload) > 0 {
		var oldReq control.UnifiedSyncRequest
		if err := json.Unmarshal(oldPayload, &oldReq); err != nil {
			return cs, err
		}
		for _, f := range oldReq.Flows {
			if f.Action != "delete" {
				b, _ := json.Marshal(f)
				oldFlowHash[f.Name] = hashDefinition(b)
			}
		}
		for _, a := range oldReq.Apis {
			if a.Action != "delete" {
				b, _ := json.Marshal(a)
				oldAPIHash[a.Name] = hashDefinition(b)
			}
		}
	}

	// Diff flows.
	for _, f := range newReq.Flows {
		switch f.Action {
		case "delete":
			cs.FlowsDeleted = append(cs.FlowsDeleted, f.Name)
		default:
			if _, existed := oldFlowHash[f.Name]; !existed {
				cs.FlowsAdded = append(cs.FlowsAdded, f.Name)
			} else {
				b, _ := json.Marshal(f)
				if hashDefinition(b) != oldFlowHash[f.Name] {
					cs.FlowsModified = append(cs.FlowsModified, f.Name)
				}
			}
		}
	}

	// Diff APIs.
	for _, a := range newReq.Apis {
		switch a.Action {
		case "delete":
			cs.APIsDeleted = append(cs.APIsDeleted, a.Name)
		default:
			b, _ := json.Marshal(a)
			newHash := hashDefinition(b)
			oldHash, existed := oldAPIHash[a.Name]
			if !existed {
				cs.APIsAdded = append(cs.APIsAdded, a.Name)
			} else if newHash != oldHash {
				cs.APIsModified = append(cs.APIsModified, a.Name)
				// Compute endpoint-level diff for modified APIs.
				var oldReq control.UnifiedSyncRequest
				if len(oldPayload) > 0 {
					_ = json.Unmarshal(oldPayload, &oldReq)
				}
				var oldDef json.RawMessage
				for _, oa := range oldReq.Apis {
					if oa.Name == a.Name {
						oldDef, _ = json.Marshal(oa)
						break
					}
				}
				ec := diffEndpoints(a.Name, oldDef, b)
				if len(ec.Added) > 0 || len(ec.Removed) > 0 {
					cs.EndpointChanges = append(cs.EndpointChanges, ec)
				}
			}
		}
	}

	return cs, nil
}

// computeSnapshot applies a Changeset to a previous Snapshot and returns the new Snapshot.
func computeSnapshot(prev Snapshot, cs Changeset) Snapshot {
	apis := make([]string, len(prev.APIs))
	copy(apis, prev.APIs)
	flows := make([]string, len(prev.Flows))
	copy(flows, prev.Flows)

	// Remove deleted items.
	apis = removeAll(apis, cs.APIsDeleted)
	flows = removeAll(flows, cs.FlowsDeleted)

	// Add new items (avoid duplicates).
	apis = appendUnique(apis, cs.APIsAdded)
	flows = appendUnique(flows, cs.FlowsAdded)

	sort.Strings(apis)
	sort.Strings(flows)
	return Snapshot{APIs: apis, Flows: flows}
}

// diffEndpoints computes added and removed endpoint paths between two serialised ApiUpdate values.
func diffEndpoints(apiName string, oldDef, newDef json.RawMessage) EndpointChange {
	ec := EndpointChange{APIName: apiName}

	type apiShape struct {
		EndpointConfigs []struct {
			Path string `json:"path"`
		} `json:"endpoint_configs"`
	}

	var oldA, newA apiShape
	if len(oldDef) > 0 {
		_ = json.Unmarshal(oldDef, &oldA)
	}
	if len(newDef) > 0 {
		_ = json.Unmarshal(newDef, &newA)
	}

	oldPaths := make(map[string]struct{}, len(oldA.EndpointConfigs))
	for _, ep := range oldA.EndpointConfigs {
		oldPaths[ep.Path] = struct{}{}
	}
	newPaths := make(map[string]struct{}, len(newA.EndpointConfigs))
	for _, ep := range newA.EndpointConfigs {
		newPaths[ep.Path] = struct{}{}
	}

	for p := range newPaths {
		if _, ok := oldPaths[p]; !ok {
			ec.Added = append(ec.Added, p)
		}
	}
	for p := range oldPaths {
		if _, ok := newPaths[p]; !ok {
			ec.Removed = append(ec.Removed, p)
		}
	}
	sort.Strings(ec.Added)
	sort.Strings(ec.Removed)
	return ec
}

func removeAll(slice []string, toRemove []string) []string {
	rm := make(map[string]struct{}, len(toRemove))
	for _, s := range toRemove {
		rm[s] = struct{}{}
	}
	out := slice[:0]
	for _, s := range slice {
		if _, skip := rm[s]; !skip {
			out = append(out, s)
		}
	}
	return out
}

func appendUnique(slice []string, toAdd []string) []string {
	existing := make(map[string]struct{}, len(slice))
	for _, s := range slice {
		existing[s] = struct{}{}
	}
	for _, s := range toAdd {
		if _, ok := existing[s]; !ok {
			slice = append(slice, s)
			existing[s] = struct{}{}
		}
	}
	return slice
}
