package control

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/engine/steps"
	"rah/internal/ingest"
	"rah/internal/mcpreg"
	"rah/internal/rctx"
	registrypkg "rah/internal/registry"
	"rah/internal/vectorstore"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// pendingJump tracks an on_error:jump: wrapper that referenced a not-yet-compiled
// flow. Resolved in a second pass after all flows are compiled.
type pendingJump struct {
	instrIdx int               // index in GlobalTable to patch
	flowName string            // target flow name to resolve
	inner    engine.Instruction // original unwrapped instruction
}

type Compiler struct {
	slotMap   map[string]int
	freeSlots []int // slots freed by liveness analysis, available for reuse
	nextSlot  int
	fm        *engine.FlowManager
	RegMgr      *registrypkg.RegistryManager // optional; enables KeyID pre-resolution at bake time
	SecretsMgr  steps.SecretLoader           // optional; enables load_secret steps
	CredMgr     steps.CredentialLookup       // optional; enables load_credential steps
	CacheMgr    steps.CacheStore             // optional; enables cache_get/cache_put steps
	DSM          *DataStoreManager                   // optional; enables load_history/save_history steps
	LLMCfg       config.LLMConfig                    // optional; enables llm_call steps
	VectorStores map[string]vectorstore.VectorStore  // optional; enables vector_search/vector_upsert steps
	MCPRegistry    *mcpreg.Registry                    // optional; virtual MCP server and API tool catalog
	GatewayBase    string                              // base URL for api_tool loopback calls (e.g. "http://localhost:8080")
	IngestPipeline *ingest.Pipeline                    // optional; enables emit_event steps
	PricingManager steps.PricingLookup                 // optional; enables calculate_cost steps
	GlobalTable  []engine.Instruction
	FragmentMap  map[string]int16
	FlowLibrary  map[string][]StepConfig
	FlowProfiles map[string]FlowProfile // flow name → profile; populated by BakeAll and Compile
	// pendingJumps tracks on_error:jump: wrappers that referenced a not-yet-compiled
	// flow. Resolved in a second pass after all flows are compiled.
	pendingJumps []pendingJump

	// currentAPIPolicies holds the resolved APIRateLimitEntry slice for the API
	// currently being compiled. Set by the management server before each
	// CompileExecutable call. Used by:
	//   - "api_rate_limits" step marker → inject policies at that position.
	//   - Auto-inject → prepend policies when the flow tree has no RL step.
	currentAPIPolicies []APIRateLimitEntry
	// currentAPISkipRL suppresses auto-injection and "api_rate_limits" marker
	// expansion when true. Set alongside currentAPIPolicies.
	currentAPISkipRL bool
}

// resolveAPIKey resolves a credential reference (e.g. "env:OPENAI_KEY", "file:///run/secrets/key")
// through the secrets manager if one is configured. Literal values (no scheme prefix) are returned
// as-is, matching the secrets manager's own passthrough behaviour for non-prefixed strings.
// If no secrets manager is configured the raw ref is returned unchanged.
func (c *Compiler) resolveAPIKey(ref string) (string, error) {
	if ref == "" || c.SecretsMgr == nil {
		return ref, nil
	}
	val, err := c.SecretsMgr.Resolve(context.Background(), ref)
	if err != nil {
		return "", fmt.Errorf("resolving api_key_ref %q: %w", ref, err)
	}
	s := string(val)
	clear(val)
	return s, nil
}

func NewCompiler(fm *engine.FlowManager) *Compiler {
	return &Compiler{
		slotMap:      make(map[string]int),
		nextSlot:     0,
		fm:           fm,
		GlobalTable:  make([]engine.Instruction, 0, 4096),
		FragmentMap:  make(map[string]int16),
		FlowProfiles: make(map[string]FlowProfile),
		pendingJumps: make([]pendingJump, 0, 8),
	}
}

// BakeAll flattens Fragments and APIs into a single Instruction Table.
func (c *Compiler) BakeAll(cfg GatewayConfig) error {
	c.LLMCfg = cfg.LLM
	// 1. Map Fragments (Subflows)
	for name, flow := range cfg.Flows {
		c.FragmentMap[name] = int16(len(c.GlobalTable))
		c.resetSlots()
		if err := c.bakeFlow(flow, cfg.Flows); err != nil {
			return fmt.Errorf("flow %q: %w", name, err)
		}
		ret := c.newReturnStep()
		ret.StepIdx = -1 // system instruction
		c.GlobalTable = append(c.GlobalTable, ret)
		c.FlowProfiles[name] = buildFlowProfile(name, flow)
	}

	// 2. Bake APIs
	for _, api := range cfg.Apis {
		c.resetSlots()

		// AUTO-BINDING: Discover what headers/query params this flow needs
		deps := c.discoverDependencies(cfg.Flows[api.FlowName])
		depStart := len(c.GlobalTable)
		for _, dep := range deps {
			slot, err := c.getSlot(dep.Identifier)
			if err != nil {
				return fmt.Errorf("api %q auto-bind: %w", api.FlowName, err)
			}
			c.GlobalTable = append(c.GlobalTable, steps.BindInput(dep.Source, dep.Key, slot))
		}
		// Mark auto-bind preamble as system instructions (not user flow steps).
		for j := depStart; j < len(c.GlobalTable); j++ {
			c.GlobalTable[j].StepIdx = -1
		}

		if err := c.bakeFlow(cfg.Flows[api.FlowName], cfg.Flows); err != nil {
			return fmt.Errorf("api %q: %w", api.FlowName, err)
		}
		stop := c.newStopStep()
		stop.StepIdx = -1 // system instruction
		c.GlobalTable = append(c.GlobalTable, stop)
	}
	if err := c.resolvePendingJumps(); err != nil {
		return err
	}
	return nil
}

// bakeFlow compiles a flow with slot liveness analysis.
// Dead slots are freed after each step so they can be reused by subsequent steps.
func (c *Compiler) bakeFlow(flow []StepConfig, fragments map[string][]StepConfig) error {
	lastUse := c.computeLastUse(flow, fragments)
	for i, step := range flow {
		startIdx := len(c.GlobalTable)
		if err := c.compileStep(step, fragments); err != nil {
			return err
		}
		// Tag all instructions emitted for this step with its index (OBS-4: flow-aligned trace view).
		for j := startIdx; j < len(c.GlobalTable); j++ {
			c.GlobalTable[j].StepIdx = int16(i)
		}
		c.releaseDeadSlots(i, lastUse)
	}
	return nil
}

// bakeFlowRaw compiles a flow without liveness analysis.
// Used for recursive sub-flow calls within compileStep (if/else branches, call)
// where the outer bakeFlow's liveness pass already accounts for variables in branches.
func (c *Compiler) bakeFlowRaw(flow []StepConfig, fragments map[string][]StepConfig) error {
	for _, step := range flow {
		if err := c.compileStep(step, fragments); err != nil {
			return err
		}
	}
	return nil
}

