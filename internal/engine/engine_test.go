package engine

import (
	"sync"
	"testing"
)

func ParallelStep(reg *FlowRegistry, flowNames []string) func(*RequestContext) int16 {
	return func(ctx *RequestContext) int16 {
		var wg sync.WaitGroup
		wg.Add(len(flowNames))
		for _, name := range flowNames {
			go func(n string) {
				defer wg.Done()
				reg.Call(n, ctx)
			}(name)
		}
		wg.Wait()
		return 1
	}
}

func TestGatewayArchitecture(t *testing.T) {
	reg := NewRegistry()

	// 1. Static Inbuilt Flow (Native Go Speed)
	reg.staticFlows["AuthNative"] = func(ctx *RequestContext) int16 {
		ctx.Slots[0] = true // Auth Success
		return 1
	}

	// 2. Dynamic Flow (Runtime Configurable)
	reg.dynamicFlows["LogAnalytics"] = []Instruction{
		{Name: "WriteToLog", Action: func(ctx *RequestContext) int16 {
			ctx.Slots[1] = "SUCCESS"
			return 1
		}},
	}

	// 3. The Main Execution Plan
	plan := []Instruction{
		{Name: "RunAuth", Action: reg.staticFlows["AuthNative"]},
		{
			Name:   "ParallelTasks",
			Action: ParallelStep(reg, []string{"LogAnalytics"}),
		},
	}

	// 4. Execution
	ctx := &RequestContext{}
	Run(plan, ctx)

	// 5. Verification
	if ctx.Slots[0] != true {
		t.Errorf("Expected Slot 0 to be true (AuthNative failed)")
	}
	if ctx.Slots[1] != "SUCCESS" {
		t.Errorf("Expected Slot 1 to be SUCCESS (Dynamic LogAnalytics failed)")
	}
}
