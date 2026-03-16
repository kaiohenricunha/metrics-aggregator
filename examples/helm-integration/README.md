# Helm Integration

How to add the metrics-aggregator sidecar to an existing generic microservices Helm chart.

## What to add to your chart

Three pieces:

| File | What it does |
|---|---|
| `values.yaml` | Adds the `metricsAggregator` block (disabled by default) |
| `templates/_metrics-aggregator.tpl` | Two helpers: sidecar container + pod annotations |
| `templates/pod-monitor.yaml` | Optional PodMonitor (only rendered when `discovery: podMonitor`) |

Then wire the helpers into your existing `templates/deployment.yaml` at two points:

```yaml
# In pod metadata:
annotations:
  {{- include "mychart.metricsAggregatorAnnotations" . | nindent 8 }}

# In containers list:
containers:
  - name: {{ .Chart.Name }}
    ...
  {{- include "mychart.metricsAggregatorContainer" . | nindent 8 }}
```

## How a dev enables it

A team that needs multi-container metrics merging adds this to their `values.yaml` override:

```yaml
metricsAggregator:
  enabled: true
  endpoints:
    web: http://localhost:8080/metrics
    redis-exporter: http://localhost:9121/metrics
```

That's it. The sidecar container, probes, security context, and Prometheus discovery
are all handled by the chart templates. Teams that don't set `enabled: true` get
no sidecar — zero impact.

## Discovery options

### Annotations (default)

Works with any Prometheus that uses `kubernetes_sd_configs` with the standard
`prometheus.io/*` annotation relabeling. No CRDs needed.

```yaml
metricsAggregator:
  enabled: true
  discovery: annotations   # this is the default
  endpoints:
    app: http://localhost:8080/metrics
```

### PodMonitor (Prometheus Operator)

Creates a `PodMonitor` CRD. Use `additionalLabels` if your Prometheus
instance selects monitors by label (common with kube-prometheus-stack).

```yaml
metricsAggregator:
  enabled: true
  discovery: podMonitor
  podMonitor:
    interval: 30s
    additionalLabels:
      release: kube-prometheus-stack
  endpoints:
    app: http://localhost:8080/metrics
```

### None

Disables automatic discovery. Use this when you manage Prometheus scrape
configs externally (e.g., Terraform, ArgoCD ApplicationSets).

```yaml
metricsAggregator:
  enabled: true
  discovery: none
  endpoints:
    app: http://localhost:8080/metrics
```
