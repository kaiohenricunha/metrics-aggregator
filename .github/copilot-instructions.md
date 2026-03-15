# Copilot instructions for `metrics-aggregator`

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
  - Calls `aggregator.SetupEndpoints()` during startup and fails fast if endpoint config is invalid.
  - Serves `/metrics`, delegating collection/merge to `aggregator.AggregateMetrics()`.
  - Binds HTTP server to `METRICS_AGGREGATOR_PORT`, falling back to `aggregator.DefaultAggregatorPort` (`9090`).

- `pkg/aggregator/aggregator.go` owns all scrape/merge logic:
  - Maintains package-level `endpoints []Endpoint` state populated by `SetupEndpoints`.
  - Reads `METRICS_ENDPOINTS` in two formats:
    - JSON map (`{"serviceA":"http://.../metrics"}`) with endpoint names preserved.
    - Fallback comma-separated URL list with auto names (`endpoint1`, `endpoint2`, ...).
  - `AggregateMetrics()` scrapes each endpoint, skips failed/non-200 sources, and merges successful payloads.
  - Injects `origin_container="<endpoint name>"` into every non-comment metric sample line before merge.

- `docker-compose.yaml` defines the local/demo topology:
  - `aggregator` plus two Prometheus demo services (`prometheus1`, `prometheus2`).
  - Aggregator receives `METRICS_ENDPOINTS` as a JSON map of service names to in-network URLs.
  - Ports are controlled by `AGG_PORT`, `PROM_PORT`, `PROM1_EXT`, `PROM2_EXT` (same model as compose smoke CI).

## Key repository conventions

- Prefer JSON-map `METRICS_ENDPOINTS`; map keys become `origin_container` label values in output metrics.
- Runtime app port env var is `METRICS_AGGREGATOR_PORT` (Compose maps it from `AGG_PORT`).
- Aggregation is best-effort per source: endpoint failures are logged and skipped; request fails only if zero sources yield metrics.
- `addCustomLabel` behavior:
  - Labeled metric: prepends `origin_container` inside existing `{...}`.
  - Unlabeled metric: creates a new `{origin_container="..."}` block.
  - Comment/empty lines are unchanged.
- Aggregator tests use package-global `endpoints` overrides and `httptest` servers; follow this style for new scrape/merge tests.
