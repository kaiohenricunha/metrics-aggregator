#!/usr/bin/env bash
# lib.sh — shared helpers for the metrics-aggregator K8s E2E test suite

set -euo pipefail

# ── Colours ──────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Colour

# ── Counters ─────────────────────────────────────────────────────
PASSES=0
FAILURES=0
SKIPS=0

# ── Report lines (appended by pass/fail/skip) ───────────────────
REPORT_LINES=()

pass() {
  local desc="$1"
  (( PASSES++ )) || true
  REPORT_LINES+=("PASS  $desc")
  printf "${GREEN}PASS${NC}  %s\n" "$desc"
}

fail() {
  local desc="$1"
  (( FAILURES++ )) || true
  REPORT_LINES+=("FAIL  $desc")
  printf "${RED}FAIL${NC}  %s\n" "$desc" >&2
}

skip() {
  local desc="$1"
  (( SKIPS++ )) || true
  REPORT_LINES+=("SKIP  $desc")
  printf "${YELLOW}SKIP${NC}  %s\n" "$desc"
}

info() {
  printf "${CYAN}INFO${NC}  %s\n" "$1"
}

# ── Assertions ───────────────────────────────────────────────────

# assert_grep PATTERN FILE DESCRIPTION
assert_grep() {
  local pattern="$1" file="$2" desc="$3"
  if grep -q "$pattern" "$file"; then
    pass "$desc"
  else
    fail "$desc (pattern: $pattern)"
  fi
}

# assert_not_grep PATTERN FILE DESCRIPTION
assert_not_grep() {
  local pattern="$1" file="$2" desc="$3"
  if ! grep -q "$pattern" "$file"; then
    pass "$desc"
  else
    fail "$desc (unexpected pattern found: $pattern)"
  fi
}

# assert_eq ACTUAL EXPECTED DESCRIPTION
assert_eq() {
  local actual="$1" expected="$2" desc="$3"
  if [[ "$actual" == "$expected" ]]; then
    pass "$desc"
  else
    fail "$desc (got '$actual', expected '$expected')"
  fi
}

# assert_lt ACTUAL THRESHOLD DESCRIPTION  (numeric: actual < threshold)
assert_lt() {
  local actual="$1" threshold="$2" desc="$3"
  if awk "BEGIN{exit(!($actual < $threshold))}"; then
    pass "$desc"
  else
    fail "$desc (got $actual, expected < $threshold)"
  fi
}

# assert_gt ACTUAL THRESHOLD DESCRIPTION  (numeric: actual > threshold)
assert_gt() {
  local actual="$1" threshold="$2" desc="$3"
  if awk "BEGIN{exit(!($actual > $threshold))}"; then
    pass "$desc"
  else
    fail "$desc (got $actual, expected > $threshold)"
  fi
}

# ── Port-forward management ──────────────────────────────────────
PF_PIDS=()

# start_port_forward NAMESPACE RESOURCE LOCAL_PORT:REMOTE_PORT
start_port_forward() {
  local ns="$1" resource="$2" ports="$3"
  kubectl port-forward -n "$ns" "$resource" "$ports" &>/dev/null &
  local pid=$!
  PF_PIDS+=("$pid")
  # Give the port-forward a moment to establish
  sleep 2
  info "port-forward $resource $ports (PID $pid)"
}

cleanup_port_forwards() {
  for pid in "${PF_PIDS[@]:-}"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  PF_PIDS=()
}

# ── Wait helpers ─────────────────────────────────────────────────

# wait_for_pod NAMESPACE POD_NAME TIMEOUT_SECONDS
wait_for_pod() {
  local ns="$1" pod="$2" timeout="${3:-90}"
  info "waiting for pod $pod in $ns (timeout ${timeout}s)…"
  kubectl wait --for=condition=Ready "pod/$pod" -n "$ns" --timeout="${timeout}s"
}

# wait_for_deployment NAMESPACE DEPLOYMENT TIMEOUT_SECONDS
wait_for_deployment() {
  local ns="$1" deploy="$2" timeout="${3:-120}"
  info "waiting for deployment $deploy in $ns (timeout ${timeout}s)…"
  kubectl rollout status "deployment/$deploy" -n "$ns" --timeout="${timeout}s"
}

# ── Prerequisites ────────────────────────────────────────────────

# require_cmd CMD
require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" &>/dev/null; then
    echo "ERROR: '$cmd' is required but not found on PATH" >&2
    return 1
  fi
}

# ── Auto-install helpers ─────────────────────────────────────────

