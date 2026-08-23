package control

import (
	"fmt"
	"log"
	"regexp"
	"strconv"

	"github.com/amitkhosla/rah/internal/connectors/document"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// compileDocumentStep handles the "doc_get", "doc_put", "doc_delete", "doc_query", "doc_count", and "doc_execute" step types.
//
// These steps interact with document storage providers (e.g., MongoDB, Firestore, etc.)
// via the DocumentConnectorManager.
//
// Step input keys:
//
//	key             – connector name (required; must exist in DocumentConnectorMgr)
//	value           – collection name (required; bake-time validated against ^[a-zA-Z0-9_]+$)
//	as              – output slot name
//	source          – input slot name (filter/command/payload)
//	path            – static document ID
//	body_var        – slot name for document body (doc_put only)
//	input["id_var"]              – slot name for dynamic ID
//	input["filter_var"]          – slot name for filter bytes
//	input["projection_var"]      – slot name for projection bytes
//	input["sort_var"]            – slot name for sort bytes
//	input["limit"]               – limit as string (doc_query)
//	input["skip"]                – skip as string (doc_query)
//	input["upsert"]              – "true" for upsert mode (doc_put)

func (c *Compiler) compileDocumentGet(step StepConfig) error {
	return c.compileDocumentStep(step, "doc_get")
}

func (c *Compiler) compileDocumentPut(step StepConfig) error {
	return c.compileDocumentStep(step, "doc_put")
}

func (c *Compiler) compileDocumentDelete(step StepConfig) error {
	return c.compileDocumentStep(step, "doc_delete")
}

func (c *Compiler) compileDocumentQuery(step StepConfig) error {
	return c.compileDocumentStep(step, "doc_query")
}

func (c *Compiler) compileDocumentCount(step StepConfig) error {
	return c.compileDocumentStep(step, "doc_count")
}

func (c *Compiler) compileDocumentExecute(step StepConfig) error {
	return c.compileDocumentStep(step, "doc_execute")
}

// compileDocumentStep centralizes compilation logic for all doc_* steps.
func (c *Compiler) compileDocumentStep(step StepConfig, actionType string) error {
	// Validate required fields
	providerName := step.Key
	if providerName == "" {
		return fmt.Errorf("%s: 'key' (connector name) is required", actionType)
	}

	collection := step.Value
	if collection == "" && actionType != "doc_execute" {
		return fmt.Errorf("%s: 'value' (collection name) is required", actionType)
	}

	// Validate collection name format (bake-time only)
	if collection != "" {
		collectionRegex := regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
		if !collectionRegex.MatchString(collection) {
			return fmt.Errorf("%s: invalid collection name %q — must match ^[a-zA-Z0-9_]+$", actionType, collection)
		}
	}

	// Resolve output slot (destination)
	destSlot := -1
	if step.As != "" {
		s, err := c.getSlot(step.As)
		if err != nil {
			return fmt.Errorf("%s: as: %w", actionType, err)
		}
		destSlot = s
	}

	// Common bake-time captures
	mgr := c.DocumentConnectorMgr

	switch actionType {
	case "doc_get":
		return c.compileDocGetImpl(step, mgr, providerName, collection, destSlot)
	case "doc_put":
		return c.compileDocPutImpl(step, mgr, providerName, collection, destSlot)
	case "doc_delete":
		return c.compileDocDeleteImpl(step, mgr, providerName, collection, destSlot)
	case "doc_query":
		return c.compileDocQueryImpl(step, mgr, providerName, collection, destSlot)
	case "doc_count":
		return c.compileDocCountImpl(step, mgr, providerName, collection, destSlot)
	case "doc_execute":
		return c.compileDocExecuteImpl(step, mgr, providerName, destSlot)
	default:
		return fmt.Errorf("unknown document action type: %s", actionType)
	}
}

// compileDocGetImpl compiles a "doc_get" step.
// Retrieves a single document by ID or filter.
func (c *Compiler) compileDocGetImpl(step StepConfig, mgr *LiveDocConnMgr, providerName, collection string, destSlot int) error {
	// Resolve optional slots
	staticID := step.Path
	idSlot := -1
	if step.Input["id_var"] != "" {
		s, err := c.getSlot(step.Input["id_var"])
		if err != nil {
			return fmt.Errorf("doc_get: id_var: %w", err)
		}
		idSlot = s
	}

	filterSlot := -1
	if step.Input["filter_var"] != "" {
		s, err := c.getSlot(step.Input["filter_var"])
		if err != nil {
			return fmt.Errorf("doc_get: filter_var: %w", err)
		}
		filterSlot = s
	}

	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "doc_get",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if mgr == nil || mgr.Load() == nil {
				log.Printf("[doc_get] DocumentConnectorMgr is nil — no document connectors configured")
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			provider, ok := mgr.Get(providerName)
			if !ok {
				log.Printf("[doc_get] connector %q not found", providerName)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			// Determine ID: prefer static, fall back to slot
			id := staticID
			if idSlot >= 0 && idSlot < len(ctx.ByteSlots) {
				id = string(ctx.ByteSlots[idSlot])
			}

			// Determine filter: from slot if configured
			var filter []byte
			if filterSlot >= 0 && filterSlot < len(ctx.ByteSlots) {
				filter = ctx.ByteSlots[filterSlot]
			}

			req := document.GetRequest{
				Collection: collection,
				ID:         id,
				Filter:     filter,
			}

			result, err := provider.Get(ctx.Request.Context(), req)
			if err != nil {
				log.Printf("[doc_get] collection %q id %q error: %v", collection, id, err)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			if destSlot >= 0 && destSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[destSlot] = ctx.Alloc(len(result))
				copy(ctx.ByteSlots[destSlot], result)
			}

			return state.PC + 1
		},
	})

	return nil
}

