package control

import (
	"rah/internal/engine"
	"rah/internal/engine/steps"
	"rah/internal/rctx"
	"regexp"
	"strings"
)

type Compiler struct {
	slotMap     map[string]int
	nextSlot    int
	fm          *engine.FlowManager
	GlobalTable []engine.Instruction
	FragmentMap map[string]int16
	FlowLibrary map[string][]StepConfig
}

func NewCompiler(fm *engine.FlowManager) *Compiler {
	return &Compiler{
		slotMap:     make(map[string]int),
		nextSlot:    10,
		fm:          fm,
		GlobalTable: make([]engine.Instruction, 0, 4096),
		FragmentMap: make(map[string]int16),
	}
}

// BakeAll flattens Fragments and APIs into a single Instruction Table.
func (c *Compiler) BakeAll(cfg GatewayConfig) {
	// 1. Map Fragments (Subflows)
	for name, flow := range cfg.Flows {
		c.FragmentMap[name] = int16(len(c.GlobalTable))
		c.resetSlots()
		c.bakeFlow(flow, cfg.Flows)
		c.GlobalTable = append(c.GlobalTable, c.newReturnStep())
	}

	// 2. Bake APIs
	for _, api := range cfg.Apis {
		// Update the API config entry point
		api.EntryPoint = int16(len(c.GlobalTable))
		c.resetSlots()

		// AUTO-BINDING: Discover what headers/query params this flow needs
		deps := c.discoverDependencies(cfg.Flows[api.FlowName])
		for _, dep := range deps {
			slot := c.getSlot(dep.Identifier)
			c.GlobalTable = append(c.GlobalTable, steps.BindInput(dep.Source, dep.Key, slot))
		}

		c.bakeFlow(cfg.Flows[api.FlowName], cfg.Flows)
		c.GlobalTable = append(c.GlobalTable, c.newStopStep())
	}
}

func (c *Compiler) bakeFlow(flow []StepConfig, fragments map[string][]StepConfig) {
	for _, step := range flow {
		c.compileStep(step, fragments)
	}
}

