package sync

import (
	"strings"

	"rah/internal/control"
)

// UserFacingFields returns the subset of a step descriptor's Fields that
// should appear in user-authored YAML and in the linter's schema validation.
//
// It applies two transformations to the raw descriptor Fields list:
//
//  1. Any field whose Key ends in "_slot" and has a known user-facing alias in
//     the namedVarToSlotKey table is replaced with a renamed StepField using
//     the alias key (e.g. body_slot → body_var). The Label, Description and
//     Placeholder are preserved.
//
//  2. Any remaining field whose Key ends in "_slot" (i.e. has no alias) is
//     suppressed entirely — it is an internal-only field that users should
//     never set directly.
//
// Fields that do not end in "_slot" are passed through unchanged.
//
// This function is used by the JSON Schema generator (S4) and the linter
// (S5) so they only validate against user-visible fields.
func UserFacingFields(descriptor control.StepDescriptor) []control.StepField {
	// Build reverse lookup: internal slot key → user-facing key
	slotToUser := make(map[string]string, len(namedVarToSlotKey))
	for userKey, slotKey := range namedVarToSlotKey {
		slotToUser[slotKey] = userKey
	}

	out := make([]control.StepField, 0, len(descriptor.Fields))
	for _, f := range descriptor.Fields {
		if !strings.HasSuffix(f.Key, "_slot") {
			out = append(out, f)
			continue
		}
		// _slot field: substitute user-facing alias when one exists
		if userKey, ok := slotToUser[f.Key]; ok {
			out = append(out, control.StepField{
				Key:         userKey,
				Label:       f.Label,
				Description: f.Description,
				Placeholder: f.Placeholder,
			})
		}
		// No alias → internal-only; suppress from user-facing view
	}
	return out
}

// IsSlotKey reports whether the given input key is an internal _slot-suffixed
// key that must not appear in user YAML. Returns true for any key ending in
// "_slot", whether or not it appears in the translation table.
func IsSlotKey(key string) bool {
	return strings.HasSuffix(key, "_slot")
}

// SlotKeyAlternative returns the user-facing alternative for a forbidden slot
// key, or an empty string if no named alternative is registered. Used by the
// linter to produce actionable error suggestions.
func SlotKeyAlternative(slotKey string) string {
	alt, _ := SlotKeyToNamedVar(slotKey)
	return alt
}