// compileDocPutImpl compiles a "doc_put" step.
// Inserts or replaces a document.
func (c *Compiler) compileDocPutImpl(step StepConfig, mgr *LiveDocConnMgr, providerName, collection string, destSlot int) error {
	// Resolve optional slots
	staticID := step.Path
	idSlot := -1
	if step.Input["id_var"] != "" {
		s, err := c.getSlot(step.Input["id_var"])
		if err != nil {
			return fmt.Errorf("doc_put: id_var: %w", err)
		}
		idSlot = s
	}

	docSlot := -1
	bodyVar := step.BodyVar
	if bodyVar == "" {
		bodyVar = step.Variable
	}
	if bodyVar != "" {
		s, err := c.getSlot(bodyVar)
		if err != nil {
			return fmt.Errorf("doc_put: body_var: %w", err)
		}
		docSlot = s
	}

	upsert := step.Input["upsert"] == "true"

	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "doc_put",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if mgr == nil || mgr.Load() == nil {
				log.Printf("[doc_put] DocumentConnectorMgr is nil — no document connectors configured")
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			provider, ok := mgr.Get(providerName)
			if !ok {
				log.Printf("[doc_put] connector %q not found", providerName)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			// Determine ID: prefer static, fall back to slot
			id := staticID
			if idSlot >= 0 && idSlot < len(ctx.ByteSlots) {
				id = string(ctx.ByteSlots[idSlot])
			}

			// Determine document: from slot
			var doc []byte
			if docSlot >= 0 && docSlot < len(ctx.ByteSlots) {
				doc = ctx.ByteSlots[docSlot]
			}

			req := document.PutRequest{
				Collection: collection,
				Document:   doc,
				ID:         id,
				Upsert:     upsert,
			}

			if err := provider.Put(ctx.Request.Context(), req); err != nil {
				log.Printf("[doc_put] collection %q id %q error: %v", collection, id, err)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			return state.PC + 1
		},
	})

	return nil
}

