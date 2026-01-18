package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"rah/internal/router"
)

func main() {
	port := flag.Int("port", 8080, "Port to start Rah Gateway on")
	flag.Parse()

	// ---- build router snapshot (cold path) ----
	r := router.New()

	r.Add("/v1/common", "common-root")
	r.Add("/v1/common/person", "common-person")
	r.Add("/v1/orders", "orders-api")

	// ---- single hot-path handler ----
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		apiName := r.Lookup(req.URL.Path)

		if apiName == "" {
			http.NotFound(w, req)
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(
			w,
			"Matched API: %s\nRequest Path: %s\n",
			apiName,
			req.URL.Path,
		)
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Rah Gateway listening on %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, handler))
}