func (c *Compiler) compileStep(step StepConfig, fragments map[string][]StepConfig) error {
	switch step.Action {
	case "if":
		thenBlock := c.simulateBake(fragments[step.Then], fragments)
		elseBlock := c.simulateBake(fragments[step.Else], fragments)

		thenStartID := int16(len(c.GlobalTable)) + 1
		skipElseID := thenStartID + int16(len(thenBlock))
		elseStartID := skipElseID + 1
		postElseID := elseStartID + int16(len(elseBlock))

		c.GlobalTable = append(c.GlobalTable, steps.NewComplexLogicGate(step.Condition, thenStartID, elseStartID, c.slotMap))
		if err := c.bakeFlowRaw(fragments[step.Then], fragments); err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, c.newInternalJump(postElseID))
		if err := c.bakeFlowRaw(fragments[step.Else], fragments); err != nil {
			return err
		}

	case "pattern_match":
		return c.compilePatternMatch(step, fragments)

	case "validate_pattern":
		return c.compileValidatePattern(step)

	case "extract_pattern":
		return c.compileExtractPattern(step)

	case "switch":
		slot, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		jumpTable := make(map[string]int16)
		dispatcherIdx := len(c.GlobalTable)
		c.GlobalTable = append(c.GlobalTable, engine.Instruction{Name: "SWITCH_DISPATCH"})

		for val, fragName := range step.Cases {
			jumpTable[val] = int16(len(c.GlobalTable))
			if err := c.bakeFlow(fragments[fragName], fragments); err != nil {
				return err
			}
			c.GlobalTable = append(c.GlobalTable, engine.Instruction{Name: "BREAK"})
		}
		exitID := int16(len(c.GlobalTable))
		c.GlobalTable[dispatcherIdx] = steps.SwitchGate(slot, jumpTable, exitID)
		c.linkBreaks(exitID)

	case "http_call":
		urlSlot := -1
		if step.UrlVar != "" {
			var err error
			urlSlot, err = c.getSlot(step.UrlVar)
			if err != nil {
				return err
			}
		}
		// Compile retry condition at bake time (S11 will wire it into HttpAction).
		if step.RetryCondition != "" {
			if _, compErr := steps.CompileCondition(step.RetryCondition, c.slotMap); compErr != nil {
				return fmt.Errorf("http_call retry_condition: %w", compErr)
			}
		}
		c.GlobalTable = append(c.GlobalTable, steps.HttpAction(urlSlot, step.URL, step.Timeout, step.RetryCondition, step.MaxRetries, step.Input))

	case "llm_call":
		return c.compileLLMCall(step)

	case "route_llm":
		return c.compileRouteLLM(step)

	case "estimate_tokens":
		return c.compileEstimateTokens(step)

	case "sanitize_prompt":
		return c.compileSanitizePrompt(step)

	case "compress_prompt":
		return c.compileCompressPrompt(step)

	case "append_message":
		return c.compileAppendMessage(step)
	case "trim_history":
		return c.compileTrimHistory(step)
	case "load_history":
		return c.compileLoadHistory(step)
	case "save_history":
		return c.compileSaveHistory(step)

	case "mcp_list_tools":
		return c.compileMCPListTools(step)

	case "mcp_fetch_schemas":
		return c.compileMCPFetchSchemas(step)

	case "mcp_call_tool":
		return c.compileMCPCallTool(step)

	case "detect_message_format":
		return c.compileDetectMessageFormat(step)

	case "check_context_fit":
		return c.compileCheckContextFit(step)

	case "transform_messages":
		return c.compileTransformMessages(step)

	case "overflow_history":
		return c.compileOverflowHistory(step)

	case "load_overflow_history":
		return c.compileLoadOverflowHistory(step)

	case "parse_tool_calls":
		return c.compileParseToolCalls(step)

	case "append_tool_result":
		return c.compileAppendToolResult(step)

	case "load_llm_key":
		return c.compileLoadLLMKey(step)

	case "embed_text":
		return c.compileEmbedText(step)

	case "execute_plan":
		return c.compileExecutePlan(step)

	case "vector_search":
		return c.compileVectorSearch(step)

	case "vector_upsert":
		return c.compileVectorUpsert(step)

	case "chunk_text":
		return c.compileChunkText(step)

	case "send_sse_event":
		return c.compileSSEEvent(step)

	case "rerank":
		return c.compileRerank(step)

	case "semantic_cache_get":
		return c.compileSemanticCacheGet(step)

	case "semantic_cache_put":
		return c.compileSemanticCachePut(step)

	case "call_mcp_tool":
		return c.compileMCPToolCall(step)

	case "serve_mcp":
		return c.compileServeMCP(step)

	case "emit_event":
		return c.compileEmitEvent(step)

	case "parse_message_format":
		return c.compileParseMessageFormat(step)

	case "format_response":
		return c.compileFormatResponse(step)

	case "parallel":
		// Compile each branch's inline flow into its own independent instruction table.
		// Each sub-table is self-contained: it has its own PC space starting at 0 and
		// does not reference indices in the parent GlobalTable.
		var subTables [][]engine.Instruction
		for _, branch := range step.Branches {
			subTable, err := c.CompileExecutable(branch.Flow, fragments)
			if err != nil {
				return fmt.Errorf("parallel branch %q: %w", branch.Name, err)
			}
			// CompileExecutable returns a slice backed by the compiler's GlobalTable.
			// We must copy it before the next CompileExecutable call resets c.GlobalTable.
			tableCopy := make([]engine.Instruction, len(subTable))
			copy(tableCopy, subTable)
			subTables = append(subTables, tableCopy)
		}
		failFast := step.ErrorPolicy == "fail_fast"
		c.GlobalTable = append(c.GlobalTable, steps.ParallelStep(subTables, step.TimeoutMs, failFast))

	case "registry_lookup":
		// Resolves the alias in keySlot → ctx.TenantID.
		// key_identifier names the slot holding the alias (e.g. "header.X-Tenant").
		keySlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.RegistryLookup(keySlot))

	case "load_service_url":
		// Loads the named URL for the current tenant into the slot named by "as".
		// EnsureURLKeyID pre-resolves the radix walk once at bake time; hot path is a
		// single array index (2–5 ns) regardless of how many URL keys exist.
		destSlot, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		var urlKeyID uint16
		if c.RegMgr != nil {
			urlKeyID = c.RegMgr.EnsureURLKeyID(step.Key)
		}
		c.GlobalTable = append(c.GlobalTable, steps.LoadServiceURL(urlKeyID, destSlot))

	case "load_service_url_var":
		// Loads a service URL using a runtime key name from keySlot.
		// The key name is read from ByteSlots[keySlot] at request time — radix walk ~50–100 ns.
		// Use when the key differs per API (e.g. set via route constants). Prefer load_service_url
		// (~2–5 ns) when the key is known at compile time.
		keySlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		destSlot, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.LoadServiceURLVar(keySlot, destSlot))

	case "load_identifier":
		// Loads the named identifier for the current tenant into the slot named by "as".
		// Same bake-time KeyID resolution as load_service_url.
		destSlot, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		var idKeyID uint16
		if c.RegMgr != nil {
			idKeyID = c.RegMgr.EnsureIDKeyID(step.Key)
		}
		c.GlobalTable = append(c.GlobalTable, steps.LoadIdentifier(idKeyID, destSlot))

	case "set_service_url":
		// Writes a service URL for the current tenant into the URLs store.
		// key: URL name e.g. "primary", "fallback".
		// source: slot name holding the value to write.
		// Management-plane write — use in admin/onboarding flows, not hot request loops.
		if c.RegMgr == nil {
			return fmt.Errorf("set_service_url requires a RegistryManager (not available in standalone mode)")
		}
		srcSlot, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.SetServiceURL(c.RegMgr, step.Key, srcSlot))

	case "set_identifier":
		// Writes an identifier for the current tenant into the IDs store.
		// key: identifier name e.g. "api_key", "client_id".
		// source: slot name holding the value to write.
		if c.RegMgr == nil {
			return fmt.Errorf("set_identifier requires a RegistryManager (not available in standalone mode)")
		}
		srcSlot, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.SetIdentifier(c.RegMgr, step.Key, srcSlot))

	case "set_meta":
		// Writes a metadata value for the current tenant into the Meta store.
		// key: metadata key name e.g. "tier", "region".
		// source: slot name holding the value to write.
		if c.RegMgr == nil {
			return fmt.Errorf("set_meta requires a RegistryManager (not available in standalone mode)")
		}
		srcSlot, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.SetMeta(c.RegMgr, step.Key, srcSlot))

	case "delete_service_url":
		// Deletes a service URL for the current tenant from the URLs store.
		// key: URL name e.g. "primary", "fallback".
		// Management-plane write — use in admin/onboarding flows, not hot request loops.
		if c.RegMgr == nil {
			return fmt.Errorf("delete_service_url requires a RegistryManager (not available in standalone mode)")
		}
		c.GlobalTable = append(c.GlobalTable, steps.DeleteServiceURL(c.RegMgr, step.Key))

	case "delete_identifier":
		// Deletes an identifier for the current tenant from the IDs store.
		// key: identifier name e.g. "api_key", "client_id".
		if c.RegMgr == nil {
			return fmt.Errorf("delete_identifier requires a RegistryManager (not available in standalone mode)")
		}
		c.GlobalTable = append(c.GlobalTable, steps.DeleteIdentifier(c.RegMgr, step.Key))

	case "delete_meta":
		// Deletes a metadata value for the current tenant from the Meta store.
		// key: metadata key name e.g. "tier", "region".
		if c.RegMgr == nil {
			return fmt.Errorf("delete_meta requires a RegistryManager (not available in standalone mode)")
		}
		c.GlobalTable = append(c.GlobalTable, steps.DeleteMeta(c.RegMgr, step.Key))

	case "check_rate_limit":
		// Opt-in rate limit enforcement. Must be placed explicitly in the flow.
		// Resolves the limit via ResolveRateLimit and enforces a fixed-window counter.
		// Returns 403 if the tenant is blocked; 429 if the rate is exceeded.
		// Optional quota group map in step.Input: {"1": "free_rl", "2": "pro_rl"}
		// Keys are group IDs (uint8), values are rate limit config names.
		// Set "emit_quota_headers": "true" to send X-RateLimit-* headers to callers.
		// Set "scope": "ip" to key the counter on client IP instead of TenantID.
		//   "ip_slot": slot name holding the client IP (from bind_client_ip).
		// Set "scope": "slot" to key the counter on any arbitrary value.
		//   "key_slot": slot name holding the rate limit key (e.g., user ID).
		//
		// V2 upgrade path: if "config" is set and refers to a V2 config, or if the
		// named V1 config can be translated to V2 windows, emit CheckRateLimitV2 instead.
		// Falls back to the original V1 instruction emission if translation is not possible.
		{
			configName := strings.TrimSpace(step.Input["config"])
			scope := step.Input["scope"]

			// Attempt V2 emission if a RegMgr is available.
			emittedV2 := false
			if c.RegMgr != nil {
				var v2cfg *registrypkg.RateLimitConfigV2
				var v2Windows []steps.WindowSpec

				// Try explicit V2 config reference first.
				if configName != "" {
					v2cfg = c.RegMgr.GetRateLimitConfigV2(configName)
				}

				// If no explicit V2 config found, try to synthesise one from the V1 config.
				if v2cfg == nil && configName != "" {
					if rec, ok := c.RegMgr.GetRateLimitConfig(configName); ok {
						// Translate V1 per_sec / per_min into V2 WindowSpec.
						synthWindows := make([]steps.WindowSpec, 0, 2)
						if rec.Config.PerSec > 0 {
							synthWindows = append(synthWindows, steps.WindowSpec{EpochDiv: 1, Limit: rec.Config.PerSec, Idx: 0})
						}
						if rec.Config.PerMin > 0 {
							synthWindows = append(synthWindows, steps.WindowSpec{EpochDiv: 60, Limit: rec.Config.PerMin, Idx: 1})
						}
						if len(synthWindows) > 0 {
							v2Windows = synthWindows
						}
					}
				}

				// Build V2 windows from explicit V2 config if we have one.
				if v2cfg != nil && len(v2Windows) == 0 {
					for i, w := range v2cfg.Windows {
						epochDiv := w.PeriodSecs
						if epochDiv == 0 {
							epochDiv = 1
						}
						v2Windows = append(v2Windows, steps.WindowSpec{EpochDiv: epochDiv, Limit: w.Limit, Idx: i})
					}
				}

				if len(v2Windows) > 0 {
					// Resolve V1 config ID (used as arena key).
					var configID uint16
					if id, ok := c.RegMgr.GetRateLimitConfigId(configName); ok {
						configID = id
					}

					// Build CountBy from scope field.
					var countBy engine.RateLimitCountBy
					switch scope {
					case "ip":
						ipSlotName := strings.TrimSpace(step.Input["ip_slot"])
						ipSlot := -1
						if ipSlotName != "" {
							ipSlot, _ = c.getSlot(ipSlotName)
						}
						countBy = engine.RateLimitCountBy{Kind: engine.CountByIP, SlotIndex: ipSlot}
					case "slot":
						keySlotName := strings.TrimSpace(step.Input["key_slot"])
						keySlot := -1
						if keySlotName != "" {
							keySlot, _ = c.getSlot(keySlotName)
						}
						countBy = engine.RateLimitCountBy{Kind: engine.CountBySlot, SlotIndex: keySlot}
					default:
						countBy = engine.RateLimitCountBy{Kind: engine.CountByTenant}
					}

					// Wire RemoteRL when V2 config requires strict enforcement.
					var rlv2RemoteRL engine.ExternalRateLimitProvider
					if v2cfg != nil && v2cfg.Enforcement == "strict" && c.fm.RemoteRL != nil {
						rlv2RemoteRL = c.fm.RemoteRL
					}

					capturedConfigID := configID
					capturedCountBy := countBy
					capturedWindows := v2Windows
					capturedConfigName := configName
					capturedRemoteRL := rlv2RemoteRL
					rlv2NextPC := len(c.GlobalTable) + 1
					rlv2DeniedPC := len(c.GlobalTable) + 2
					c.GlobalTable = append(c.GlobalTable, engine.Instruction{
						Name: "CHECK_RATE_LIMIT_V2",
						Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
							step := &steps.CheckRateLimitV2{
								ConfigID:   capturedConfigID,
								CountBy:    capturedCountBy,
								Windows:    capturedWindows,
								DeniedPC:   rlv2DeniedPC,
								NextPC:     rlv2NextPC,
								RemoteRL:   capturedRemoteRL,
								ConfigName: capturedConfigName,
							}
							return step.Execute(ctx, s)
						},
					})
					emittedV2 = true
				}
			}

			// V1 fallback: emit original V1 steps when V2 translation was not possible.
			if !emittedV2 {
				emitQuotaHeaders := step.Input["emit_quota_headers"] == "true"
				syncPolicy := uint8(0)
				if c.fm.RemoteRL != nil {
					syncPolicy = c.fm.DistRLPolicy
				}
				if scope == "ip" {
					ipSlotName := strings.TrimSpace(step.Input["ip_slot"])
					ipSlot := -1
					if ipSlotName != "" {
						ipSlot, _ = c.getSlot(ipSlotName)
					}
					c.GlobalTable = append(c.GlobalTable, steps.CheckRateLimitIP(c.fm.RateLimitStore, ipSlot, syncPolicy, c.fm.RemoteRL, emitQuotaHeaders))
				} else if scope == "slot" {
					keySlotName := strings.TrimSpace(step.Input["key_slot"])
					keySlot := -1
					if keySlotName != "" {
						keySlot, _ = c.getSlot(keySlotName)
					}
					c.GlobalTable = append(c.GlobalTable, steps.CheckRateLimitSlot(c.fm.RateLimitStore, keySlot, syncPolicy, c.fm.RemoteRL, emitQuotaHeaders))
				} else {
					var quotaGroupRLIds []uint16
					if len(step.Input) > 0 && c.RegMgr != nil {
						quotaGroupRLIds = c.compileQuotaGroupMap(step.Input)
					}
					c.GlobalTable = append(c.GlobalTable, steps.CheckRateLimit(c.fm.RateLimitStore, c.fm.RemoteRL, syncPolicy, quotaGroupRLIds, emitQuotaHeaders))
				}
			}
		}

	case "check_rate_limit_global":
		// Global (tenant-agnostic) rate limit enforcement. Counter key omits TenantID so
		// all tenants hitting the same API/endpoint share a single counter bucket.
		// Optional: "emit_quota_headers": "true" to emit X-RateLimit-* response headers.
		emitQuotaHeadersGlobal := step.Input["emit_quota_headers"] == "true"
		syncPolicyGlobal := uint8(0)
		if c.fm.RemoteRL != nil {
			syncPolicyGlobal = c.fm.DistRLPolicy
		}
		c.GlobalTable = append(c.GlobalTable, steps.CheckRateLimitGlobal(c.fm.RateLimitStore, c.fm.RemoteRL, syncPolicyGlobal, emitQuotaHeadersGlobal))

	case "check_rate_limit_v2":
		// V2 multi-window rate limit enforcement. Resolves config name → configID at bake
		// time, then emits a CheckRateLimitV2 instruction with the full CountBy spec.
		//
		// input fields:
		//   config        - named RateLimitConfigV2 (required)
		//   count_by      - "tenant"|"ip"|"slot"|"static"|"composite"|"global" (default: "tenant")
		//   slot          - slot variable name (for count_by=slot)
		//   slots         - comma-separated slot names (for count_by=composite)
		//   static_key    - static key string (for count_by=static)
		//   xff_index     - integer string, XFF position (for count_by=ip, default 0)
		//   on_empty      - "fail"|"skip"|"fallback_tenant" (default: "fail")
		//   fail_fast     - "true"/"false" (default false)
		//   denied_label  - label for the denied branch (optional)
		countBy := buildRLCountBy(step.Input, func(name string) int {
			idx, _ := c.getSlot(name)
			return idx
		})
		var configID uint16
		var windows []steps.WindowSpec
		var rlv2Enforcement string
		configName := step.Input["config"]
		if c.RegMgr != nil {
			// Resolve V1 config ID (used as arena key) from the named config.
			if id, ok := c.RegMgr.GetRateLimitConfigId(configName); ok {
				configID = id
			}
			// Resolve V2 window specs and enforcement mode from the named config.
			if cfg := c.RegMgr.GetRateLimitConfigV2(configName); cfg != nil {
				rlv2Enforcement = cfg.Enforcement
				for i, w := range cfg.Windows {
					epochDiv := w.PeriodSecs
					if epochDiv == 0 {
						epochDiv = 1 // default: per-second window
					}
					windows = append(windows, steps.WindowSpec{
						EpochDiv: epochDiv,
						Limit:    w.Limit,
						Idx:      i,
					})
				}
			}
		}
		// Wire distributed rate limiting when enforcement == "strict" and a
		// RemoteRL provider is configured. Local (approximate) mode is the default.
		var rlv2RemoteRL engine.ExternalRateLimitProvider
		if rlv2Enforcement == "strict" && c.fm.RemoteRL != nil {
			rlv2RemoteRL = c.fm.RemoteRL
		}
		rlv2NextPC := len(c.GlobalTable) + 1
		rlv2DeniedPC := len(c.GlobalTable) + 2
		c.GlobalTable = append(c.GlobalTable, engine.Instruction{
			Name: "CHECK_RATE_LIMIT_V2",
			Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
				step := &steps.CheckRateLimitV2{
					ConfigID:   configID,
					CountBy:    countBy,
					Windows:    windows,
					DeniedPC:   rlv2DeniedPC,
					NextPC:     rlv2NextPC,
					RemoteRL:   rlv2RemoteRL,
					ConfigName: configName,
				}
				return step.Execute(ctx, s)
			},
		})

	case "check_upstream_rate_limit":
		// Upstream rate limit enforcement using URL-pattern matching.
		// Reads the upstream URL from the named slot and checks it against
		// the UpstreamRegistry. Returns DeniedPC if denied, NextPC otherwise.
		//
		// input fields:
		//   url_slot      - slot variable name holding the upstream URL (required)
		//   denied_label  - label for the denied branch (optional)
		urlSlotName := strings.TrimSpace(step.Input["url_slot"])
		urlSlotIdx := -1
		if urlSlotName != "" {
			var err error
			urlSlotIdx, err = c.getSlot(urlSlotName)
			if err != nil {
				return fmt.Errorf("check_upstream_rate_limit: url_slot %q: %w", urlSlotName, err)
			}
		}
		urlNextPC := len(c.GlobalTable) + 1
		urlDeniedPC := len(c.GlobalTable) + 2
		c.GlobalTable = append(c.GlobalTable, engine.Instruction{
			Name: "CHECK_UPSTREAM_RATE_LIMIT",
			Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
				step := &steps.CheckUpstreamRateLimit{
					URLSlotIndex: urlSlotIdx,
					DeniedPC:     urlDeniedPC,
					NextPC:       urlNextPC,
				}
				return step.Execute(ctx, s)
			},
		})

	case "assign_quota_group":
		// Reads ByteSlots[key_identifier] and maps the string value to a QuotaGroupID.
		// step.Input: {"free": "1", "pro": "2", "enterprise": "3"}
		// Keys are string tier names, values are group IDs (uint8 1–255).
		srcSlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		groupMap := make(map[string]uint8, len(step.Input))
		for name, idStr := range step.Input {
			if id, err := strconv.ParseUint(idStr, 10, 8); err == nil {
				groupMap[name] = uint8(id)
			}
		}
		c.GlobalTable = append(c.GlobalTable, steps.AssignQuotaGroup(srcSlot, groupMap))

	case "enforce_cost_budget":
		// Opt-in cost budget enforcement. Must be placed explicitly in the flow.
		// Reads estimated cost from IntSlot and checks against tenant quota.
		// Returns 429 if budget exceeded.
		// step.KeyIdentifier: slot name containing estimated cost (fixed-point: value/1e9 = cost in $)
		costSlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.EnforceCostBudget(c.fm.CostQuotaManager, costSlot))

	case "record_cost":
		// Records actual cost after LLM call completes.
		// Reads actual cost from IntSlot and updates tenant quota.
		// Also emits a cost event to the ingest pipeline (deferred, after response sent).
		// step.KeyIdentifier: slot name containing actual cost (fixed-point: value/1e9 = cost in $)
		costSlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		modelSlot := -1
		if ms, ok := step.Input["model_slot"]; ok && ms != "" {
			if s, err2 := c.getSlot(ms); err2 == nil {
				modelSlot = s
			}
		}
		cfg := steps.RecordCostConfig{
			QuotaManager:   c.fm.CostQuotaManager,
			IngestPipeline: c.IngestPipeline,
			CostSlot:       costSlot,
			ModelSlot:      modelSlot,
		}
		c.GlobalTable = append(c.GlobalTable, steps.RecordCost(cfg))

	case "calculate_cost":
		// Computes LLM cost from token counts using the pricing catalog.
		// key:                slot name for the result (IntSlot, fixed-point: value/1e9 = USD)
		// input_tokens_slot:  slot name holding input token count
		// output_tokens_slot: slot name holding output token count
		// model_slot:         slot name holding the model ID string (ByteSlot)
		costSlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		inputTokensSlot, err := c.getSlot(step.Input["input_tokens_slot"])
		if err != nil {
			return err
		}
		outputTokensSlot, err := c.getSlot(step.Input["output_tokens_slot"])
		if err != nil {
			return err
		}
		calcModelSlot := -1
		if ms := step.Input["model_slot"]; ms != "" {
			if s, err2 := c.getSlot(ms); err2 == nil {
				calcModelSlot = s
			}
		}
		c.GlobalTable = append(c.GlobalTable, steps.CalculateCost(steps.CalculateCostConfig{
			PricingManager:   c.PricingManager,
			CostSlot:         costSlot,
			InputTokensSlot:  inputTokensSlot,
			OutputTokensSlot: outputTokensSlot,
			ModelSlot:        calcModelSlot,
		}))

	case "classify_llm":
		return c.compileClassifyLLM(step)

	case "set_const":
		// Writes a static string value into a slot at bake time.
		// value: the literal string to store
		// as:    slot name
		destSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("set_const: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.SetConstStep(step.Value, destSlot))

	case "respond":
		// Writes ByteSlots[key_identifier] as the HTTP response body and stops the flow.
		// key_identifier: slot name holding the response content
		srcSlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return fmt.Errorf("respond: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.RespondStep(srcSlot))

	case "respond_token_count":
		// Writes {"input_tokens": N} where N is IntSlots[total_slot].
		// Used to implement the Anthropic /v1/messages/count_tokens endpoint.
		// input["total_slot"]: named slot holding the token count (from check_context_fit)
		intSlot := -1
		if name := step.Input["total_slot"]; name != "" {
			if s, slotErr := c.getSlot(name); slotErr == nil {
				intSlot = s
			}
		}
		c.GlobalTable = append(c.GlobalTable, steps.RespondTokenCountStep(intSlot))

	case "bind_body":
		// Reads the request body (buffering on first access) and extracts a JSON
		// field by gjson path into the named slot.
		// key / key_identifier: gjson path, e.g. "message" or "data.prompt"
		// as:                   slot name to store the extracted string value
		jsonPath := step.Key
		if jsonPath == "" {
			jsonPath = step.KeyIdentifier
		}
		destSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("bind_body: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.BindBody(jsonPath, destSlot))

	case "bind_header":
		// Explicit header binding — equivalent to the auto-discovered header dependency
		// but usable anywhere in a flow as an explicit step.
		// key / key_identifier: HTTP header name, e.g. "X-Session-Id"
		// as:                   slot name
		headerKey := step.Key
		if headerKey == "" {
			headerKey = step.KeyIdentifier
		}
		destSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("bind_header: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.BindHeader(headerKey, destSlot))

	case "bind_query_param":
		// Explicit query-param binding.
		// key / key_identifier: query param name, e.g. "session_id"
		// as:                   slot name
		qKey := step.Key
		if qKey == "" {
			qKey = step.KeyIdentifier
		}
		destSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("bind_query_param: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.BindQuery(qKey, destSlot))

	case "bind_path":
		// Explicit path-param binding by positional index.
		// input.index: 0-based index into the route's captured path parameters.
		// as:          slot name to store the captured value.
		idx, _ := strconv.Atoi(step.Input["index"])
		destSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("bind_path: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.BindPath(idx, destSlot))

	case "bind_client_ip":
		// Extracts the real client IP (X-Forwarded-For → X-Real-IP → RemoteAddr)
		// and stores it in the named slot.
		// as: slot name to store the client IP.
		// Optional input["xff_index"]: which XFF entry to use (0=first, -1=last). Default 0.
		destSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("bind_client_ip: %w", err)
		}
		xffIndex := 0
		if raw := strings.TrimSpace(step.Input["xff_index"]); raw != "" {
			if parsed, parseErr := strconv.Atoi(raw); parseErr == nil {
				xffIndex = parsed
			}
		}
		c.GlobalTable = append(c.GlobalTable, steps.BindClientIP(destSlot, xffIndex))

	case "set_request_header":
		// Injects a header into the upstream request before proxying.
		// key / key_identifier: header name (static, baked at compile time)
		// source: slot name whose value is injected as the header value
		headerName := step.Key
		if headerName == "" {
			headerName = step.KeyIdentifier
		}
		valueSlot, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("set_request_header: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.SetRequestHeader(headerName, valueSlot))

	case "extract_cookie":
		cookieName := step.Key
		if cookieName == "" {
			cookieName = step.KeyIdentifier
		}
		destSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("extract_cookie: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.ExtractCookie(cookieName, destSlot))

	case "set_response_cookie":
		cookieName := step.Key
		if cookieName == "" {
			cookieName = step.KeyIdentifier
		}
		valueSlot, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("set_response_cookie: %w", err)
		}
		path := step.Input["path"]
		if path == "" {
			path = "/"
		}
		maxAge := 0
		if v, ok := step.Input["max_age"]; ok {
			maxAge, _ = strconv.Atoi(v)
		}
		httpOnly := step.Input["http_only"] == "true"
		secure := step.Input["secure"] == "true"
		sameSite := step.Input["same_site"]
		c.GlobalTable = append(c.GlobalTable, steps.SetResponseCookie(cookieName, valueSlot, path, maxAge, httpOnly, secure, sameSite))

	case "set_request_cookie":
		cookieName := step.Key
		if cookieName == "" {
			cookieName = step.KeyIdentifier
		}
		valueSlot, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("set_request_cookie: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.SetRequestCookie(cookieName, valueSlot))

	case "remove_response_cookie":
		cookieName := step.Key
		if cookieName == "" {
			cookieName = step.KeyIdentifier
		}
		path := step.Input["path"]
		c.GlobalTable = append(c.GlobalTable, steps.RemoveResponseCookie(cookieName, path))

	case "json_set":
		srcSlot, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("json_set: source: %w", err)
		}
		dstSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("json_set: as: %w", err)
		}
		path := step.Input["key"]
		if path == "" {
			path = step.Key
		}
		valueSlot := -1
		if step.Input["value_var"] != "" {
			valueSlot, err = c.getSlot(step.Input["value_var"])
			if err != nil {
				return fmt.Errorf("json_set: value_var: %w", err)
			}
		}
		staticVal := []byte(step.Value)
		c.GlobalTable = append(c.GlobalTable, steps.JsonSetStep(srcSlot, dstSlot, path, staticVal, valueSlot))

	case "cookie_flatten":
		dstSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("cookie_flatten: as: %w", err)
		}
		var bindings []steps.CookieSlotBinding
		for cookieName, slotName := range step.Input {
			s, err := c.getSlot(slotName)
			if err != nil {
				return fmt.Errorf("cookie_flatten: slot %q: %w", slotName, err)
			}
			bindings = append(bindings, steps.CookieSlotBinding{Name: []byte(cookieName), Slot: s})
		}
		c.GlobalTable = append(c.GlobalTable, steps.CookieFlattenStep(bindings, dstSlot))

	case "ip_restriction":
		// Enforces allow/deny CIDR policy for the resolved client IP.
		// Optional key_identifier can point to a slot containing a pre-resolved IP
		// (e.g. from bind_client_ip) and takes precedence over source resolution.
		cfg, err := steps.ParseIPRestrictionConfig(step.Input)
		if err != nil {
			return fmt.Errorf("ip_restriction: %w", err)
		}
		sourceSlot := -1
		if name := strings.TrimSpace(step.KeyIdentifier); name != "" {
			slot, slotErr := c.getSlot(name)
			if slotErr != nil {
				return fmt.Errorf("ip_restriction: key_identifier %q: %w", name, slotErr)
			}
			sourceSlot = slot
		}
		c.GlobalTable = append(c.GlobalTable, steps.IPRestriction(cfg, sourceSlot))

	case "token_validation":
		alloc := func(key string) int {
			name := strings.TrimSpace(step.Input[key])
			if name == "" {
				return -1
			}
			s, _ := c.getSlot(name)
			return s
		}

		tokenSlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}

		// Build per-claim variable slots from jwt.custom_claims_vars JSON
		customClaimsSlots := map[string]int{}
		if raw := strings.TrimSpace(step.Input["jwt.custom_claims_vars"]); raw != "" {
			var varMap map[string]string
			if json.Unmarshal([]byte(raw), &varMap) == nil {
				for claimKey, varName := range varMap {
					if s, e := c.getSlot(strings.TrimSpace(varName)); e == nil {
						customClaimsSlots[claimKey] = s
					}
				}
			}
		}

		tvSlots := steps.TokenValidationSlots{
			Token:          tokenSlot,
			JWKSURI:        alloc("jwt.jwks_uri_var"),
			Algorithm:      alloc("jwt.alg_var"),
			Leeway:         alloc("jwt.leeway_var"),
			ValidateSet:    alloc("jwt.validate_var"),
			Issuer:         alloc("jwt.issuer_var"),
			Audience:       alloc("jwt.audience_var"),
			RequiredScopes: alloc("jwt.required_scopes_var"),
			ScopeClaimKeys: alloc("jwt.scope_claims_var"),
			OnFailureMode:  alloc("jwt.on_failure_var"),
			FailureStatus:  alloc("jwt.failure_status_var"),
			FailureBody:    alloc("jwt.failure_body_var"),
			ResultSuccess:  alloc("jwt.result_success_var"),
			ResultFailure:  alloc("jwt.result_failure_var"),
			Result:         alloc("jwt.result_var"),
			Claims:         alloc("jwt.claims_var"),
			Subject:        alloc("jwt.subject_var"),
			ClientID:       alloc("jwt.client_id_var"),
			ScopesOut:      alloc("jwt.scopes_out_var"),
			CustomClaimsVars: customClaimsSlots,
		}

		cfg := steps.ParseTokenValidationConfig(step.KeyIdentifier, step.Input)
		c.GlobalTable = append(c.GlobalTable, steps.TokenValidation(tvSlots, cfg))

	case "foreach":
		// Allocate hidden iterSlot (IntSlot index) for the loop counter and
		// indexSlot (ByteSlot) for the packed (start,end) array index — O(1) per step.
		if c.nextSlot+1 >= rctx.BaseByteSlots {
			return fmt.Errorf("slot limit exceeded at foreach iterator: max %d", rctx.BaseByteSlots)
		}
		iterSlot := c.nextSlot
		c.nextSlot++
		indexSlot := c.nextSlot // hidden slot; caches packed gjson index between iterations
		c.nextSlot++
		valSlot, err := c.getSlot(step.As)
		if err != nil {
			return err
		}

		gateID := int16(len(c.GlobalTable))
		c.GlobalTable = append(c.GlobalTable, engine.Instruction{Name: "LOOP_GATE_PLACEHOLDER"})

		for _, subStep := range step.Do {
			if err := c.compileStep(subStep, fragments); err != nil {
				return err
			}
		}

		c.GlobalTable = append(c.GlobalTable, engine.Instruction{
			Name:   "LOOP_REPEAT",
			Action: steps.LoopRepeat(gateID, iterSlot),
		})

		exitID := int16(len(c.GlobalTable))
		// If source resolves to a named slot (var.x), use slot-based JSON array iteration.
		// Otherwise fall back to the string-based GetCollection (headers, cookies).
		if srcSlot, slotErr := c.getSlotReadOnly(step.Source); slotErr == nil {
			c.GlobalTable[gateID] = engine.Instruction{
				Name:   "LOOP_GATE_SLOT",
				Action: steps.LoopGateSlot(srcSlot, valSlot, indexSlot, iterSlot, gateID+1, exitID),
			}
		} else {
			c.GlobalTable[gateID] = engine.Instruction{
				Name:   "LOOP_GATE",
				Action: steps.LoopGate(step.Source, valSlot, iterSlot, gateID+1, exitID),
			}
		}

	case "while":
		// while loops until ctx.BoolSlots[condSlot] is false or maxIter is reached.
		// step.Source: BoolSlot name for the loop condition
		// step.Do: sub-steps executed each iteration
		// step.Input["max_iter"]: optional integer safety limit (default 100)
		if c.nextSlot >= rctx.BaseByteSlots {
			return fmt.Errorf("slot limit exceeded at while iterator: max %d", rctx.BaseByteSlots)
		}
		iterSlot := c.nextSlot
		c.nextSlot++

		// Build ConditionFunc from step.Condition (if set) or fall back to bool slot.
		var condFn steps.ConditionFunc
		if step.Condition != "" {
			var compErr error
			condFn, compErr = steps.CompileCondition(step.Condition, c.slotMap)
			if compErr != nil {
				return fmt.Errorf("while: condition: %w", compErr)
			}
		} else {
			cs, condErr := c.getBoolSlot(step.Source)
			if condErr != nil {
				return fmt.Errorf("while: condition slot %q: %w", step.Source, condErr)
			}
			condSlot := cs // capture for closure
			condFn = func(ctx *rctx.Context) bool {
				return condSlot >= 0 && condSlot < len(ctx.BoolSlots) && ctx.BoolSlots[condSlot]
			}
		}

		maxIter := 100
		if v, ok := step.Input["max_iter"]; ok {
			if n, parseErr := strconv.Atoi(v); parseErr == nil && n > 0 {
				maxIter = n
			}
		}

		whileGateID := int16(len(c.GlobalTable))
		c.GlobalTable = append(c.GlobalTable, engine.Instruction{Name: "WHILE_GATE_PLACEHOLDER"})

		for _, subStep := range step.Do {
			if err := c.compileStep(subStep, fragments); err != nil {
				return err
			}
		}

		c.GlobalTable = append(c.GlobalTable, engine.Instruction{
			Name:   "WHILE_REPEAT",
			Action: steps.WhileRepeat(whileGateID),
		})

		whileExitID := int16(len(c.GlobalTable))
		c.GlobalTable[whileGateID] = engine.Instruction{
			Name:   "WHILE_GATE",
			Action: steps.WhileGate(condFn, iterSlot, maxIter, whileGateID+1, whileExitID),
		}

	case "foreach_header":
		// Iterate over HTTP request headers.
		// Allocate hidden iterSlot (IntSlot) and indexSlot (ByteSlot) for the packed index.
		if c.nextSlot+1 >= rctx.BaseIntSlots {
			return fmt.Errorf("slot limit exceeded at foreach_header iterator: max %d", rctx.BaseIntSlots)
		}
		iterSlot := c.nextSlot
		c.nextSlot++
		indexSlot := c.nextSlot // hidden slot; caches packed index between iterations
		c.nextSlot++

		nameSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("foreach_header: name slot: %w", err)
		}

		valueVar := step.Input["value_as"]
		if valueVar == "" {
			valueVar = step.Input["value_var"]
		}
		if valueVar == "" {
			return fmt.Errorf("foreach_header: missing value_as or value_var")
		}
		valueSlot, err := c.getSlot(valueVar)
		if err != nil {
			return fmt.Errorf("foreach_header: value slot: %w", err)
		}

		gateID := int16(len(c.GlobalTable))
		c.GlobalTable = append(c.GlobalTable, engine.Instruction{Name: "FOREACH_HEADER_GATE_PLACEHOLDER"})

		for _, subStep := range step.Do {
			if err := c.compileStep(subStep, fragments); err != nil {
				return err
			}
		}

		c.GlobalTable = append(c.GlobalTable, engine.Instruction{
			Name:   "FOREACH_HEADER_REPEAT",
			Action: steps.LoopRepeat(gateID, iterSlot),
		})

		exitID := int16(len(c.GlobalTable))
		c.GlobalTable[gateID] = engine.Instruction{
			Name:   "FOREACH_HEADER_GATE",
			Action: steps.ForeachHeader(nameSlot, valueSlot, indexSlot, iterSlot, gateID+1, exitID),
		}

	case "foreach_param":
		// Iterate over URL query parameters.
		// Allocate hidden iterSlot (IntSlot) and indexSlot (ByteSlot) for the packed index.
		if c.nextSlot+1 >= rctx.BaseIntSlots {
			return fmt.Errorf("slot limit exceeded at foreach_param iterator: max %d", rctx.BaseIntSlots)
		}
		iterSlot := c.nextSlot
		c.nextSlot++
		indexSlot := c.nextSlot // hidden slot; caches packed index between iterations
		c.nextSlot++

		nameSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("foreach_param: name slot: %w", err)
		}

		valueVar := step.Input["value_as"]
		if valueVar == "" {
			valueVar = step.Input["value_var"]
		}
		if valueVar == "" {
			return fmt.Errorf("foreach_param: missing value_as or value_var")
		}
		valueSlot, err := c.getSlot(valueVar)
		if err != nil {
			return fmt.Errorf("foreach_param: value slot: %w", err)
		}

		gateID := int16(len(c.GlobalTable))
		c.GlobalTable = append(c.GlobalTable, engine.Instruction{Name: "FOREACH_PARAM_GATE_PLACEHOLDER"})

		for _, subStep := range step.Do {
			if err := c.compileStep(subStep, fragments); err != nil {
				return err
			}
		}

		c.GlobalTable = append(c.GlobalTable, engine.Instruction{
			Name:   "FOREACH_PARAM_REPEAT",
			Action: steps.LoopRepeat(gateID, iterSlot),
		})

		exitID := int16(len(c.GlobalTable))
		c.GlobalTable[gateID] = engine.Instruction{
			Name:   "FOREACH_PARAM_GATE",
			Action: steps.ForeachParam(nameSlot, valueSlot, indexSlot, iterSlot, gateID+1, exitID),
		}

	case "foreach_cookie":
		// Iterate over HTTP request cookies.
		// Allocate hidden iterSlot (IntSlot) and indexSlot (ByteSlot) for the packed index.
		if c.nextSlot+1 >= rctx.BaseIntSlots {
			return fmt.Errorf("slot limit exceeded at foreach_cookie iterator: max %d", rctx.BaseIntSlots)
		}
		iterSlot := c.nextSlot
		c.nextSlot++
		indexSlot := c.nextSlot // hidden slot; caches packed index between iterations
		c.nextSlot++

		nameSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("foreach_cookie: name slot: %w", err)
		}

		valueVar := step.Input["value_as"]
		if valueVar == "" {
			valueVar = step.Input["value_var"]
		}
		if valueVar == "" {
			return fmt.Errorf("foreach_cookie: missing value_as or value_var")
		}
		valueSlot, err := c.getSlot(valueVar)
		if err != nil {
			return fmt.Errorf("foreach_cookie: value slot: %w", err)
		}

		gateID := int16(len(c.GlobalTable))
		c.GlobalTable = append(c.GlobalTable, engine.Instruction{Name: "FOREACH_COOKIE_GATE_PLACEHOLDER"})

		for _, subStep := range step.Do {
			if err := c.compileStep(subStep, fragments); err != nil {
				return err
			}
		}

		c.GlobalTable = append(c.GlobalTable, engine.Instruction{
			Name:   "FOREACH_COOKIE_REPEAT",
			Action: steps.LoopRepeat(gateID, iterSlot),
		})

		exitID := int16(len(c.GlobalTable))
		c.GlobalTable[gateID] = engine.Instruction{
			Name:   "FOREACH_COOKIE_GATE",
			Action: steps.ForeachCookie(nameSlot, valueSlot, indexSlot, iterSlot, gateID+1, exitID),
		}

	case "call":
		if targetID, ok := c.FragmentMap[step.FlowName]; ok {
			c.GlobalTable = append(c.GlobalTable, engine.Instruction{
				Name:   "CALL",
				Action: steps.CallFragment(targetID),
			})
		} else if fragments != nil {
			if called, exists := fragments[step.FlowName]; exists {
				if err := c.bakeFlowRaw(called, fragments); err != nil {
					return err
				}
			}
		}

	case "concat":
		slotA, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		slotB, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.ConcatStep(slotA, slotB, result, step.Value))

	case "to_lower":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.ToLowerStep(src, result))

	case "to_upper":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.ToUpperStep(src, result))

	case "substring":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		start := 0
		length := -1
		if v, ok := step.Input["start"]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				start = n
			}
		}
		if v, ok := step.Input["length"]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				length = n
			}
		}
		c.GlobalTable = append(c.GlobalTable, steps.SubstringStep(src, result, start, length))

	case "trim":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("trim: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("trim: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.TrimStep(src, result))

	case "contains":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("contains: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("contains: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.ContainsStep(src, result, []byte(step.Value)))

	case "starts_with":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("starts_with: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("starts_with: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.StartsWithStep(src, result, []byte(step.Value)))

	case "ends_with":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("ends_with: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("ends_with: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.EndsWithStep(src, result, []byte(step.Value)))

	case "replace":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("replace: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("replace: %w", err)
		}
		oldVal := step.Input["old"]
		newVal := step.Input["new"]
		c.GlobalTable = append(c.GlobalTable, steps.ReplaceStep(src, result, []byte(oldVal), []byte(newVal)))

	case "split":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("split: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("split: %w", err)
		}
		sep := step.Value
		if sep == "" {
			sep = ","
		}
		c.GlobalTable = append(c.GlobalTable, steps.SplitStep(src, result, []byte(sep)))

	case "index_of":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("index_of: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("index_of: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.IndexOfStep(src, result, []byte(step.Value)))

	case "to_int":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.ToIntStep(src, result))

	case "byte_length":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		dest, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.ByteLengthStep(src, dest))

	// ── Encoding ─────────────────────────────────────────────────────────────

	case "base64_encode":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("base64_encode: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("base64_encode: %w", err)
		}
		enc := base64.StdEncoding
		switch step.Input["encoding"] {
		case "url":
			enc = base64.URLEncoding
		case "raw_url":
			enc = base64.RawURLEncoding
		case "raw_std":
			enc = base64.RawStdEncoding
		}
		c.GlobalTable = append(c.GlobalTable, steps.Base64EncodeStep(src, result, enc))

	case "base64_decode":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("base64_decode: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("base64_decode: %w", err)
		}
		enc := base64.RawURLEncoding // default: JWT-friendly
		switch step.Input["encoding"] {
		case "std":
			enc = base64.StdEncoding
		case "url":
			enc = base64.URLEncoding
		case "raw_std":
			enc = base64.RawStdEncoding
		}
		c.GlobalTable = append(c.GlobalTable, steps.Base64DecodeStep(src, result, enc))

	case "hex_encode":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("hex_encode: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("hex_encode: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.HexEncodeStep(src, result))

	case "hex_decode":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("hex_decode: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("hex_decode: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.HexDecodeStep(src, result))

	case "url_encode":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("url_encode: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("url_encode: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.URLEncodeStep(src, result))

	case "url_decode":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("url_decode: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("url_decode: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.URLDecodeStep(src, result))

	// ── Crypto ───────────────────────────────────────────────────────────────

	case "hmac_sha256":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("hmac_sha256: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("hmac_sha256: %w", err)
		}
		key := step.Input["key"]
		if key == "" {
			return fmt.Errorf("hmac_sha256: missing input.key")
		}
		c.GlobalTable = append(c.GlobalTable, steps.HMACSha256Step(src, result, []byte(key)))

	case "hmac_sha1":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("hmac_sha1: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("hmac_sha1: %w", err)
		}
		key := step.Input["key"]
		if key == "" {
			return fmt.Errorf("hmac_sha1: missing input.key")
		}
		c.GlobalTable = append(c.GlobalTable, steps.HMACSha1Step(src, result, []byte(key)))

	case "sha256_hash":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("sha256_hash: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("sha256_hash: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.SHA256HashStep(src, result))

	case "md5_hash":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("md5_hash: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("md5_hash: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.MD5HashStep(src, result))

	case "aes_encrypt":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("aes_encrypt: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("aes_encrypt: %w", err)
		}
		keyHex := step.Input["key"]
		if keyHex == "" {
			return fmt.Errorf("aes_encrypt: missing input.key (hex-encoded AES key)")
		}
		keyBytes, decErr := hex.DecodeString(keyHex)
		if decErr != nil {
			return fmt.Errorf("aes_encrypt: invalid hex key: %w", decErr)
		}
		instr, stepErr := steps.AESEncryptStep(src, result, keyBytes)
		if stepErr != nil {
			return stepErr
		}
		c.GlobalTable = append(c.GlobalTable, instr)

	case "aes_decrypt":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("aes_decrypt: %w", err)
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("aes_decrypt: %w", err)
		}
		keyHex := step.Input["key"]
		if keyHex == "" {
			return fmt.Errorf("aes_decrypt: missing input.key (hex-encoded AES key)")
		}
		keyBytes, decErr := hex.DecodeString(keyHex)
		if decErr != nil {
			return fmt.Errorf("aes_decrypt: invalid hex key: %w", decErr)
		}
		instr, stepErr := steps.AESDecryptStep(src, result, keyBytes)
		if stepErr != nil {
			return stepErr
		}
		c.GlobalTable = append(c.GlobalTable, instr)

	case "add":
		slotA, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		slotB, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.AddStep(slotA, slotB, result))

	case "sub":
		slotA, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		slotB, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.SubStep(slotA, slotB, result))

	case "mul":
		slotA, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		slotB, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.MulStep(slotA, slotB, result))

	case "div":
		slotA, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		slotB, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		result, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.DivStep(slotA, slotB, result))

	case "set_response_header":
		var src int
		if _, known := c.slotMap[step.Source]; !known {
			// Source is not a slot variable — treat the raw string as a literal value.
			litSlot, err := c.getSlot(step.Source)
			if err != nil {
				return err
			}
			c.GlobalTable = append(c.GlobalTable, steps.SetConstStep(step.Source, litSlot))
			src = litSlot
		} else {
			var err error
			src, err = c.getSlot(step.Source)
			if err != nil {
				return err
			}
		}
		c.GlobalTable = append(c.GlobalTable, steps.SetResponseHeaderFromSlot(step.Key, src))

	case "echo_request":
		c.GlobalTable = append(c.GlobalTable, steps.EchoRequestStep())

	case "set_response_body":
		var src int
		if _, known := c.slotMap[step.Source]; !known {
			// Source is not a slot variable — treat the raw string as a literal value.
			litSlot, err := c.getSlot(step.Source)
			if err != nil {
				return err
			}
			c.GlobalTable = append(c.GlobalTable, steps.SetConstStep(step.Source, litSlot))
			src = litSlot
		} else {
			var err error
			src, err = c.getSlot(step.Source)
			if err != nil {
				return err
			}
		}
		c.GlobalTable = append(c.GlobalTable, steps.SetResponseBodyStep(src))

	case "set_response_status":
		code := 200
		if step.Value != "" {
			if n, err := strconv.Atoi(step.Value); err == nil {
				code = n
			}
		}
		c.GlobalTable = append(c.GlobalTable, steps.SetResponseStatusStep(code))

	case "store_internal_tx_id":
		slot, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.StoreInternalTxID(slot))

	case "bind_correlation_id":
		slot, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable,
			steps.BindCorrelationID(step.Key, step.GenerateIfMissing, c.fm.TxIDGen, slot))

	case "log_field":
		// key / key_identifier: field name in access log Extra
		// source: ByteSlot to read value from
		fieldName := step.Key
		if fieldName == "" {
			fieldName = step.KeyIdentifier
		}
		srcSlot, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("log_field: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.LogFieldStep(fieldName, srcSlot))

	case "load_secret":
		if c.SecretsMgr == nil {
			return fmt.Errorf("load_secret step requires a secrets manager — set SecretsMgr on the Compiler")
		}
		if step.Source == "" {
			return fmt.Errorf("load_secret: 'source' (secret reference) is required")
		}
		slot, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.LoadSecret(c.SecretsMgr, step.Source, slot))

	case "load_credential":
		if c.CredMgr == nil {
			return fmt.Errorf("load_credential step requires a credential registry — set CredMgr on the Compiler")
		}
		if step.Source == "" {
			return fmt.Errorf("load_credential: 'source' (credential name) is required")
		}
		slot, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.LoadCredential(c.CredMgr, step.Source, slot))

	case "cache_get":
		if c.CacheMgr == nil {
			return fmt.Errorf("cache_get step requires a cache manager — enable the cache in config")
		}
		keySlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		destSlot, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.CacheGet(c.CacheMgr, keySlot, destSlot))

	case "cache_put":
		if c.CacheMgr == nil {
			return fmt.Errorf("cache_put step requires a cache manager — enable the cache in config")
		}
		keySlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		valueSlot, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.CachePut(keySlot, valueSlot, step.TTL))

	case "cache_get_global":
		if c.CacheMgr == nil {
			return fmt.Errorf("cache_get_global step requires a cache manager — enable the cache in config")
		}
		keySlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		destSlot, err := c.getSlot(step.As)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.CacheGetGlobal(c.CacheMgr, keySlot, destSlot))

	case "cache_put_global":
		if c.CacheMgr == nil {
			return fmt.Errorf("cache_put_global step requires a cache manager — enable the cache in config")
		}
		keySlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		valueSlot, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.CachePutGlobal(keySlot, valueSlot, step.TTL))

	case "cache_delete":
		if c.CacheMgr == nil {
			return fmt.Errorf("cache_delete step requires a cache manager — enable the cache in config")
		}
		keySlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.CacheDelete(c.CacheMgr, keySlot))

	case "cache_delete_global":
		if c.CacheMgr == nil {
			return fmt.Errorf("cache_delete_global step requires a cache manager — enable the cache in config")
		}
		keySlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.CacheDeleteGlobal(c.CacheMgr, keySlot))

	case "cache_exists":
		if c.CacheMgr == nil {
			return fmt.Errorf("cache_exists: no cache store configured")
		}
		keySlot, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("cache_exists: %w", err)
		}
		resultSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("cache_exists: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.CacheExists(c.CacheMgr, keySlot, resultSlot))

	case "cache_incr":
		if c.CacheMgr == nil {
			return fmt.Errorf("cache_incr: no cache store configured")
		}
		keySlot, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("cache_incr: %w", err)
		}
		resultSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("cache_incr: %w", err)
		}
		delta := step.Delta
		if delta == 0 {
			delta = 1
		}
		c.GlobalTable = append(c.GlobalTable, steps.CacheIncr(c.CacheMgr, keySlot, resultSlot, delta, step.TTL))

	case "cache_touch":
		if c.CacheMgr == nil {
			return fmt.Errorf("cache_touch: no cache store configured")
		}
		keySlot, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("cache_touch: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.CacheTouch(c.CacheMgr, keySlot, step.TTL))

	case "batch_flush":
		c.GlobalTable = append(c.GlobalTable, steps.BatchFlush())

	case "cache_get_batched":
		keySlot, err := c.getSlot(step.Variable)
		if err != nil {
			return fmt.Errorf("cache_get_batched: %w", err)
		}
		destSlot, err := c.getSlot(step.Destination)
		if err != nil {
			return fmt.Errorf("cache_get_batched: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.CacheGetBatched(keySlot, destSlot))

	case "json_extract_emit":
		bodySlot, err := c.getSlot(step.Variable)
		if err != nil {
			return fmt.Errorf("json_extract_emit: %w", err)
		}
		ops, err := c.buildExtractOps(step)
		if err != nil {
			return fmt.Errorf("json_extract_emit: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.JSONExtractEmit(bodySlot, ops))

	case "json_foreach_emit":
		bodySlot, err := c.getSlot(step.Variable)
		if err != nil {
			return fmt.Errorf("json_foreach_emit: %w", err)
		}
		arrayPath := step.Path
		if arrayPath == "" {
			return fmt.Errorf("json_foreach_emit: missing path")
		}
		ops, err := c.buildExtractOps(step)
		if err != nil {
			return fmt.Errorf("json_foreach_emit: %w", err)
		}
		c.GlobalTable = append(c.GlobalTable, steps.JSONForeachEmit(bodySlot, arrayPath, ops))

	case "return":
		// Terminate flow immediately with a given HTTP status and body.
		// status: HTTP response code (default 200).
		// body: static response body string (optional; if empty, body slot named by As is used).
		// as: slot name holding a dynamic body (used when body is empty).
		bodySlot := -1
		if step.As != "" {
			if s, ok := c.slotMap[step.As]; ok {
				bodySlot = s
			}
		}
		status := step.Status
		if status == 0 {
			status = 200
		}
		var staticBody []byte
		if step.Body != "" {
			staticBody = []byte(step.Body)
		}
		c.GlobalTable = append(c.GlobalTable, steps.EarlyReturn(status, bodySlot, staticBody))

	case "fail":
		// Mark the request as failed with a code and message, then stop.
		// status: error code stored in ctx.ErrorCode (default 500).
		// body: static error message string (optional; if empty, msg slot named by As is used).
		// as: slot name holding a dynamic error message (used when body is empty).
		msgSlot := -1
		if step.As != "" {
			if s, ok := c.slotMap[step.As]; ok {
				msgSlot = s
			}
		}
		code := int16(step.Status)
		if code == 0 {
			code = 500
		}
		var staticMsg []byte
		if step.Body != "" {
			staticMsg = []byte(step.Body)
		}
		c.GlobalTable = append(c.GlobalTable, steps.Fail(code, msgSlot, staticMsg))

	case "capture_error":
		// Capture current error state into slots and clear it.
		// key: slot name to capture the error code into (optional).
		// as: slot name to capture the error message into (optional, allocated if new).
		codeSlot := -1
		msgSlot := -1
		if step.Key != "" {
			if s, ok := c.slotMap[step.Key]; ok {
				codeSlot = s
			}
		}
		if step.As != "" {
			var err error
			msgSlot, err = c.getSlot(step.As)
			if err != nil {
				return err
			}
		}
		c.GlobalTable = append(c.GlobalTable, steps.CaptureError(codeSlot, msgSlot))

	case "api_rate_limits":
		// Position marker: emit the API's rate limit policies at this exact position
		// in the flow. If absent, policies are auto-injected at the start of the flow
		// by CompileExecutable. No-op when skip_rate_limit is set or no policies exist.
		if !c.currentAPISkipRL {
			c.emitRLPoliciesIntoTable(c.currentAPIPolicies)
		}

	case "set_request_body":
		srcSlot, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("set_request_body: %w", err)
		}
		ct := step.Input["content_type"]
		if ct == "" {
			ct = "application/json"
		}
		c.GlobalTable = append(c.GlobalTable, steps.SetRequestBody(srcSlot, []byte(ct)))

	case "bind_request_url":
		dstSlot, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("bind_request_url: %w", err)
		}
		includeQuery := true // default: include query string
		if step.IncludeQuery != nil {
			includeQuery = *step.IncludeQuery
		}
		c.GlobalTable = append(c.GlobalTable, steps.BindRequestURL(dstSlot, includeQuery))

	case "copy_header":
		srcName := step.Key
		if srcName == "" {
			srcName = step.KeyIdentifier
		}
		dstName := step.As
		if srcName == "" {
			return fmt.Errorf("copy_header: missing key (source header name)")
		}
		if dstName == "" {
			return fmt.Errorf("copy_header: missing as (destination header name)")
		}
		c.GlobalTable = append(c.GlobalTable, steps.CopyHeader(srcName, dstName))

	// ── Resilience ───────────────────────────────────────────────────────────

	case "spike_arrest":
		intervalMs := uint32(100) // default: 1 req / 100 ms
		if v, ok := step.Input["interval_ms"]; ok {
			if n, parseErr := strconv.Atoi(v); parseErr == nil && n > 0 {
				intervalMs = uint32(n)
			}
		}
		keySlot := -1
		if step.Source != "" {
			if ks, ksErr := c.getSlotReadOnly(step.Source); ksErr == nil {
				keySlot = ks
			}
		}
		flowID := uint32(len(c.GlobalTable))
		intervalNs := int64(intervalMs) * int64(time.Millisecond)
		c.GlobalTable = append(c.GlobalTable, engine.SpikeArrestStep(c.fm.SpikeArrestStore, flowID, intervalNs, keySlot))

	case "circuit_breaker":
		failureThresh := int64(parseIntInput(step.Input, "failure_threshold", 5))
		successThresh := int64(parseIntInput(step.Input, "success_threshold", 2))
		openDurMs := uint32(parseIntInput(step.Input, "open_duration_ms", 30000))
		cbIdx, cbErr := c.fm.CircuitBreakerArena.Alloc(failureThresh, successThresh, openDurMs)
		if cbErr != nil {
			return fmt.Errorf("circuit_breaker: %w", cbErr)
		}
		c.GlobalTable = append(c.GlobalTable, engine.CircuitBreakerGateStep(c.fm.CircuitBreakerArena, cbIdx))

	case "record_circuit_outcome":
		cbIdx := c.fm.CircuitBreakerArena.Count() - 1
		if cbIdx < 0 {
			return fmt.Errorf("record_circuit_outcome: no circuit_breaker step has been compiled yet")
		}
		var successFn engine.OutcomeFunc
		if step.Condition != "" {
			cf, cfErr := steps.CompileCondition(step.Condition, c.slotMap)
			if cfErr != nil {
				return fmt.Errorf("record_circuit_outcome condition: %w", cfErr)
			}
			successFn = engine.OutcomeFunc(cf)
		}
		c.GlobalTable = append(c.GlobalTable, engine.RecordCircuitOutcomeStep(c.fm.CircuitBreakerArena, cbIdx, successFn))

	default:
		return fmt.Errorf("unknown step action %q", step.Action)
	}

	// Apply on_error wrapping to the last emitted instruction for fallible leaf steps.
	// Branching/control-flow steps (if, switch, call, return, fail, capture_error)
	// manage their own termination and must not be wrapped.
	if step.OnError != "" && !isControlFlowAction(step.Action) && len(c.GlobalTable) > 0 {
		lastIdx := len(c.GlobalTable) - 1
		last := c.GlobalTable[lastIdx]
		switch step.OnError {
		case "continue":
			c.GlobalTable[lastIdx] = steps.WrapOnErrorContinue(last)
		default:
			if flowName, ok2 := strings.CutPrefix(step.OnError, "jump:"); ok2 {
				
				if errPC, ok := c.FragmentMap[flowName]; ok {
					c.GlobalTable[lastIdx] = steps.WrapOnErrorJump(last, errPC)
				} else {
					// Flow not yet compiled — record for second pass
					c.pendingJumps = append(c.pendingJumps, pendingJump{
						instrIdx: lastIdx,
						flowName: flowName,
						inner:    last,
					})
				}
			} else if codeStr, ok2 := strings.CutPrefix(step.OnError, "status:"); ok2 {
				
				if httpStatus, err2 := strconv.Atoi(codeStr); err2 == nil {
					c.GlobalTable[lastIdx] = steps.WrapOnErrorStatus(last, httpStatus, nil)
				}
			}
		}
	}

	// ── Per-step observability hooks ──────────────────────────────────
	// Applied after the step's own instruction(s) and any on_error wrapper.
	// Control-flow steps (if, switch, call, return, fail) are skipped because
	// they don't produce a single output variable in the conventional sense.
	if !isControlFlowAction(step.Action) {
		outSlotName := step.As
		if outSlotName == "" {
			outSlotName = step.Destination // fallback for cache_get_batched
		}
		if outSlot, ok := c.slotMap[outSlotName]; ok && outSlotName != "" {
			if step.LogAs != "" {
				c.GlobalTable = append(c.GlobalTable, steps.LogFieldStep(step.LogAs, outSlot))
			}
			if step.TraceCapture {
				c.GlobalTable = append(c.GlobalTable, steps.TraceCaptureStep(outSlot, outSlotName))
			}
		}
		// TraceVars: emit a trace_capture for each explicitly named variable.
		// Deduplicates against the primary TraceCapture variable.
		if len(step.TraceVars) > 0 {
			seen := outSlotName
			_ = seen
			for _, varName := range step.TraceVars {
				if varName == "" || (step.TraceCapture && varName == outSlotName) {
					continue
				}
				if slot, ok2 := c.slotMap[varName]; ok2 {
					c.GlobalTable = append(c.GlobalTable, steps.TraceCaptureStep(slot, varName))
				}
			}
		}
	}

	return nil
}

// simulateBake calculates the number of instructions a flow would generate
func (c *Compiler) simulateBake(flow []StepConfig, frags map[string][]StepConfig) []bool {
	count := 0
	for _, step := range flow {
		switch step.Action {
		case "if":
			count += 2 + len(c.simulateBake(frags[step.Then], frags)) + len(c.simulateBake(frags[step.Else], frags))
		case "pattern_match":
			// Same layout as "if": 1 gate + len(then) + 1 GOTO + len(else)
			count += 2 + len(c.simulateBake(frags[step.Then], frags)) + len(c.simulateBake(frags[step.Else], frags))
		case "switch":
			count += 1
			for _, fragName := range step.Cases {
				count += len(c.simulateBake(frags[fragName], frags)) + 1
			}
		case "foreach":
			count += 2 + len(c.simulateBake(step.Do, frags))
		case "while":
			count += 2 + len(c.simulateBake(step.Do, frags))
		case "http_call":
			count += 1
		default:
			count += 1
		}
	}
	return make([]bool, count)
}

func (c *Compiler) discoverDependencies(flow []StepConfig) []Dependency {
	return c.discoverDependenciesWithFragments(flow, nil)
}

func (c *Compiler) discoverDependenciesWithFragments(flow []StepConfig, fragments map[string][]StepConfig) []Dependency {
	var deps []Dependency
	found := make(map[string]bool)
	re := regexp.MustCompile(`(header|query|path|host)\.([a-zA-Z0-9_-]+)`)
	visitedFlows := make(map[string]bool)

	var walkFlow func([]StepConfig)
	walkFlow = func(stepsToWalk []StepConfig) {
		for _, step := range stepsToWalk {
			blob := strings.Join([]string{step.Condition, step.KeyIdentifier, step.UrlVar, step.Source, step.As, step.Variable}, " ")
			matches := re.FindAllStringSubmatch(blob, -1)
			for _, m := range matches {
				if !found[m[0]] {
					deps = append(deps, Dependency{Identifier: m[0], Source: m[1], Key: m[2]})
					found[m[0]] = true
				}
			}

			if len(step.Do) > 0 {
				walkFlow(step.Do)
			}

			if fragments == nil {
				continue
			}

			for _, ref := range []string{step.FlowName, step.Then, step.Else} {
				if ref == "" || visitedFlows[ref] {
					continue
				}
				if nested, ok := fragments[ref]; ok {
					visitedFlows[ref] = true
					walkFlow(nested)
				}
			}

			for _, ref := range step.Cases {
				if ref == "" || visitedFlows[ref] {
					continue
				}
				if nested, ok := fragments[ref]; ok {
					visitedFlows[ref] = true
					walkFlow(nested)
				}
			}
		}
	}

	walkFlow(flow)
	return deps
}

func (c *Compiler) CompileExecutable(flow []StepConfig, fragments map[string][]StepConfig) ([]engine.Instruction, error) {
	c.GlobalTable = make([]engine.Instruction, 0)
	c.resetSlots()

	// Auto-bind preamble: discover and bind header/query dependencies.
	deps := c.discoverDependenciesWithFragments(flow, fragments)
	for _, dep := range deps {
		slot, err := c.getSlot(dep.Identifier)
		if err != nil {
			return nil, fmt.Errorf("auto-bind dependency %q: %w", dep.Identifier, err)
		}
		switch dep.Source {
		case "header":
			c.GlobalTable = append(c.GlobalTable, steps.BindHeader(dep.Key, slot))
		case "query":
			c.GlobalTable = append(c.GlobalTable, steps.BindQuery(dep.Key, slot))
		}
	}

	// Auto-inject: when the flow tree contains no rate limit step (no explicit
	// placement or "api_rate_limits" marker), and the API has RL policies defined
	// and skip_rate_limit is not set, prepend RL instructions after the auto-bind
	// preamble so enforcement is guaranteed even without flow-level RL steps.
	if !c.currentAPISkipRL && len(c.currentAPIPolicies) > 0 && !flowHasRateLimitStep(flow, fragments) {
		c.emitRLPoliciesIntoTable(c.currentAPIPolicies)
	}

	if err := c.bakeFlow(flow, fragments); err != nil {
		return nil, err
	}
	return c.GlobalTable, nil
}

// Helpers
func (c *Compiler) getSlot(name string) (int, error) {
	if idx, ok := c.slotMap[name]; ok {
		return idx, nil
	}
	var idx int
	if len(c.freeSlots) > 0 {
		// Reuse a slot freed by liveness analysis.
		idx = c.freeSlots[len(c.freeSlots)-1]
		c.freeSlots = c.freeSlots[:len(c.freeSlots)-1]
	} else {
		if c.nextSlot >= rctx.BaseByteSlots {
			return -1, fmt.Errorf("slot limit exceeded: flow requires more than %d byte slots (max %d); split into sub-flows or reduce variables", c.nextSlot, rctx.BaseByteSlots)
		}
		idx = c.nextSlot
		c.nextSlot++
	}
	c.slotMap[name] = idx
	return idx, nil
}

// getSlotReadOnly returns the slot index for name only if it was already allocated.
// Returns an error if the name is unknown, without allocating a new slot.
// Used for foreach source slots that must have been declared earlier in the flow.
func (c *Compiler) getSlotReadOnly(name string) (int, error) {
	if idx, ok := c.slotMap[name]; ok {
		return idx, nil
	}
	return -1, fmt.Errorf("slot %q not yet declared in this flow", name)
}

// getBoolSlot resolves (or allocates) a slot index used as a BoolSlot index.
// BoolSlots share the same name→index mapping as ByteSlots by convention;
// the instruction is responsible for selecting the correct slice.
func (c *Compiler) getBoolSlot(name string) (int, error) {
	return c.getSlot(name)
}

func (c *Compiler) resetSlots() {
	c.slotMap = make(map[string]int)
	c.freeSlots = c.freeSlots[:0]
	c.nextSlot = 0
}

// parseIntInput returns the integer value for key in input, or def if absent or unparseable.
func parseIntInput(input map[string]string, key string, def int) int {
	if v, ok := input[key]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// SnapshotSlots returns a copy of the current slot map and nextSlot counter.
// Used to save slot state after a CompileExecutable call so it can be restored
// for multiple endpoints that share the same flow (no per-endpoint override).
func (c *Compiler) SnapshotSlots() (map[string]int, int) {
	snap := make(map[string]int, len(c.slotMap))
	for k, v := range c.slotMap {
		snap[k] = v
	}
	return snap, c.nextSlot
}

// RestoreSlots replaces the current slot map with a fresh copy of snap and
// resets nextSlot. Used to restore the default-flow slot context before
// allocating constants for a no-override endpoint in the same API.
func (c *Compiler) RestoreSlots(snap map[string]int, nextSlot int) {
	fresh := make(map[string]int, len(snap))
	for k, v := range snap {
		fresh[k] = v
	}
	c.slotMap = fresh
	c.nextSlot = nextSlot
	c.freeSlots = c.freeSlots[:0]
}

// UpstreamUrlSlotName is the canonical slot name used for the upstream URL.
// Flows that reference the upstream URL via `url_var: upstream_url` will map
// to this same slot — keeping gateway-native injection and flow-level references
// in sync without requiring any special coordination.
const UpstreamUrlSlotName = "upstream_url"

// AllocUpstreamUrlSlot resolves (or allocates) the slot index for the
// canonical "upstream_url" slot.  Must be called immediately after
// CompileExecutable so the slot map reflects the compiled flow.
// Returns -1 and an error only when the slot cap is exceeded.
func (c *Compiler) AllocUpstreamUrlSlot() (int, error) {
	return c.getSlot(UpstreamUrlSlotName)
}

// AllocConstantSlots resolves slot indices for each constant key using the
// compiler's current slotMap (populated by the most recent CompileExecutable call).
// Keys already referenced in the flow map to existing slots; new keys get fresh ones.
// Returns pre-allocated []ConstantSlot ready to be stored in EngineState.RouteConstants.
// Must be called immediately after CompileExecutable, before any other compilation resets the slotMap.
func (c *Compiler) AllocConstantSlots(constants map[string]string) ([]engine.ConstantSlot, error) {
	if len(constants) == 0 {
		return nil, nil
	}
	slots := make([]engine.ConstantSlot, 0, len(constants))
	for k, v := range constants {
		idx, err := c.getSlot(k)
		if err != nil {
			return nil, fmt.Errorf("constant %q: %w", k, err)
		}
		slots = append(slots, engine.ConstantSlot{SlotIdx: idx, Value: []byte(v)})
	}
	return slots, nil
}

// compilePatternMatch compiles a "pattern_match" step.
//
// DSL fields:
//
//	source:  slot name whose bytes are matched (e.g. "header.x-service")
//	pattern: regex pattern string (e.g. "^(api|data).*")
//	flags:   optional regex flags string: i (case-insensitive), m (multiline),
//	         s (dot-all), x (verbose). Combined as (?flags) prefix.
//	then:    flow name to execute when the pattern matches
//	else:    flow name to execute when the pattern does not match (optional)
//
// The regex is compiled once at bake time; runtime cost is a single
// *regexp.Regexp.Match call on the raw []byte in the slot — zero allocations.
func (c *Compiler) compilePatternMatch(step StepConfig, fragments map[string][]StepConfig) error {
	// --- Field validation -------------------------------------------------------
	if step.Source == "" {
		return fmt.Errorf("pattern_match: 'source' is required (slot whose value is matched)")
	}
	pattern := step.Input["pattern"]
	if pattern == "" {
		return fmt.Errorf("pattern_match: 'pattern' is required (regex string)")
	}

	// --- Slot resolution --------------------------------------------------------
	// The source slot must resolve; it may or may not already exist in slotMap.
	srcSlot, err := c.getSlot(step.Source)
	if err != nil {
		return fmt.Errorf("pattern_match: source slot %q: %w", step.Source, err)
	}

	// --- Regex compilation with flags -------------------------------------------
	flags := strings.TrimSpace(step.Input["flags"])
	finalPattern := pattern
	if flags != "" {
		// Validate flag characters: only i, m, s, x are supported.
		for _, ch := range flags {
			switch ch {
			case 'i', 'm', 's', 'x':
				// valid
			default:
				return fmt.Errorf("pattern_match: unsupported regex flag %q (supported: i, m, s, x)", string(ch))
			}
		}
		// Prepend (?flags) — Go's regexp supports inline flags at the start.
		finalPattern = "(?" + flags + ")" + pattern
	}

	compiledRE, err := regexp.Compile(finalPattern)
	if err != nil {
		return fmt.Errorf("pattern_match: invalid regex %q (flags %q): %w", pattern, flags, err)
	}

	// --- Jump target calculation (mirrors the "if" pattern) --------------------
	thenBlock := c.simulateBake(fragments[step.Then], fragments)
	elseBlock := c.simulateBake(fragments[step.Else], fragments)

	// Layout:
	//   [N]   PATTERN_MATCH_REGEX   (gate — jumps to thenStart or elseStart)
	//   [N+1 .. N+len(then)]        then-branch instructions
	//   [N+1+len(then)]             GOTO postElse
	//   [N+2+len(then) .. ...]      else-branch instructions
	//   [postElseID]                next step
	thenStartID := int16(len(c.GlobalTable)) + 1
	skipElseID := thenStartID + int16(len(thenBlock))
	elseStartID := skipElseID + 1
	postElseID := elseStartID + int16(len(elseBlock))

	// Emit the gate instruction.
	c.GlobalTable = append(c.GlobalTable, steps.PatternMatchRegex(srcSlot, compiledRE, thenStartID, elseStartID))

	// Bake then-branch.
	if err := c.bakeFlowRaw(fragments[step.Then], fragments); err != nil {
		return fmt.Errorf("pattern_match: then-branch %q: %w", step.Then, err)
	}

	// Skip-else jump (unconditional goto after then-branch).
	c.GlobalTable = append(c.GlobalTable, c.newInternalJump(postElseID))

	// Bake else-branch (may be empty when step.Else == "").
	if err := c.bakeFlowRaw(fragments[step.Else], fragments); err != nil {
		return fmt.Errorf("pattern_match: else-branch %q: %w", step.Else, err)
	}

	return nil
}

// compileValidatePattern compiles a validate_pattern step.
//
// Fields:
//
//	source:          slot name whose value is tested
//	input.pattern:   template pattern string (literals, (capture), {ref}, *)
//	as:              result slot name — set to []byte{1} on match, nil on no-match
func (c *Compiler) compileValidatePattern(step StepConfig) error {
	if step.Source == "" {
		return fmt.Errorf("validate_pattern: 'source' is required")
	}
	pattern := step.Input["pattern"]
	if pattern == "" {
		return fmt.Errorf("validate_pattern: 'pattern' is required in input")
	}
	if step.As == "" {
		return fmt.Errorf("validate_pattern: 'as' is required (result variable name)")
	}
	srcSlot, err := c.getSlot(step.Source)
	if err != nil {
		return fmt.Errorf("validate_pattern: source slot %q: %w", step.Source, err)
	}
	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("validate_pattern: result slot %q: %w", step.As, err)
	}
	// Build slotMap snapshot AFTER allocating src/result slots so they are also
	// findable as {ref} targets inside the pattern if needed.
	slotSnap := make(map[string]int, len(c.slotMap))
	for k, v := range c.slotMap {
		slotSnap[k] = v
	}
	compiled, err := steps.ParseTemplatePattern(pattern, slotSnap, c.getSlot)
	if err != nil {
		return fmt.Errorf("validate_pattern: %w", err)
	}
	c.GlobalTable = append(c.GlobalTable, steps.ValidateTemplatePattern(srcSlot, compiled, resultSlot))
	return nil
}

// compileExtractPattern compiles an extract_pattern step.
//
// Fields:
//
//	source:          slot name whose value is parsed
//	input.pattern:   template pattern string — capture groups (name) write to named slots
//
// Note: 'as' is NOT used for extract_pattern; capture slot names are declared
// inside the pattern via the (name) syntax.
func (c *Compiler) compileExtractPattern(step StepConfig) error {
	if step.Source == "" {
		return fmt.Errorf("extract_pattern: 'source' is required")
	}
	pattern := step.Input["pattern"]
	if pattern == "" {
		return fmt.Errorf("extract_pattern: 'pattern' is required in input")
	}
	srcSlot, err := c.getSlot(step.Source)
	if err != nil {
		return fmt.Errorf("extract_pattern: source slot %q: %w", step.Source, err)
	}
	// Build slotMap snapshot AFTER allocating srcSlot so it is findable as a
	// {ref} target inside the pattern if needed.
	slotSnap := make(map[string]int, len(c.slotMap))
	for k, v := range c.slotMap {
		slotSnap[k] = v
	}
	compiled, err := steps.ParseTemplatePattern(pattern, slotSnap, c.getSlot)
	if err != nil {
		return fmt.Errorf("extract_pattern: %w", err)
	}
	c.GlobalTable = append(c.GlobalTable, steps.ExtractTemplatePattern(srcSlot, compiled))
	return nil
}

// isControlFlowAction returns true for step actions that must not be wrapped
// with on_error handlers because they control their own execution flow.
func isControlFlowAction(action string) bool {
	switch action {
	case "if", "switch", "call", "return", "fail", "capture_error", "pattern_match":
		return true
	}
	return false
}

// ─── Rate Limit Policy Helpers (S7: auto-inject + position-aware injection) ──

// rateLimitStepActions is the set of step action names that count as rate limit
// steps when walking the flow tree for the auto-inject / marker decision.
var rateLimitStepActions = map[string]bool{
	"check_rate_limit":        true,
	"check_rate_limit_v2":     true,
	"check_rate_limit_global": true,
	"api_rate_limits":         true,
}

// flowHasRateLimitStep returns true if any step in the flow (or any sub-flow
// reachable via call/if/switch/foreach/while/pattern_match) is a rate limit step.
// Uses visited to prevent infinite recursion on cyclic sub-flow references.
func flowHasRateLimitStep(flow []StepConfig, fragments map[string][]StepConfig) bool {
	visited := make(map[string]bool)
	return walkForRLStep(flow, fragments, visited)
}

func walkForRLStep(flow []StepConfig, fragments map[string][]StepConfig, visited map[string]bool) bool {
	for _, step := range flow {
		if rateLimitStepActions[step.Action] {
			return true
		}
		// Recurse into inline nested step lists (foreach.Do, while.Do).
		if len(step.Do) > 0 && walkForRLStep(step.Do, fragments, visited) {
			return true
		}
		// Recurse into named sub-flow references: call.FlowName, if.Then/Else.
		for _, ref := range []string{step.FlowName, step.Then, step.Else} {
			if ref == "" || visited[ref] {
				continue
			}
			if nested, ok := fragments[ref]; ok {
				visited[ref] = true
				if walkForRLStep(nested, fragments, visited) {
					return true
				}
			}
		}
		// Recurse into switch case sub-flows.
		for _, ref := range step.Cases {
			if ref == "" || visited[ref] {
				continue
			}
			if nested, ok := fragments[ref]; ok {
				visited[ref] = true
				if walkForRLStep(nested, fragments, visited) {
					return true
				}
			}
		}
	}
	return false
}

// buildRLEntryCountBy constructs a RateLimitCountBy from an APIRateLimitEntry's
// CountBy / SlotSource / StaticKey fields.
func (c *Compiler) buildRLEntryCountBy(entry APIRateLimitEntry) engine.RateLimitCountBy {
	var cb engine.RateLimitCountBy
	switch entry.CountBy {
	case "ip":
		cb = engine.RateLimitCountBy{Kind: engine.CountByIP}
	case "slot":
		slot := -1
		if entry.SlotSource != "" {
			slot, _ = c.getSlot(entry.SlotSource)
		}
		cb = engine.RateLimitCountBy{Kind: engine.CountBySlot, SlotIndex: slot}
	case "static":
		cb = engine.RateLimitCountBy{Kind: engine.CountByStatic, StaticKey: []byte(entry.StaticKey)}
	case "global":
		cb = engine.RateLimitCountBy{Kind: engine.CountByGlobal}
	default: // "tenant"
		cb = engine.RateLimitCountBy{Kind: engine.CountByTenant}
	}
	return cb
}

// emitRLPoliciesIntoTable emits CheckRateLimitV2 (and AssignQuotaGroup for dynamic)
// instructions into c.GlobalTable for each resolved APIRateLimitEntry.
//
//   - named / fixed: entry.Config is the resolved config name (set by resolveAndRegisterRLPolicies).
//   - dynamic:       emits AssignQuotaGroup first, then CheckRateLimitV2 using the first
//     mapped config as a representative config for the windows.
//
// When denied, the instruction returns StopPlan (-1) so the flow halts immediately
// and the 429 ResponseStatus set by CheckRateLimitV2 is preserved.
func (c *Compiler) emitRLPoliciesIntoTable(policies []APIRateLimitEntry) {
	for _, entry := range policies {
		configName := entry.Config

		switch entry.Kind {
		case RLEntryDynamic:
			// Emit AssignQuotaGroup to map the runtime value to a quota group ID.
			// The quota group enables V1 CheckRateLimit dispatch; full V2 dynamic
			// dispatch (per-group CheckRateLimitV2) is a future enhancement.
			if entry.Dynamic == nil || len(entry.Dynamic.Mappings) == 0 {
				log.Printf("[Compiler] api_rate_limits dynamic entry: empty mapping — skipped")
				continue
			}
			groupMap := make(map[string]uint8, len(entry.Dynamic.Mappings))
			gid := uint8(1)
			for runtimeVal := range entry.Dynamic.Mappings {
				groupMap[runtimeVal] = gid
				gid++
			}
			srcSlot := -1
			if entry.Dynamic.Source != "" {
				srcSlot, _ = c.getSlot(entry.Dynamic.Source)
			}
			c.GlobalTable = append(c.GlobalTable, steps.AssignQuotaGroup(srcSlot, groupMap))
			// Use the first mapped config name as the representative V2 config.
			for _, mappedName := range entry.Dynamic.Mappings {
				configName = mappedName
				break
			}

		case RLEntryNamed, RLEntryFixed:
			// configName is already set from entry.Config (resolved by management server).

		default:
			continue
		}

		if configName == "" {
			log.Printf("[Compiler] api_rate_limits entry (kind=%s): no config name — skipped", entry.Kind)
			continue
		}

		// Resolve windows from the V2 config registry.
		var configID uint16
		var windows []steps.WindowSpec
		var enforcement string
		if c.RegMgr != nil {
			if id, ok := c.RegMgr.GetRateLimitConfigId(configName); ok {
				configID = id
			}
			if cfg := c.RegMgr.GetRateLimitConfigV2(configName); cfg != nil {
				enforcement = cfg.Enforcement
				for i, w := range cfg.Windows {
					epochDiv := w.PeriodSecs
					if epochDiv == 0 {
						epochDiv = 1
					}
					windows = append(windows, steps.WindowSpec{EpochDiv: epochDiv, Limit: w.Limit, Idx: i})
				}
			}
		}
		if len(windows) == 0 {
			log.Printf("[Compiler] api_rate_limits: config %q not found or has no windows — skipped", configName)
			continue
		}

		countBy := c.buildRLEntryCountBy(entry)

		var remoteRL engine.ExternalRateLimitProvider
		if enforcement == "strict" && c.fm.RemoteRL != nil {
			remoteRL = c.fm.RemoteRL
		}

		// Capture loop variables for the closure.
		capturedConfigID := configID
		capturedCountBy := countBy
		capturedWindows := windows
		capturedConfigName := configName
		capturedRemoteRL := remoteRL
		// NextPC: the instruction immediately following this check (continue flow).
		// DeniedPC: -1 (StopPlan) — halt execution immediately when denied.
		// This is safe because we set ctx.ResponseStatus = 429 before returning.
		nextPC := len(c.GlobalTable) + 1
		const deniedPC = -1 // StopPlan: halt flow on denial
		c.GlobalTable = append(c.GlobalTable, engine.Instruction{
			Name:    "CHECK_RATE_LIMIT_V2",
			StepIdx: -1, // system instruction — not a user-defined flow step
			Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
				rl := &steps.CheckRateLimitV2{
					ConfigID:   capturedConfigID,
					CountBy:    capturedCountBy,
					Windows:    capturedWindows,
					DeniedPC:   deniedPC,
					NextPC:     nextPC,
					RemoteRL:   capturedRemoteRL,
					ConfigName: capturedConfigName,
				}
				return rl.Execute(ctx, s)
			},
		})
	}
}

