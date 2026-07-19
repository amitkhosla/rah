# Contributing to RAH

## Prerequisites

- Go 1.26+
- Redis 7+ (required for tests)
- PostgreSQL 14+ (required only if changing datastore code)
- Docker (optional, for full-stack local setup)

## Getting Started

Clone and build all binaries:

```bash
go build ./...
```

Run all binaries listed: `rah-gateway`, `rah-studio`, `rah-sync`.

For a local stack via Docker:

```bash
docker-compose -f docker-compose.full.yml up
```

## Running Tests

**Standard tests** (requires Redis running at localhost:6379):

```bash
go test -timeout 120s ./internal/... ./cmd/...
```

Set `REDIS_ADDR` to use a different Redis address:

```bash
REDIS_ADDR=localhost:6379 go test -timeout 120s ./internal/... ./cmd/...
```

**With PostgreSQL** (required for datastore changes):

Set the `RAH_TEST_PG_DSN` environment variable:

```bash
RAH_TEST_PG_DSN="host=localhost port=5432 user=postgres password=postgres dbname=gateway sslmode=disable" go test ./internal/...
```

**Known skipped tests** (pre-existing, being worked on):

- TestHighTPSWithEviction
- TestVsModels
- TestMemoryVsOldIndex
- TestSchemaAndUIServed
- TestUnifiedSyncRegistersApiAndRuntimeConsumesCompiledFlow

## Linting

Run linting:

```bash
golangci-lint run --timeout=5m
```

New PRs must not introduce new lint errors — CI enforces this via `--new-from-rev=origin/main`.

Pre-existing lint debt is tracked and being fixed gradually. If you fix existing lint errors in your PR, that's welcome and counts as a contribution.

## Commit Messages

Use conventional commits — the prefix matters for changelog grouping:

- `feat:` new feature
- `fix:` bug fix
- `perf:` performance improvement
- `docs:` documentation only
- `test:` tests only
- `ci:` CI/CD changes

## Pull Request Process

1. Fork the repository
2. Create a branch off `main`
3. Make your changes
4. Open a PR against `main`
5. Ensure CI passes (lint, build, test)
6. If touching PostgreSQL datastore code, run postgres tests locally before submitting
7. Keep one PR per logical change — don't bundle unrelated fixes
8. Describe *what* and *why* in the PR description, not what the code does

## Samples

Test your changes with the provided samples:

- `samples/` — single-file bundles, good for quick tests
- `samples-code/` — split `apis/` + `instructions/` structure, mirrors real-world team projects

Lint your changes:

```bash
rah-sync lint ./samples
rah-sync lint ./samples-code
```

## Getting Help

- Open a GitHub Issue for bugs or questions
- Check the `docs/` directory for architecture and feature guides
