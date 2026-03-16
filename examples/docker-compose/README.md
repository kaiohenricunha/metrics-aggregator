# Docker Compose

Try the metrics-aggregator locally without Kubernetes.

## Quick start

```bash
cd examples/docker-compose
docker compose up
```

Then in another terminal:

```bash
# Merged metrics from both apps (with origin_container labels)
curl http://localhost:9090/metrics

# Raw metrics from each app individually
curl http://localhost:8081/metrics
curl http://localhost:8082/metrics
```

## What's running

| Service | Port | Description |
|---|---|---|
| `aggregator` | 9090 | Metrics aggregator — scrapes both apps, serves merged output |
| `app-a` | 8081 | Simulated HTTP API server (static Prometheus metrics) |
| `app-b` | 8082 | Simulated background worker (static Prometheus metrics) |

## Difference from Kubernetes

In a Kubernetes pod, all containers share the same network namespace, so the
aggregator reaches other containers via `localhost`. In Docker Compose, each
service gets its own network, so the aggregator uses **service names** as
hostnames instead:

```
# Kubernetes (shared network namespace)
METRICS_ENDPOINTS='{"app-a":"http://localhost:8081/metrics"}'

# Docker Compose (Docker DNS)
METRICS_ENDPOINTS='{"app-a":"http://app-a:8080/metrics"}'
```

Because the URLs are not `localhost`, this example sets `METRICS_SECURITY_MODE=legacy`
to relax the strict-mode URL validation. In Kubernetes this is not needed.

## Customizing

To test with your own app, replace one of the nginx services with your container
and update the `METRICS_ENDPOINTS` value in the aggregator service to point at it.
