package engine

type PathParamMapping struct {
	SlotIndex int
	Name      string
}

type Plan struct {
	ApiId            uint32
	BaseUpstreamURL  string
	PathParamMapping []PathParamMapping
	Instructions     []Instruction
	MaxByteSlots     int
	MaxIntSlots      int
}

// CompilePlan transforms a raw API definition into a high-performance Plan
func CompilePlan(apiID uint32, route string, upstream string, flow []Instruction) *Plan {
	plan := &Plan{
		ApiId:           apiID,
		BaseUpstreamURL: upstream,
		Instructions:    flow,
	}

	// 1. Analyze route for parameters (e.g., /user/:id)
	// We determine which slot each parameter will live in.
	// For simplicity, let's say :id goes to ByteSlot[0]
	// In a real system, you'd check which slots are already used by headers.

	return plan
}
