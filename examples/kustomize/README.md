# Kustomize

Inject the metrics-aggregator sidecar into any existing Deployment using a
strategic merge patch — no Helm required.

## Structure

```
kustomize/
├── base/
│   ├── kustomization.yaml
│   └── deployment.yaml         # Your app (no aggregator)
└── overlays/
    └── with-aggregator/
        ├── kustomization.yaml
        └── sidecar-patch.yaml  # Adds the sidecar + annotations
```

## Usage

```bash
# Preview the merged output
kubectl kustomize examples/kustomize/overlays/with-aggregator/

# Apply directly
kubectl apply -k examples/kustomize/overlays/with-aggregator/
```

## How it works

The base contains your normal Deployment. The overlay patches it with a
strategic merge that:

1. Adds `prometheus.io/*` annotations for discovery
2. Appends the `metrics-aggregator` container with probes, security context,
   and resource limits
3. Sets `METRICS_ENDPOINTS` to point at the other containers via localhost

## Adapting to your app

Edit `sidecar-patch.yaml`:

- Change `metadata.name` to match your Deployment name
- Update the `METRICS_ENDPOINTS` value to list your containers' metrics ports
- Adjust resource limits if needed

The patch merges cleanly with any Deployment — the base never needs to know
about the aggregator.

## Per-environment endpoints

Create additional overlays (e.g., `overlays/staging/`, `overlays/prod/`) that
each reference the base and apply their own sidecar patch with different
endpoint configurations or image tags.