// compileDocDeleteImpl compiles a "doc_delete" step.
// Deletes documents matching a filter.
func (c *Compiler) compileDocDeleteImpl(step StepConfig, mgr *LiveDocConnMgr, providerName, collection string, destSlot int) error {
	// Resolve filter slot (required for delete)
	filterSlot := -1
	if step.Source != "" {
		s, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("doc_delete: source: %w", err)
		}
		filterSlot = s
	}

	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "doc_delete",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if mgr == nil || mgr.Load() == nil {
				log.Printf("[doc_delete] DocumentConnectorMgr is nil — no document connectors configured")
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			provider, ok := mgr.Get(providerName)
			if !ok {
				log.Printf("[doc_delete] connector %q not found", providerName)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			// Determine filter: from slot
			var filter []byte
			if filterSlot >= 0 && filterSlot < len(ctx.ByteSlots) {
				filter = ctx.ByteSlots[filterSlot]
			}

			req := document.DeleteRequest{
				Collection: collection,
				Filter:     filter,
			}

			if err := provider.Delete(ctx.Request.Context(), req); err != nil {
				log.Printf("[doc_delete] collection %q error: %v", collection, err)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			return state.PC + 1
		},
	})

	return nil
}

// compileDocQueryImpl compiles a "doc_query" step.
// Queries documents with filtering, projection, sorting, limit, and skip.
func (c *Compiler) compileDocQueryImpl(step StepConfig, mgr *LiveDocConnMgr, providerName, collection string, destSlot int) error {
	// Resolve optional slots
	filterSlot := -1
	if step.Input["filter_var"] != "" {
		s, err := c.getSlot(step.Input["filter_var"])
		if err != nil {
			return fmt.Errorf("doc_query: filter_var: %w", err)
		}
		filterSlot = s
	}

	projSlot := -1
	if step.Input["projection_var"] != "" {
		s, err := c.getSlot(step.Input["projection_var"])
		if err != nil {
			return fmt.Errorf("doc_query: projection_var: %w", err)
		}
		projSlot = s
	}

	sortSlot := -1
	if step.Input["sort_var"] != "" {
		s, err := c.getSlot(step.Input["sort_var"])
		if err != nil {
			return fmt.Errorf("doc_query: sort_var: %w", err)
		}
		sortSlot = s
	}

	// Parse limit and skip
	limit := 0
	if step.Input["limit"] != "" {
		n, err := strconv.Atoi(step.Input["limit"])
		if err != nil {
			return fmt.Errorf("doc_query: invalid limit %q: %w", step.Input["limit"], err)
		}
		limit = n
	}

	skip := 0
	if step.Input["skip"] != "" {
		n, err := strconv.Atoi(step.Input["skip"])
		if err != nil {
			return fmt.Errorf("doc_query: invalid skip %q: %w", step.Input["skip"], err)
		}
		skip = n
	}

	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "doc_query",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if mgr == nil || mgr.Load() == nil {
				log.Printf("[doc_query] DocumentConnectorMgr is nil — no document connectors configured")
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			provider, ok := mgr.Get(providerName)
			if !ok {
				log.Printf("[doc_query] connector %q not found", providerName)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			// Build filter, projection, sort from slots
			var filter, projection, sort []byte
			if filterSlot >= 0 && filterSlot < len(ctx.ByteSlots) {
				filter = ctx.ByteSlots[filterSlot]
			}
			if projSlot >= 0 && projSlot < len(ctx.ByteSlots) {
				projection = ctx.ByteSlots[projSlot]
			}
			if sortSlot >= 0 && sortSlot < len(ctx.ByteSlots) {
				sort = ctx.ByteSlots[sortSlot]
			}

			req := document.QueryRequest{
				Collection: collection,
				Filter:     filter,
				Projection: projection,
				Sort:       sort,
				Limit:      limit,
				Skip:       skip,
			}

			result, err := provider.Query(ctx.Request.Context(), req)
			if err != nil {
				log.Printf("[doc_query] collection %q error: %v", collection, err)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			if destSlot >= 0 && destSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[destSlot] = ctx.Alloc(len(result))
				copy(ctx.ByteSlots[destSlot], result)
			}

			return state.PC + 1
		},
	})

	return nil
}

