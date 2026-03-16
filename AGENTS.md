# Repository Guidelines

## Project Structure & Module Organization
This repository is a small Go service that merges Prometheus metrics from multiple endpoints into one `/metrics` output. Entry point code lives in `main.go` (server startup, request ID middleware, graceful shutdown) with integration tests in `main_test.go`. Core aggregation logic lives in `pkg/aggregator/` — the `Aggregator` struct (constructed via `NewAggregator(config)`) holds endpoints, HTTP client, logger, and atomic request/error counters with no global mutable state. Container and local demo assets are at the repo root: `Dockerfile`, `docker-compose.yaml`, `prometheus1.yml`, and `prometheus2.yml`. CI and release automation live under `.github/workflows/`. Deployment examples live in `examples/` — Kubernetes manifests (basic pod, Deployment, PodMonitor, service discovery), Helm integration, Kustomize overlays, and Docker Compose.

## Go Toolchain (gvm)

This project uses **Go 1.26**. The local toolchain is managed with [gvm](https://github.com/moovweb/gvm).

```bash
# Activate gvm (already sourced in the default shell profile)
source ~/.gvm/scripts/gvm

# Install the project Go version (first time only)
gvm install go1.26.1

# Switch to it (--default persists across new shells)
gvm use go1.26.1 --default

# Verify
go version   # should print go1.26.1

# List available 1.26.x patch releases
gvm listall | grep "^   go1\.26"
```

After installing or switching versions run `go mod tidy` to keep `go.sum` consistent.

## Build, Test, and Development Commands
Use Go 1.26, matching `go.mod`.

- `go build ./...`: compile all packages.
- `go build -o metrics-aggregator .`: build the service binary from the repo root.
- `go test -v ./...`: run unit tests.
- `go test -race -coverprofile=cover.out ./...`: match the coverage workflow.
- `go tool cover -html=cover.out -o cover.html`: inspect coverage locally.
- `docker compose config`: validate the compose stack before running it.
- `docker compose up --build`: start the aggregator with the demo Prometheus services.
- `docker compose down -v`: stop the demo stack and remove volumes.

Claude Code users may also invoke `/check`, `/lint`, `/smoke`, `/cover`, and `/test-one <TestName>` as slash commands — see `.claude/commands/` for definitions.

### K8s E2E tests (requires `kind`, `kubectl`, `docker`, `curl`, `jq`)

- `make e2e`: full run — create kind cluster → deploy sidecar pod → assert → Istio mTLS → teardown (~8-10 min). `promtool` and `istioctl` are auto-installed if missing.
- `make e2e-up`: create kind cluster + build/load image only (no tests). Use for manifest validation: `kubectl apply --dry-run=server -f test/e2e/manifests/`.
- `make e2e-keep`: full run but keep cluster alive for `kubectl` inspection.
- `make e2e-clean`: delete the kind cluster.

## Coding Style & Naming Conventions
Format Go code with `gofmt` before committing. Keep packages lowercase and short (`aggregator`), exported identifiers in `CamelCase`, and unexported helpers in `camelCase`. Prefer small functions with explicit errors over hidden side effects. Preserve current environment variable names such as `METRICS_ENDPOINTS`, `METRICS_AGGREGATOR_PORT`, and `LOG_LEVEL`.

CI runs `staticcheck`, `revive`, and `govulncheck`; contributors should run those locally when changing behavior or dependencies.

## Testing Guidelines
Place tests next to the code they cover in `*_test.go` files. Name tests by behavior, for example `TestNewAggregator` or `TestAggregator_AggregateMetrics`. Prefer `httptest` servers for scrape scenarios and assert on merged metric output, labels, and error paths. All aggregator tests use struct instances (no global state) for isolation. `main_test.go` contains integration tests for server endpoints and graceful shutdown. Keep coverage from dropping on touched code paths.

### E2E Testing (Kubernetes)
The `test/e2e/` directory contains a kind-based E2E suite that deploys the aggregator as a real K8s sidecar alongside lightweight nginx metric servers (serving static Prometheus-format metrics from ConfigMaps). It validates correctness (origin_container labels, metadata stripping, self-instrumentation, content-specific metrics), partial failure resilience, observer Prometheus ingestion, performance, healthz probes, and Istio mTLS STRICT. Evidence artifacts are written to `test/e2e/evidence/` (gitignored).

When iterating on K8s manifests or the test script, use `make e2e-up` to create a kind cluster and load the image without running tests. This gives a live API server for `kubectl apply --dry-run=server` manifest validation. Run `make e2e-clean` to tear down.

Key files:
- `test/e2e/run.sh` — main orchestrator (flags: `--keep-cluster`, `--fresh`); Istio always runs
- `test/e2e/lib.sh` — shared assertion helpers, port-forward management, auto-install helpers (`ensure_promtool`, `ensure_istioctl`)
- `test/e2e/manifests/` — K8s manifests (namespace, pods, services, ConfigMaps including `app-metrics.yaml`)
- `test/e2e/istio/` — Istio-injected pod, PeerAuthentication, in-mesh observer

## Commit & Pull Request Guidelines
Prefer Conventional Commit style because releases are automated from `main` via semantic release. Follow patterns already in history such as `chore(ci): ...`, `ci(trivy): ...`, `feat: ...`, or `fix: ...`. PRs should include a short summary, linked issue if applicable, test evidence (`go test`, coverage, or `docker compose` smoke output), and sample metric output when `/metrics` behavior changes.

## Working Style

- **Prefer targeted action over exploration.** This is a small, focused Go project (~1,750 lines across 7 files). Go directly to the relevant file rather than surveying the whole codebase first.
- **Commits should be surgical.** Verify with `git diff --staged` that only relevant files are included. Do not bundle unrelated formatting or refactoring.
- **One concern per commit.** Follow Conventional Commits style.

## Verification Discipline

- After implementing a fix, **run the relevant test(s)** before reporting success.
- After editing `.go` files, confirm `go vet ./...` passes.
- Before opening a PR, run `make check`. All three static analysis tools must pass.
- When fixing a CI failure, reproduce locally first, then verify end-to-end before pushing.

## Configuration & Ops Notes
`METRICS_ENDPOINTS` is required and may be passed as a JSON map or comma-separated list. Keep Docker and workflow changes aligned with the ports and env vars used by `docker-compose.yaml` and the compose smoke test.
