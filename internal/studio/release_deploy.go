package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// releaseDeployHandler handles POST /api/releases/:id/deploy.
//
// Flow:
//  1. Check require_promotion_from gate.
//  2. If require_manual_approval and not yet approved → set pending state, return 202.
//  3. Run test suites if require_tests is set.
//  4. Execute rollout (all or phased). Phased rollout returns 202 on phase-approval pause.
//  5. Record version history on full success.
func (s *Server) releaseDeployHandler(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Env     string `json:"env"`
		ByUser  string `json:"by_user"`
		Message string `json:"message"`
		// Force skips the test gate (requires explicit opt-in).
		Force bool `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Env == "" {
		http.Error(w, "env is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	rec, err := s.store.Get(ctx, id)
	if err != nil {
		http.Error(w, "release not found", http.StatusNotFound)
		return
	}

	envCfg := findEnvironment(s.config.Environments, req.Env)

	// ── Gate: require_promotion_from ────────────────────────────────────────
	if envCfg != nil && envCfg.Gate.RequirePromotionFrom != "" {
		fromEnv := envCfg.Gate.RequirePromotionFrom
		if dep, ok := rec.Environments[fromEnv]; !ok || dep.Status != "deployed" {
			http.Error(w, fmt.Sprintf("env %q requires prior deployment to %q", req.Env, fromEnv), http.StatusPreconditionFailed)
			return
		}
	}

	// ── Gate: manual approval ───────────────────────────────────────────────
	if envCfg != nil && envCfg.Gate.RequireManualApproval {
		dep := rec.Environments[req.Env]
		if dep.ApprovalStatus != "approved" {
			// Set pending state and return 202 so the caller knows to approve.
			if rec.Environments == nil {
				rec.Environments = make(map[string]EnvDeployment)
			}
			dep.ApprovalRequired = true
			dep.ApprovalStatus = "pending"
			dep.ByUser = req.ByUser
			// Stash the message so the approve handler can replay the deploy.
			rec.Environments[req.Env] = dep
			_ = s.store.Put(ctx, rec)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":     "pending_approval",
				"release_id": rec.ReleaseID,
				"env":        req.Env,
			})
			return
		}
	}

	// ── Gate: test suites ────────────────────────────────────────────────────
	if !req.Force && envCfg != nil && envCfg.Gate.RequireTests && len(envCfg.Gate.TestSuites) > 0 {
		selected := s.selectTargets(nil, nil)
		if err := s.runTestGates(ctx, selected, envCfg.Gate.TestSuites); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":  "test gate failed",
				"detail": err.Error(),
			})
			return
		}
	}

	// ── Rollout ──────────────────────────────────────────────────────────────
	var results []ReleaseDeployResult

	if envCfg != nil && envCfg.Rollout.Strategy == "phased" {
		results, err = s.executePhasedRollout(ctx, w, r, rec, req.Env, req.ByUser, envCfg, 0)
		if err != nil {
			// err == errPausedForApproval means we already wrote the 202 response.
			return
		}
	} else {
		targetURLs, resolveErr := s.resolveTargetURLs(req.Env)
		if resolveErr != nil {
			http.Error(w, resolveErr.Error(), http.StatusInternalServerError)
			return
		}
		results = s.deployToURLs(ctx, rec.Payload, targetURLs)
	}

	// ── Update release record ────────────────────────────────────────────────
	if rec.Environments == nil {
		rec.Environments = make(map[string]EnvDeployment)
	}

	prevDep := rec.Environments[req.Env]
	prevVersionID := prevDep.VersionID

	allSuccess := true
	for _, res := range results {
		if !res.Success {
			allSuccess = false
			break
		}
	}

	deployStatus := "deployed"
	if !allSuccess {
		deployStatus = "partial"
	}

	// Version history (always record, even on partial success).
	versionID := fmt.Sprintf("v-%d", time.Now().UnixNano())
	rec.Environments[req.Env] = EnvDeployment{
		DeployedAt: time.Now(),
		Status:     deployStatus,
		ByUser:     req.ByUser,
		Results:    results,
		VersionID:  versionID,
	}

	_ = s.store.Put(ctx, rec)

	// Record version in history store.
	s.appendVersionRecord(ctx, rec, req.Env, versionID, prevVersionID, req.ByUser, req.Message)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"release_id": rec.ReleaseID,
		"env":        req.Env,
		"version_id": versionID,
		"status":     deployStatus,
		"results":    results,
	})
}

// releaseApproveHandler handles POST /api/releases/:id/approve.
// Transitions a pending-approval deployment to approved and triggers the actual deploy.
func (s *Server) releaseApproveHandler(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Env        string `json:"env"`
		ApprovedBy string `json:"approved_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Env == "" {
		http.Error(w, "env is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	rec, err := s.store.Get(ctx, id)
	if err != nil {
		http.Error(w, "release not found", http.StatusNotFound)
		return
	}

	dep, ok := rec.Environments[req.Env]
	if !ok || dep.ApprovalStatus != "pending" {
		http.Error(w, "no pending approval for this env", http.StatusBadRequest)
		return
	}

	now := time.Now()
	dep.ApprovalStatus = "approved"
	dep.ApprovedBy = req.ApprovedBy
	dep.ApprovedAt = &now
	if rec.Environments == nil {
		rec.Environments = make(map[string]EnvDeployment)
	}
	rec.Environments[req.Env] = dep
	_ = s.store.Put(ctx, rec)

	// Trigger actual deployment now that approval is granted.
	envCfg := findEnvironment(s.config.Environments, req.Env)

	var results []ReleaseDeployResult
	if envCfg != nil && envCfg.Rollout.Strategy == "phased" {
		// Resume phased rollout from the phase that paused for approval.
		// dep.CurrentPhase is 1-indexed (phases completed), so next phase is at that index.
		startPhase := dep.CurrentPhase
		var rolloutErr error
		results, rolloutErr = s.executePhasedRollout(ctx, w, r, rec, req.Env, req.ApprovedBy, envCfg, startPhase)
		if rolloutErr != nil {
			// errPausedForApproval: another phase boundary was hit, response already written.
			return
		}
	} else {
		targetURLs, resolveErr := s.resolveTargetURLs(req.Env)
		if resolveErr != nil {
			http.Error(w, resolveErr.Error(), http.StatusInternalServerError)
			return
		}
		results = s.deployToURLs(ctx, rec.Payload, targetURLs)
	}

	allSuccess := true
	for _, res := range results {
		if !res.Success {
			allSuccess = false
			break
		}
	}
	deployStatus := "deployed"
	if !allSuccess {
		deployStatus = "partial"
	}

	versionID := fmt.Sprintf("v-%d", time.Now().UnixNano())
	prevVersionID := dep.VersionID
	dep.Status = deployStatus
	dep.VersionID = versionID
	dep.Results = results
	dep.DeployedAt = now
	rec.Environments[req.Env] = dep
	_ = s.store.Put(ctx, rec)

	s.appendVersionRecord(ctx, rec, req.Env, versionID, prevVersionID, req.ApprovedBy, "approved deploy")

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"release_id":  rec.ReleaseID,
		"env":         req.Env,
		"version_id":  versionID,
		"status":      deployStatus,
		"approved_by": req.ApprovedBy,
		"results":     results,
	})
}

