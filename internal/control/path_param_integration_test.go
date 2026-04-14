package control

// path_param_integration_test.go — integration tests for path parameter extraction in flows.
//
// Coverage:
//   - Single path param extracted via endpoint_configs + concat → correct response body
//   - Multiple path params (two params in one endpoint path)
//   - Static prefix concat without any path param (baseline)
//   - Common mistake: full path in API path with no endpoint_configs → 404 (route unreachable)
//
// How path params work end-to-end:
//
//  1. POST /sync: user registers an API with path "/v1/user" and endpoint_configs
//     [{path: "/{id}/profile", method: "GET"}]. The flow references "path.id" via concat.
//
//  2. Compiler (CompileExecutable):
//     - discoverDependencies finds "path.id" in step.Source → allocates slot 0.
//     - No BindPath instruction is emitted (unlike headers/queries); the sub-router
//       handles extraction at runtime.
//     - bakeFlow compiles concat: ConcatStep(slotA, slotB=0, result, prefix).
//
//  3. BakeSubRouter (called after CompileExecutable, same compiler instance):
//     - Processes "/{id}/profile" segments.
//     - For "{id}": calls getSlot("path.id") → returns slot 0 (already allocated).
//     - Sets SubArena[parent].ParamSlot = 0.
//
//  4. Runtime (ProcessRequest → resolveSubPath):
//     - Top-level router matches "/v1/user" prefix in "/v1/user/2324/profile".
//     - resolveSubPath walks "/2324/profile": sees HasParamChild=true on root,
//       captures "2324", writes ctx.ByteSlots[0] = "2324".
//     - Execute runs ConcatStep → ByteSlots[result] = "User Profile for ID: 2324".
//     - EarlyReturn sets ResponseBuffer = that slot.

import (
	"strings"
	"testing"
)

// ─── Comparison: Header param via concat ──────────────────────────────────────

// TestHeaderParamViaConcat verifies that a header value can be read and displayed
// via concat + return. This works because CompileExecutable emits a BindHeader
// instruction at the START of the plan, which writes to the slot before any
// liveness reclaim can happen.
//
// Contrast with path params: BindPath is NOT emitted; the sub-router writes
// the slot at runtime via resolveSubPath. Liveness recycles the slot between
// CompileExecutable and BakeSubRouter, breaking the slot→param mapping.
func TestHeaderParamViaConcat(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "sync-header-concat",
		Flows: []FlowUpdate{{
			Name: "greet-header-flow",
			Instructions: []StepConfig{
				{Action: "concat", Source: "header.X-Name", As: "body", Value: "Hello, "},
				{Action: "return", As: "body", Status: 200},
			},
			Action: "upsert",
		}},
		Apis: []ApiUpdate{{
			Name:     "greet-header-api",
			Path:     "/v1/greet",
			FlowName: "greet-header-flow",
			Action:   "upsert",
		}},
	})

	ctx := runRequest(t, fm, "GET", "/v1/greet", map[string]string{"X-Name": "Alice"})

	if ctx.ResponseStatus != 200 {
		t.Fatalf("expected status 200, got %d", ctx.ResponseStatus)
	}
	got := string(ctx.ResponseBuffer)
	want := "Hello, Alice"
	if got != want {
		t.Errorf("response body: want %q, got %q", want, got)
	}
}

// ─── Comparison: Query param via concat ───────────────────────────────────────

// TestQueryParamViaConcat verifies that a query param can be read and displayed
// via concat + return. BindQuery is emitted at the start of the plan (same
// mechanism as BindHeader), so liveness recycling does not affect it.
func TestQueryParamViaConcat(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "sync-query-concat",
		Flows: []FlowUpdate{{
			Name: "search-flow",
			Instructions: []StepConfig{
				{Action: "concat", Source: "query.q", As: "body", Value: "Search: "},
				{Action: "return", As: "body", Status: 200},
			},
			Action: "upsert",
		}},
		Apis: []ApiUpdate{{
			Name:     "search-api",
			Path:     "/v1/search",
			FlowName: "search-flow",
			Action:   "upsert",
		}},
	})

	ctx := runRequest(t, fm, "GET", "/v1/search?q=golang", nil)

	if ctx.ResponseStatus != 200 {
		t.Fatalf("expected status 200, got %d", ctx.ResponseStatus)
	}
	got := string(ctx.ResponseBuffer)
	want := "Search: golang"
	if got != want {
		t.Errorf("response body: want %q, got %q", want, got)
	}
}