// resolvePendingJumps patches any on_error:jump wrappers that referenced
// flows compiled after the referencing step.
func (c *Compiler) resolvePendingJumps() error {
	for _, pj := range c.pendingJumps {
		errPC, ok := c.FragmentMap[pj.flowName]
		if !ok {
			return fmt.Errorf("on_error:jump references unknown flow %q", pj.flowName)
		}
		c.GlobalTable[pj.instrIdx] = steps.WrapOnErrorJump(pj.inner, errPC)
	}
	c.pendingJumps = c.pendingJumps[:0]
	return nil
}

// compileQuotaGroupMap builds a []uint16 indexed by QuotaGroupID where each
// element is the RateLimitConfigId for that group. The input map has string
// group IDs as keys and rate limit config names as values:
//
//	{"1": "free_rl", "2": "pro_rl", "3": "enterprise_rl"}
func (c *Compiler) compileQuotaGroupMap(input map[string]string) []uint16 {
	maxID := uint8(0)
	parsed := make(map[uint8]string, len(input))
	for idStr, rlName := range input {
		id, err := strconv.ParseUint(idStr, 10, 8)
		if err != nil || id == 0 {
			continue
		}
		gid := uint8(id)
		parsed[gid] = rlName
		if gid > maxID {
			maxID = gid
		}
	}
	if maxID == 0 {
		return nil
	}
	out := make([]uint16, int(maxID)+1)
	for gid, rlName := range parsed {
		if rlID, ok := c.RegMgr.GetRateLimitConfigId(rlName); ok {
			out[gid] = rlID
		}
	}
	return out
}

