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

| Variable            | Default | Description                                                     |
| ------------------- | ------- | --------------------------------------------------------------- |
| `METRICS_ENDPOINTS` | *—*     | **Required.** Comma-separated list of URLs to scrape.           |
| `AGGREGATOR_PORT`   | `9090`  | Port on which merged metrics are exposed.                       |
| `SCRAPE_INTERVAL`   | `15s`   | How often the aggregator polls each target (Prometheus format). |

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
