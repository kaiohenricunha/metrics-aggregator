Run the Docker Compose smoke test (~30 seconds):

```bash
make smoke
```

Starts the full demo stack (aggregator + two Prometheus instances), waits for all services to be healthy, probes `/metrics` and `/-/healthy` endpoints, then tears down. Requires Docker running locally.
