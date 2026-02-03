package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"rah/internal/api"
	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/rctx"
	"rah/internal/router"
)

func main() {
	port := flag.Int("port", 8080, "Port to start Rah Gateway")
	flag.Parse()

	// 1. Initial Configuration
	cfg := config.GlobalLayout{
		MaxBytes: 32,
		MaxInts:  16,
		DefaultLimits: config.ResourceLimit{
			MaxBodySize: 1024 * 1024, // 1MB
		},
	}

	// 2. Component Initialization
	r := router.New()
	fm := engine.NewFlowManager(1000, cfg)

	// 3. Register Headers and Setup APIs
	// This maps "Authorization" header to ByteSlots[0] globally
	fm.HeaderRegistry.RegisterHeader("Authorization")

	setupRoutes(r, fm)

	// 4. The Unified Hot-Path Handler
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
		defer fm.Pool.Put(ctx)

		ctx.ApiId = apiId

		// C. Delegate Execution to FlowManager
		fm.ProcessRequest(ctx, req)

		// D. Finalize: Flush buffered data or commit status code
		ctx.Finalize()
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Rah Gateway listening on %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, handler))
}

func setupRoutes(r *router.RahRouter, fm *engine.FlowManager) {
	// API: Public Hello
	p1 := &engine.Plan{
		Instructions: []engine.Instruction{
			{Name: "Hello", Action: func(ctx *rctx.Context) int16 {
				ctx.Write([]byte("Welcome to Rah Gateway"))
				return 1
			}},
		},
	}
	fm.Definitions[1] = api.BakeDefinition(1, "/v1/hello", p1)
	r.Add("/v1/hello", 1)

	// API: Secure User Data with Path Param
	// Route: /v1/user/:id/profile
	p2 := &engine.Plan{
		Instructions: []engine.Instruction{
			{Name: "AuthCheck", Action: func(ctx *rctx.Context) int16 {
				// Slot 0 was registered to "Authorization" in main()
				if len(ctx.ByteSlots[0]) == 0 {
					ctx.ResponseStatus = 401
					ctx.Write([]byte("Unauthorized"))
					return 99 // Stop plan
				}
				return 1
			}},
			{Name: "ShowUser", Action: func(ctx *rctx.Context) int16 {
				// BakeDefinition calculates that :id is Slot 0
				// (Because it is the first param in the path)
				userId := ctx.ByteSlots[0]
				ctx.Write([]byte("User Profile for ID: "))
				ctx.Write(userId)
				return 1
			}},
		},
	}
	// Note: We use the prefix for the router, but full path for baking offsets
	fm.Definitions[2] = api.BakeDefinition(2, "/v1/user/:id/profile", p2)
	r.Add("/v1/user", 2)
}
