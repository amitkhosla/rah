package control

import (
	"fmt"
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
	GlobalTable []engine.Instruction
	FragmentMap map[string]int16
	FlowLibrary map[string][]StepConfig
	// pendingJumps tracks on_error:jump: wrappers that referenced a not-yet-compiled
	// flow. Resolved in a second pass after all flows are compiled.
	pendingJumps []pendingJump
}

func NewCompiler(fm *engine.FlowManager) *Compiler {
	return &Compiler{
		slotMap:      make(map[string]int),
		nextSlot:     0,
		fm:           fm,
		GlobalTable:  make([]engine.Instruction, 0, 4096),
		FragmentMap:  make(map[string]int16),
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
		c.GlobalTable = append(c.GlobalTable, c.newReturnStep())
	}

	// 2. Bake APIs
	for _, api := range cfg.Apis {
		c.resetSlots()

		// AUTO-BINDING: Discover what headers/query params this flow needs
		deps := c.discoverDependencies(cfg.Flows[api.FlowName])
		for _, dep := range deps {
			slot, err := c.getSlot(dep.Identifier)
			if err != nil {
				return fmt.Errorf("api %q auto-bind: %w", api.FlowName, err)
			}
			c.GlobalTable = append(c.GlobalTable, steps.BindInput(dep.Source, dep.Key, slot))
		}

		if err := c.bakeFlow(cfg.Flows[api.FlowName], cfg.Flows); err != nil {
			return fmt.Errorf("api %q: %w", api.FlowName, err)
		}
		c.GlobalTable = append(c.GlobalTable, c.newStopStep())
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
		if err := c.compileStep(step, fragments); err != nil {
			return err
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

	case "check_rate_limit":
		// Opt-in rate limit enforcement. Must be placed explicitly in the flow.
		// Resolves the limit via ResolveRateLimit and enforces a fixed-window counter.
		// Returns 403 if the tenant is blocked; 429 if the rate is exceeded.
		// Optional quota group map in step.Input: {"1": "free_rl", "2": "pro_rl"}
		// Keys are group IDs (uint8), values are rate limit config names.
		var quotaGroupRLIds []uint16
		if len(step.Input) > 0 && c.RegMgr != nil {
			quotaGroupRLIds = c.compileQuotaGroupMap(step.Input)
		}
		c.GlobalTable = append(c.GlobalTable, steps.CheckRateLimit(c.fm.RateLimitStore, quotaGroupRLIds))

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

	case "bind_client_ip":
		// Extracts the real client IP (X-Forwarded-For → X-Real-IP → RemoteAddr)
		// and stores it in the named slot.
		destSlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.BindClientIP(destSlot))

	case "token_validation":
		tokenSlot, err := c.getSlot(step.KeyIdentifier)
		if err != nil {
			return err
		}
		cfg := steps.ParseTokenValidationConfig(step.KeyIdentifier, step.Input)
		c.GlobalTable = append(c.GlobalTable, steps.TokenValidation(tokenSlot, cfg))

	case "foreach":
		if c.nextSlot >= rctx.BaseByteSlots {
			return fmt.Errorf("slot limit exceeded at foreach iterator: max %d", rctx.BaseByteSlots)
		}
		iterSlot := c.nextSlot
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
				Action: steps.LoopGateSlot(srcSlot, valSlot, iterSlot, gateID+1, exitID),
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

		condSlot, condErr := c.getBoolSlot(step.Source)
		if condErr != nil {
			return fmt.Errorf("while: condition slot %q: %w", step.Source, condErr)
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
			Action: steps.WhileGate(condSlot, iterSlot, maxIter, whileGateID+1, whileExitID),
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
		src, err := c.getSlot(step.Source)
		if err != nil {
			return err
		}
		c.GlobalTable = append(c.GlobalTable, steps.SetResponseHeaderFromSlot(step.Key, src))

	case "echo_request":
		c.GlobalTable = append(c.GlobalTable, steps.EchoRequestStep())

	case "set_response_body":
		src, err := c.getSlot(step.Source)
		if err != nil {
			return err
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

	return nil
}

// simulateBake calculates the number of instructions a flow would generate
func (c *Compiler) simulateBake(flow []StepConfig, frags map[string][]StepConfig) []bool {
	count := 0
	for _, step := range flow {
		switch step.Action {
		case "if":
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

// isControlFlowAction returns true for step actions that must not be wrapped
// with on_error handlers because they control their own execution flow.
func isControlFlowAction(action string) bool {
	switch action {
	case "if", "switch", "call", "return", "fail", "capture_error":
		return true
	}
	return false
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
		// Join-point rule: variables used inside branches must survive until after the if step.
		if step.Action == "if" {
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

	return entryPoint, nil
}
