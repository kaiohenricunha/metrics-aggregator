# Copilot instructions for `metrics-aggregator`

## Go Toolchain (gvm)

This project requires **Go 1.26**. The local toolchain is managed with [gvm](https://github.com/moovweb/gvm).

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

After installing or switching versions run `go mod tidy` to keep `go.sum` consistent. When bumping Go: update `go.mod` (`go X.Y`) and `Dockerfile` (`ARG GO_VERSION=X.Y`).

## Preferred CLI workflow for this repo

Use `Makefile` targets first so tooling is consistent across local runs and Copilot sessions.

- Install project CLIs into repo-local `.bin/`:
  - `make tools-install`
- Install Go language server into repo-local `.bin/`:
  - `make tools-install-ls`
- Build:
  - `make build`
- Run full tests:
  - `make test`
- Run a single test:
  - `make test-one TEST='TestAddCustomLabel'`
- Run CI-aligned race + coverage flow:
  - `make test-race`
  - `make cover-html` (this target also runs `make test-race`)
- Run lint checks (uses `.bin/staticcheck`, `.bin/revive`):
  - `make lint`
- Run vulnerability scan:
  - `make vulncheck`
- Run full local quality gate:
  - `make check`
- Validate and smoke test compose stack:
  - `make compose-config`
  - `make smoke`
- K8s E2E tests (requires `kind`, `kubectl`, `docker`, `curl`, `jq`):
  - `make e2e` — full run: create kind cluster → deploy sidecar pod → assert → Istio mTLS → teardown (~8-10 min). `promtool` and `istioctl` are auto-installed if missing
  - `make e2e-up` — create kind cluster + build/load image only (no tests); use for manifest validation with `kubectl apply --dry-run=server -f test/e2e/manifests/`
  - `make e2e-keep` — full run but keep cluster alive for `kubectl` inspection
  - `make e2e-clean` — delete the kind cluster

Direct command equivalents (if Make is unavailable):

- `go build ./...`
- `go test ./...`
- `go test ./pkg/aggregator -run '^TestAddCustomLabel$' -v`
- `go test -race -coverprofile=cover.out ./...`
- `go tool cover -html=cover.out -o cover.html`
- `staticcheck ./...`
- `revive -formatter friendly ./...`
- `govulncheck ./...`

## Language servers verified as useful

- **Go**: `gopls` (install with `make tools-install-ls`)
- **YAML**: `yaml-language-server` via VS Code YAML extension (`redhat.vscode-yaml`)
- **Docker/Docker Compose**: Docker extension (`ms-azuretools.vscode-docker`)

Repository editor config for LS support lives in:

- `.vscode/extensions.json` (recommended extensions)
- `.vscode/settings.json` (enable `gopls` and YAML schema mapping for `.github/workflows/*.yml` and `docker-compose.yaml`)

When editing workflows or compose files, rely on YAML schema diagnostics before running CI.

## High-level architecture

- `main.go` is the runtime entrypoint:
  - Configures global zerolog level from `LOG_LEVEL` (defaults to `info` when parsing fails).
  - Creates `*aggregator.Aggregator` via `NewAggregator(os.Getenv("METRICS_ENDPOINTS"))` and fails fast on bad config.
  - Serves `/metrics` via `makeMetricsHandler(agg)` and `/healthz` liveness probe.
  - Request ID middleware on all routes: reads/generates `X-Request-Id`, injects into zerolog context.
  - Graceful shutdown on SIGTERM/SIGINT with 10s drain timeout via `run(ctx, agg, addr)`.
  - Binds HTTP server to `METRICS_AGGREGATOR_PORT`, falling back to `aggregator.DefaultAggregatorPort` (`9090`).
  - `main_test.go` contains integration tests for server endpoints, request ID, and graceful shutdown.

- `pkg/aggregator/aggregator.go` owns all scrape/merge logic:
  - `Aggregator` struct holds endpoints, HTTP client, logger, and atomic request/error counters — no global mutable state.
  - `NewAggregator(config)` reads `METRICS_ENDPOINTS` in two formats:
    - JSON map (`{"serviceA":"http://.../metrics"}`) with endpoint names preserved.
    - Fallback comma-separated URL list with auto names (`endpoint1`, `endpoint2`, ...).
  - `AggregateMetrics(ctx)` scrapes each endpoint, skips failed/non-200 sources, merges payloads, uses `zerolog.Ctx(ctx)` for request-correlated logging.
  - Self-instrumentation: `scrape_success`, `scrape_duration_seconds`, `requests_total`, `errors_total`.
  - Injects `origin_container="<endpoint name>"` into every non-comment metric sample line before merge.

- `examples/` contains deployment examples:
  - Kubernetes: basic pod, Deployment, PodMonitor, service discovery
  - Helm integration and Kustomize overlays
  - Docker Compose for local evaluation

- `docker-compose.yaml` defines the local/demo topology:
  - `aggregator` plus two Prometheus demo services (`prometheus1`, `prometheus2`).
  - Aggregator receives `METRICS_ENDPOINTS` as a JSON map of service names to in-network URLs.
  - Ports are controlled by `AGG_PORT`, `PROM_PORT`, `PROM1_EXT`, `PROM2_EXT` (same model as compose smoke CI).

## E2E Testing (Kubernetes)

The `test/e2e/` directory contains a kind-based E2E suite that deploys the aggregator as a real K8s sidecar alongside lightweight nginx metric servers (serving static Prometheus-format metrics from ConfigMaps). The suite validates correctness (origin_container labels, metadata stripping, self-instrumentation, content-specific metrics), partial failure resilience, observer Prometheus ingestion, performance, healthz probes, and Istio mTLS STRICT. Evidence artifacts are written to `test/e2e/evidence/` (gitignored).

When iterating on K8s manifests or the test script, use `make e2e-up` to spin up a kind cluster and load the image without running tests. This gives you a live API server for `kubectl apply --dry-run=server` validation and manual `kubectl` exploration. Run `make e2e-clean` to tear down.

Key files:
- `test/e2e/run.sh` — main orchestrator (accepts `--keep-cluster`, `--fresh`); Istio always runs
- `test/e2e/lib.sh` — shared assertion helpers, port-forward management, auto-install helpers (`ensure_promtool`, `ensure_istioctl`)
- `test/e2e/manifests/` — K8s manifests (namespace, pods, services, ConfigMaps including `app-metrics.yaml`)
- `test/e2e/istio/` — Istio-injected pod, PeerAuthentication, in-mesh observer

## Key repository conventions

- Prefer JSON-map `METRICS_ENDPOINTS`; map keys become `origin_container` label values in output metrics.
- Runtime app port env var is `METRICS_AGGREGATOR_PORT` (Compose maps it from `AGG_PORT`).
- Aggregation is best-effort per source: endpoint failures are logged and skipped; request fails only if zero sources yield metrics.
- `addCustomLabel` behavior:
  - Labeled metric: prepends `origin_container` inside existing `{...}`.
  - Unlabeled metric: creates a new `{origin_container="..."}` block.
  - Comment/empty lines are unchanged.
- Aggregator tests use `NewAggregator()` or `newTestAggregator()` for struct-based isolation; no global state.
- Request correlation: `X-Request-Id` flows through `AggregateMetrics(ctx)` for correlated log output.
- Graceful shutdown: 10s drain on SIGTERM/SIGINT.

## Working Style

- Prefer targeted action over exploration. Go directly to the relevant file rather than surveying the whole codebase first.
- Commits should be surgical — only include files relevant to the current task. One concern per commit, Conventional Commits style.

## Verification Discipline

- After implementing a fix, run the relevant tests before reporting success.
- After editing `.go` files, confirm `go vet ./...` passes.
- Before opening a PR, run `make check`. All static analysis tools must pass.
- When fixing a CI failure, reproduce locally first.
