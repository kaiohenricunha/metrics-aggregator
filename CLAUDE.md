# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Purpose

`metrics-aggregator` is a Go sidecar that scrapes Prometheus-formatted metrics from multiple containers in the same pod and exposes them on a single `/metrics` endpoint. It solves an Istio limitation where built-in metrics-merge only supports one port per pod.

## Commands

```bash
# Build
go build ./...

# Test (all)
go test -v ./...
go test -race ./...

# Test (single)
go test ./pkg/aggregator -run '^TestAddCustomLabel$' -v

# Coverage
go test -race -coverprofile=cover.out ./...
go tool cover -html=cover.out -o cover.html

# Lint & static analysis
staticcheck ./...
revive -formatter friendly ./...
govulncheck ./...

# Docker Compose demo stack
docker compose up --build
docker compose down -v
```

## Custom Commands

| Command | What it does |
|---|---|
| `/check` | `make check` — full pre-PR: build, vet, race tests, lint |
| `/lint` | `make lint` — staticcheck + revive + govulncheck |
| `/smoke` | `make smoke` — Docker Compose stack smoke test (~30 s) |
| `/cover` | `make test-race` + `make cover-html` — coverage report |
| `/test-one <TestName>` | `make test-one TEST='^<TestName>$'` — run one test by exact name |

Definitions live in `.claude/commands/`. A PostToolUse hook in `.claude/settings.json` auto-runs `gofmt -w` on any `.go` file after Claude edits it.

## Architecture

**Entry point:** `main.go` — parses `LOG_LEVEL`, calls `aggregator.SetupEndpoints()` at startup (fails fast on bad config), registers `/metrics` handler that delegates to `aggregator.AggregateMetrics()`, binds to `METRICS_AGGREGATOR_PORT` (default `9090`).

**Core logic:** `pkg/aggregator/aggregator.go` — all scraping and merging lives here:
- `SetupEndpoints()` parses `METRICS_ENDPOINTS` env var in two formats:
  - JSON map `{"serviceA":"http://.../metrics","serviceB":"..."}` — preserves names
  - Comma-separated URLs — auto-names sources `endpoint1`, `endpoint2`, …
- `AggregateMetrics()` scrapes each endpoint concurrently (best-effort: skips non-200 or failed), injects `origin_container="<name>"` label into every non-comment metric line, returns error only if zero sources succeed
- `addCustomLabel()` handles label injection: prepends inside existing `{...}` or creates a new label block; skips `#` comment and blank lines

## Key Conventions

- **Commit style:** Conventional Commits (`feat:`, `fix:`, `chore:`, `ci:`) — drives semantic-release versioning
- **Best-effort aggregation:** Never fail the whole request because one endpoint is down; only error when all sources fail
- **Prefer JSON-map** format for `METRICS_ENDPOINTS` — preserves meaningful container names as label values
- **Testing:** Use `httptest` servers for all HTTP-facing tests; keep coverage from dropping on touched paths
- **Static analysis:** All three tools (`staticcheck`, `revive`, `govulncheck`) must pass before merge

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `METRICS_ENDPOINTS` | required | Comma-separated URLs or JSON map of name→URL |
| `METRICS_AGGREGATOR_PORT` | `9090` | Port to serve `/metrics` |
| `LOG_LEVEL` | `info` | zerolog level: `debug`, `info`, `warn`, `error` |

## CI/CD Overview

Workflows in `.github/workflows/`:
- `go.yml` — build + test on every push/PR
- `lint.yml` — staticcheck, revive, govulncheck
- `test-coverage.yml` — race-detector coverage, uploads to Codecov
- `compose-smoke.yml` — full Docker Compose stack smoke test
- `docker-publish.yml` — builds/pushes to ghcr.io, Cosign signs image, Trivy scans for CRITICAL/HIGH CVEs
- `semantic-release.yml` — auto-tags `vX.Y.Z` on `main`
- `release.yml` — GoReleaser cross-compiles (linux/darwin/windows × amd64/arm64) with SBOM + SLSA provenance
