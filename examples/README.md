# Examples

These examples show how to deploy the metrics-aggregator as a Kubernetes sidecar.

## How the sidecar works

The aggregator runs as an extra container in your pod. It scrapes Prometheus metrics
from the other containers via `localhost` and serves them merged on a single `/metrics`
endpoint. There is **no auto-discovery** — you list the endpoints explicitly in the
`METRICS_ENDPOINTS` environment variable.

```
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

Prometheus only scrapes `:9090`. Each metric line gets an `origin_container` label
so you can tell which container it came from.

## Configuration

The only required setting is `METRICS_ENDPOINTS`. Two formats are supported:

**JSON map** (recommended — gives meaningful label values):
```
METRICS_ENDPOINTS='{"api":"http://localhost:8080/metrics","worker":"http://localhost:9100/metrics"}'
```

**Comma-separated URLs** (auto-names endpoints as `endpoint1`, `endpoint2`, …):
```
METRICS_ENDPOINTS=http://localhost:8080/metrics,http://localhost:9100/metrics
```

| Variable | Default | Description |
|---|---|---|
| `METRICS_ENDPOINTS` | *required* | JSON map or comma-separated URLs to scrape |
| `METRICS_AGGREGATOR_PORT` | `9090` | Port for the `/metrics` and `/healthz` endpoints |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |

## The sidecar does NOT need a Service

The aggregator works entirely within the pod. A Kubernetes Service is **not required**
for it to function — it only needs `localhost` access to the other containers.

How Prometheus discovers the aggregator port is a separate concern. The examples below
cover the three common approaches.

## Examples

| File | Discovery method | When to use |
|---|---|---|
| [`basic-pod.yaml`](basic-pod.yaml) | Pod annotations | Most clusters with standard Prometheus Kubernetes SD |
| [`deployment.yaml`](deployment.yaml) | Pod annotations | Production workloads (Deployment with replicas) |
| [`pod-monitor.yaml`](pod-monitor.yaml) | PodMonitor CRD | Clusters running the Prometheus Operator |
| [`service-discovery.yaml`](service-discovery.yaml) | Service + static config | Simple setups or when annotation-based SD is not available |
| [`helm-integration/`](helm-integration/) | Helm chart templates | Adding the sidecar to a shared microservices Helm chart |
| [`kustomize/`](kustomize/) | Kustomize overlay patch | ArgoCD / kubectl-native workflows without Helm |
| [`docker-compose/`](docker-compose/) | Docker Compose | Local evaluation without Kubernetes |
