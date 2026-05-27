package sync

// namedVarToSlotKey maps every user-facing variable field name to its
// corresponding internal compiler key. This is the only place in the sync
// package that knows about the _slot abstraction used by the compiler.
//
// Users write the left-hand key in their YAML (e.g. body_var: my_payload).
// The gateway compiler reads the right-hand key (e.g. body_slot: <index>).
// The loader and publisher translate between these at load/publish time.
//
// To add a new translatable field: add one entry here and add the matching
// user-facing StepField in named_vars.go's substitution table.
var namedVarToSlotKey = map[string]string{
	// grpc_call: dynamic URL, request body, response body, HTTP status
	"url_var":      "url_slot",
	"body_var":     "body_slot",
	"response_var": "response_slot",
	"status_var":   "status_slot",

	// check_upstream_rate_limit: URL variable passed through input map
	"input.url_var": "input.url_slot",

	// aes_encrypt_slot_key / aes_decrypt_slot_key: runtime AES key
	"input.key_var": "input.key_slot",

	// emit_event (ingest pipeline): payload, model name, session ID
	"input.payload_var": "input.payload_slot",
	"input.model_var":   "input.model_slot",
	"input.session_var": "input.session_slot",
}

// ForbiddenSlotKeys is the set of internal _slot-suffixed input keys that
// must never appear in user-authored YAML or JSON bundle files. Each entry
// maps the forbidden internal key to the user-facing alternative that should
// be used instead, enabling precise error messages in the linter.
//
// Example: "body_slot" → "body_var" (use body_var: my_payload instead)
var ForbiddenSlotKeys = func() map[string]string {
	out := make(map[string]string, len(namedVarToSlotKey))
	for userKey, slotKey := range namedVarToSlotKey {
		out[slotKey] = userKey
	}
	return out
}()

// NamedVarToSlotKey translates a user-facing variable field name to the
// corresponding internal slot key consumed by the compiler. Returns ("", false)
// when the name is not a known user-facing alias.
func NamedVarToSlotKey(userKey string) (slotKey string, ok bool) {
	slotKey, ok = namedVarToSlotKey[userKey]
	return
}

// SlotKeyToNamedVar is the inverse of NamedVarToSlotKey: given an internal
// slot key, return the user-facing alias. Returns ("", false) when not found.
func SlotKeyToNamedVar(slotKey string) (userKey string, ok bool) {
	userKey, ok = ForbiddenSlotKeys[slotKey]
	return
}
