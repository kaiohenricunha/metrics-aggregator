Run the full Kubernetes E2E test suite (~8-10 min). Keeps the cluster alive on completion for inspection:

```bash
make e2e-keep
```

Creates a kind cluster, builds/loads the Docker image, deploys the aggregator as a sidecar, and validates correctness, partial failure, observer Prometheus ingestion, performance, healthz, and Istio mTLS STRICT. Evidence artifacts are written to `test/e2e/evidence/`.

Requires: `kind`, `kubectl`, `docker`, `curl`, `jq` (promtool and istioctl are auto-installed).

To tear down the cluster afterward: `make e2e-clean`
