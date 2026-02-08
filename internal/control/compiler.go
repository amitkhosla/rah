package control

import (
	"encoding/json"
	"io"
	"rah/internal/engine"
	"rah/internal/engine/steps"
	"rah/internal/rctx"
	"rah/internal/registry"
	"time"
)

type Compiler struct {
	slotMap  map[string]int
	nextSlot int
	fm       *engine.FlowManager
}

func NewCompiler(fm *engine.FlowManager) *Compiler {
	return &Compiler{
		slotMap:  make(map[string]int),
		nextSlot: 10,
		fm:       fm,
	}
}

func (c *Compiler) getSlot(name string) int {
	if name == "" {
		return 0
	}
	if idx, ok := c.slotMap[name]; ok {
		return idx
	}
	idx := c.nextSlot
	c.slotMap[name] = idx
	c.nextSlot++
	return idx
}

// Compile handles the recursive flattening and jump calculation
func (c *Compiler) Compile(cfg ApiConfig) []engine.Instruction {
	program := make([]engine.Instruction, 0)

	for _, step := range cfg.Flow {
		instrs := c.mapStepToInstructions(step, cfg.Fragments)
		program = append(program, instrs...)
	}
	return program
}

func (c *Compiler) mapStepToInstructions(step StepConfig, fragments map[string][]StepConfig) []engine.Instruction {
	switch step.Action {
	case "extract_header":
		return []engine.Instruction{steps.PrimitiveExtractHeader(step.Key, c.getSlot(step.As))}

	case "proxy":
		return []engine.Instruction{steps.ProxyStep(step.Target)}

	case "call_fragment":
		// Inline fragment logic
		if subSteps, ok := fragments[step.Target]; ok {
			return c.Compile(ApiConfig{Flow: subSteps, Fragments: fragments})
		}
		return nil

	case "extract_query":
		slot := c.getSlot(step.As)
		key := step.Key
		return []engine.Instruction{{
			Name: "Query:" + key,
			Action: func(ctx *rctx.Context) int16 {
				ctx.ByteSlots[slot] = []byte(ctx.Request.URL.Query().Get(key))
				return 1
			},
		}}

	case "read_body":
		slot := c.getSlot(step.As)
		// We capture the max size from fm config during Bake time
		maxSize := c.fm.Config.DefaultLimits.MaxBodySize

		return []engine.Instruction{{
			Name: "ReadBody",
			Action: func(ctx *rctx.Context) int16 {
				// 1. Check if the buffer is already populated
				if len(ctx.RequestBuffer) > 0 {
					ctx.ByteSlots[slot] = ctx.RequestBuffer
					return 1
				}

				// 2. Read from stream into the pre-allocated Context buffer
				// We use a helper to prevent allocations
				body, err := io.ReadAll(io.LimitReader(ctx.Request.Body, int64(maxSize)))
				if err != nil {
					ctx.ResponseStatus = 400
					return engine.StopPlan
				}

				ctx.RequestBuffer = body
				ctx.ByteSlots[slot] = ctx.RequestBuffer
				return 1
			},
		}}
	case "extract_json":
		slot := c.getSlot(step.As)
		path := step.Path   // e.g., "user.profile.id"
		source := step.From // "body" or "upstream_response"
		return []engine.Instruction{{
			Name: "JsonExtract:" + path,
			Action: func(ctx *rctx.Context) int16 {

				// Logic to parse JSON from ctx.ByteSlots or Request Body
				// and save the specific field to ctx.ByteSlots[slot]
				return int16(slot + len(source))
			},
		}}

	case "cache_lookup":
		dataSlot := c.getSlot(step.As) // e.g., "user_data" -> 12
		//boolSlot := c.getBoolSlot(step.Status) // e.g., "is_cached" -> 3

		// We capture SlabMgr from c.fm at Bake time
		slabMgr := c.fm.SlabMgr

		return []engine.Instruction{{
			Name: "Cache:Get:" + step.As,
			Action: func(ctx *rctx.Context) int16 {
				// Triple Validation: Version, Tenant, and Key match
				if data, found := slabMgr.Get(ctx.TenantID, ctx.Path); found {
					ctx.ByteSlots[dataSlot] = data
					//ctx.BoolSlots[boolSlot] = true
					return 1
				}
				// Cache Miss: Reset slot and set status to false
				ctx.ByteSlots[dataSlot] = nil
				//ctx.BoolSlots[boolSlot] = false
				return 1
			},
		}}

	case "cache_store":
		dataSlot := c.getSlot(step.As)
		ttl := step.TTL
		slabMgr := c.fm.SlabMgr

		return []engine.Instruction{{
			Name: "Cache:Put:" + step.As,
			Action: func(ctx *rctx.Context) int16 {
				data := ctx.ByteSlots[dataSlot]
				if len(data) > 0 {
					slabMgr.Put(ctx.TenantID, ctx.Path, data, ttl)
				}
				return 1
			},
		}}

	case "get_from_registry":
		// The key in the registry (e.g., "service-mock" or "api-key")
		key := step.Key
		// The slot to store the result in (e.g., "target_url")
		slot := c.getSlot(step.As)

		return []engine.Instruction{{
			Name: "Registry:Get:" + key,
			Action: func(ctx *rctx.Context) int16 {
				// 1. Get the current active Matrix snapshot
				reg := registry.State.Active.Load()

				// 2. Resolve the Key to a Column ID (kID)
				kID, found := registry.GetKeyID(reg, key)
				if !found {
					return 1 // Key doesn't exist in registry, skip
				}

				// 3. Matrix lookup: (TenantRow * Stride) + Column
				vID := reg.Matrix[(uint32(ctx.TenantID)*reg.Stride)+uint32(kID)]
				if vID != 0 {
					// 4. Place the bytes from the ValuePool into the slot
					ctx.ByteSlots[slot] = reg.ValuePool[vID]
				}
				return 1
			},
		}}
	case "add_timestamp":
		return []engine.Instruction{{
			Name: "Transform:AddTS",
			Action: func(ctx *rctx.Context) int16 {
				if len(ctx.ResponseBuffer) == 0 {
					return 1
				}
				// Simple JSON injection for the mock requirement
				var data map[string]any
				if err := json.Unmarshal(ctx.ResponseBuffer, &data); err == nil {
					data["gateway_ts"] = time.Now().UnixMilli()
					ctx.ResponseBuffer, _ = json.Marshal(data)
				}
				return 1
			},
		}}

	case "call_upstream":
		url := step.URL
		return []engine.Instruction{{
			Name: "Call:" + url,
			Action: func(ctx *rctx.Context) int16 {
				// High-performance http.Client call
				// Results can be stored in a slot for 'extract_json' to use
				return 1
			},
		}}
	case "condition":
		// 1. Compile branches
		thenInstrs := c.Compile(ApiConfig{Flow: fragments[step.Then], Fragments: fragments})
		elseInstrs := c.Compile(ApiConfig{Flow: fragments[step.Else], Fragments: fragments})

		// 2. Logic for Jumps
		// If True: Jump 1 (into Then)
		// If False: Jump len(Then) + 2 (Skip Then block AND the skip-instruction at the end of Then)
		// We add a 'jump' instruction at the end of 'Then' to skip 'Else'

		skipElseIdx := int16(len(elseInstrs) + 1)
		jumpOverElse := engine.Instruction{
			Name:   "Jump",
			Action: func(ctx *rctx.Context) int16 { return skipElseIdx },
		}

		onFalseJump := int16(len(thenInstrs) + 2)

		gate := steps.GenericConditionStep(
			c.getSlot(step.As),
			step.Condition,
			step.Value,
			1,
			onFalseJump,
		)

		// Result: [Gate] -> [ThenBlock] -> [JumpOverElse] -> [ElseBlock]
		result := []engine.Instruction{gate}
		result = append(result, thenInstrs...)
		result = append(result, jumpOverElse)
		result = append(result, elseInstrs...)
		return result

	default:
		// Return a No-Op instead of breaking
		return []engine.Instruction{{
			Name:   "noop",
			Action: func(ctx *rctx.Context) int16 { return 1 },
		}}
	}
}

