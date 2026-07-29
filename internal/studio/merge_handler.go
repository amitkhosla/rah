package studio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/control"
)

// mergeConflict describes a single merge conflict between two releases.
type mergeConflict struct {
	Type    string `json:"type"`    // "api" or "flow"
	Name    string `json:"name"`
	Message string `json:"message"`
}

// releaseMergeHandler handles POST /api/releases/{id}/merge.
// It merges all non-conflicting flows and APIs from a source release into the target release.
func (s *Server) releaseMergeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract target release ID from the URL path.
	rest := strings.TrimPrefix(r.URL.Path, "/api/releases/")
	parts := strings.SplitN(rest, "/", 2)
	targetID := parts[0]

	var body struct {
		SourceReleaseID string `json:"source_release_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.SourceReleaseID == "" {
		http.Error(w, "source_release_id required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Load target release.
	targetRec, err := s.store.Get(ctx, targetID)
	if err != nil {
		http.Error(w, "target release not found", http.StatusNotFound)
		return
	}

	// Load source release.
	sourceRec, err := s.store.Get(ctx, body.SourceReleaseID)
	if err != nil {
		http.Error(w, "source release not found", http.StatusNotFound)
		return
	}

	// Parse both payloads as UnifiedSyncRequest.
	var targetReq control.UnifiedSyncRequest
	if len(targetRec.Payload) > 0 {
		if err := json.Unmarshal(targetRec.Payload, &targetReq); err != nil {
			http.Error(w, "failed to parse target payload", http.StatusInternalServerError)
			return
		}
	}

	var sourceReq control.UnifiedSyncRequest
	if len(sourceRec.Payload) > 0 {
		if err := json.Unmarshal(sourceRec.Payload, &sourceReq); err != nil {
			http.Error(w, "failed to parse source payload", http.StatusInternalServerError)
			return
		}
	}

	// Build maps of target items (by name → marshalled hash) for conflict detection.
	targetFlowHash := make(map[string]string, len(targetReq.Flows))
	for _, f := range targetReq.Flows {
		if f.Action != "delete" {
			b, _ := json.Marshal(f)
			targetFlowHash[f.Name] = hashDefinition(b)
		}
	}
	targetAPIHash := make(map[string]string, len(targetReq.Apis))
	for _, a := range targetReq.Apis {
		if a.Action != "delete" {
			b, _ := json.Marshal(a)
			targetAPIHash[a.Name] = hashDefinition(b)
		}
	}

	// Build maps of source items for hash comparison.
	sourceFlowHash := make(map[string]string, len(sourceReq.Flows))
	for _, f := range sourceReq.Flows {
		if f.Action != "delete" {
			b, _ := json.Marshal(f)
			sourceFlowHash[f.Name] = hashDefinition(b)
		}
	}
	sourceAPIHash := make(map[string]string, len(sourceReq.Apis))
	for _, a := range sourceReq.Apis {
		if a.Action != "delete" {
			b, _ := json.Marshal(a)
			sourceAPIHash[a.Name] = hashDefinition(b)
		}
	}

	// Detect conflicts and collect candidates to merge.
	var conflicts []mergeConflict
	var flowsToMerge []control.FlowUpdate
	var apisToMerge []control.ApiUpdate

	for _, f := range sourceReq.Flows {
		if f.Action == "delete" {
			continue
		}
		srcHash := sourceFlowHash[f.Name]
		tgtHash, inTarget := targetFlowHash[f.Name]
		if !inTarget {
			// Not in target → candidate for merge.
			flowsToMerge = append(flowsToMerge, f)
		} else if srcHash == tgtHash {
			// Same definition → skip (already in target).
		} else {
			// Different definition → conflict.
			conflicts = append(conflicts, mergeConflict{
				Type:    "flow",
				Name:    f.Name,
				Message: "modified in both",
			})
		}
	}

	for _, a := range sourceReq.Apis {
		if a.Action == "delete" {
			continue
		}
		srcHash := sourceAPIHash[a.Name]
		tgtHash, inTarget := targetAPIHash[a.Name]
		if !inTarget {
			apisToMerge = append(apisToMerge, a)
		} else if srcHash == tgtHash {
			// Same definition → skip.
		} else {
			conflicts = append(conflicts, mergeConflict{
				Type:    "api",
				Name:    a.Name,
				Message: "modified in both",
			})
		}
	}

	// Return 409 if there are conflicts.
	if len(conflicts) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"conflicts": conflicts,
		})
		return
	}

	// No conflicts — append missing items to target payload.
	itemsMerged := len(flowsToMerge) + len(apisToMerge)
	targetReq.Flows = append(targetReq.Flows, flowsToMerge...)
	targetReq.Apis = append(targetReq.Apis, apisToMerge...)

	updated, err := json.Marshal(targetReq)
	if err != nil {
		http.Error(w, "failed to serialize merged payload", http.StatusInternalServerError)
		return
	}
	targetRec.Payload = json.RawMessage(updated)

	if err := s.store.Put(ctx, targetRec); err != nil {
		http.Error(w, "failed to save merged release", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"merged_from":  body.SourceReleaseID,
		"items_merged": itemsMerged,
	})
}

// releaseCherryPickHandler handles POST /api/releases/{id}/cherry-pick.
// It copies specific named flows/APIs from a source release into the target release.
func (s *Server) releaseCherryPickHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract target release ID from the URL path.
	rest := strings.TrimPrefix(r.URL.Path, "/api/releases/")
	parts := strings.SplitN(rest, "/", 2)
	targetID := parts[0]

	var body struct {
		FromReleaseID string        `json:"from_release_id"`
		Items         []ReleaseItem `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.FromReleaseID == "" {
		http.Error(w, "from_release_id required", http.StatusBadRequest)
		return
	}
	if len(body.Items) == 0 {
		http.Error(w, "items required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Load target and source releases.
	targetRec, err := s.store.Get(ctx, targetID)
	if err != nil {
		http.Error(w, "target release not found", http.StatusNotFound)
		return
	}

	sourceRec, err := s.store.Get(ctx, body.FromReleaseID)
	if err != nil {
		http.Error(w, "source release not found", http.StatusNotFound)
		return
	}

	// Parse source payload.
	var sourceReq control.UnifiedSyncRequest
	if len(sourceRec.Payload) > 0 {
		if err := json.Unmarshal(sourceRec.Payload, &sourceReq); err != nil {
			http.Error(w, "failed to parse source payload", http.StatusInternalServerError)
			return
		}
	}

	// Index source flows and APIs by name for fast lookup.
	sourceFlows := make(map[string]control.FlowUpdate, len(sourceReq.Flows))
	for _, f := range sourceReq.Flows {
		sourceFlows[f.Name] = f
	}
	sourceAPIs := make(map[string]control.ApiUpdate, len(sourceReq.Apis))
	for _, a := range sourceReq.Apis {
		sourceAPIs[a.Name] = a
	}

	// Resolve each requested item from the source.
	type pickedFlow struct{ name string; val control.FlowUpdate }
	type pickedAPI struct{ name string; val control.ApiUpdate }
	var pickedFlows []pickedFlow
	var pickedAPIs []pickedAPI

	for _, item := range body.Items {
		switch item.Type {
		case "flow":
			f, ok := sourceFlows[item.Name]
			if !ok {
				http.Error(w, fmt.Sprintf("flow %s not in source release", item.Name), http.StatusUnprocessableEntity)
				return
			}
			pickedFlows = append(pickedFlows, pickedFlow{name: item.Name, val: f})
		case "api":
			a, ok := sourceAPIs[item.Name]
			if !ok {
				http.Error(w, fmt.Sprintf("api %s not in source release", item.Name), http.StatusUnprocessableEntity)
				return
			}
			pickedAPIs = append(pickedAPIs, pickedAPI{name: item.Name, val: a})
		default:
			http.Error(w, fmt.Sprintf("unknown item type %q", item.Type), http.StatusBadRequest)
			return
		}
	}

	// Parse target payload.
	var targetReq control.UnifiedSyncRequest
	if len(targetRec.Payload) > 0 {
		if err := json.Unmarshal(targetRec.Payload, &targetReq); err != nil {
			http.Error(w, "failed to parse target payload", http.StatusInternalServerError)
			return
		}
	}

	// Build index of existing target flows/APIs for in-place replacement.
	tgtFlowIdx := make(map[string]int, len(targetReq.Flows))
	for i, f := range targetReq.Flows {
		tgtFlowIdx[f.Name] = i
	}
	tgtAPIIdx := make(map[string]int, len(targetReq.Apis))
	for i, a := range targetReq.Apis {
		tgtAPIIdx[a.Name] = i
	}

	var names []string

	// Apply picked flows to target.
	for _, pf := range pickedFlows {
		if idx, exists := tgtFlowIdx[pf.name]; exists {
			targetReq.Flows[idx] = pf.val
		} else {
			targetReq.Flows = append(targetReq.Flows, pf.val)
		}
		names = append(names, pf.name)
	}

	// Apply picked APIs to target.
	for _, pa := range pickedAPIs {
		if idx, exists := tgtAPIIdx[pa.name]; exists {
			targetReq.Apis[idx] = pa.val
		} else {
			targetReq.Apis = append(targetReq.Apis, pa.val)
		}
		names = append(names, pa.name)
	}

	// Re-marshal and save.
	updated, err := json.Marshal(targetReq)
	if err != nil {
		http.Error(w, "failed to serialize updated payload", http.StatusInternalServerError)
		return
	}
	targetRec.Payload = json.RawMessage(updated)

	if err := s.store.Put(ctx, targetRec); err != nil {
		http.Error(w, "failed to save updated release", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"cherry_picked": len(names),
		"items":         names,
	})
}

// releaseBranchHandler handles POST /api/releases/{id}/branch.
// It creates a new draft release whose payload is copied from the most recently
// deployed release for a given environment (base_env).
func (s *Server) releaseBranchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		BaseEnv     string `json:"base_env"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if body.BaseEnv == "" {
		http.Error(w, "base_env required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Find the latest deployed release for the specified environment.
	all, err := s.store.List(ctx)
	if err != nil {
		http.Error(w, "failed to list releases", http.StatusInternalServerError)
		return
	}

	var sourceRec ReleaseRecord
	var latestTime time.Time
	found := false

	for _, rec := range all {
		dep, ok := rec.Environments[body.BaseEnv]
		if !ok {
			continue
		}
		if dep.Status != "deployed" {
			continue
		}
		if !found || dep.DeployedAt.After(latestTime) {
			sourceRec = rec
			latestTime = dep.DeployedAt
			found = true
		}
	}

	if !found {
		http.Error(w, fmt.Sprintf("no deployed release found for env: %s", body.BaseEnv), http.StatusNotFound)
		return
	}

	// Auto-generate a name if none provided.
	name := body.Name
	if name == "" {
		name = fmt.Sprintf("branch-from-%s", sourceRec.ReleaseID)
	}

	newID := fmt.Sprintf("rel-%d", time.Now().UnixNano())
	newRec := ReleaseRecord{
		ReleaseID:     newID,
		CreatedAt:     nowJSON(),
		Name:          name,
		Description:   body.Description,
		Payload:       sourceRec.Payload,
		BaseVersionID: sourceRec.ReleaseID,
		Status:        "draft",
	}

	if err := s.store.Put(ctx, newRec); err != nil {
		http.Error(w, "failed to save new release", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(newRec)
}
