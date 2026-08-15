##
## RAH Gateway — Makefile
## Common dev tasks. Requires Go 1.21+ and Docker.
##

BINARY_GATEWAY  := bin/rah-gateway
BINARY_STUDIO   := bin/rah-studio
BINARY_SYNC     := bin/rah-sync
CMD_GATEWAY     := ./cmd/rah-gateway
CMD_STUDIO      := ./cmd/rah-studio
CMD_SYNC        := ./cmd/rah-sync
LDFLAGS         := -ldflags="-s -w"
BUILD_FLAGS     := $(LDFLAGS) -trimpath

## Overridable at call site:  make samples-publish STUDIO_URL=http://prod:8092
STUDIO_URL      ?= http://localhost:8092
SAMPLES_DIR     ?= ./samples-code
## Cores to pin gateway to during perf tests:  make perf CORES=4
CORES           ?= 2

## ── Build ─────────────────────────────────────────────────────────────────────

.PHONY: build
build: build-gateway build-studio build-sync

.PHONY: build-gateway
build-gateway:
	CGO_ENABLED=0 go build $(BUILD_FLAGS) -o $(BINARY_GATEWAY) $(CMD_GATEWAY)

.PHONY: build-studio
build-studio:
	CGO_ENABLED=0 go build $(BUILD_FLAGS) -o $(BINARY_STUDIO) $(CMD_STUDIO)

.PHONY: build-sync
build-sync:
	CGO_ENABLED=0 go build $(BUILD_FLAGS) -o $(BINARY_SYNC) $(CMD_SYNC)

.PHONY: build-all
build-all:
	@for os in linux darwin windows; do \
	  for arch in amd64 arm64; do \
	    [ "$$os" = "windows" ] && [ "$$arch" = "arm64" ] && continue; \
	    ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
	    echo "  building $$os/$$arch"; \
	    CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(BUILD_FLAGS) \
	      -o bin/rah-gateway_$${os}_$${arch}$$ext $(CMD_GATEWAY); \
	    CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(BUILD_FLAGS) \
	      -o bin/rah-studio_$${os}_$${arch}$$ext $(CMD_STUDIO); \
	  done; \
	done

## ── Run ───────────────────────────────────────────────────────────────────────

.PHONY: run
run: build-gateway
	$(BINARY_GATEWAY) -port 8080 -mport 8081 -config gateway.yaml

## run-local: gateway with disk-only config — no Redis or Postgres needed
.PHONY: run-local
run-local: build-gateway
	$(BINARY_GATEWAY) -port 8080 -mport 8081 -config gateway-local.yaml

## studio-run: studio pointing at the local management port
.PHONY: studio-run
studio-run: build-studio
	$(BINARY_STUDIO) -port 8092 -gateway-management-url http://localhost:8081

## dev: build + run gateway with Redis + Postgres (set POSTGRES_PASSWORD first)
.PHONY: dev
dev: build-gateway
	$(BINARY_GATEWAY) -port 8080 -mport 8081 -config gateway-dev.yaml

## dev-local: build + run gateway with disk store only (no Redis/Postgres needed)
.PHONY: dev-local
dev-local: build-gateway
	$(BINARY_GATEWAY) -port 8080 -mport 8081 -config gateway-inmem.yaml

## ── Samples ───────────────────────────────────────────────────────────────────

## samples-lint: validate samples-code/ locally without hitting the server
.PHONY: samples-lint
samples-lint: build-sync
	$(BINARY_SYNC) lint $(SAMPLES_DIR)

## samples-publish: lint + push all flows and APIs to the running studio + deploy
.PHONY: samples-publish
samples-publish: build-sync
	$(BINARY_SYNC) lint $(SAMPLES_DIR)
	$(BINARY_SYNC) publish $(SAMPLES_DIR) --studio $(STUDIO_URL) --auto-deploy default

## samples-resync: re-push without lint (fast iteration while editing flows)
.PHONY: samples-resync
samples-resync: build-sync
	$(BINARY_SYNC) publish $(SAMPLES_DIR) --studio $(STUDIO_URL) --auto-deploy default

## ── Verify ────────────────────────────────────────────────────────────────────

## verify: run curl-based functional checks against all deployed sample APIs
.PHONY: verify
verify:
	@bash samples-code/verify.sh

## ── Perf ──────────────────────────────────────────────────────────────────────

## perf: start gateway pinned to CORES, publish samples, ramp Vegeta, report TPS
##   make perf              (default: CORES=2)
##   make perf CORES=4
.PHONY: perf
perf: build-gateway build-sync
	@CORES=$(CORES) STUDIO_URL=$(STUDIO_URL) SAMPLES_DIR=$(SAMPLES_DIR) bash bench/perf.sh

## perf-bom: same as perf but uses Bombardier instead of Vegeta
##   make perf-bom              (default: CORES=2)
##   make perf-bom CORES=4
##   make perf-scale            run CORES 1,2,4,8 sequentially and save all results
.PHONY: perf-bom
perf-bom: build-gateway build-sync
	@CORES=$(CORES) STUDIO_URL=$(STUDIO_URL) SAMPLES_DIR=$(SAMPLES_DIR) bash bench/perf-bombardier.sh

