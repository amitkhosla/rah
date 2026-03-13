package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"rah/internal/studio"
	"strconv"
)

func main() {
	defaultPort := envInt("RAH_STUDIO_PORT", 8092)
	defaultGateway := os.Getenv("RAH_GATEWAY_MANAGEMENT_URL")
	if defaultGateway == "" {
		defaultGateway = "http://127.0.0.1:8081"
	}
	defaultTargetsFile := os.Getenv("RAH_STUDIO_TARGETS_FILE")
	defaultStoreKind := os.Getenv("RAH_STUDIO_STORE_KIND")
	if defaultStoreKind == "" {
		defaultStoreKind = "memory"
	}
	defaultStorePath := os.Getenv("RAH_STUDIO_STORE_PATH")

	port := flag.Int("port", defaultPort, "Studio UI Port")
	gatewayManagementURL := flag.String("gateway-management-url", defaultGateway, "Gateway management base URL")
	targetsFile := flag.String("targets-file", defaultTargetsFile, "Path to JSON file with deployment targets")
	storeKind := flag.String("store-kind", defaultStoreKind, "Release store kind: memory|file")
	storePath := flag.String("store-path", defaultStorePath, "Release store path when kind=file")
	flag.Parse()

	cfg := studio.ServerConfig{StoreKind: *storeKind, StorePath: *storePath}
	if *targetsFile != "" {
		bytes, err := os.ReadFile(*targetsFile)
		if err != nil {
			log.Fatalf("failed to read targets file: %v", err)
		}
		if err := json.Unmarshal(bytes, &cfg); err != nil {
			log.Fatalf("failed to parse targets file: %v", err)
		}
		if cfg.StoreKind == "" {
			cfg.StoreKind = *storeKind
		}
		if cfg.StorePath == "" {
			cfg.StorePath = *storePath
		}
	}

	srv, err := studio.NewServer(*gatewayManagementURL, cfg)
	if err != nil {
		log.Fatalf("invalid studio config: %v", err)
	}

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("RAH Studio listening on %s (gateway management: %s, targets_file: %s, store=%s, store_path=%s)", addr, *gatewayManagementURL, *targetsFile, cfg.StoreKind, cfg.StorePath)
	log.Fatal(http.ListenAndServe(addr, srv.Handler()))
}

func envInt(name string, fallback int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
