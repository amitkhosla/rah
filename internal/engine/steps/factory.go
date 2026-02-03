package steps

import (
	"rah/internal/engine"
	"sync"
)

// CreateParallelStep wraps multiple sub-flows into concurrent goroutines.
func CreateParallelStep(reg *engine.FlowRegistry, flowNames []string) func(*engine.RequestContext) int16 {
	return func(ctx *engine.RequestContext) int16 {
		var wg sync.WaitGroup
		wg.Add(len(flowNames))

		for _, name := range flowNames {
			go func(n string) {
				defer wg.Done()
				reg.Call(n, ctx)
			}(name)
		}

		wg.Wait()
		return 1 // Move to next sequential instruction
	}
}

// CreateSwitchStep implements a jump table based on a slot value.
func CreateSwitchStep(slotIdx int, jumpTable map[any]int16) func(*engine.RequestContext) int16 {
	return func(ctx *engine.RequestContext) int16 {
		val := ctx.Slots[slotIdx]
		if offset, ok := jumpTable[val]; ok {
			return offset
		}
		return 1 // Default to next step
	}
}