func (c *Compiler) compileStep(step StepConfig, fragments map[string][]StepConfig) {
	// currentID := int16(len(c.GlobalTable)) // (Unused but kept if needed)

	switch step.Action {
	case "if":
		thenBlock := c.simulateBake(fragments[step.Then], fragments)
		elseBlock := c.simulateBake(fragments[step.Else], fragments)

		thenStartID := int16(len(c.GlobalTable)) + 1
		skipElseID := thenStartID + int16(len(thenBlock))
		elseStartID := skipElseID + 1
		postElseID := elseStartID + int16(len(elseBlock))

		c.GlobalTable = append(c.GlobalTable, steps.NewComplexLogicGate(step.Condition, thenStartID, elseStartID, c.slotMap))
		c.bakeFlow(fragments[step.Then], fragments)
		c.GlobalTable = append(c.GlobalTable, c.newInternalJump(postElseID))
		c.bakeFlow(fragments[step.Else], fragments)

	case "switch":
		slot := c.getSlot(step.As)
		jumpTable := make(map[string]int16)
		dispatcherIdx := len(c.GlobalTable)
		c.GlobalTable = append(c.GlobalTable, engine.Instruction{Name: "SWITCH_DISPATCH"})

		for val, fragName := range step.Cases {
			jumpTable[val] = int16(len(c.GlobalTable))
			c.bakeFlow(fragments[fragName], fragments)
			c.GlobalTable = append(c.GlobalTable, engine.Instruction{Name: "BREAK"})
		}
		exitID := int16(len(c.GlobalTable))
		c.GlobalTable[dispatcherIdx] = steps.SwitchGate(slot, jumpTable, exitID)
		c.linkBreaks(exitID)

	case "http_call":
		urlSlot := -1
		if step.UrlVar != "" {
			urlSlot = c.getSlot(step.UrlVar)
		}
		c.GlobalTable = append(c.GlobalTable, steps.HttpAction(urlSlot, step.URL, step.Timeout, step.RetryCondition, step.MaxRetries, step.Input))
	case "registry_lookup":
		keySlot := c.getSlot(step.KeyIdentifier)
		metaSlot := c.getSlot(step.As)
		// RegistryLookup returns engine.Instruction, so append it directly
		c.GlobalTable = append(c.GlobalTable, steps.RegistryLookup(keySlot, metaSlot, step.Scope))

	case "foreach":
		iterSlot := c.nextSlot
		c.nextSlot++
		valSlot := c.getSlot(step.As)

		gateID := int16(len(c.GlobalTable))
		c.GlobalTable = append(c.GlobalTable, engine.Instruction{Name: "LOOP_GATE_PLACEHOLDER"})

		// 1. Recursive compilation with fragment context
		for _, subStep := range step.Do {
			c.compileStep(subStep, fragments)
		}

		// 2. Append the Repeat Instruction
		c.GlobalTable = append(c.GlobalTable, engine.Instruction{
			Name:   "LOOP_REPEAT",
			Action: steps.LoopRepeat(gateID, iterSlot),
		})

		// 3. Back-fill the Entry Gate
		exitID := int16(len(c.GlobalTable))
		c.GlobalTable[gateID] = engine.Instruction{
			Name:   "LOOP_GATE",
			Action: steps.LoopGate(step.Source, valSlot, iterSlot, gateID+1, exitID),
		}

	case "call":
		// Resolved at compile time via FragmentMap
		if targetID, ok := c.FragmentMap[step.FlowName]; ok {
			c.GlobalTable = append(c.GlobalTable, engine.Instruction{
				Name:   "CALL",
				Action: steps.CallFragment(targetID),
			})
		} else if fragments != nil {
			if called, exists := fragments[step.FlowName]; exists {
				// Fallback for per-flow compilation mode where absolute
				// fragment IDs are not precomputed.
				c.bakeFlow(called, fragments)
			}
		}
	} // End of Switch
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
			// Correctly simulate the inline steps in 'Do'
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
			blob := strings.Join([]string{step.Condition, step.KeyIdentifier, step.UrlVar, step.Source}, " ")
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

func (c *Compiler) CompileExecutable(flow []StepConfig, fragments map[string][]StepConfig) []engine.Instruction {
	c.GlobalTable = make([]engine.Instruction, 0)
	c.resetSlots()

	deps := c.discoverDependenciesWithFragments(flow, fragments)
	for _, dep := range deps {
		slot := c.getSlot(dep.Identifier)
		switch dep.Source {
		case "header":
			c.GlobalTable = append(c.GlobalTable, steps.BindHeader(dep.Key, slot))
		case "query":
			c.GlobalTable = append(c.GlobalTable, steps.BindQuery(dep.Key, slot))
		}
	}

	c.bakeFlow(flow, fragments)
	return c.GlobalTable
}

// Helpers
func (c *Compiler) getSlot(name string) int {
	if idx, ok := c.slotMap[name]; ok {
		return idx
	}
	idx := c.nextSlot
	c.slotMap[name] = idx
	c.nextSlot++
	return idx
}

func (c *Compiler) resetSlots() {
	c.slotMap = make(map[string]int)
	c.nextSlot = 10
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

func (c *Compiler) Compile(flow []StepConfig) []engine.Instruction {
	c.GlobalTable = make([]engine.Instruction, 0) // Reset for fresh build
	c.resetSlots()
	c.bakeFlow(flow, nil)
	return c.GlobalTable
}

func (c *Compiler) ResetLocalScope() {
	c.resetSlots()
}

// internal/control/compiler.go

func (c *Compiler) BakeAPI(api ApiUpdate, fragments map[string][]StepConfig) int16 {
	// 1. The actual entry point is the current end of the GlobalTable
	entryPoint := int16(len(c.GlobalTable))

	// 2. Dependency Discovery
	sharedFlow := fragments[api.FlowName]
	deps := c.discoverDependencies(sharedFlow)

	// 3. Bake Index-Specific Bindings
	// These are unique to THIS API's path/config
	for i, dep := range deps {
		slot := c.getSlot(dep.Identifier) // Identifier like "path.userId"

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

	// 4. Final Jump to the Shared Flow
	// This allows the 2,000 APIs to reuse the same logic block
	sharedFlowStartID := c.FragmentMap[api.FlowName]
	c.GlobalTable = append(c.GlobalTable, c.newInternalJump(sharedFlowStartID))

	return entryPoint
}
