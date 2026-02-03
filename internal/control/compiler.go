package control

import (
	"rah/internal/engine"
	"rah/internal/engine/steps" // Use the steps package for implementation
	"rah/internal/rctx"
)

type Compiler struct {
	slotMap  map[string]int
	nextSlot int
}

func NewCompiler() *Compiler {
	return &Compiler{
		slotMap:  make(map[string]int),
		nextSlot: 0,
	}
}

func (c *Compiler) getSlot(name string) int {
	if idx, ok := c.slotMap[name]; ok {
		return idx
	}
	idx := c.nextSlot
	c.slotMap[name] = idx
	c.nextSlot++
	return idx
}

func (c *Compiler) Compile(jsonSteps []StepConfig) []engine.Instruction {
	var instructions []engine.Instruction

	for _, step := range jsonSteps {
		switch step.Action {
		case "extract_header":
			slot := c.getSlot(step.As)
			// Corrected: point to steps package
			instructions = append(instructions, steps.PrimitiveExtractHeader(step.Key, slot))

		case "proxy":
			// Corrected: point to steps package
			instructions = append(instructions, steps.ProxyStep(step.Target))

		case "add_response_header":
			// Corrected: point to steps package
			instructions = append(instructions, steps.PrimitiveAddResponseHeader(step.Key, step.Value))

		case "condition":
			slot := c.getSlot(step.As)
			// Note: We use the logic from steps.ConditionStep
			instructions = append(instructions, steps.ConditionStep(func(ctx *rctx.Context) bool {
				val, ok := ctx.Slots[slot].(string)
				return ok && len(val) > 50
			}, 1, 2))
		}
	}
	return instructions
}
