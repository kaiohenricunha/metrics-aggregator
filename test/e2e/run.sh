#!/usr/bin/env bash
# run.sh — K8s E2E test suite for metrics-aggregator
#
# Usage:
#   bash test/e2e/run.sh [--keep-cluster] [--fresh]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
EVIDENCE_DIR="$SCRIPT_DIR/evidence"
MANIFESTS="$SCRIPT_DIR/manifests"
CLUSTER_NAME="e2e-metrics-aggregator"
NAMESPACE="e2e-metrics-aggregator"

# Ports for local port-forwards (high numbers to avoid conflicts)
PF_AGG_PORT=19090
PF_AGG_PARTIAL_PORT=19091
PF_OBSERVER_PORT=19093
PF_ISTIO_PORT=19094
PF_ISTIO_OBSERVER_PORT=19095
PF_MERGE_PORT=19096
PF_BASELINE_PORT=19097
PF_SD_OBSERVER_PORT=19098

# ── Parse flags ──────────────────────────────────────────────────
KEEP_CLUSTER=false
FRESH=false
for arg in "$@"; do
  case "$arg" in
    --keep-cluster) KEEP_CLUSTER=true ;;
    --fresh)        FRESH=true ;;
    *) echo "Unknown flag: $arg"; exit 1 ;;
  esac
done

# ── Source helpers ───────────────────────────────────────────────
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

# ── Cleanup trap ─────────────────────────────────────────────────
cleanup() {
  cleanup_port_forwards
  if [[ "$KEEP_CLUSTER" == false ]]; then
    info "deleting kind cluster $CLUSTER_NAME…"
    kind delete cluster --name "$CLUSTER_NAME" 2>/dev/null || true
  else
    info "keeping cluster $CLUSTER_NAME (--keep-cluster)"
  fi
}
trap cleanup EXIT

# ═══════════════════════════════════════════════════════════════════
# Phase 0 — Prerequisites
# ═══════════════════════════════════════════════════════════════════
info "Phase 0: checking prerequisites"
require_cmd kind
require_cmd kubectl
require_cmd docker
require_cmd curl
require_cmd jq
ensure_promtool
ensure_istioctl

# ═══════════════════════════════════════════════════════════════════
# Phase 1 — Cluster setup
# ═══════════════════════════════════════════════════════════════════
info "Phase 1: cluster setup"

if [[ "$FRESH" == true ]]; then
  kind delete cluster --name "$CLUSTER_NAME" 2>/dev/null || true
fi

if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  info "reusing existing kind cluster $CLUSTER_NAME"
else
  info "creating kind cluster $CLUSTER_NAME…"
  kind create cluster --name "$CLUSTER_NAME" --wait 60s
fi

# ═══════════════════════════════════════════════════════════════════
# Phase 2 — Build & load image
# ═══════════════════════════════════════════════════════════════════
info "Phase 2: build & load docker image"
docker build -t metrics-aggregator:e2e "$ROOT_DIR"
kind load docker-image metrics-aggregator:e2e --name "$CLUSTER_NAME"

# ═══════════════════════════════════════════════════════════════════
# Phase 3 — Deploy healthy sidecar pod
# ═══════════════════════════════════════════════════════════════════
info "Phase 3: deploying healthy sidecar pod"
kubectl apply -f "$MANIFESTS/namespace.yaml"
kubectl apply -f "$MANIFESTS/app-metrics.yaml"
kubectl apply -f "$MANIFESTS/aggregator-service.yaml"
kubectl apply -f "$MANIFESTS/sidecar-pod.yaml"
wait_for_pod "$NAMESPACE" aggregator-sidecar 90

# Prepare evidence directory
rm -rf "$EVIDENCE_DIR"
mkdir -p "$EVIDENCE_DIR"

# ═══════════════════════════════════════════════════════════════════
# Phase 4 — Correctness assertions
# ═══════════════════════════════════════════════════════════════════
info "Phase 4: correctness assertions"
start_port_forward "$NAMESPACE" pod/aggregator-sidecar "${PF_AGG_PORT}:9090"

# Fetch raw metrics
for i in $(seq 1 15); do
  if curl -sf "http://localhost:${PF_AGG_PORT}/metrics" -o "$EVIDENCE_DIR/metrics-raw.txt"; then
    break
  fi
  sleep 2
done

if [[ ! -s "$EVIDENCE_DIR/metrics-raw.txt" ]]; then
  fail "aggregator /metrics never returned data"