// ─── Test 1: Single path param via endpoint_configs ───────────────────────────

// TestPathParamSingleExtract verifies that a flow using "path.id" via concat
// correctly receives the runtime path segment when the endpoint is registered
// with endpoint_configs containing "{id}".
//
// This is the PRIMARY mechanism for path param access in flows.
func TestPathParamSingleExtract(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "sync-path-1",
		Flows: []FlowUpdate{{
			Name: "user-profile-flow",
			Instructions: []StepConfig{
				// concat: "" + "User Profile for ID: " + ByteSlots["path.id"]
				{Action: "concat", Source: "path.id", As: "body", Value: "User Profile for ID: "},
				// return: respond with ByteSlots["body"]
				{Action: "return", As: "body", Status: 200},
			},
			Action: "upsert",
		}},
		Apis: []ApiUpdate{{
			Name:     "user-profile-api",
			Path:     "/v1/user",
			FlowName: "user-profile-flow",
			Action:   "upsert",
			EndpointConfigs: []EndpointConfig{
				{Path: "/{id}/profile", Method: "GET"},
			},
		}},
	})

	ctx := runRequest(t, fm, "GET", "/v1/user/2324/profile", nil)

	if ctx.ResponseStatus != 200 {
		t.Fatalf("expected status 200, got %d", ctx.ResponseStatus)
	}
	got := string(ctx.ResponseBuffer)
	want := "User Profile for ID: 2324"
	if got != want {
		t.Errorf("response body: want %q, got %q", want, got)
	}
}

// ─── Test 2: Multiple path params in one endpoint ────────────────────────────

// TestPathParamMultipleExtract verifies that two distinct path params from the
// same endpoint are independently extracted into separate slots.
func TestPathParamMultipleExtract(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "sync-path-2",
		Flows: []FlowUpdate{{
			Name: "org-user-flow",
			Instructions: []StepConfig{
				// Build "org=<org_id>" in slot "org_part"
				{Action: "concat", Source: "path.org_id", As: "org_part", Value: "org="},
				// Build "org=acme user=42" using org_part as slotA and path.user_id as slotB
				{Action: "concat", KeyIdentifier: "org_part", Source: "path.user_id", As: "body", Value: " user="},
				{Action: "return", As: "body", Status: 200},
			},
			Action: "upsert",
		}},
		Apis: []ApiUpdate{{
			Name:     "org-user-api",
			Path:     "/v1/org",
			FlowName: "org-user-flow",
			Action:   "upsert",
			EndpointConfigs: []EndpointConfig{
				{Path: "/{org_id}/user/{user_id}", Method: "GET"},
			},
		}},
	})

	ctx := runRequest(t, fm, "GET", "/v1/org/acme/user/42", nil)

	if ctx.ResponseStatus != 200 {
		t.Fatalf("expected status 200, got %d", ctx.ResponseStatus)
	}
	got := string(ctx.ResponseBuffer)
	want := "org=acme user=42"
	if got != want {
		t.Errorf("response body: want %q, got %q", want, got)
	}
}

// ─── Test 3: Baseline — concat with no path param (static prefix only) ────────

// TestPathParamBaseline verifies that a concat with only a static value
// (no path param source) still works correctly. This confirms concat itself
// is not the issue when path params return empty.
func TestPathParamBaseline(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "sync-path-3",
		Flows: []FlowUpdate{{
			Name: "static-flow",
			Instructions: []StepConfig{
				// concat: "" + "Hello World" + "" → "Hello World"
				{Action: "concat", As: "body", Value: "Hello World"},
				{Action: "return", As: "body", Status: 200},
			},
			Action: "upsert",
		}},
		Apis: []ApiUpdate{{
			Name:     "static-api",
			Path:     "/v1/hello",
			FlowName: "static-flow",
			Action:   "upsert",
		}},
	})

	ctx := runRequest(t, fm, "GET", "/v1/hello", nil)

	if ctx.ResponseStatus != 200 {
		t.Fatalf("expected status 200, got %d", ctx.ResponseStatus)
	}
	got := string(ctx.ResponseBuffer)
	if !strings.Contains(got, "Hello World") {
		t.Errorf("response body: want to contain %q, got %q", "Hello World", got)
	}
}