.PHONY: perf-scale
perf-scale: build-gateway build-sync
	@for c in 1 2 4 8; do \
	  echo ""; \
	  echo "======================================================"; \
	  echo "  Benchmarking GOMAXPROCS=$$c"; \
	  echo "======================================================"; \
	  CORES=$$c STUDIO_URL=$(STUDIO_URL) SAMPLES_DIR=$(SAMPLES_DIR) \
	    SKIP_PUBLISH=1 bash bench/perf-bombardier.sh; \
	done
	@echo ""; \
	echo "All runs complete. Compare with:"; \
	echo "  bash bench/compare.sh bench-results/bom-cores*"

## ── Test ──────────────────────────────────────────────────────────────────────

.PHONY: test
test:
	go test -timeout 120s \
	  -skip "TestHighTPSWithEviction|TestVsModels|TestMemoryVsOldIndex|TestSchemaAndUIServed|TestUnifiedSyncRegistersApiAndRuntimeConsumesCompiledFlow" \
	  ./internal/... ./cmd/...

.PHONY: test-race
test-race:
	go test -race -timeout 180s \
	  -skip "TestHighTPSWithEviction|TestVsModels|TestMemoryVsOldIndex|TestSchemaAndUIServed|TestUnifiedSyncRegistersApiAndRuntimeConsumesCompiledFlow" \
	  ./internal/... ./cmd/...

.PHONY: bench
bench:
	go test -bench=. -benchmem -run=^$$ ./internal/...

## ── Lint ──────────────────────────────────────────────────────────────────────

.PHONY: lint
lint:
	golangci-lint run --timeout=5m --skip-dirs=internal/studio/ui

.PHONY: vet
vet:
	go vet ./...

## ── Docker ────────────────────────────────────────────────────────────────────

.PHONY: docker-build
docker-build:
	docker build -t rah-gateway:local .

.PHONY: docker-up
docker-up:
	docker compose up -d

.PHONY: docker-up-studio
docker-up-studio:
	docker compose --profile studio up -d

.PHONY: docker-down
docker-down:
	docker compose down

.PHONY: docker-clean
docker-clean:
	docker compose down -v
	docker rmi rah-gateway:local 2>/dev/null || true

## ── Release ───────────────────────────────────────────────────────────────────

.PHONY: release-dry
release-dry:
	goreleaser release --snapshot --clean

.PHONY: release-check
release-check:
	goreleaser check

## ── Helm ──────────────────────────────────────────────────────────────────────

.PHONY: helm-lint
helm-lint:
	helm lint deploy/helm/rah-gateway

.PHONY: helm-template
helm-template:
	helm template rah deploy/helm/rah-gateway --debug

.PHONY: helm-install-local
helm-install-local:
	helm upgrade --install rah deploy/helm/rah-gateway \
	  --namespace rah --create-namespace \
	  --set image.tag=local

## ── Clean ─────────────────────────────────────────────────────────────────────

.PHONY: clean
clean:
	rm -rf bin/
	go clean -testcache

.PHONY: tidy
tidy:
	go mod tidy

## ── Help ──────────────────────────────────────────────────────────────────────

.PHONY: help
help:
	@echo ""
	@echo "  build            build gateway + studio + sync binaries"
	@echo "  build-all        cross-compile for linux/darwin/windows × amd64/arm64"
	@echo ""
	@echo "  run              run gateway with gateway.yaml  (needs Redis + Postgres)"
	@echo "  run-local        run gateway with gateway-local.yaml  (disk only, no deps)"
	@echo "  dev              run gateway with Redis + Postgres  (set POSTGRES_PASSWORD)"
	@echo "  dev-local        run gateway with disk store only  (no Redis/Postgres needed)"
	@echo "  studio-run       run studio pointing at localhost:8081"
	@echo ""
	@echo "  samples-lint     validate samples-code/ locally"
	@echo "  samples-publish  lint + push samples to studio  (STUDIO_URL=...)"
	@echo "  samples-resync   re-push samples without lint  (fast iterate)"
	@echo ""
	@echo "  verify           curl-based functional checks against all sample APIs"
	@echo ""
	@echo "  perf             ramp TPS test pinned to CORES (default 2)  [Vegeta]"
	@echo "                   e.g.  make perf CORES=4"
	@echo "  perf-bom         same as perf but uses Bombardier"
	@echo "  perf-scale       run CORES=1,2,4,8 sequentially (Bombardier)"
	@echo ""
	@echo "  test             run unit tests (skip known-failing)"
	@echo "  bench            run in-process Go benchmarks"
	@echo "  lint             golangci-lint"
	@echo "  docker-build     build local Docker image"
	@echo "  docker-up        start full stack (redis + postgres + gateway)"
	@echo "  docker-down      stop stack"
	@echo "  release-dry      goreleaser snapshot (no push)"
	@echo "  helm-lint        lint Helm chart"
	@echo "  helm-template    render Helm templates to stdout"
	@echo "  clean            remove bin/ and test cache"
	@echo ""