// buildRLCountBy builds an engine.RateLimitCountBy from a step input map.
// slotLookup maps a variable name to its ByteSlot index; called only for slot/composite kinds.
func buildRLCountBy(input map[string]string, slotLookup func(name string) int) engine.RateLimitCountBy {
	var cb engine.RateLimitCountBy
	switch input["count_by"] {
	case "ip":
		xffIdx := 0
		if s := strings.TrimSpace(input["xff_index"]); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				xffIdx = n
			}
		}
		cb = engine.RateLimitCountBy{Kind: engine.CountByIP, XFFIndex: xffIdx}
	case "slot":
		cb = engine.RateLimitCountBy{Kind: engine.CountBySlot, SlotIndex: slotLookup(strings.TrimSpace(input["slot"]))}
	case "static":
		cb = engine.RateLimitCountBy{Kind: engine.CountByStatic, StaticKey: []byte(input["static_key"])}
	case "composite":
		var idxs []int
		for _, name := range strings.Split(input["slots"], ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				idxs = append(idxs, slotLookup(name))
			}
		}
		cb = engine.RateLimitCountBy{Kind: engine.CountByComposite, SlotIndexes: idxs}
	case "global":
		cb = engine.RateLimitCountBy{Kind: engine.CountByGlobal}
	default: // "tenant"
		cb = engine.RateLimitCountBy{Kind: engine.CountByTenant}
	}
	// OnEmpty policy
	switch strings.TrimSpace(input["on_empty"]) {
	case "skip":
		cb.OnEmpty = engine.OnEmptyKeySkip
	case "fallback_tenant":
		cb.OnEmpty = engine.OnEmptyKeyTenant
	default: // "fail"
		cb.OnEmpty = engine.OnEmptyKeyFail
	}
	cb.FailFast = strings.TrimSpace(input["fail_fast"]) == "true"
	return cb
}