# ensure_promtool — download promtool to /tmp if not already on PATH
ensure_promtool() {
  if command -v promtool &>/dev/null; then
    info "promtool found at $(command -v promtool)"
    return 0
  fi
  info "promtool not found — downloading v2.53.0…"
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64)  arch="amd64" ;;
    aarch64) arch="arm64" ;;
  esac
  local tarball="prometheus-2.53.0.${os}-${arch}.tar.gz"
  local base_url="https://github.com/prometheus/prometheus/releases/download/v2.53.0"
  local url="${base_url}/${tarball}"
  local checksum_url="${base_url}/sha256sums.txt"
  local tmp_tar; tmp_tar="$(mktemp /tmp/promtool-XXXXXX.tar.gz)"
  local tmp_sums; tmp_sums="$(mktemp /tmp/promtool-sums-XXXXXX.txt)"
  curl -fsSL "$url" -o "$tmp_tar"
  curl -fsSL "$checksum_url" -o "$tmp_sums"
  # Verify checksum — locate the tarball entry and verify it explicitly
  local checksum_line
  checksum_line="$(awk -v t="$tarball" '$2 == t { print; exit }' "$tmp_sums")"
  if [[ -z "$checksum_line" ]]; then
    echo "ERROR: checksum entry for ${tarball} not found in sha256sums.txt" >&2
    rm -f "$tmp_tar" "$tmp_sums"
    return 1
  fi
  local expected
  expected="$(printf '%s\n' "$checksum_line" | awk '{print $1}')"
  if ! printf '%s  %s\n' "$expected" "$tmp_tar" | sha256sum -c --status; then
    echo "ERROR: checksum verification failed for ${tarball}" >&2
    rm -f "$tmp_tar" "$tmp_sums"
    return 1
  fi
  rm -f "$tmp_sums"
  tar xz --strip-components=1 -C /tmp "prometheus-2.53.0.${os}-${arch}/promtool" < "$tmp_tar"
  rm -f "$tmp_tar"
  chmod +x /tmp/promtool
  export PATH="/tmp:$PATH"
  info "promtool installed to /tmp/promtool"
}

# ensure_istioctl — download istioctl to /tmp if not already on PATH
ensure_istioctl() {
  if command -v istioctl &>/dev/null; then
    info "istioctl found at $(command -v istioctl)"
    return 0
  fi
  info "istioctl not found — downloading 1.24.2…"
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64)  arch="amd64" ;;
    aarch64) arch="arm64" ;;
  esac
  local tarball="istioctl-1.24.2-${os}-${arch}.tar.gz"
  local base_url="https://github.com/istio/istio/releases/download/1.24.2"
  local url="${base_url}/${tarball}"
  local checksum_url="${base_url}/${tarball}.sha256"
  local tmp_tar; tmp_tar="$(mktemp /tmp/istioctl-XXXXXX.tar.gz)"
  local tmp_sum; tmp_sum="$(mktemp /tmp/istioctl-sum-XXXXXX.txt)"
  curl -fsSL "$url" -o "$tmp_tar"
  curl -fsSL "$checksum_url" -o "$tmp_sum"
  # Verify checksum — the .sha256 file contains just the hex digest
  local expected; expected="$(cat "$tmp_sum" | awk '{print $1}')"
  local actual; actual="$(sha256sum "$tmp_tar" | awk '{print $1}')"
  if [[ "$expected" != "$actual" ]]; then
    echo "ERROR: checksum verification failed for ${tarball}" >&2
    rm -f "$tmp_tar" "$tmp_sum"
    return 1
  fi
  rm -f "$tmp_sum"
  tar xz -C /tmp istioctl < "$tmp_tar"
  rm -f "$tmp_tar"
  chmod +x /tmp/istioctl
  export PATH="/tmp:$PATH"
  info "istioctl installed to /tmp/istioctl"
}

# ── Prometheus query helper ───────────────────────────────────────

# prom_query_to PORT OUTPUT_FILE QUERY DESCRIPTION
# Queries a Prometheus instance and appends evidence to the output file.
prom_query_to() {
  local port="$1" output_file="$2" query="$3" desc="$4"
  local result
  result=$(curl -sf "http://localhost:${port}/api/v1/query" \
    --data-urlencode "query=$query" 2>/dev/null || echo '{"status":"error"}')
  echo "--- $desc ---" >> "$output_file"
  echo "query: $query" >> "$output_file"
  echo "result: $result" >> "$output_file"
  echo "" >> "$output_file"
  echo "$result"
}

# ── Report ───────────────────────────────────────────────────────
write_report() {
  local dir="$1"
  {
    echo "=== metrics-aggregator K8s E2E Test Report ==="
    echo "Date: $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    echo ""
    printf "%s\n" "${REPORT_LINES[@]}"
    echo ""
    echo "--- Summary ---"
    echo "PASSED:  $PASSES"
    echo "FAILED:  $FAILURES"
    echo "SKIPPED: $SKIPS"
    echo "TOTAL:   $(( PASSES + FAILURES + SKIPS ))"
  } > "$dir/report.txt"
  info "report written to $dir/report.txt"
}
