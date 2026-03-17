# Metrics Aggregator

[![CI](https://github.com/kaiohenricunha/metrics-aggregator/actions/workflows/go.yml/badge.svg)](https://github.com/kaiohenricunha/metrics-aggregator/actions/workflows/go.yml)
[![Lint](https://github.com/kaiohenricunha/metrics-aggregator/actions/workflows/lint.yml/badge.svg)](https://github.com/kaiohenricunha/metrics-aggregator/actions/workflows/lint.yml)
[![E2E](https://github.com/kaiohenricunha/metrics-aggregator/actions/workflows/e2e.yml/badge.svg)](https://github.com/kaiohenricunha/metrics-aggregator/actions/workflows/e2e.yml)
[![codecov](https://codecov.io/gh/kaiohenricunha/metrics-aggregator/branch/main/graph/badge.svg)](https://codecov.io/gh/kaiohenricunha/metrics-aggregator)
[![Go Report Card](https://goreportcard.com/badge/github.com/kaiohenricunha/metrics-aggregator)](https://goreportcard.com/report/github.com/kaiohenricunha/metrics-aggregator)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/metrics-aggregator)](https://artifacthub.io/packages/container/kaiohenricunha447/metrics-aggregator)

A Go sidecar that scrapes Prometheus-formatted metrics from every container in a pod, merges them into a single `/metrics` endpoint, and injects an `origin_container` label so you can tell which container produced each metric.

## How it works

```txt
┌─────────────────────────────────────────────────┐
│  Pod                                            │
│                                                 │
│  ┌───────────┐   localhost:8080/metrics          │
│  │  api       ├──────────────┐                  │
│  └───────────┘               │                  │
│                         ┌────▼──────────┐       │
│  ┌───────────┐          │  aggregator   │       │
│  │  worker    ├─────────►  :9090/metrics ◄──── Prometheus
│  └───────────┘          └───────────────┘       │
│                localhost:9100/metrics            │
└─────────────────────────────────────────────────┘
```

- **Concurrent scraping** — each endpoint is fetched in parallel (5 s timeout, 10 MiB body limit)
- **Metadata stripping** — `# TYPE` and `# HELP` lines are removed to prevent duplicates when merging
- **Label injection** — every metric line gets an `origin_container="<name>"` label
- **Best-effort** — partial failures are reported per-endpoint; the request only errors when *all* sources fail
- **Self-instrumentation** — exposes `scrape_success`, `scrape_duration_seconds`, `requests_total`, and `errors_total`

## Install

### Docker

```bash
docker pull ghcr.io/kaiohenricunha/metrics-aggregator:latest
```

Images are signed with [Cosign](https://docs.sigstore.dev/cosign/overview/). Verify with:

```bash
cosign verify ghcr.io/kaiohenricunha/metrics-aggregator:latest \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp github.com/kaiohenricunha/metrics-aggregator
```

### Binary

Pre-built binaries for 6 platforms (linux/darwin/windows x amd64/arm64) with SBOM and SLSA provenance are available on the [Releases](https://github.com/kaiohenricunha/metrics-aggregator/releases) page.

### From source

```bash
go install github.com/kaiohenricunha/metrics-aggregator@latest
```

## Quick start

### Kubernetes (recommended)

Add the aggregator as a sidecar to any pod and point Prometheus at its port:

```yaml
# In your pod / Deployment spec
metadata:
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "9090"
spec:
  containers:
    - name: metrics-aggregator
      image: ghcr.io/kaiohenricunha/metrics-aggregator:latest
      ports:
        - containerPort: 9090
      env:
        - name: METRICS_ENDPOINTS
          value: '{"api":"http://localhost:8080/metrics","worker":"http://localhost:9100/metrics"}'
    # ... your existing containers
```

Copy-paste ready manifests for Deployment, PodMonitor, Helm, and Kustomize are in [`examples/`](examples/).

### Docker Compose (local dev)

```bash
cd examples/docker-compose
docker compose up
# then: curl http://localhost:9090/metrics
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `METRICS_ENDPOINTS` | *required* | JSON map or comma-separated URLs to scrape |
| `METRICS_AGGREGATOR_PORT` | `9090` | Port for the `/metrics` and `/healthz` endpoints |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |

**JSON map** (recommended — gives meaningful `origin_container` label values):

```yaml
METRICS_ENDPOINTS='{"api":"http://localhost:8080/metrics","worker":"http://localhost:9100/metrics"}'
```

**Comma-separated URLs** (auto-names endpoints as `endpoint1`, `endpoint2`, ...):

```txt
METRICS_ENDPOINTS=http://localhost:8080/metrics,http://localhost:9100/metrics
```

<details>
<summary><strong>Advanced configuration</strong></summary>

| Variable | Default | Description |
|---|---|---|
| `METRICS_SECURITY_MODE` | `strict` | `strict` blocks unsafe URL forms; `legacy` keeps permissive behavior |
| `METRICS_CACHE_TTL` | `1s` | Cache window for merged output to reduce fan-out amplification |
| `METRICS_MAX_INFLIGHT` | `32` | Max concurrent `/metrics` requests before returning `503` |
| `METRICS_SERVER_READ_HEADER_TIMEOUT` | `2s` | Max time to receive request headers |
| `METRICS_SERVER_READ_TIMEOUT` | `5s` | Max time to read full request |
| `METRICS_SERVER_WRITE_TIMEOUT` | `10s` | Max time to write response |
| `METRICS_SERVER_IDLE_TIMEOUT` | `60s` | Keep-alive idle connection timeout |
| `METRICS_SERVER_MAX_HEADER_BYTES` | `1048576` | Max request header size (1 MiB) |
| `OTEL_TRACES_EXPORTER` | `none` | Tracing exporter: `none` (disabled), `stdout`, or `otlp` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | *—* | OTLP collector gRPC endpoint (e.g. `jaeger:4317`). Required when `OTEL_TRACES_EXPORTER=otlp` |
| `OTEL_EXPORTER_OTLP_INSECURE` | `false` | Set to `true` to disable TLS for the OTLP gRPC exporter (e.g. local Jaeger) |

**Security defaults:** `strict` mode rejects unsafe endpoint shapes (unsupported schemes, URL credentials, non-metrics paths). Redirect responses from scrape targets are not followed. Invalid metric samples are dropped and reflected in self-instrumentation metrics.

</details>

## Observability

### Self-Instrumentation Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `metrics_aggregator_scrape_success` | gauge | Whether each endpoint's last scrape succeeded (1/0) |
| `metrics_aggregator_scrape_duration_seconds` | histogram | Per-endpoint scrape duration with percentile support |
| `metrics_aggregator_scrape_errors_total` | counter | Per-endpoint cumulative scrape failure count |
| `metrics_aggregator_scrape_invalid_samples` | gauge | Invalid metric lines dropped per endpoint |
| `metrics_aggregator_requests_total` | counter | Total aggregation executions |
| `metrics_aggregator_errors_total` | counter | Total failed aggregations (all endpoints down) |
| `metrics_aggregator_http_requests_total` | counter | Total HTTP requests to `/metrics` (includes cache hits) |
| `metrics_aggregator_http_request_duration_seconds` | histogram | Handler-level request duration |

### Alerting Rules

Production-ready Prometheus alerting rules are shipped in [`alerts.rules.yml`](alerts.rules.yml). Load them into your Prometheus or Alertmanager configuration. See [`runbook.md`](runbook.md) for investigation steps and SLO targets.

### Grafana Dashboard

Import [`grafana-dashboard.json`](grafana-dashboard.json) into Grafana. It includes panels for scrape health, duration heatmaps, error rates, and quantile time series. Uses a `$datasource` template variable.

### Distributed Tracing

Set `OTEL_TRACES_EXPORTER=otlp` and `OTEL_EXPORTER_OTLP_ENDPOINT=<collector>:4317` to enable OpenTelemetry tracing. Each `/metrics` request creates a parent span with child spans per scrape endpoint. W3C `traceparent` headers are forwarded to scrape targets.

To try locally with Jaeger:

```bash
OTEL_EXPORTER_OTLP_INSECURE=true docker compose --profile tracing up --build
# Open http://localhost:16686 for Jaeger UI
```

### Log Correlation

Inbound `traceparent` headers are parsed and `trace_id`/`span_id` fields are added to structured log output. `X-Request-Id` is forwarded to scrape targets for request correlation.

## Deployment examples

| Example | Method | When to use |
|---|---|---|
| [`basic-pod.yaml`](examples/basic-pod.yaml) | Pod annotations | Standard Prometheus Kubernetes SD |
| [`deployment.yaml`](examples/deployment.yaml) | Pod annotations | Production Deployments |
| [`pod-monitor.yaml`](examples/pod-monitor.yaml) | PodMonitor CRD | Prometheus Operator |
| [`service-discovery.yaml`](examples/service-discovery.yaml) | Service + static config | Non-annotation setups |
| [`helm-integration/`](examples/helm-integration/) | Helm templates | Shared microservices chart |
| [`kustomize/`](examples/kustomize/) | Kustomize overlay | ArgoCD / kubectl workflows |
| [`docker-compose/`](examples/docker-compose/) | Docker Compose | Local evaluation |

The aggregator needs no Kubernetes Service to function — it works entirely within the pod network.

## Why this exists

Istio's built-in metrics-merge only supports one metrics port per pod, which breaks multi-container pods where each container exposes its own `/metrics`. The metrics-aggregator solves this by scraping all containers and serving a single merged endpoint. See [Prometheus, Istio, and mTLS](https://superorbital.io/blog/istio-metrics-merging/) for background. It's equally useful outside Istio for any scenario where you need to merge metrics from multiple containers.

## Development

```bash
make check       # build + vet + race tests + lint (pre-PR gate)
make smoke       # Docker Compose smoke test (~30 s)
make e2e         # kind + Istio E2E suite (~8-10 min)
make cover-html  # race-detector coverage report
```

See [`CLAUDE.md`](CLAUDE.md) for full development docs.

## License

[Apache 2.0](LICENSE)