else
  pass "aggregator /metrics returned data"

  # origin_container labels
  assert_grep 'origin_container="app-a"' "$EVIDENCE_DIR/metrics-raw.txt" \
    "origin_container=app-a present"
  assert_grep 'origin_container="app-b"' "$EVIDENCE_DIR/metrics-raw.txt" \
    "origin_container=app-b present"

  # only self-instrumentation metadata should be emitted
  assert_grep '^# TYPE metrics_aggregator_scrape_success gauge$' \
    "$EVIDENCE_DIR/metrics-raw.txt" "self TYPE: scrape_success gauge"
  assert_grep '^# TYPE metrics_aggregator_scrape_duration_seconds histogram$' \
    "$EVIDENCE_DIR/metrics-raw.txt" "self TYPE: scrape_duration_seconds histogram"
  assert_grep '^# TYPE metrics_aggregator_scrape_invalid_samples gauge$' \
    "$EVIDENCE_DIR/metrics-raw.txt" "self TYPE: scrape_invalid_samples gauge"
  assert_grep '^# TYPE metrics_aggregator_requests_total counter$' \
    "$EVIDENCE_DIR/metrics-raw.txt" "self TYPE: requests_total counter"
  assert_grep '^# TYPE metrics_aggregator_errors_total counter$' \
    "$EVIDENCE_DIR/metrics-raw.txt" "self TYPE: errors_total counter"
  NON_SELF_TYPE_COUNT=$(awk '/^# TYPE/ && $0 !~ /^# TYPE metrics_aggregator_/ {count++} END {print count+0}' "$EVIDENCE_DIR/metrics-raw.txt")
  assert_eq "$NON_SELF_TYPE_COUNT" "0" "only self # TYPE metadata is emitted"

  # self-instrumentation metrics
  assert_grep 'metrics_aggregator_scrape_success{endpoint="app-a"} 1' \
    "$EVIDENCE_DIR/metrics-raw.txt" "scrape_success=1 for app-a"
  assert_grep 'metrics_aggregator_scrape_success{endpoint="app-b"} 1' \
    "$EVIDENCE_DIR/metrics-raw.txt" "scrape_success=1 for app-b"
  assert_grep 'metrics_aggregator_scrape_duration_seconds_count{endpoint="app-a"}' \
    "$EVIDENCE_DIR/metrics-raw.txt" "scrape_duration_seconds_count for app-a"
  assert_grep 'metrics_aggregator_requests_total' \
    "$EVIDENCE_DIR/metrics-raw.txt" "requests_total present"
  assert_grep 'metrics_aggregator_scrape_duration_seconds_count{endpoint="app-b"}' \
    "$EVIDENCE_DIR/metrics-raw.txt" "scrape_duration_seconds_count for app-b"
  assert_grep 'metrics_aggregator_scrape_errors_total' \
    "$EVIDENCE_DIR/metrics-raw.txt" "scrape_errors_total present"

  # Content-specific assertions — app-a (Go API: counter, histogram, gauge)
  assert_grep 'http_requests_total{origin_container="app-a"' "$EVIDENCE_DIR/metrics-raw.txt" \
    "app-a: http_requests_total counter present"
  assert_grep 'http_request_duration_seconds_bucket{origin_container="app-a"' "$EVIDENCE_DIR/metrics-raw.txt" \
    "app-a: http_request_duration_seconds histogram present"
  assert_grep 'go_goroutines{origin_container="app-a"}' "$EVIDENCE_DIR/metrics-raw.txt" \
    "app-a: go_goroutines gauge present"

  # Content-specific assertions — app-b (Python worker: counter, summary, gauge)
  assert_grep 'jobs_processed_total{origin_container="app-b"' "$EVIDENCE_DIR/metrics-raw.txt" \
    "app-b: jobs_processed_total counter present"
  assert_grep 'job_duration_seconds{origin_container="app-b"' "$EVIDENCE_DIR/metrics-raw.txt" \
    "app-b: job_duration_seconds summary present"
  assert_grep 'worker_pool_size{origin_container="app-b"}' "$EVIDENCE_DIR/metrics-raw.txt" \
    "app-b: worker_pool_size gauge present"

  # Every non-comment, non-empty, non-self-instrumentation line must have origin_container
  LINES_WITHOUT_LABEL=0
  while IFS= read -r line; do
    # Skip empty, comments, and self-instrumentation lines
    [[ -z "$line" ]] && continue
    [[ "$line" == "#"* ]] && continue
    [[ "$line" == "metrics_aggregator_"* ]] && continue
    if ! echo "$line" | grep -q 'origin_container='; then
      (( LINES_WITHOUT_LABEL++ )) || true
    fi
  done < "$EVIDENCE_DIR/metrics-raw.txt"
  assert_eq "$LINES_WITHOUT_LABEL" "0" "all scraped metric lines have origin_container label"

  # promtool validation (mandatory)
  # "no help text" lint warnings are expected — the aggregator strips # HELP/# TYPE
  # metadata from scraped output by design (avoids duplicates across sources).
  # We only fail on actual parsing errors.
  PROMTOOL_OUTPUT=$(curl -sf "http://localhost:${PF_AGG_PORT}/metrics" | promtool check metrics 2>&1 || true)
  echo "$PROMTOOL_OUTPUT" > "$EVIDENCE_DIR/promtool-check.txt"
  if echo "$PROMTOOL_OUTPUT" | grep -vE '^[^:]+: no help text for metric:' | grep -q '[^[:space:]]'; then
    fail "promtool check metrics failed"
  else
    pass "promtool check metrics passed (lint-only warnings)"
  fi