// ResetLocalScope clears the name-to-slot mapping for a new API Bake session.
func (c *Compiler) ResetLocalScope() {
	for k := range c.slotMap {
		delete(c.slotMap, k)
	}
	c.nextSlot = 10
}
func (c *Compiler) BakeReadBody(step Step) engine.Instruction {
	// If we are on a 2-core machine, the compiler bakes the Synchronous block.
	// This choice happens once during "Bake", making the runtime extremely fast.

	return engine.Instruction{
		Name: "StreamBody",
		Action: func(ctx *rctx.Context) int16 {
			// The compiler captures the specific writers needed for this API
			writers := []io.Writer{ctx.Writer, ctx.Writer}

			err := c.fm.ExecuteMultiWrite(ctx, writers)
			if err != nil {
				ctx.ResponseStatus = 502
				return engine.StopPlan
			}
			return 1
		},
	}
}

// Instruction: Resolve Host to TenantID
func (c *Compiler) ResolveTenant() engine.Instruction {
	return engine.Instruction{
		Name: "Registry:Resolve",
		Action: func(ctx *rctx.Context) int16 {
			reg := registry.State.Active.Load()
			// Use the pre-built Radix lookup on the Host header
			tID, found := registry.GetTenantID(reg, ctx.Request.Host)
			if !found || tID == 0 {
				return -1
			} // 403 Forbidden
			ctx.TenantID = tID
			return 1
		},
	}
}

// Instruction: Load URL into Slot 5 (The Proxy Slot)
func (c *Compiler) LoadUpstream(serviceName string) engine.Instruction {
	key := "url:" + serviceName
	return engine.Instruction{
		Name: "Registry:LoadURL:" + serviceName,
		Action: func(ctx *rctx.Context) int16 {
			reg := registry.State.Active.Load()
			kID, found := registry.GetKeyID(reg, key)
			if !found {
				return 1
			}

			vID := reg.Matrix[(uint32(ctx.TenantID)*reg.Stride)+uint32(kID)]
			if vID != 0 {
				ctx.ByteSlots[5] = reg.ValuePool[vID]
			}
			return 1
		},
	}
}

// Instruction: Setup Dynamic Alias (Runtime Discovery)
func (c *Compiler) DiscoverAlias(headerName string, mgr *registry.RegistryManager) engine.Instruction {
	return engine.Instruction{
		Name: "Registry:Discover",
		Action: func(ctx *rctx.Context) int16 {
			// 1. Get the value from the header (e.g., a session-based hostname)
			val := ctx.Request.Header.Get(headerName)
			if val == "" {
				return 1
			}

			// 2. SAFETY CHECK: Only discover if we have a valid tenant identified
			if ctx.TenantID == 0 {
				return 1
			}

			// 3. Call the MANAGER (mgr), not the Registry State (reg).
			// Note: This triggers a Mutex Lock internally in the Manager.
			mgr.AddAlias(ctx.Request.Host, val)

			return 1
		},
	}
}
