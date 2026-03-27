package control

import (
	"fmt"
	"rah/internal/engine"
	"rah/internal/engine/steps"
	"rah/internal/rctx"
	registrypkg "rah/internal/registry"
	"regexp"
	"strconv"
	"strings"
)

type Compiler struct {
	slotMap   map[string]int
	freeSlots []int // slots freed by liveness analysis, available for reuse
	nextSlot  int
	fm        *engine.FlowManager
	RegMgr      *registrypkg.RegistryManager // optional; enables KeyID pre-resolution at bake time
	SecretsMgr  steps.SecretLoader           // optional; enables load_secret steps
	CredMgr     steps.CredentialLookup       // optional; enables load_credential steps
	CacheMgr    steps.CacheStore             // optional; enables cache_get/cache_put steps
	GlobalTable []engine.Instruction
	FragmentMap map[string]int16
	FlowLibrary map[string][]StepConfig
}

func NewCompiler(fm *engine.FlowManager) *Compiler {
	return &Compiler{
		slotMap:     make(map[string]int),
		nextSlot:    0,
		fm:          fm,
		GlobalTable: make([]engine.Instruction, 0, 4096),
		FragmentMap: make(map[string]int16),
	}
}

// BakeAll flattens Fragments and APIs into a single Instruction Table.
func (c *Compiler) BakeAll(cfg GatewayConfig) error {
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
		api.EntryPoint = int16(len(c.GlobalTable))
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
		c.GlobalTable[gateID] = engine.Instruction{
			Name:   "LOOP_GATE",
			Action: steps.LoopGate(step.Source, valSlot, iterSlot, gateID+1, exitID),
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

	default:
		return fmt.Errorf("unknown step action %q", step.Action)
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

func (c *Compiler) resetSlots() {
	c.slotMap = make(map[string]int)
	c.freeSlots = c.freeSlots[:0]
	c.nextSlot = 0
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