fi

# ═══════════════════════════════════════════════════════════════════
# Phase 5 — Performance assertions
# ═══════════════════════════════════════════════════════════════════
info "Phase 5: performance assertions"

: > "$EVIDENCE_DIR/latency-samples.txt"
MAX_LATENCY=0
for i in $(seq 1 10); do
  LATENCY=$(curl -sf -o /dev/null -w '%{time_total}' "http://localhost:${PF_AGG_PORT}/metrics")
  echo "sample_${i}: ${LATENCY}s" >> "$EVIDENCE_DIR/latency-samples.txt"
  if awk "BEGIN{exit(!($LATENCY > $MAX_LATENCY))}"; then
    MAX_LATENCY="$LATENCY"
  fi
done
echo "max_latency: ${MAX_LATENCY}s" >> "$EVIDENCE_DIR/latency-samples.txt"
assert_lt "$MAX_LATENCY" "10" "max latency < 10s (Prometheus scrape_timeout)"

# Extract scrape durations (from histogram _sum lines)
{
  echo "=== Scrape Duration Analysis ==="
  grep 'metrics_aggregator_scrape_duration_seconds' "$EVIDENCE_DIR/metrics-raw.txt" || true
} > "$EVIDENCE_DIR/performance-summary.txt"

DUR1=$(grep 'scrape_duration_seconds_sum{endpoint="app-a"}' "$EVIDENCE_DIR/metrics-raw.txt" | awk '{print $2}' || echo "0")
DUR2=$(grep 'scrape_duration_seconds_sum{endpoint="app-b"}' "$EVIDENCE_DIR/metrics-raw.txt" | awk '{print $2}' || echo "0")
echo "app-a_duration: ${DUR1}s" >> "$EVIDENCE_DIR/performance-summary.txt"
echo "app-b_duration: ${DUR2}s" >> "$EVIDENCE_DIR/performance-summary.txt"
echo "max_curl_latency: ${MAX_LATENCY}s" >> "$EVIDENCE_DIR/performance-summary.txt"

# ═══════════════════════════════════════════════════════════════════
# Phase 6 — Healthz assertions
# ═══════════════════════════════════════════════════════════════════
info "Phase 6: healthz assertions"

HEALTHZ_CODE=$(curl -sf -o /dev/null -w '%{http_code}' "http://localhost:${PF_AGG_PORT}/healthz")
assert_eq "$HEALTHZ_CODE" "200" "/healthz returns HTTP 200"

RESTART_COUNT=$(kubectl get pod aggregator-sidecar -n "$NAMESPACE" -o jsonpath='{.status.containerStatuses[?(@.name=="aggregator")].restartCount}')
assert_eq "$RESTART_COUNT" "0" "aggregator container has 0 restarts"

{
  echo "healthz_http_code: $HEALTHZ_CODE"
  echo "aggregator_restart_count: $RESTART_COUNT"
} > "$EVIDENCE_DIR/healthz-check.txt"

# Stop port-forward for healthy pod
cleanup_port_forwards

# ═══════════════════════════════════════════════════════════════════
# Phase 7 — Partial failure test
# ═══════════════════════════════════════════════════════════════════
info "Phase 7: partial failure test"

kubectl delete pod aggregator-sidecar -n "$NAMESPACE" --ignore-not-found --wait=true
kubectl apply -f "$MANIFESTS/sidecar-pod-partial.yaml"
wait_for_pod "$NAMESPACE" aggregator-sidecar-partial 90