// buildExtractOps parses step.Params into a []steps.ExtractOp slice.
// Each param map must have: path, key_prefix, op_type ("get"/"put"),
// target ("cache"/"registry_url"/"registry_id"/"registry_meta"),
// dest_slot (variable name for get ops), value_slot (variable name for put ops, or "" to use extracted value).
// async ("true"/"false", put only).
func (c *Compiler) buildExtractOps(step StepConfig) ([]steps.ExtractOp, error) {
	ops := make([]steps.ExtractOp, 0, len(step.Params))
	for _, p := range step.Params {
		op := steps.ExtractOp{
			Path:      p["path"],
			KeyPrefix: p["key_prefix"],
			Async:     p["async"] == "true",
		}
		switch p["op_type"] {
		case "get":
			op.OpType = rctx.OpGet
		case "put":
			op.OpType = rctx.OpPut
		default:
			return nil, fmt.Errorf("unknown op_type %q", p["op_type"])
		}
		switch p["target"] {
		case "cache":
			op.Target = rctx.TargetCache
		case "registry_url":
			op.Target = rctx.TargetRegistryURL
		case "registry_id":
			op.Target = rctx.TargetRegistryID
		case "registry_meta":
			op.Target = rctx.TargetRegistryMeta
		default:
			return nil, fmt.Errorf("unknown target %q", p["target"])
		}
		if op.OpType == rctx.OpGet {
			if p["dest_slot"] != "" {
				slot, err := c.getSlot(p["dest_slot"])
				if err != nil {
					return nil, err
				}
				op.DestSlot = slot
			} else {
				op.DestSlot = -1
			}
		}
		if op.OpType == rctx.OpPut {
			if p["value_slot"] != "" {
				slot, err := c.getSlot(p["value_slot"])
				if err != nil {
					return nil, err
				}
				op.ValueSlot = slot
			} else {
				op.ValueSlot = -1
			}
			if ttlStr := p["ttl"]; ttlStr != "" {
				if n, err := strconv.ParseUint(ttlStr, 10, 32); err == nil {
					op.TTL = uint32(n)
				}
			}
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// computeLastUse performs a single-pass liveness analysis over a flat flow.
// Returns a map from variable name to the last step index that references it.
// For if/else branches, all variables used in either branch are extended to the
// if step's index (the join point), preventing premature slot release.
func (c *Compiler) computeLastUse(flow []StepConfig, frags map[string][]StepConfig) map[string]int {
	lastUse := make(map[string]int, len(flow)*2)
	for i, step := range flow {
		for _, name := range c.varRefsInStep(step) {
			lastUse[name] = i
		}
		// Join-point rule: variables used inside branches must survive until after the if/pattern_match step.
		if step.Action == "if" || step.Action == "pattern_match" {
			for _, name := range c.varRefsInFlow(frags[step.Then], frags) {
				if cur, ok := lastUse[name]; !ok || cur < i {
					lastUse[name] = i
				}
			}
			for _, name := range c.varRefsInFlow(frags[step.Else], frags) {
				if cur, ok := lastUse[name]; !ok || cur < i {
					lastUse[name] = i
				}
			}
		}
		// foreach: variables in the Do body survive until after the foreach step.
		if step.Action == "foreach" {
			for _, name := range c.varRefsInFlow(step.Do, frags) {
				if cur, ok := lastUse[name]; !ok || cur < i {
					lastUse[name] = i
				}
			}
		}
		// call: variables in the called flow survive until after the call step.
		if step.Action == "call" && step.FlowName != "" {
			for _, name := range c.varRefsInFlow(frags[step.FlowName], frags) {
				if cur, ok := lastUse[name]; !ok || cur < i {
					lastUse[name] = i
				}
			}
		}
	}
	return lastUse
}

// varRefsInStep returns the variable names directly referenced in a single step.
func (c *Compiler) varRefsInStep(step StepConfig) []string {
	names := [7]string{step.As, step.Variable, step.Destination, step.Source, step.KeyIdentifier, step.UrlVar}
	var refs []string
	for _, n := range names {
		if n != "" {
			refs = append(refs, n)
		}
	}
	// Extract slot names from the Condition field so liveness analysis keeps them
	// alive until after the if/while step that consumes them. Without this, slots
	// written by an immediately preceding step and consumed only via Condition are
	// freed before the gate instruction is compiled, causing the runtime to always
	// take the else branch.
	if step.Condition != "" {
		for _, tok := range strings.Fields(step.Condition) {
			tok = strings.Trim(tok, "()")
			if tok == "&&" || tok == "||" || tok == "" {
				continue
			}
			// Skip comparison operators and their operands (literals / numeric values).
			// We only want bare slot names or header.X / query.Y style refs.
			if strings.ContainsAny(tok, "=<>!\"'") {
				continue
			}
			refs = append(refs, tok)
		}
	}
	// Scan step.Input values for slot names (e.g. model_slot: 'var.model',
	// system_slot: 'var.cls_system', total_slot: 'var.total_tok', etc.).
	// Without this, slots written by earlier steps are freed prematurely and
	// re-allocated to different indices before the consuming step runs.
	// Use substring scan (not just HasPrefix) to catch embedded var. references
	// such as route_llm rules JSON: "[{\"condition\":\"var.complexity == low\"}]"
	for _, v := range step.Input {
		rest := v
		for {
			idx := strings.Index(rest, "var.")
			if idx < 0 {
				break
			}
			rest = rest[idx:]
			end := strings.IndexAny(rest[4:], " \"'\t\n}],=<>!{()")
			if end < 0 {
				refs = append(refs, rest)
				break
			}
			refs = append(refs, rest[:4+end])
			rest = rest[4+end:]
		}
	}
	// Extract params dest_slot / value_slot variable names.
	for _, p := range step.Params {
		if n := p["dest_slot"]; n != "" {
			refs = append(refs, n)
		}
		if n := p["value_slot"]; n != "" {
			refs = append(refs, n)
		}
	}
	return refs
}

// varRefsInFlow collects all variable names referenced anywhere in a flow
// (recursively through branches and called sub-flows).
func (c *Compiler) varRefsInFlow(flow []StepConfig, frags map[string][]StepConfig) []string {
	var refs []string
	visited := make(map[string]bool)
	var walk func([]StepConfig)
	walk = func(steps []StepConfig) {
		for _, s := range steps {
			refs = append(refs, c.varRefsInStep(s)...)
			if len(s.Do) > 0 {
				walk(s.Do)
			}
			for _, ref := range []string{s.Then, s.Else, s.FlowName} {
				if ref != "" && !visited[ref] {
					visited[ref] = true
					if nested, ok := frags[ref]; ok {
						walk(nested)
					}
				}
			}
		}
	}
	walk(flow)
	return refs
}

// releaseDeadSlots frees slots whose last use was at stepIdx.
// Freed slots are added to freeSlots for reuse by getSlot.
func (c *Compiler) releaseDeadSlots(stepIdx int, lastUse map[string]int) {
	for name, last := range lastUse {
		if last == stepIdx {
			// Path param slots must not be recycled: BakeSubRouter (called after
			// bakeFlow) calls getSlot("path.X") and must get the same index that
			// bakeFlow allocated, so the compiled instructions and the sub-router
			// node agree on which ByteSlot the runtime BindPath instruction writes to.
			if strings.HasPrefix(name, "path.") {
				continue
			}
			if slot, ok := c.slotMap[name]; ok {
				c.freeSlots = append(c.freeSlots, slot)
				delete(c.slotMap, name)
			}
		}
	}
}

func (c *Compiler) newReturnStep() engine.Instruction {
	return engine.Instruction{Name: "RET", Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
		if s.StackPtr <= 0 {
			return -1
		}
		s.StackPtr--
		return s.LinkStack[s.StackPtr]
	}}
}

func (c *Compiler) newStopStep() engine.Instruction {
	return engine.Instruction{Name: "STOP", Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 { return -1 }}
}

func (c *Compiler) newInternalJump(target int16) engine.Instruction {
	return engine.Instruction{Name: "GOTO", Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 { return target }}
}

func (c *Compiler) linkBreaks(exitID int16) {
	for i := range c.GlobalTable {
		if c.GlobalTable[i].Name == "BREAK" {
			c.GlobalTable[i] = c.newInternalJump(exitID)
		}
	}
}

type Dependency struct {
	Identifier, Source, Key string
}

func (c *Compiler) Compile(flow []StepConfig) ([]engine.Instruction, error) {
	c.GlobalTable = make([]engine.Instruction, 0)
	c.resetSlots()
	if err := c.bakeFlow(flow, nil); err != nil {
		return nil, err
	}
	return c.GlobalTable, nil
}

func (c *Compiler) ResetLocalScope() {
	c.resetSlots()
}

func (c *Compiler) BakeAPI(api ApiUpdate, fragments map[string][]StepConfig) (int16, error) {
	entryPoint := int16(len(c.GlobalTable))

	sharedFlow := fragments[api.FlowName]
	deps := c.discoverDependencies(sharedFlow)

	for i, dep := range deps {
		slot, err := c.getSlot(dep.Identifier)
		if err != nil {
			return entryPoint, fmt.Errorf("api %q dep %q: %w", api.FlowName, dep.Identifier, err)
		}

		var instr engine.Instruction
		switch dep.Source {
		case "path":
			instr = steps.BindPath(i, slot)
		case "header":
			instr = steps.BindHeader(dep.Key, slot)
		case "query":
			instr = steps.BindQuery(dep.Key, slot)
		}
		c.GlobalTable = append(c.GlobalTable, instr)
	}

	sharedFlowStartID := c.FragmentMap[api.FlowName]
	c.GlobalTable = append(c.GlobalTable, c.newInternalJump(sharedFlowStartID))

	// Mark all preamble instructions (auto-binds + GOTO) as system.
	for j := int(entryPoint); j < len(c.GlobalTable); j++ {
		c.GlobalTable[j].StepIdx = -1
	}

	return entryPoint, nil
}

// GetFlowProfile returns the profile for a compiled flow by name.
// Returns the zero FlowProfile and false if the flow has not been compiled yet.
func (c *Compiler) GetFlowProfile(name string) (FlowProfile, bool) {
	p, ok := c.FlowProfiles[name]
	return p, ok
}
