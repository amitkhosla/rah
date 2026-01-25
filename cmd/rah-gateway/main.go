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

	r.Add("/v1/common", uint32(1))
	r.Add("/v1/common/person", uint32(2))
	r.Add("/v1/orders", uint32(3))
	r.Add("/v1/a", uint32(4))
	r.Add("/v2/b", uint32(5)) // this WILL misbehave

	// ---- single hot-path handler ----
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		apiName := r.Lookup(req.URL.Path)

		if apiName == 0 {
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