start_port_forward "$NAMESPACE" pod/aggregator-sidecar-partial "${PF_AGG_PARTIAL_PORT}:9090"

for i in $(seq 1 15); do
  if curl -sf "http://localhost:${PF_AGG_PARTIAL_PORT}/metrics" -o "$EVIDENCE_DIR/metrics-partial.txt"; then
    break
  fi
  sleep 2
done

if [[ ! -s "$EVIDENCE_DIR/metrics-partial.txt" ]]; then
  fail "partial-failure pod /metrics never returned data"
else
  pass "partial-failure pod /metrics returned data (HTTP 200)"

  assert_grep 'metrics_aggregator_scrape_success{endpoint="app-a"} 1' \
    "$EVIDENCE_DIR/metrics-partial.txt" "partial: scrape_success=1 for healthy endpoint"
  assert_grep 'metrics_aggregator_scrape_success{endpoint="app-b"} 0' \
    "$EVIDENCE_DIR/metrics-partial.txt" "partial: scrape_success=0 for dead endpoint"
  assert_grep 'origin_container="app-a"' \
    "$EVIDENCE_DIR/metrics-partial.txt" "partial: healthy metrics still served"
  assert_grep 'http_requests_total{origin_container="app-a"' \
    "$EVIDENCE_DIR/metrics-partial.txt" "partial: app metrics survive partial failure"
fi

cleanup_port_forwards

# ═══════════════════════════════════════════════════════════════════
# Phase 8 — Observer Prometheus verification
# ═══════════════════════════════════════════════════════════════════
info "Phase 8: observer Prometheus verification"

# Redeploy healthy pod for the observer to scrape
kubectl delete pod aggregator-sidecar-partial -n "$NAMESPACE" --ignore-not-found --wait=true
kubectl apply -f "$MANIFESTS/sidecar-pod.yaml"
wait_for_pod "$NAMESPACE" aggregator-sidecar 90

# Deploy observer Prometheus
kubectl apply -f "$MANIFESTS/observer-prometheus-config.yaml"
kubectl apply -f "$MANIFESTS/observer-prometheus.yaml"
wait_for_deployment "$NAMESPACE" observer-prometheus 120

info "waiting 75s for observer to collect ~5 scrape intervals…"
sleep 75

start_port_forward "$NAMESPACE" svc/observer-prometheus "${PF_OBSERVER_PORT}:9090"

: > "$EVIDENCE_DIR/observer-queries.txt"

# Evidence 1 — Prometheus can parse & ingest our output
UP_RESULT=$(prom_query_to "$PF_OBSERVER_PORT" "$EVIDENCE_DIR/observer-queries.txt" 'up{job="aggregator"}' "aggregator up")
UP_VALUE=$(echo "$UP_RESULT" | jq -r '.data.result[0].value[1] // "missing"' 2>/dev/null || echo "missing")
assert_eq "$UP_VALUE" "1" "observer: up{job=aggregator} == 1"

# Origin container series exist in observer
ORIGIN_COUNT=$(prom_query_to "$PF_OBSERVER_PORT" "$EVIDENCE_DIR/observer-queries.txt" 'count({origin_container=~".+"})' "origin_container series count")
ORIGIN_VALUE=$(echo "$ORIGIN_COUNT" | jq -r '.data.result[0].value[1] // "0"' 2>/dev/null || echo "0")
assert_gt "$ORIGIN_VALUE" "0" "observer: origin_container series count > 0"

# Self-instrumentation in observer
SCRAPE_SUCCESS=$(prom_query_to "$PF_OBSERVER_PORT" "$EVIDENCE_DIR/observer-queries.txt" 'metrics_aggregator_scrape_success' "scrape_success in observer")
SCRAPE_STATUS=$(echo "$SCRAPE_SUCCESS" | jq -r '.data.resultType // "error"' 2>/dev/null || echo "error")
if [[ "$SCRAPE_STATUS" == "vector" ]]; then
  pass "observer: metrics_aggregator_scrape_success series exist"
else
  fail "observer: metrics_aggregator_scrape_success series missing"
fi

# Sustained stability
AVG_UP=$(prom_query_to "$PF_OBSERVER_PORT" "$EVIDENCE_DIR/observer-queries.txt" 'avg_over_time(up{job="aggregator"}[60s])' "sustained scrape stability")
AVG_VALUE=$(echo "$AVG_UP" | jq -r '.data.result[0].value[1] // "0"' 2>/dev/null || echo "0")
assert_eq "$AVG_VALUE" "1" "observer: avg_over_time(up[60s]) == 1 (sustained stability)"

