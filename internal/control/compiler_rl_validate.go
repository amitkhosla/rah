package control

import (
	"fmt"
	"strings"

	registrypkg "github.com/amitkhosla/rah/internal/registry"
)

// rlConfigLookup is a narrow interface used by validation so it is testable
// without a full RegistryManager.
type rlConfigLookup interface {
	GetRateLimitConfigV2(name string) *registrypkg.RateLimitConfigV2
}

// validateRLPolicies runs the four advisory validation checks for one API's
// rate limit policy table and returns any warnings found. Warnings are
// non-blocking â€” the caller should still proceed with bake.
//
// Checks performed:
//  1. no_flow        â€” flow name empty or not found in flowCfgs
//  2. not_enforced   â€” policies defined, skip=false, but flow tree has no RL step
//  3. config_missing â€” named / dynamic entries reference a config that doesn't exist
//  4. slot_unfilled  â€” count_by=slot or dynamic source=slot.<name>, but nothing
//     in the flow tree assigns that slot
func validateRLPolicies(
	apiName string,
	flowName string,
	policies []APIRateLimitEntry,
	skipRL bool,
	flowCfgs map[string][]StepConfig,
	reg rlConfigLookup,
) []RateLimitWarning {

	var warns []RateLimitWarning

	// â”€â”€ 1. no_flow â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
	if flowName == "" {
		warns = append(warns, RateLimitWarning{
			Code:    RLWarnNoFlow,
			Message: fmt.Sprintf("API %q has no flow assigned", apiName),
			API:     apiName,
		})
		return warns // can't check further
	}

	flow, exists := flowCfgs[flowName]
	if !exists {
		warns = append(warns, RateLimitWarning{
			Code:    RLWarnNoFlow,
			Message: fmt.Sprintf("API %q references unknown flow %q", apiName, flowName),
			API:     apiName,
		})
		return warns // can't check further
	}

	// Nothing to validate when there are no policies or skip is set.
	if skipRL || len(policies) == 0 {
		return warns
	}

	// â”€â”€ 2. not_enforced â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
	if !flowHasRateLimitStep(flow, flowCfgs) {
		warns = append(warns, RateLimitWarning{
			Code: RLWarnNotEnforced,
			Message: fmt.Sprintf(
				"API %q has rate limit policies but flow %q contains no rate limit step â€” "+
					"policies will be auto-injected at flow start",
				apiName, flowName),
			API: apiName,
		})
	}

	// â”€â”€ Per-entry checks (3 + 4) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
	for i, entry := range policies {
		switch entry.Kind {
		case RLEntryNamed:
			// 3. config_missing â€” named config must exist in the registry
			if entry.Config != "" && reg != nil {
				if reg.GetRateLimitConfigV2(entry.Config) == nil {
					warns = append(warns, RateLimitWarning{
						Code:    RLWarnConfigMissing,
						Message: fmt.Sprintf("API %q entry %d: named config %q not found", apiName, i, entry.Config),
						API:     apiName,
						Row:     i,
					})
				}
			}

		case RLEntryDynamic:
			if entry.Dynamic != nil {
				// 3. config_missing â€” every mapped config must exist
				for rtVal, cfgName := range entry.Dynamic.Mappings {
					if reg != nil && reg.GetRateLimitConfigV2(cfgName) == nil {
						warns = append(warns, RateLimitWarning{
							Code: RLWarnConfigMissing,
							Message: fmt.Sprintf(
								"API %q entry %d: dynamic mapping key %q â†’ config %q not found",
								apiName, i, rtVal, cfgName),
							API: apiName,
							Row: i,
						})
					}
				}
				// 4. slot_unfilled â€” if source=slot.<name>, check flow fills it
				if strings.HasPrefix(entry.Dynamic.Source, "slot.") {
					slotName := strings.TrimPrefix(entry.Dynamic.Source, "slot.")
					if slotName != "" && !flowFillsSlot(flow, flowCfgs, slotName) {
						warns = append(warns, RateLimitWarning{
							Code: RLWarnSlotUnfilled,
							Message: fmt.Sprintf(
								"API %q entry %d: dynamic source slot %q is never filled in flow %q",
								apiName, i, slotName, flowName),
							API:  apiName,
							Row:  i,
							Slot: slotName,
						})
					}
				}
			}

		case RLEntryFixed:
			// Fixed entries are self-contained inline config â€” nothing to validate externally.
		}

		// 4. slot_unfilled â€” count_by=slot must have a slot that is filled
		if entry.CountBy == "slot" && entry.SlotSource != "" {
			if !flowFillsSlot(flow, flowCfgs, entry.SlotSource) {
				warns = append(warns, RateLimitWarning{
					Code: RLWarnSlotUnfilled,
					Message: fmt.Sprintf(
						"API %q entry %d: count_by=slot references slot %q which is never filled in flow %q",
						apiName, i, entry.SlotSource, flowName),
					API:  apiName,
					Row:  i,
					Slot: entry.SlotSource,
				})
			}
		}
	}

	return warns
}

// flowFillsSlot returns true if any step in the flow (or any reachable sub-flow)
// assigns or binds the named slot (matched by the step's 'as' field).
func flowFillsSlot(flow []StepConfig, fragments map[string][]StepConfig, slotName string) bool {
	visited := make(map[string]bool)
	return walkForSlotFill(flow, fragments, slotName, visited)
}

func walkForSlotFill(
	flow []StepConfig,
	fragments map[string][]StepConfig,
	slotName string,
	visited map[string]bool,
) bool {
	for _, step := range flow {
		// A step fills the slot when its output alias matches the slot name.
		if step.As == slotName {
			return true
		}
		// Recurse into inline nested lists (foreach/while Do).
		if len(step.Do) > 0 {
			if walkForSlotFill(step.Do, fragments, slotName, visited) {
				return true
			}
		}
		// Recurse into named sub-flow references.
		for _, ref := range []string{step.FlowName, step.Then, step.Else} {
			if ref == "" || visited[ref] {
				continue
			}
			if sub, ok := fragments[ref]; ok {
				visited[ref] = true
				if walkForSlotFill(sub, fragments, slotName, visited) {
					return true
				}
			}
		}
		// Recurse into switch case sub-flows.
		for _, caseFlow := range step.Cases {
			if caseFlow == "" || visited[caseFlow] {
				continue
			}
			if sub, ok := fragments[caseFlow]; ok {
				visited[caseFlow] = true
				if walkForSlotFill(sub, fragments, slotName, visited) {
					return true
				}
			}
		}
	}
	return false
}
