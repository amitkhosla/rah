package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"rah/internal/config"
	"rah/internal/control"
	"rah/internal/engine"
	"rah/internal/rctx"
	"rah/internal/router"
)

func main() {
	port := flag.Int("port", 8080, "Gateway Port")
	mPort := flag.Int("mport", 8081, "Management Port")
	flag.Parse()

	// 1. Initial Configuration
	cfg := config.GlobalLayout{
		MaxBytesSlots: 32,
		MaxIntsSlots:  16,
		DefaultLimits: config.ResourceLimit{
			MaxBodySize: 1024 * 1024, // 1MB
		},
	}

	// 2. Component Initialization
	r := router.New()
	fm := engine.NewFlowManager(12000, cfg)

	// 3. Register Headers and Setup APIs
	// This maps "Authorization" header to ByteSlots[0] globally
	fm.HeaderRegistry.RegisterHeader("Authorization")
	compiler := control.NewCompiler(fm)
	setupRoutes(r, fm, compiler)

	// 4. The Unified Hot-Path Handler
	go func() {
		handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// A. Router Lookup (Returns uint32)
			apiId := r.Lookup(req.URL.Path)
			if apiId == 0 {
				http.NotFound(w, req)
				return
			}

			// B. Lifecycle: Get Context and Reset with ResponseWriter Interface
			ctx := fm.Pool.Get().(*rctx.Context)
			ctx.Reset(w)

			ctx.ApiId = apiId

			ctx.SnapshotMetadata(req.Method, req.URL.Path, req.URL.RawQuery)

			// C. Delegate Execution to FlowManager
			fm.ProcessRequest(ctx, req)

			// D. Finalize: Flush buffered data or commit status code
			ctx.Finalize()

			if ctx.ShouldReturnToPool() {
				fm.Pool.Put(ctx)
			}
		})

		addr := fmt.Sprintf(":%d", *port)
		log.Printf("Rah Gateway listening on %s\n", addr)
		log.Fatal(http.ListenAndServe(addr, handler))
	}()

	registry := control.NewNameRegistry()
	ms := &control.ManagementServer{
		FlowManager: fm,
		Compiler:    compiler,
		Registry:    registry,
	}

	//Control Plane (Management)
	mux := http.NewServeMux()
	mux.HandleFunc("/sync", ms.UnifiedSyncHandler)
	log.Printf("Management API running on %d", *mPort)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *mPort), mux))
}

func setupRoutes(r *router.RahRouter, fm *engine.FlowManager, compiler *control.Compiler) {
	state := fm.State.Load()
	state.Router = r

	// --- API 1: Public Hello ---
	p1Instructions := []engine.Instruction{
		{
			Name: "HelloStep",
			// ADDED: *engine.ExecutionState parameter
			Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
				ctx.Write([]byte("Welcome to Rah Gateway"))
				// Return PC + 1 to move to the next instruction
				return s.PC + 1
			},
		},
	}

	def1 := engine.BakeDefinition(1, "/v1/hello")
	compiler.BakeSubRouter(def1, "/", "GET", p1Instructions, true)

	state.Definitions[1] = def1
	state.Router.Add(def1.BaseRawPath, 1)

	// --- API 2: Secure User Data with Path Params ---
	p2Instructions := []engine.Instruction{
		{
			Name: "ShowProfile",
			// ADDED: *engine.ExecutionState parameter
			Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
				// In your compiler setup, you assigned nextSlot starting at 10.
				// Ensure the parameter extraction actually maps to this slot.
				userId := ctx.ByteSlots[10]
				ctx.Write([]byte("User Profile for ID: "))
				ctx.Write(userId)
				return s.PC + 1
			},
		},
	}

	// Base path is /v1/user
	def2 := engine.BakeDefinition(2, "/v1/user")
	// Sub-path contains the dynamic segment {id}
	compiler.BakeSubRouter(def2, "/{id}/profile", "GET", p2Instructions, true)

	state.Definitions[2] = def2
	state.Router.Add(def2.BaseRawPath, 2)
}