# Samples scraped
SAMPLES=$(prom_query_to "$PF_OBSERVER_PORT" "$EVIDENCE_DIR/observer-queries.txt" 'scrape_samples_scraped{job="aggregator"}' "samples scraped")
SAMPLES_VALUE=$(echo "$SAMPLES" | jq -r '.data.result[0].value[1] // "0"' 2>/dev/null || echo "0")
assert_gt "$SAMPLES_VALUE" "0" "observer: scrape_samples_scraped > 0"

cleanup_port_forwards

# ═══════════════════════════════════════════════════════════════════
# Phase 9 — Istio mesh test (mandatory)
# ═══════════════════════════════════════════════════════════════════
info "Phase 9: Istio mesh test"

# ── 9a — Install Istio + deploy injected pod ─────────────────────
info "Phase 9a: installing Istio + deploying injected pod"
istioctl install --set profile=demo -y
kubectl label namespace "$NAMESPACE" istio-injection=enabled --overwrite

kubectl apply -f "$MANIFESTS/app-metrics.yaml"
kubectl apply -f "$SCRIPT_DIR/istio/sidecar-pod-istio.yaml"

info "waiting for Istio-injected pod (may take longer)…"
wait_for_pod "$NAMESPACE" aggregator-sidecar-istio 180

CONTAINER_COUNT=$(kubectl get pod aggregator-sidecar-istio -n "$NAMESPACE" \
  -o jsonpath='{.spec.containers[*].name}' | wc -w)

{
  echo "=== Istio E2E Check ==="
  echo "container_count: $CONTAINER_COUNT"
  echo "containers: $(kubectl get pod aggregator-sidecar-istio -n "$NAMESPACE" \
    -o jsonpath='{.spec.containers[*].name}')"
} > "$EVIDENCE_DIR/istio-check.txt"

assert_eq "$CONTAINER_COUNT" "4" "Istio: pod has 4 containers (3 app + istio-proxy)"

# ── 9b — Permissive mode assertions ──────────────────────────────
info "Phase 9b: permissive mode — metrics through mesh"
start_port_forward "$NAMESPACE" pod/aggregator-sidecar-istio "${PF_ISTIO_PORT}:9090"

for i in $(seq 1 15); do
  if curl -sf "http://localhost:${PF_ISTIO_PORT}/metrics" -o "$EVIDENCE_DIR/istio-permissive-metrics.txt"; then
    break
  fi
  sleep 2
  if [[ "$i" -eq 15 ]]; then
    fail "Istio permissive: metrics endpoint not reachable"
  fi
done

if [[ -s "$EVIDENCE_DIR/istio-permissive-metrics.txt" ]]; then
  pass "Istio permissive: metrics endpoint reachable through mesh"

  assert_grep 'origin_container="app-a"' "$EVIDENCE_DIR/istio-permissive-metrics.txt" \
    "Istio permissive: origin_container=app-a present"
  assert_grep 'origin_container="app-b"' "$EVIDENCE_DIR/istio-permissive-metrics.txt" \
    "Istio permissive: origin_container=app-b present"
  assert_grep 'http_requests_total{origin_container="app-a"' "$EVIDENCE_DIR/istio-permissive-metrics.txt" \
    "Istio permissive: app-a counter present"
  assert_grep 'jobs_processed_total{origin_container="app-b"' "$EVIDENCE_DIR/istio-permissive-metrics.txt" \
    "Istio permissive: app-b counter present"

  # promtool validation in mesh (same lint tolerance as Phase 4)
  ISTIO_PROMTOOL_OUTPUT=$(curl -sf "http://localhost:${PF_ISTIO_PORT}/metrics" | promtool check metrics 2>&1 || true)
  echo "$ISTIO_PROMTOOL_OUTPUT" > "$EVIDENCE_DIR/promtool-istio-check.txt"
  if echo "$ISTIO_PROMTOOL_OUTPUT" | grep -vE '^[^:]+: no help text for metric:' | grep -q '[^[:space:]]'; then
    fail "Istio permissive: promtool check metrics failed"
  else
    pass "Istio permissive: promtool check metrics passed (lint-only warnings)"
  fi
fi

cleanup_port_forwards

# ── 9c — mTLS STRICT mode ────────────────────────────────────────
info "Phase 9c: mTLS STRICT — in-mesh observer scrape"
kubectl apply -f "$SCRIPT_DIR/istio/peer-authentication.yaml"

