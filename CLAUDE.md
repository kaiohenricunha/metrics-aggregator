# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Purpose

`metrics-aggregator` is a Go sidecar that scrapes Prometheus-formatted metrics from multiple containers in the same pod and exposes them on a single `/metrics` endpoint. It solves an Istio limitation where built-in metrics-merge only supports one port per pod.

## Go Toolchain (gvm)

This project uses **Go 1.26**. The local toolchain is managed with [gvm](https://github.com/moovweb/gvm).

```bash
# Activate gvm in the current shell (already done if using the default shell profile)
source ~/.gvm/scripts/gvm

# See what is installed
gvm list

# Install the current project version (first time only)
gvm install go1.26.1

# Switch to it (--default persists across new shells)
gvm use go1.26.1 --default

# Find the latest patch for any minor
gvm listall | grep "^   go1\.26"
```

When bumping Go: update `go.mod` (`go X.Y`), `Dockerfile` (`ARG GO_VERSION=X.Y`), and the `go` directive in `go.mod`. Run `go mod tidy` after switching.

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
| `make e2e` | K8s E2E tests via kind, includes Istio (~8-10 min) — creates cluster, runs all phases, tears down |
| `make e2e-up` | Create kind cluster + build/load image only (no tests) — use for manifest validation |
| `make e2e-keep` | K8s E2E, keep cluster for debugging |
| `make e2e-clean` | Delete the E2E kind cluster |

Definitions live in `.claude/commands/`. A PostToolUse hook in `.claude/settings.json` auto-runs `gofmt -w` on any `.go` file after Claude edits it.

## Architecture

**Entry point:** `main.go` — parses `LOG_LEVEL`, creates `*aggregator.Aggregator` via `NewAggregator(os.Getenv("METRICS_ENDPOINTS"))` at startup (fails fast on bad config). Serves `/healthz` (lightweight liveness probe) and `/metrics` via handler factory `makeMetricsHandler(agg)`. Request ID middleware (`X-Request-Id`) wraps all routes. Graceful shutdown on SIGTERM/SIGINT with 10s drain timeout. Uses `http.NewServeMux` (not `DefaultServeMux`), and a testable `run(ctx, agg, addr)` function.

**Core logic:** `pkg/aggregator/aggregator.go` — all scraping and merging lives here:
- `Aggregator` struct holds `endpoints`, `*http.Client`, `zerolog.Logger`, and atomic `requestsTotal`/`errorsTotal` counters — no global mutable state
- `NewAggregator(config string)` parses `METRICS_ENDPOINTS` in two formats:
  - JSON map `{"serviceA":"http://.../metrics","serviceB":"..."}` — preserves names
  - Comma-separated URLs — auto-names sources `endpoint1`, `endpoint2`, …
  - Validates endpoint names against `[a-zA-Z0-9_-]+` to prevent malformed label values
- `AggregateMetrics(ctx context.Context)` scrapes each endpoint **concurrently** (goroutines with `sync.WaitGroup`, 5s HTTP timeout, 10 MiB body limit), **strips `# TYPE`/`# HELP` metadata** from scraped output to avoid duplicates, injects `origin_container="<name>"` label into every metric line (skipping lines that already have it), prepends self-instrumentation metrics, returns error only if zero sources succeed. Uses `zerolog.Ctx(ctx)` for request-correlated log output (inherits `request_id` from middleware).
- Self-instrumentation metrics (4 families): `metrics_aggregator_scrape_success` (gauge), `metrics_aggregator_scrape_duration_seconds` (gauge), `metrics_aggregator_requests_total` (counter), `metrics_aggregator_errors_total` (counter)
- `addCustomLabel()` handles label injection: prepends inside existing `{...}` or creates a new label block; skips lines that already contain `origin_container`

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
- `go.yml` — build + race-detector test on every push/PR
- `lint.yml` — staticcheck, revive, govulncheck
- `test-coverage.yml` — race-detector coverage, uploads to Codecov
- `compose-smoke.yml` — full Docker Compose stack smoke test
- `docker-publish.yml` — builds/pushes to ghcr.io, Cosign signs image, Trivy scans for CRITICAL/HIGH CVEs
- `semantic-release.yml` — auto-tags `vX.Y.Z` on `main`
- `e2e.yml` — kind-based K8s E2E tests: deploys aggregator as sidecar with nginx metric servers, validates correctness/partial-failure/observer ingestion/Istio mTLS STRICT
- `release.yml` — GoReleaser cross-compiles (linux/darwin/windows × amd64/arm64) with SBOM + SLSA provenance

## E2E Testing (Kubernetes)

The `test/e2e/` directory contains a kind-based E2E suite that proves the aggregator works as a real K8s sidecar. Uses lightweight nginx metric servers (serving static Prometheus-format metrics from ConfigMaps) instead of full Prometheus instances. Prerequisites: `kind`, `kubectl`, `docker`, `curl`, `jq` (`promtool` and `istioctl` are auto-installed if missing).

**Quick start:**
```bash
make e2e          # Full run: create cluster → deploy → assert → Istio → teardown (~8-10 min)
make e2e-keep     # Same but keep the cluster alive for kubectl inspection
make e2e-clean    # Delete the kind cluster when done debugging
```

**Iterating on manifests or the test script:**
```bash
make e2e-up       # Create kind cluster + build/load image (no tests)
                  # Now you have a running cluster and can:
kubectl apply --dry-run=server -f test/e2e/manifests/   # validate manifests against the API server
kubectl apply -f test/e2e/manifests/namespace.yaml      # apply individual resources
bash test/e2e/run.sh --keep-cluster                     # run the full suite, keep cluster after
make e2e-clean    # tear down when done
```

**What it tests** (evidence artifacts in `test/e2e/evidence/`, gitignored):
- Correctness: `origin_container` labels, metadata stripping, self-instrumentation, content-specific (histogram/summary/counter/gauge), promtool format check
- Partial failure: dead endpoint → `scrape_success=0`, healthy endpoint still served, app metrics survive
- Observer Prometheus: ingests aggregator output, sustained `up==1` over 60s
- Performance: all responses under Prometheus 10s scrape_timeout
- Healthz: liveness probe succeeds, zero restarts
- Istio (always runs): 4-container pod (3 app + istio-proxy), permissive metrics flow, promtool in mesh, mTLS STRICT observer scrape
