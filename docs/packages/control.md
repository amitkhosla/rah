# Control Package

## Purpose
Implements the **control plane** - the compiler and configuration management system. Transforms high-level flow definitions and API configurations into flattened instruction tables that the execution engine can run.

## Files
- **compiler.go**: Compiler that flattens flows into GlobalTable via BakeAll()
- **config.go**: Control plane configuration structures
- **cache_manager.go**: Cache management in control plane
- **cache_blocks.go**: Cache region allocation blocks
- Multiple feature-specific files (token validation, cache API, tenant setup, sync flow)
- Test files with comprehensive coverage

## Key Types
- **Compiler**: Transforms GatewayConfig into flattened GlobalTable with FragmentMap and SlotMap
- **GatewayConfig**: Top-level configuration with APIs, Flows (fragments), bindings
- **StepConfig**: Single step in a flow (action, condition, parameters)
- **ApiConfig**: API definition with FlowName and EntryPoint
- **FragmentMap**: Maps fragment names to starting instruction indices

## Responsibilities
1. **Configuration Compilation**: BakeAll() flattens Flows and APIs into single instruction table
2. **Slot Allocation**: Manages variable slots (ByteSlots, IntSlots, BoolSlots) per request
3. **Dependency Discovery**: Auto-binds headers/query params needed by flow
4. **Fragment Management**: Handles subflows with absolute jump addresses
5. **Control Flow Compilation**: Compiles if/switch/loop logic to instruction jumps
6. **Instruction Generation**: Creates step-specific instructions (HTTPCall, CacheRead, etc.)

## Dependencies
- **engine**: Uses engine.Instruction, engine.FlowManager
- **engine/steps**: Provides step builders (BindInput, HTTPCall, etc.)
- **datastore**: May read flow/API definitions from store
- **config**: Provides configuration types
- **observability**: May record compilation metrics
- **clock** (ANTIPATTERN): Used during compilation for initialization
  - See [docs/packages/clock.md#anti-pattern-background-goroutine-for-clock-updates](clock.md#anti-pattern-background-goroutine-for-clock-updates)
  - Refactor to remove clock dependency entirely

## Compilation Flow
```
GatewayConfig {
  Flows: {
    "ValidateToken": [...steps],
    "FetchUser": [...steps]
  },
  Apis: [
    { Path: "/user/:id", FlowName: "FetchUser", ... }
  ]
}
      ↓
Compiler.BakeAll()
      ↓
For each Flow in Flows:
  - Assign starting PC (FragmentMap[name])
  - Allocate slots for variables
  - Compile each step
  - Add return instruction
      ↓
For each API in Apis:
  - Discover dependencies (headers/query params)
  - Auto-bind them as first instructions
  - Assign entry point (API.EntryPoint)
  - Compile flow steps
  - Add stop instruction
      ↓
GlobalTable: flat array of Instructions
```

## Step Compilation
- **if**: Conditions → complex logic gates, absolute jump addresses for then/else
- **switch**: Cases → jump table, dispatcher, break points
- **http_call**: URL slot setup, upstream call instruction
- **cache_read/write**: Cache operations with slot management
- **store_internal_tx_id**: Formats `ctx.InternalTxID` into a slot for header injection
- **bind_correlation_id**: Reads incoming correlation header or generates a new TX ID

## Slot Allocation
`getSlot(name string) (int, error)` allocates a slot index starting at 0.

```go
// compiler.go
func (c *Compiler) getSlot(name string) (int, error) {
    if idx, ok := c.slotMap[name]; ok {
        return idx, nil  // reuse existing mapping
    }
    if c.nextSlot >= rctx.BaseByteSlots {
        return -1, fmt.Errorf("slot limit exceeded: max %d", rctx.BaseByteSlots)
    }
    idx := c.nextSlot
    c.slotMap[name] = idx
    c.nextSlot++
    return idx, nil
}
```

Key properties:
- **Starts at 0**: No reserved slots — full 48-slot capacity available to flows
- **Error on cap**: Returns an error if a flow exceeds `BaseByteSlots = 48`; `BakeAll` propagates this as a compile error before the flow goes live
- **Reset per flow**: `resetSlots()` resets `nextSlot = 0` between fragments/APIs so each flow gets a fresh namespace

## Performance Notes
- **Single-pass compilation**: No multi-pass optimization (keeps simple)
- **Flattened table**: Eliminates nested function calls, uses jumps
- **Slot reuse**: Slots reset per API/flow to minimize memory; starts at 0
- **Auto-binding**: Dependency discovery avoids manual configuration
- **Compile-time cap**: Flows exceeding 48 slots fail at bake time, not at runtime
- **Zero runtime interpretation**: Instructions pre-compiled at startup

## Key Files
- **compiler.go**: Core compilation logic
- **CACHE_API.md**: Cache control plane API specification
- **TENANT_SETUP_API.md**: Tenant initialization API
- **TOKEN_VALIDATION.md**: Token validation flow
- **UNIFIED_SYNC_FLOW.md**: Data synchronization flow