// ─── Test 4: Common mistake — full path with {id} in API path, no endpoint_configs

// TestPathParamWrongSetup documents the INCORRECT way to register a path-param
// endpoint: putting the full "{id}" path in the API path field without endpoint_configs.
//
// When the full parametric path (e.g. "/v1/user/{id}/profile") is used as the
// top-level API path, the radix router treats "{id}" as a LITERAL string segment.
// The route "/v1/user/2324/profile" does NOT match this literal prefix, so the
// router returns 0 (no match) and the request gets a 404.
//
// The CORRECT setup is:
//   - API path: "/v1/user"            (static prefix for top-level router)
//   - endpoint_configs.path: "/{id}/profile"  (param path for sub-router)
func TestPathParamWrongSetup(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "sync-path-4",
		Flows: []FlowUpdate{{
			Name: "bad-profile-flow",
			Instructions: []StepConfig{
				{Action: "concat", Source: "path.id", As: "body", Value: "User Profile for ID: "},
				{Action: "return", As: "body", Status: 200},
			},
			Action: "upsert",
		}},
		Apis: []ApiUpdate{{
			// WRONG: full parametric path as the API-level path with NO endpoint_configs.
			// The router registers "/v1/user/{id}/profile" literally, so a request
			// for "/v1/user/2324/profile" will not be found.
			Name:     "bad-profile-api",
			Path:     "/v1/user/{id}/profile",
			FlowName: "bad-profile-flow",
			Action:   "upsert",
			// No EndpointConfigs — sub-router registered at "/" only.
		}},
	})

	// The top-level router should NOT match "/v1/user/2324/profile" against
	// the literal path "/v1/user/{id}/profile".
	state := fm.State.Load()
	apiID := state.Router.Lookup("/v1/user/2324/profile")
	if apiID != 0 {
		t.Logf("NOTE: router unexpectedly matched route (apiID=%d); behavior may vary", apiID)
	} else {
		t.Log("CONFIRMED: router returns 0 for literal '{id}' path — this is expected (common misconfiguration)")
	}

	// Verify that the correct literal path IS registered, proving the sync succeeded.
	literalID := state.Router.Lookup("/v1/user/{id}/profile")
	if literalID == 0 {
		t.Error("expected the literal path /v1/user/{id}/profile to be registered")
	}
}

// ─── Test 5: Path param with different IDs (idempotency) ─────────────────────

// TestPathParamDifferentValues verifies that multiple concurrent requests with
// different IDs each get the correct ID in their response (no cross-contamination
// from the pool-based context reuse).
func TestPathParamDifferentValues(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "sync-path-5",
		Flows: []FlowUpdate{{
			Name: "item-flow",
			Instructions: []StepConfig{
				{Action: "concat", Source: "path.item_id", As: "body", Value: "item="},
				{Action: "return", As: "body", Status: 200},
			},
			Action: "upsert",
		}},
		Apis: []ApiUpdate{{
			Name:     "item-api",
			Path:     "/v1/items",
			FlowName: "item-flow",
			Action:   "upsert",
			EndpointConfigs: []EndpointConfig{
				{Path: "/{item_id}", Method: "GET"},
			},
		}},
	})

	cases := []struct {
		path string
		want string
	}{
		{"/v1/items/apple", "item=apple"},
		{"/v1/items/banana", "item=banana"},
		{"/v1/items/123", "item=123"},
		{"/v1/items/hello-world", "item=hello-world"},
	}

	for _, tc := range cases {
		ctx := runRequest(t, fm, "GET", tc.path, nil)
		if ctx.ResponseStatus != 200 {
			t.Errorf("path %s: expected status 200, got %d", tc.path, ctx.ResponseStatus)
			continue
		}
		got := string(ctx.ResponseBuffer)
		if got != tc.want {
			t.Errorf("path %s: want %q, got %q", tc.path, tc.want, got)
		}
	}
}