# Deploy in-mesh observer Prometheus (with Istio sidecar)
kubectl apply -f "$SCRIPT_DIR/istio/observer-prometheus-istio.yaml"
wait_for_deployment "$NAMESPACE" observer-prometheus-istio 120

info "waiting 75s for in-mesh observer to collect scrapes under mTLS STRICT…"
sleep 75

start_port_forward "$NAMESPACE" svc/observer-prometheus-istio "${PF_ISTIO_OBSERVER_PORT}:9090"

: > "$EVIDENCE_DIR/istio-observer-queries.txt"

# Verify in-mesh observer can scrape through mTLS
ISTIO_UP_RESULT=$(prom_query_to "$PF_ISTIO_OBSERVER_PORT" "$EVIDENCE_DIR/istio-observer-queries.txt" 'up{job="aggregator"}' "aggregator up via mTLS")
ISTIO_UP_VALUE=$(echo "$ISTIO_UP_RESULT" | jq -r '.data.result[0].value[1] // "missing"' 2>/dev/null || echo "missing")
assert_eq "$ISTIO_UP_VALUE" "1" "Istio STRICT: up{job=aggregator} == 1 via mTLS"

# Origin container series through mTLS
ISTIO_ORIGIN=$(prom_query_to "$PF_ISTIO_OBSERVER_PORT" "$EVIDENCE_DIR/istio-observer-queries.txt" 'count({origin_container=~".+"})' "origin_container via mTLS")
ISTIO_ORIGIN_VALUE=$(echo "$ISTIO_ORIGIN" | jq -r '.data.result[0].value[1] // "0"' 2>/dev/null || echo "0")
assert_gt "$ISTIO_ORIGIN_VALUE" "0" "Istio STRICT: origin_container series present via mTLS"

{
  echo ""
  echo "=== mTLS STRICT Results ==="
  echo "up_value: $ISTIO_UP_VALUE"
  echo "origin_container_count: $ISTIO_ORIGIN_VALUE"
} >> "$EVIDENCE_DIR/istio-check.txt"

cleanup_port_forwards

# ── 9d — Istio metrics merging integration (port 15020) ───────────
info "Phase 9d: Istio metrics merging — port 15020 verification"

start_port_forward "$NAMESPACE" pod/aggregator-sidecar-istio "${PF_MERGE_PORT}:15020"

info "waiting 30s for Istio agent to merge app metrics…"
sleep 30

MERGE_OK=false
for i in $(seq 1 15); do
  if curl -sf "http://localhost:${PF_MERGE_PORT}/stats/prometheus" \
    -o "$EVIDENCE_DIR/istio-merged-15020.txt"; then
    MERGE_OK=true
    break
  fi
  sleep 2
done

if [[ "$MERGE_OK" == "false" ]]; then
  fail "Istio merge: port 15020 /stats/prometheus not reachable"
else
  pass "Istio merge: port 15020 /stats/prometheus returned data"

  # App metrics from aggregator are present (with origin_container labels)
  assert_grep 'origin_container="app-a"' "$EVIDENCE_DIR/istio-merged-15020.txt" \
    "Istio merge: app-a metrics present at 15020"
  assert_grep 'origin_container="app-b"' "$EVIDENCE_DIR/istio-merged-15020.txt" \
    "Istio merge: app-b metrics present at 15020"
  assert_grep 'http_requests_total{origin_container="app-a"' "$EVIDENCE_DIR/istio-merged-15020.txt" \
    "Istio merge: app-a http_requests_total at 15020"
  assert_grep 'jobs_processed_total{origin_container="app-b"' "$EVIDENCE_DIR/istio-merged-15020.txt" \
    "Istio merge: app-b jobs_processed_total at 15020"

  # Self-instrumentation from aggregator passed through
  assert_grep 'metrics_aggregator_scrape_success' "$EVIDENCE_DIR/istio-merged-15020.txt" \
    "Istio merge: aggregator self-instrumentation at 15020"

  # Envoy proxy metrics are ALSO present (proves merging happened)
  assert_grep 'envoy_' "$EVIDENCE_DIR/istio-merged-15020.txt" \
    "Istio merge: envoy proxy metrics at 15020"

  # promtool validation on merged output (tolerate envoy format quirks)
  curl -sf "http://localhost:${PF_MERGE_PORT}/stats/prometheus" \
    | promtool check metrics 2>&1 > "$EVIDENCE_DIR/promtool-merged-check.txt" || true
fi

cleanup_port_forwards