// errPausedForApproval is a sentinel that tells executePhasedRollout's caller
// that a 202 response has already been written and the function should return.
var errPausedForApproval = fmt.Errorf("paused for phase approval")

// executePhasedRollout deploys phase by phase starting at startPhase (0-indexed).
// startPhase=0 begins a fresh rollout; higher values resume after an approval pause.
// If a phase has RequireApprovalAfter, it writes a 202 and returns errPausedForApproval.
func (s *Server) executePhasedRollout(
	ctx context.Context, w http.ResponseWriter, r *http.Request,
	rec ReleaseRecord, env, byUser string, envCfg *EnvironmentConfig, startPhase int,
) ([]ReleaseDeployResult, error) {
	var allResults []ReleaseDeployResult

	for phaseIdx := startPhase; phaseIdx < len(envCfg.Rollout.Phases); phaseIdx++ {
		phase := envCfg.Rollout.Phases[phaseIdx]
		var phaseURLs []string
		for _, dName := range phase.Deployments {
			dep := findDeployment(s.config.Deployments, dName)
			if dep != nil {
				phaseURLs = append(phaseURLs, dep.Targets...)
			}
		}
		phaseResults := s.deployToURLs(ctx, rec.Payload, phaseURLs)
		allResults = append(allResults, phaseResults...)

		// Update current phase in record.
		if rec.Environments == nil {
			rec.Environments = make(map[string]EnvDeployment)
		}
		dep := rec.Environments[env]
		dep.CurrentPhase = phaseIdx + 1
		dep.ByUser = byUser
		dep.Status = "in_progress"
		rec.Environments[env] = dep
		_ = s.store.Put(ctx, rec)

		if phase.RequireApprovalAfter && phaseIdx < len(envCfg.Rollout.Phases)-1 {
			dep.ApprovalRequired = true
			dep.ApprovalStatus = "pending"
			rec.Environments[env] = dep
			_ = s.store.Put(ctx, rec)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":          "pending_phase_approval",
				"release_id":      rec.ReleaseID,
				"env":             env,
				"completed_phase": phaseIdx + 1,
				"next_phase":      phaseIdx + 2,
				"results_so_far":  allResults,
			})
			return nil, errPausedForApproval
		}
	}

	return allResults, nil
}