// compileDocCountImpl compiles a "doc_count" step.
// Counts documents matching a query.
func (c *Compiler) compileDocCountImpl(step StepConfig, mgr *LiveDocConnMgr, providerName, collection string, destSlot int) error {
	// Resolve optional slots (same as doc_query)
	filterSlot := -1
	if step.Input["filter_var"] != "" {
		s, err := c.getSlot(step.Input["filter_var"])
		if err != nil {
			return fmt.Errorf("doc_count: filter_var: %w", err)
		}
		filterSlot = s
	}

	projSlot := -1
	if step.Input["projection_var"] != "" {
		s, err := c.getSlot(step.Input["projection_var"])
		if err != nil {
			return fmt.Errorf("doc_count: projection_var: %w", err)
		}
		projSlot = s
	}

	sortSlot := -1
	if step.Input["sort_var"] != "" {
		s, err := c.getSlot(step.Input["sort_var"])
		if err != nil {
			return fmt.Errorf("doc_count: sort_var: %w", err)
		}
		sortSlot = s
	}

	limit := 0
	if step.Input["limit"] != "" {
		n, err := strconv.Atoi(step.Input["limit"])
		if err != nil {
			return fmt.Errorf("doc_count: invalid limit %q: %w", step.Input["limit"], err)
		}
		limit = n
	}

	skip := 0
	if step.Input["skip"] != "" {
		n, err := strconv.Atoi(step.Input["skip"])
		if err != nil {
			return fmt.Errorf("doc_count: invalid skip %q: %w", step.Input["skip"], err)
		}
		skip = n
	}

	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "doc_count",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if mgr == nil || mgr.Load() == nil {
				log.Printf("[doc_count] DocumentConnectorMgr is nil — no document connectors configured")
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			provider, ok := mgr.Get(providerName)
			if !ok {
				log.Printf("[doc_count] connector %q not found", providerName)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			// Build filter, projection, sort from slots
			var filter, projection, sort []byte
			if filterSlot >= 0 && filterSlot < len(ctx.ByteSlots) {
				filter = ctx.ByteSlots[filterSlot]
			}
			if projSlot >= 0 && projSlot < len(ctx.ByteSlots) {
				projection = ctx.ByteSlots[projSlot]
			}
			if sortSlot >= 0 && sortSlot < len(ctx.ByteSlots) {
				sort = ctx.ByteSlots[sortSlot]
			}

			req := document.QueryRequest{
				Collection: collection,
				Filter:     filter,
				Projection: projection,
				Sort:       sort,
				Limit:      limit,
				Skip:       skip,
			}

			count, err := provider.Count(ctx.Request.Context(), req)
			if err != nil {
				log.Printf("[doc_count] collection %q error: %v", collection, err)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			// Convert count to string bytes
			countStr := strconv.FormatInt(count, 10)
			if destSlot >= 0 && destSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[destSlot] = ctx.Alloc(len(countStr))
				copy(ctx.ByteSlots[destSlot], []byte(countStr))
			}

			return state.PC + 1
		},
	})

	return nil
}

// compileDocExecuteImpl compiles a "doc_execute" step.
// Executes a raw database command (admin-only).
func (c *Compiler) compileDocExecuteImpl(step StepConfig, mgr *LiveDocConnMgr, providerName string, destSlot int) error {
	// Log a warning about raw command passthrough
	log.Printf("[doc_execute] WARNING: flow uses raw command passthrough on connector %q — admin-only", providerName)

	// Resolve command slot
	cmdSlot := -1
	if step.Source != "" {
		s, err := c.getSlot(step.Source)
		if err != nil {
			return fmt.Errorf("doc_execute: source: %w", err)
		}
		cmdSlot = s
	}

	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "doc_execute",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if mgr == nil || mgr.Load() == nil {
				log.Printf("[doc_execute] DocumentConnectorMgr is nil — no document connectors configured")
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			provider, ok := mgr.Get(providerName)
			if !ok {
				log.Printf("[doc_execute] connector %q not found", providerName)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			// Determine command: from slot
			var command []byte
			if cmdSlot >= 0 && cmdSlot < len(ctx.ByteSlots) {
				command = ctx.ByteSlots[cmdSlot]
			}

			req := document.ExecuteRequest{
				Command: command,
			}

			result, err := provider.Execute(ctx.Request.Context(), req)
			if err != nil {
				log.Printf("[doc_execute] error: %v", err)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}

			if destSlot >= 0 && destSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[destSlot] = ctx.Alloc(len(result))
				copy(ctx.ByteSlots[destSlot], result)
			}

			return state.PC + 1
		},
	})

	return nil
}
