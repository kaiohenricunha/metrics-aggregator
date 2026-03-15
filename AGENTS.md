# Repository Guidelines

## Project Structure & Module Organization
This repository is a small Go service that merges Prometheus metrics from multiple endpoints into one `/metrics` output. Entry point code lives in `main.go`. Core aggregation logic and unit tests live in `pkg/aggregator/` (`aggregator.go`, `aggregator_test.go`). Container and local demo assets are at the repo root: `Dockerfile`, `docker-compose.yaml`, `prometheus1.yml`, and `prometheus2.yml`. CI and release automation live under `.github/workflows/`.

## Build, Test, and Development Commands
Use Go 1.24, matching `go.mod`.

- `go build ./...`: compile all packages.
- `go build -o metrics-aggregator .`: build the service binary from the repo root.
- `go test -v ./...`: run unit tests.
- `go test -race -coverprofile=cover.out ./...`: match the coverage workflow.
- `go tool cover -html=cover.out -o cover.html`: inspect coverage locally.
- `docker compose config`: validate the compose stack before running it.
- `docker compose up --build`: start the aggregator with the demo Prometheus services.
- `docker compose down -v`: stop the demo stack and remove volumes.

Claude Code users may also invoke `/check`, `/lint`, `/smoke`, `/cover`, and `/test-one <TestName>` as slash commands — see `.claude/commands/` for definitions.

## Coding Style & Naming Conventions
Format Go code with `gofmt` before committing. Keep packages lowercase and short (`aggregator`), exported identifiers in `CamelCase`, and unexported helpers in `camelCase`. Prefer small functions with explicit errors over hidden side effects. Preserve current environment variable names such as `METRICS_ENDPOINTS`, `METRICS_AGGREGATOR_PORT`, and `LOG_LEVEL`.

CI runs `staticcheck`, `revive`, and `govulncheck`; contributors should run those locally when changing behavior or dependencies.

## Testing Guidelines
Place tests next to the code they cover in `*_test.go` files. Name tests by behavior, for example `TestSetupEndpoints` or `TestAggregateMetrics`. Prefer `httptest` servers for scrape scenarios and assert on merged metric output, labels, and error paths. Keep coverage from dropping on touched code paths.

## Commit & Pull Request Guidelines
Prefer Conventional Commit style because releases are automated from `main` via semantic release. Follow patterns already in history such as `chore(ci): ...`, `ci(trivy): ...`, `feat: ...`, or `fix: ...`. PRs should include a short summary, linked issue if applicable, test evidence (`go test`, coverage, or `docker compose` smoke output), and sample metric output when `/metrics` behavior changes.

## Configuration & Ops Notes
`METRICS_ENDPOINTS` is required and may be passed as a JSON map or comma-separated list. Keep Docker and workflow changes aligned with the ports and env vars used by `docker-compose.yaml` and the compose smoke test.