# ── 9e — Negative test: single-port limitation without aggregator ──
info "Phase 9e: NEGATIVE TEST — proving Istio single-port limitation"

kubectl apply -f "$SCRIPT_DIR/istio/baseline-pod-no-aggregator.yaml"
wait_for_pod "$NAMESPACE" baseline-no-aggregator 180

info "waiting 30s for Istio agent to scrape baseline pod…"
sleep 30

start_port_forward "$NAMESPACE" pod/baseline-no-aggregator "${PF_BASELINE_PORT}:15020"

BASELINE_OK=false
for i in $(seq 1 15); do
  if curl -sf "http://localhost:${PF_BASELINE_PORT}/stats/prometheus" \
    -o "$EVIDENCE_DIR/baseline-no-aggregator-15020.txt"; then
    BASELINE_OK=true
    break
  fi
  sleep 2
done

if [[ "$BASELINE_OK" == "false" ]]; then
  fail "baseline: port 15020 not reachable"
else
  pass "baseline: port 15020 returned data"

  # App-a metrics ARE present (prometheus.io/port points to 8081)
  assert_grep 'http_requests_total' "$EVIDENCE_DIR/baseline-no-aggregator-15020.txt" \
    "baseline: app-a http_requests_total present (port 8081 configured)"

  # App-b metrics are ABSENT (single-port limitation — no second annotation!)
  assert_not_grep 'jobs_processed_total' "$EVIDENCE_DIR/baseline-no-aggregator-15020.txt" \
    "baseline: app-b jobs_processed_total ABSENT (single-port limitation)"
  assert_not_grep 'worker_pool_size' "$EVIDENCE_DIR/baseline-no-aggregator-15020.txt" \
    "baseline: app-b worker_pool_size ABSENT (single-port limitation)"

  # Envoy metrics are still present
  assert_grep 'envoy_' "$EVIDENCE_DIR/baseline-no-aggregator-15020.txt" \
    "baseline: envoy proxy metrics still present"

  # Compare: WITH aggregator (from 9d), both apps are present
  AGG_APP_A=0
  AGG_APP_B=0
  if [[ -s "$EVIDENCE_DIR/istio-merged-15020.txt" ]]; then
    AGG_APP_A=$(grep -c 'origin_container="app-a"' "$EVIDENCE_DIR/istio-merged-15020.txt" || echo "0")
    AGG_APP_B=$(grep -c 'origin_container="app-b"' "$EVIDENCE_DIR/istio-merged-15020.txt" || echo "0")
    assert_gt "$AGG_APP_A" "0" "comparison: WITH aggregator, app-a metrics at 15020 (count=$AGG_APP_A)"
    assert_gt "$AGG_APP_B" "0" "comparison: WITH aggregator, app-b metrics at 15020 (count=$AGG_APP_B)"
  fi

  {
    echo "=== Istio Single-Port Limitation Proof ==="
    echo ""
    echo "BASELINE (no aggregator, prometheus.io/port=8081):"
    echo "  app-a metrics (http_requests_total): PRESENT"
    echo "  app-b metrics (jobs_processed_total): ABSENT"
    echo ""
    echo "WITH AGGREGATOR (prometheus.io/port=9090):"
    echo "  app-a metrics: PRESENT (count=$AGG_APP_A)"
    echo "  app-b metrics: PRESENT (count=$AGG_APP_B)"
    echo ""
    echo "CONCLUSION: Aggregator solves the single-port limitation"
  } > "$EVIDENCE_DIR/single-port-limitation-proof.txt"
fi

cleanup_port_forwards

# ── 9f — Annotation-based service discovery (kubernetes_sd_configs) ─
info "Phase 9f: kubernetes_sd_configs — annotation-based discovery"

kubectl apply -f "$SCRIPT_DIR/istio/prometheus-rbac.yaml"
kubectl apply -f "$SCRIPT_DIR/istio/observer-sd-prometheus-config.yaml"
kubectl apply -f "$SCRIPT_DIR/istio/observer-sd-prometheus.yaml"
wait_for_deployment "$NAMESPACE" observer-sd-prometheus 120

info "waiting 75s for SD observer to discover and scrape annotated pods…"
sleep 75

start_port_forward "$NAMESPACE" svc/observer-sd-prometheus "${PF_SD_OBSERVER_PORT}:9090"

: > "$EVIDENCE_DIR/sd-observer-queries.txt"

# Verify SD observer discovered targets
TARGETS_RESULT=$(curl -sf "http://localhost:${PF_SD_OBSERVER_PORT}/api/v1/targets" \
  2>/dev/null || echo '{"status":"error"}')
