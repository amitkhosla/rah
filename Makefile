##
## RAH Gateway — Makefile
## Common dev tasks. Requires Go 1.21+ and Docker.
##

BINARY_GATEWAY  := bin/rah-gateway
BINARY_STUDIO   := bin/rah-studio
CMD_GATEWAY     := ./cmd/rah-gateway
CMD_STUDIO      := ./cmd/rah-studio
LDFLAGS         := -ldflags="-s -w"
BUILD_FLAGS     := $(LDFLAGS) -trimpath

## ── Build ─────────────────────────────────────────────────────────────────────

.PHONY: build
build: build-gateway build-studio

.PHONY: build-gateway
build-gateway:
	CGO_ENABLED=0 go build $(BUILD_FLAGS) -o $(BINARY_GATEWAY) $(CMD_GATEWAY)

.PHONY: build-studio
build-studio:
	CGO_ENABLED=0 go build $(BUILD_FLAGS) -o $(BINARY_STUDIO) $(CMD_STUDIO)

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

.PHONY: dev
dev:
	docker compose up -d redis postgres
	$(MAKE) run

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
	@echo "  build          build gateway + studio binaries"
	@echo "  build-all      cross-compile for linux/darwin/windows × amd64/arm64"
	@echo "  run            build and run gateway with gateway.yaml"
	@echo "  dev            start redis+postgres via docker, then run gateway"
	@echo "  test           run tests (skip known-failing)"
	@echo "  bench          run benchmarks"
	@echo "  lint           golangci-lint"
	@echo "  docker-build   build local Docker image"
	@echo "  docker-up      start full stack (redis + postgres + gateway)"
	@echo "  docker-down    stop stack"
	@echo "  release-dry    goreleaser snapshot (no push)"
	@echo "  helm-lint      lint Helm chart"
	@echo "  helm-template  render Helm templates to stdout"
	@echo "  clean          remove bin/ and test cache"
	@echo ""
