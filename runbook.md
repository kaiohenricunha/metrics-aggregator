# Metrics Aggregator Runbook

Operational runbook for Prometheus alerts defined in `alerts.rules.yml`.

---

## MetricsAggregatorAllEndpointsDown

**Severity:** critical

### Symptom
All configured scrape endpoints are returning errors. The `/metrics` endpoint responds with HTTP 500.

### Impact
No application metrics are being collected. Downstream dashboards and alerts that depend on scraped metrics are blind.

### Investigation
1. Check aggregator logs: `kubectl logs <pod> -c aggregator | grep "HTTP GET failed\|non-200"`
2. Verify target pods are running: `kubectl get pods` in the target namespace
3. Check network policies or Istio mTLS configuration
4. Verify `METRICS_ENDPOINTS` env var is correct

### Remediation
- If targets are down: restart the target workloads
- If network issue: check NetworkPolicy, Istio PeerAuthentication, or Service entries
- If config issue: fix `METRICS_ENDPOINTS` and restart the aggregator

---

## MetricsAggregatorEndpointDown

**Severity:** warning

### Symptom
A single endpoint has been failing for 5+ minutes while others may still be healthy.

### Impact
Metrics from the affected container are missing. Aggregated output is partial but still served (best-effort).

### Investigation
1. Identify the failing endpoint from the alert label: `endpoint="<name>"`
2. Check aggregator logs for that endpoint
3. Verify the target container is running and its `/metrics` port is accessible
4. Test connectivity: `kubectl exec <aggregator-pod> -- wget -qO- http://<target>:<port>/metrics`

### Remediation
- Restart the failing container if it's crashed
- Check resource limits if the container is OOMKilled
- Verify the metrics endpoint path and port

---

## MetricsAggregatorHighErrorRate

**Severity:** warning

### Symptom
The per-endpoint error counter `scrape_errors_total` is incrementing faster than 0.1/s sustained over 5 minutes.

### Impact
Frequent transient failures are degrading metric freshness for the affected endpoint.

### Investigation
1. Check if the error rate correlates with target pod restarts or deployments
2. Look for timeout errors in logs (the aggregator uses a 5s HTTP timeout)
3. Check for resource contention on the target pod

### Remediation
- If timeouts: consider increasing the target's response capacity or the aggregator's HTTP timeout
- If intermittent 5xx: investigate the target application's health
- If DNS resolution failures: check CoreDNS or service discovery

---

## MetricsAggregatorSlowScrape

**Severity:** warning

### Symptom
The P99 scrape duration exceeds 5 seconds, approaching the Prometheus default `scrape_timeout` of 10s.

### Impact
Slow scrapes risk timing out the entire aggregation request, causing Prometheus to mark the target as down.

### Investigation
1. Check `scrape_duration_seconds` histogram: which endpoint is slow?
2. Check the target pod's CPU/memory usage
3. Look for large metric payloads (the aggregator has a 10 MiB body limit)

### Remediation
- Reduce the number of metrics exposed by the target
- Increase target pod resources
- Consider splitting high-cardinality metrics into a separate endpoint

---

## MetricsAggregatorHighInvalidSamples

**Severity:** warning

### Symptom
The aggregator is dropping more than 10 metric lines per scrape because they fail Prometheus format validation.

### Impact
Some metrics from the affected endpoint are silently dropped.

### Investigation
1. Check aggregator logs: `grep "dropped invalid scrape samples"`
2. Fetch raw metrics from the target: `curl http://<target>:<port>/metrics`
3. Validate with promtool: `curl ... | promtool check metrics`

### Remediation
- Fix the target application's metrics output format
- If using a client library, upgrade to the latest version
- If custom metrics: ensure they follow Prometheus exposition format

---

## SLO Targets

| Metric | Target | Window |
|--------|--------|--------|
| Availability | 99.9% of scrape requests succeed (at least one endpoint returns data) | 30 days |
| Latency | P99 aggregation response < 5 seconds | 5 minutes |
| Error rate | < 0.1% of individual endpoint scrapes fail | 30 days |

### Availability SLI
```promql
1 - (rate(metrics_aggregator_errors_total[5m]) / rate(metrics_aggregator_requests_total[5m]))
```

### Latency SLI
```promql
histogram_quantile(0.99, rate(metrics_aggregator_http_request_duration_seconds_bucket[5m]))
```

### Error Rate SLI (per-endpoint)
```promql
rate(metrics_aggregator_scrape_errors_total[5m]) / rate(metrics_aggregator_requests_total[5m])
```