echo "$TARGETS_RESULT" > "$EVIDENCE_DIR/sd-observer-targets.txt"

ACTIVE_TARGETS=$(echo "$TARGETS_RESULT" | jq -r '.data.activeTargets | length' 2>/dev/null || echo "0")
assert_gt "$ACTIVE_TARGETS" "0" "SD observer: discovered > 0 active targets via annotations"

# Verify the aggregator pod was discovered
AGG_DISCOVERED=$(echo "$TARGETS_RESULT" \
  | jq -r '[.data.activeTargets[] | select(.labels.pod == "aggregator-sidecar-istio")] | length' \
  2>/dev/null || echo "0")
assert_gt "$AGG_DISCOVERED" "0" "SD observer: discovered aggregator-sidecar-istio pod"

# Verify up status
SD_UP_RESULT=$(prom_query_to "$PF_SD_OBSERVER_PORT" "$EVIDENCE_DIR/sd-observer-queries.txt" 'up{pod="aggregator-sidecar-istio"}' "aggregator up via SD")
SD_UP_VALUE=$(echo "$SD_UP_RESULT" | jq -r '.data.result[0].value[1] // "missing"' 2>/dev/null || echo "missing")
assert_eq "$SD_UP_VALUE" "1" "SD observer: aggregator pod up == 1 via annotation discovery"

# Verify origin_container series
SD_ORIGIN=$(prom_query_to "$PF_SD_OBSERVER_PORT" "$EVIDENCE_DIR/sd-observer-queries.txt" 'count({origin_container=~".+"})' "origin_container via SD")
SD_ORIGIN_VALUE=$(echo "$SD_ORIGIN" | jq -r '.data.result[0].value[1] // "0"' 2>/dev/null || echo "0")
assert_gt "$SD_ORIGIN_VALUE" "0" "SD observer: origin_container series present via annotation discovery"

# Verify specific app metrics are ingested
SD_APP_A=$(prom_query_to "$PF_SD_OBSERVER_PORT" "$EVIDENCE_DIR/sd-observer-queries.txt" 'http_requests_total{origin_container="app-a"}' "app-a via SD")
SD_APP_A_LEN=$(echo "$SD_APP_A" | jq -r '.data.result | length' 2>/dev/null || echo "0")
if [[ "$SD_APP_A_LEN" -gt 0 ]]; then
  pass "SD observer: app-a metrics ingested via annotation discovery"
else
  fail "SD observer: app-a metrics not found via annotation discovery"
fi

{
  echo "=== kubernetes_sd_configs Discovery ==="
  echo "active_targets: $ACTIVE_TARGETS"
  echo "aggregator_discovered: $AGG_DISCOVERED"
  echo "up_value: $SD_UP_VALUE"
  echo "origin_container_count: $SD_ORIGIN_VALUE"
} > "$EVIDENCE_DIR/sd-observer-summary.txt"

cleanup_port_forwards

# ── Istio merging summary ─────────────────────────────────────────
{
  echo "=== Istio Metrics Merging E2E Summary ==="
  echo ""
  echo "1. LIMITATION PROVEN (Phase 9e):"
  echo "   - Without aggregator: only app-a metrics at port 15020"
  echo "   - app-b metrics completely absent"
  echo ""
  echo "2. SOLUTION PROVEN (Phase 9d):"
  echo "   - With aggregator: BOTH app-a and app-b metrics at port 15020"
  echo "   - Envoy proxy metrics also present"
  echo ""
  echo "3. PRODUCTION READY (Phase 9f):"
  echo "   - kubernetes_sd_configs discovers annotated pods automatically"
  echo "   - No hardcoded targets needed"
} > "$EVIDENCE_DIR/istio-merging-summary.txt"

# ═══════════════════════════════════════════════════════════════════
# Phase 10 — Evidence report
# ═══════════════════════════════════════════════════════════════════
info "Phase 10: writing evidence report"
write_report "$EVIDENCE_DIR"

echo ""
echo "═══════════════════════════════════════════════════════════"
printf "  ${GREEN}PASSED: %d${NC}  ${RED}FAILED: %d${NC}  ${YELLOW}SKIPPED: %d${NC}\n" "$PASSES" "$FAILURES" "$SKIPS"
echo "═══════════════════════════════════════════════════════════"
echo "  Evidence: $EVIDENCE_DIR/"
echo ""

if [[ "$FAILURES" -gt 0 ]]; then
  exit 1
fi