// runTestGates calls POST /test/suites/{id}/run on the first available target for each suite.
// Returns an error if any suite fails.
func (s *Server) runTestGates(ctx context.Context, targets []Target, suiteIDs []string) error {
	if len(targets) == 0 {
		return fmt.Errorf("no gateway targets available for test gating")
	}

	for _, suiteID := range suiteIDs {
		var lastErr error
		var ran bool
		for _, t := range targets {
			for _, raw := range t.URLs {
				testURL, err := buildTargetURL(raw, "/test/suites/"+suiteID+"/run", "")
				if err != nil {
					lastErr = err
					continue
				}
				body, status, err := s.gatewayCall(ctx, http.MethodPost, testURL, nil)
				if err != nil {
					lastErr = err
					continue
				}
				if status >= 300 {
					lastErr = fmt.Errorf("test suite %q: gateway returned %d", suiteID, status)
					continue
				}
				var result struct {
					Passed bool `json:"passed"`
				}
				if err := json.Unmarshal(body, &result); err != nil {
					lastErr = fmt.Errorf("test suite %q: malformed response: %w", suiteID, err)
					continue
				}
				if !result.Passed {
					return fmt.Errorf("test suite %q did not pass", suiteID)
				}
				ran = true
				break
			}
			if ran {
				break
			}
		}
		if !ran && lastErr != nil {
			return fmt.Errorf("test suite %q: could not run: %w", suiteID, lastErr)
		}
	}
	return nil
}

// deployToURLs sends the sync payload to each raw gateway URL in sequence.
func (s *Server) deployToURLs(ctx context.Context, payload []byte, rawURLs []string) []ReleaseDeployResult {
	results := make([]ReleaseDeployResult, 0, len(rawURLs))
	for _, raw := range rawURLs {
		s.preSyncDeploy(ctx, raw, payload)
		targetURL, err := buildTargetURL(raw, "/sync", "")
		if err != nil {
			results = append(results, ReleaseDeployResult{Target: raw, Success: false, Message: err.Error()})
			continue
		}
		hReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(payload))
		if err != nil {
			results = append(results, ReleaseDeployResult{Target: raw, Success: false, Message: err.Error()})
			continue
		}
		hReq.Header.Set("Content-Type", "application/json")
		resp, err := s.httpClient.Do(hReq)
		if err != nil {
			results = append(results, ReleaseDeployResult{Target: raw, Success: false, Message: err.Error()})
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		success := resp.StatusCode >= 200 && resp.StatusCode < 300
		msg := ""
		if !success {
			detail := strings.TrimSpace(string(body))
			if detail == "" {
				msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
			} else {
				msg = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, detail)
			}
		}
		results = append(results, ReleaseDeployResult{Target: raw, Success: success, Message: msg})
	}
	return results
}

// appendVersionRecord computes changeset + snapshot and appends to the version store.
func (s *Server) appendVersionRecord(ctx context.Context, rec ReleaseRecord, envID, versionID, prevVersionID, byUser, message string) {
	var prevPayload []byte
	if prevVersionID != "" {
		prevRec, err := s.versionStore.GetVersion(envID, prevVersionID)
		if err == nil && prevRec.PayloadRef != "" {
			if pr, err := s.store.Get(ctx, prevRec.PayloadRef); err == nil {
				prevPayload = pr.Payload
			}
		}
	}

	cs, err := computeChangeset(prevPayload, rec.Payload)
	if err != nil {
		log.Printf("[Studio] appendVersionRecord: changeset error: %v", err)
	}

	var prevSnapshot Snapshot
	if prevVersionID != "" {
		if pv, err := s.versionStore.GetVersion(envID, prevVersionID); err == nil {
			prevSnapshot = pv.Snapshot
		}
	}
	snap := computeSnapshot(prevSnapshot, cs)

	vr := &VersionRecord{
		VersionID:         versionID,
		EnvironmentID:     envID,
		ReleaseID:         rec.ReleaseID,
		ReleaseName:       rec.Name,
		DeployedAt:        time.Now().Unix(),
		DeployedBy:        byUser,
		Message:           message,
		Status:            "active",
		PreviousVersionID: prevVersionID,
		Changeset:         cs,
		Snapshot:          snap,
		PayloadRef:        rec.ReleaseID,
	}

	if err := s.versionStore.AppendVersion(envID, vr); err != nil {
		log.Printf("[Studio] appendVersionRecord: store error: %v", err)
	}
}

// resolveTargetURLs returns the gateway URLs to deploy to for the given environment.
// If the environment has an explicit Rollout.Deployments list, only those are used;
// an error is returned when none resolve rather than silently falling back to all targets.
// When no environment config exists the full registered target list is returned.
func (s *Server) resolveTargetURLs(env string) ([]string, error) {
	envCfg := findEnvironment(s.config.Environments, env)
	if envCfg != nil && len(envCfg.Rollout.Deployments) > 0 {
		var urls []string
		for _, dName := range envCfg.Rollout.Deployments {
			dep := findDeployment(s.config.Deployments, dName)
			if dep != nil {
				urls = append(urls, dep.Targets...)
			}
		}
		if len(urls) == 0 {
			return nil, fmt.Errorf("env %q: rollout.deployments is configured but none of the named deployments resolved to targets; check deployment names in config", env)
		}
		return urls, nil
	}
	// No env config or no explicit deployment list — use all registered targets.
	var urls []string
	for _, t := range s.selectTargets(nil, nil) {
		urls = append(urls, t.URLs...)
	}
	return urls, nil
}
