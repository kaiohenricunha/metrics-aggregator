# Metrics Aggregator

A **sidecar microservice** that scrapes Prometheus-formatted metrics from multiple containers in the same pod and exposes **one** unified `/metrics` endpoint. This helps work around a known Istio limitation:

Istio’s built-in metrics-merge only supports _one port per pod_ (see [Prometheus, Istio, and mTLS: the definitive explanation](https://superorbital.io/blog/istio-metrics-merging/) for details).

## Features

- Scrape any number of HTTP metric endpoints (from other containers in the pod)  
- Merge them into a single Prometheus-compatible stream  
- Expose on a configurable port (default `9090`)  
- Ideal for multi-container pods running under Istio with strict mTLS

## Running Locally

### Prerequisites

- [Docker](https://www.docker.com/get-started) (v20+)  
- [Docker Compose](https://docs.docker.com/compose/install/) (v1.29+)

### Quick Start

1. **Clone the repo**  
   ```bash
   git clone git@github.com:kaiohenricunha/metrics-aggregator.git
   cd metrics-aggregator
   ```

2. **Configure endpoints**
   Edit `docker-compose.yml` (or set an environment variable) to list all HTTP metric sources in the pod:

   ```yaml
   services:
     aggregator:
       environment:
         METRICS_ENDPOINTS: |
           http://prometheus1:9091/metrics,
           http://prometheus2:9092/metrics
   ```

3. **Build & run**

   ```bash
   docker-compose up --build
   ```

4. **Verify**
   Open your browser or curl:

   ```
   http://localhost:9090/metrics
   ```

### Configuration Reference

| Variable | Default | Description |
| --- | --- | --- |
| `METRICS_ENDPOINTS` | *—* | **Required.** JSON map or comma-separated endpoint list to scrape. |
| `METRICS_AGGREGATOR_PORT` | `9090` | Port on which merged metrics are exposed. |
| `METRICS_SECURITY_MODE` | `strict` | Endpoint validation mode. `strict` blocks unsafe URL forms; `legacy` keeps previous permissive behavior. |
| `METRICS_CACHE_TTL` | `1s` | Cache window for merged `/metrics` output to reduce fan-out amplification. |
| `METRICS_MAX_INFLIGHT` | `32` | Maximum concurrent `/metrics` requests served before returning `503`. |
| `METRICS_SERVER_READ_HEADER_TIMEOUT` | `2s` | Maximum time to receive request headers. |
| `METRICS_SERVER_READ_TIMEOUT` | `5s` | Maximum time to read full request. |
| `METRICS_SERVER_WRITE_TIMEOUT` | `10s` | Maximum time to write response. |
| `METRICS_SERVER_IDLE_TIMEOUT` | `60s` | Keep-alive idle connection timeout. |
| `METRICS_SERVER_MAX_HEADER_BYTES` | `1048576` | Max request header size (1 MiB). |

### Security Defaults

- `strict` endpoint mode rejects unsafe endpoint shapes (unsupported schemes, URL credentials, and non-metrics paths).
- Redirect responses from scrape targets are not followed by default.
- Invalid metric samples are dropped and reflected in self-instrumentation metrics.
- The server applies explicit HTTP timeouts and bounded request concurrency.

> **Note:** The two demo services, `prometheus1` and `prometheus2`, each run a tiny Prometheus instance exposing sample metrics. Their configs live in `prometheus1.yml` and `prometheus2.yml`.

## Stopping & Cleanup

```bash
docker-compose down
```

This will also remove the demo Prometheus containers.

## Viewing Logs

To tail the aggregator’s logs:

```bash
docker-compose logs -f aggregator
```

---

> **Read more about the underlying Istio limitation:**
> [https://superorbital.io/blog/istio-metrics-merging/](https://superorbital.io/blog/istio-metrics-merging/)
> *“Primary among these limitations is the fact that you cannot … fetch metrics from multiple ports in a single pod …”*
