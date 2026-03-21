# Audit Findings — Implementation Spec

**Generated:** 2026-03-20
**Scope:** Full codebase audit — bugs, security, enhancements
**Total findings:** 20 (2 High, 5 Medium, 13 Low/Info)

Each section defines the **problem**, the **exact change**, and the **acceptance criteria**. Findings are ordered by execution priority within each severity tier.

---

## Table of Contents

| ID | Title | Severity | Category | File |
|---|---|---|---|---|
| [SEC-01](#sec-01) | SSRF via legacy security mode | High | Security | `aggregator.go` |
| [SEC-02](#sec-02) | No custom TLS config for HTTPS scrape targets | High | Security | `aggregator.go` |
| [BUG-01](#bug-01) | Histogram sum/count not atomically consistent | Medium | Bug | `histogram.go` |
| [BUG-03](#bug-03) | `sampleLine` regex rejects valid metrics with `}` in label values | Medium | Bug | `aggregator.go:40` |
| [SEC-03](#sec-03) | E2E binary downloads lack checksum verification | Medium | Security | `test/e2e/lib.sh` |
| [SEC-04](#sec-04) | Dockerfile uses mutable image tags | Medium | Security | `Dockerfile` |
| [ENH-01](#enh-01) | No cache hit/miss metrics | Medium | Enhancement | `main.go` |
| [ENH-02](#enh-02) | No build version info metric | Medium | Enhancement | `Dockerfile`, `main.go` |
| [BUG-02](#bug-02) | Oversized body incorrectly increments `scrape_errors_total` | Low | Bug | `aggregator.go` |
| [BUG-04](#bug-04) | `X-Request-Id` header not length/character validated | Low | Bug/Security | `main.go` |
| [SEC-05](#sec-05) | JSON parse error log reflects config structure | Low | Security | `aggregator.go:95-98` |
| [SEC-06](#sec-06) | `METRICS_AGGREGATOR_PORT` not validated | Low | Security | `main.go` |
| [SEC-07](#sec-07) | No listener bind-address restriction | Low | Security | `main.go` |
| [ENH-03](#enh-03) | `Histogram.Observe` is O(N) in bucket count | Low | Enhancement | `histogram.go` |
| [ENH-04](#enh-04) | No stale-cache-on-error option | Low | Enhancement | `main.go` |
| [ENH-05](#enh-05) | Test gap: `validateEndpointURL` HTTPS path | Low | Enhancement | `aggregator_test.go` |
| [ENH-06](#enh-06) | Test gap: `parseDurationEnv` negative / `parsePositiveIntEnv` zero | Low | Enhancement | `main_test.go` |
| [ENH-07](#enh-07) | Context cancellation does not cancel in-flight fetch | Low | Enhancement | `main.go` |
| [ENH-08](#enh-08) | `promtool` lint filter too broad | Low | Enhancement | `test/e2e/run.sh` |
| [ENH-09](#enh-09) | `docker-compose.yaml` uses `prom/prometheus:latest` | Low | Enhancement | `docker-compose.yaml` |

---

## HIGH

---

### SEC-01

**Title:** SSRF via legacy security mode
**Severity:** High | **Category:** Security

#### Problem

`validateEndpointURL` (`aggregator.go:404-406`) returns immediately after checking scheme and host when `METRICS_SECURITY_MODE=legacy`. All strict-mode guards (no userinfo, no query params, path must be `/metrics`, redirect block) are skipped. Any `http://` or `https://` URL is accepted verbatim, including AWS IMDS (`http://169.254.169.254/latest/meta-data/`), the Docker daemon socket proxy (`http://localhost:2376/v1.41/containers/json`), internal etcd, or any other service reachable from the pod network namespace. An operator — or a compromised Helm values file — who sets `METRICS_SECURITY_MODE=legacy` opens an SSRF path with no IP-range restriction whatsoever.

```go
// aggregator.go:404-406 — current
if securityMode == securityModeLegacy {
    return parsed.String(), nil  // early return, no guards
}
```

#### Change

1. **Add private/link-local IP block that runs in all security modes, including legacy.**

   After resolving the host to an IP (use `net.DefaultResolver.LookupIPAddr`), reject any address that falls in:
   - `169.254.0.0/16` (link-local / AWS IMDS)
   - `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` (RFC-1918 private) — **only in legacy mode** if operators explicitly need to reach pods by cluster IP; skip for strict mode where the path restriction already limits blast radius. Reconsider: since strict mode restricts to `/metrics` path only, RFC-1918 is still reachable. Apply the IMDS block (`169.254.0.0/16`) unconditionally in both modes, and add an option `METRICS_ALLOW_PRIVATE_IPS=true` to opt out for legitimate on-cluster scraping.

   **Simpler, correct approach** (preferred):
   - In legacy mode, after confirming scheme and host, still reject `169.254.0.0/16` (link-local) addresses unconditionally — this blocks IMDS on all major clouds.
   - Add a deprecation log: `log.Warn().Msg("METRICS_SECURITY_MODE=legacy is deprecated and will be removed in a future version; switch to strict mode")`.

2. **Block redirects in legacy mode.** Legacy mode currently allows redirects (`CheckRedirect` is only set for non-legacy). Add the same `http.ErrUseLastResponse` redirect block unconditionally regardless of security mode.

3. **Add `# Deprecated` comment to `securityModeLegacy` constant.**

```go
// aggregator.go — proposed change to validateEndpointURL
if securityMode == securityModeLegacy {
    log.Warn().Msg("METRICS_SECURITY_MODE=legacy is deprecated; switch to strict mode")
    if err := rejectLinkLocal(parsed.Host); err != nil {
        return "", err
    }
    return parsed.String(), nil
}
```

```go
// new helper — rejectLinkLocal resolves the host and blocks 169.254.0.0/16
func rejectLinkLocal(host string) error {
    hostname, _, err := net.SplitHostPort(host)
    if err != nil {
        hostname = host
    }
    addrs, err := net.DefaultResolver.LookupIPAddr(context.Background(), hostname)
    if err != nil {
        return nil // let the actual HTTP request fail naturally
    }
    linkLocal := net.IPNet{IP: net.IP{169, 254, 0, 0}, Mask: net.CIDRMask(16, 32)}
    for _, a := range addrs {
        if linkLocal.Contains(a.IP.To4()) {
            return fmt.Errorf("link-local address %s is not allowed", a.IP)
        }
    }
    return nil
}
```

4. **Redirect block — move outside the security-mode branch:**

```go
// aggregator.go:128-132 — move to unconditional
client := &http.Client{
    Timeout: 5 * time.Second,
    CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
        return http.ErrUseLastResponse
    },
}
// delete the `if securityMode != securityModeLegacy` wrapper
```

#### Acceptance Criteria

- `TestValidateEndpointURL_LegacyBlocksLinkLocal`: `http://169.254.169.254/latest/meta-data/` rejected in legacy mode.
- `TestValidateEndpointURL_LegacyLogsDeprecation`: deprecation warning is logged when legacy mode is active.
- `TestAggregateMetrics_LegacyNoRedirectFollow`: a redirect to `http://169.254.169.254` is not followed in legacy mode.
- Existing tests for strict mode continue to pass.
- `/check` passes.

---

### SEC-02

**Title:** No custom TLS configuration for HTTPS scrape targets
**Severity:** High | **Category:** Security

#### Problem

The `http.Client` (`aggregator.go:127`) uses Go's default TLS configuration. There is no mechanism to supply a custom CA bundle or client certificates. Deployments using self-signed or internal-CA-signed HTTPS on `/metrics` endpoints see silent TLS handshake errors that are indistinguishable from "endpoint down". There is no `METRICS_SCRAPE_TLS_*` configuration.

#### Change

Add three optional environment variables read at `NewAggregator` time:

| Variable | Description |
|---|---|
| `METRICS_SCRAPE_TLS_CACERT` | Path to PEM CA bundle for verifying scrape targets |
| `METRICS_SCRAPE_TLS_CERT` | Path to PEM client certificate (mTLS) |
| `METRICS_SCRAPE_TLS_KEY` | Path to PEM client private key (mTLS) |
| `METRICS_SCRAPE_TLS_INSECURE_SKIP_VERIFY` | `"true"` disables server cert verification (last resort, logs a warning) |

Implementation:

```go
// pkg/aggregator/tls.go (new file)
package aggregator

import (
    "crypto/tls"
    "crypto/x509"
    "fmt"
    "os"
)

func buildTLSConfig() (*tls.Config, error) {
    cfg := &tls.Config{MinVersion: tls.VersionTLS12}

    if caPath := os.Getenv("METRICS_SCRAPE_TLS_CACERT"); caPath != "" {
        pem, err := os.ReadFile(caPath)
        if err != nil {
            return nil, fmt.Errorf("reading METRICS_SCRAPE_TLS_CACERT: %w", err)
        }
        pool := x509.NewCertPool()
        if !pool.AppendCertsFromPEM(pem) {
            return nil, fmt.Errorf("no valid certificates found in METRICS_SCRAPE_TLS_CACERT")
        }
        cfg.RootCAs = pool
    }

    certPath := os.Getenv("METRICS_SCRAPE_TLS_CERT")
    keyPath := os.Getenv("METRICS_SCRAPE_TLS_KEY")
    if certPath != "" || keyPath != "" {
        if certPath == "" || keyPath == "" {
            return nil, fmt.Errorf("METRICS_SCRAPE_TLS_CERT and METRICS_SCRAPE_TLS_KEY must both be set")
        }
        cert, err := tls.LoadX509KeyPair(certPath, keyPath)
        if err != nil {
            return nil, fmt.Errorf("loading TLS client cert/key: %w", err)
        }
        cfg.Certificates = []tls.Certificate{cert}
    }

    if os.Getenv("METRICS_SCRAPE_TLS_INSECURE_SKIP_VERIFY") == "true" {
        log.Warn().Msg("METRICS_SCRAPE_TLS_INSECURE_SKIP_VERIFY=true: TLS verification disabled — not recommended for production")
        cfg.InsecureSkipVerify = true //nolint:gosec // intentional, operator opt-in
    }

    return cfg, nil
}
```

Wire it into `NewAggregator`:

```go
// aggregator.go — replace client construction
tlsCfg, err := buildTLSConfig()
if err != nil {
    return nil, fmt.Errorf("TLS configuration error: %w", err)
}
transport := &http.Transport{TLSClientConfig: tlsCfg}
client := &http.Client{
    Timeout:   5 * time.Second,
    Transport: transport,
    CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
        return http.ErrUseLastResponse
    },
}
```

#### Acceptance Criteria

- `TestNewAggregator_TLSCACert`: `NewAggregator` fails fast with a clear error if `METRICS_SCRAPE_TLS_CACERT` points to a nonexistent file.
- `TestNewAggregator_TLSCertWithoutKey`: fails with a clear error when only `METRICS_SCRAPE_TLS_CERT` is set.
- `TestAggregateMetrics_HTTPSEndpoint`: uses `httptest.NewTLSServer` — succeeds when `METRICS_SCRAPE_TLS_INSECURE_SKIP_VERIFY=true`, fails with the default client (demonstrating the feature is needed).
- `CLAUDE.md` environment variables table updated with the four new vars.
- `/check` passes.

---

## MEDIUM

---

### BUG-01

**Title:** Histogram sum/count not atomically consistent
**Severity:** Medium | **Category:** Bug

#### Problem

`Histogram.Observe` (`histogram.go:48-56`) updates bucket counters via `atomic.Int64.Add` (no lock) and then acquires `h.mu` to update `h.sum`. A concurrent `RenderSamples` call can snapshot `h.sum` under `h.mu` and then read `+Inf` bucket count via `atomic.Load` — but these two reads are not atomic with respect to each other. An `Observe` that completed its `atomic.Add` but hasn't yet updated `h.sum` will cause `_count` to be N while `_sum` reflects only N-1 observations. Prometheus does not fail on this, but it produces a permanently incorrect `rate(…_sum)` / `rate(…_count)` ratio and corrupts histogram-derived quantile estimates.

#### Change

Unify all per-observation state under a single mutex. Since the histogram only has 11 default buckets (12 with +Inf), lock contention is negligible compared to the HTTP scrape overhead.

```go
// histogram.go — revised struct
type Histogram struct {
    bounds []float64
    mu     sync.Mutex
    counts []int64  // protected by mu; change from []atomic.Int64
    sum    float64  // protected by mu
}

// NewHistogram — replace atomic slice with plain int64 slice
return &Histogram{
    bounds: bounds,
    counts: make([]int64, len(bounds)),
}

// Observe — single lock covers both bucket increments and sum
func (h *Histogram) Observe(v float64) {
    h.mu.Lock()
    for i, b := range h.bounds {
        if v <= b {
            h.counts[i]++
        }
    }
    h.sum += v
    h.mu.Unlock()
}

// Count — read under lock
func (h *Histogram) Count() int64 {
    h.mu.Lock()
    defer h.mu.Unlock()
    return h.counts[len(h.counts)-1]
}

// RenderSamples — take a single snapshot under lock
func RenderSamples(name string, labels string, h *Histogram) string {
    h.mu.Lock()
    counts := make([]int64, len(h.counts))
    copy(counts, h.counts)
    sum := h.sum
    count := h.counts[len(h.counts)-1]
    h.mu.Unlock()
    // render from local copies — no further locking needed
    ...
}
```

**Note:** This change must be coordinated with ENH-03 (binary search optimisation) — implement both together and apply the binary search inside the mutex-protected `Observe`.

#### Acceptance Criteria

- `TestHistogram_ConcurrentObserveAndRender`: 100 goroutines each call `Observe(1.0)` 1000 times concurrently while a reader calls `RenderSamples` repeatedly. At the end, `_count` == `_sum` == 100000. Use `-race` flag.
- All existing `histogram_test.go` tests continue to pass.
- `go test -race ./pkg/aggregator/...` reports no data races.

---

### BUG-03

**Title:** `sampleLine` regex rejects valid metrics with `}` in label values
**Severity:** Medium | **Category:** Bug

#### Problem

The `sampleLine` regex (`aggregator.go:40`) uses `(\{[^}]*\})?` to match the optional label block. The character class `[^}]*` stops at the first unescaped `}`, but Prometheus exposition format allows `}` inside quoted label values when escaped as `\}`. Example of a valid metric line that would be silently dropped:

```
http_requests_total{status="5xx\\}escaped"} 42
```

The line passes `addCustomLabel` (which uses `strings.Contains` / `strings.Replace`, not the regex), but the post-injection `isValidPrometheusSampleLine` check rejects it, incrementing `invalidSamples` with no actionable error for the operator.

#### Change

Replace `[^}]*` with a pattern that properly handles quoted label values:

```go
// aggregator.go:40 — replace sampleLine definition
sampleLine = regexp.MustCompile(
    `^[a-zA-Z_:][a-zA-Z0-9_:]*` +
    `(\{(?:[^"{}\\]|"(?:[^"\\]|\\.)*")*\})?` +
    `\s+(?:NaN|[+-]?Inf|[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)` +
    `(?:\s+\d+)?$`,
)
```

Pattern breakdown:
- `(?:[^"{}\\]|"(?:[^"\\]|\\.)*")*` — inside the label block, match either: a character that is not a quote, brace, or backslash; OR a fully-quoted string (handling `\"` and `\\` escapes).

#### Acceptance Criteria

- `TestIsValidPrometheusSampleLine_EscapedBrace`: a line like `http_requests_total{status="5xx\\}escaped"} 42` returns `true`.
- `TestIsValidPrometheusSampleLine_MultipleLabels`: existing tests continue to pass.
- `TestAggregateMetrics_EscapedBraceInLabel`: end-to-end test with an `httptest` server that serves a metric with `\}` in a label value — the line appears in the aggregated output (not dropped as invalid).
- `go test -race ./pkg/aggregator/...` passes.

---

### SEC-03

**Title:** E2E binary downloads lack checksum verification
**Severity:** Medium | **Category:** Security

#### Problem

`ensure_promtool` and `ensure_istioctl` in `test/e2e/lib.sh` (lines 164-168, 185-188) download tarballs from GitHub Releases and pipe them directly to `tar xz` with no integrity check:

```bash
curl -sL "$url" | tar xz --strip-components=1 -C /tmp "prometheus-2.53.0.${os}-${arch}/promtool"
curl -sL "$url" | tar xz -C /tmp istioctl
```

A compromised GitHub Releases CDN, a BGP hijack, or DNS spoofing on a CI runner would result in arbitrary code execution with access to all CI secrets (`GITHUB_TOKEN`, `CODECOV_TOKEN`).

#### Change

For `ensure_promtool` — Prometheus publishes `sha256sums.txt` in every release:

```bash
ensure_promtool() {
  ...
  local tarball="prometheus-2.53.0.${os}-${arch}.tar.gz"
  local base_url="https://github.com/prometheus/prometheus/releases/download/v2.53.0"
  local url="${base_url}/${tarball}"
  # Published at: https://github.com/prometheus/prometheus/releases/download/v2.53.0/sha256sums.txt
  local expected_sha256
  expected_sha256=$(curl -sL "${base_url}/sha256sums.txt" | grep " ${tarball}$" | awk '{print $1}')
  if [[ -z "$expected_sha256" ]]; then
    echo "ERROR: could not fetch checksum for $tarball" >&2
    return 1
  fi

  local tmpfile
  tmpfile=$(mktemp /tmp/promtool-XXXXXX.tar.gz)
  curl -sL "$url" -o "$tmpfile"
  echo "${expected_sha256}  ${tmpfile}" | sha256sum -c - || {
    echo "ERROR: checksum mismatch for $tarball" >&2
    rm -f "$tmpfile"
    return 1
  }
  tar xz --strip-components=1 -C /tmp "$tmpfile" "prometheus-2.53.0.${os}-${arch}/promtool"
  rm -f "$tmpfile"
  ...
}
```

For `ensure_istioctl` — Istio 1.24.2 publishes `istioctl-1.24.2-linux-amd64.tar.gz.sha256` alongside each tarball:

```bash
ensure_istioctl() {
  ...
  local base_url="https://github.com/istio/istio/releases/download/1.24.2"
  local url="${base_url}/${tarball}"
  local sha_url="${url}.sha256"

  local tmpfile
  tmpfile=$(mktemp /tmp/istioctl-XXXXXX.tar.gz)
  curl -sL "$url" -o "$tmpfile"
  local expected_sha256
  expected_sha256=$(curl -sL "$sha_url" | awk '{print $1}')
  echo "${expected_sha256}  ${tmpfile}" | sha256sum -c - || {
    echo "ERROR: checksum mismatch for $tarball" >&2
    rm -f "$tmpfile"
    return 1
  }
  tar xz -C /tmp "$tmpfile" istioctl
  rm -f "$tmpfile"
  ...
}
```

**Note:** `sha256sum` is available on all Linux CI runners. Add `require_cmd sha256sum` to the prerequisites check at the top of `run.sh`.

#### Acceptance Criteria

- Both functions download to a temp file and verify checksum before extracting.
- Any checksum mismatch causes the function to `return 1` (not silently continue).
- The `e2e` CI job (`e2e.yml`) still passes.

---

### SEC-04

**Title:** Dockerfile uses mutable image tags
**Severity:** Medium | **Category:** Security

#### Problem

`Dockerfile` lines 3 and 12:

```dockerfile
FROM golang:${GO_VERSION}-alpine AS builder   # mutable: tag can be pushed over
FROM alpine:3.20                               # mutable: patch updates not guaranteed
```

A supply-chain attacker who pushes a malicious layer under these tags would be silently incorporated on the next `docker build --no-cache`. GitHub Actions workflow steps are SHA-pinned (good), but the image build itself is not.

#### Change

Pin both base images by digest. Use `docker buildx imagetools inspect` to get the current digests:

```dockerfile
# Stage 1 — builder
ARG GO_VERSION=1.26.1
FROM golang:${GO_VERSION}-alpine@sha256:<digest-for-golang-1.26.1-alpine> AS builder

# Stage 2 — runtime
FROM alpine:3.20@sha256:<digest-for-alpine-3.20>
```

**Process:**

```bash
# Get digests (run once, commit the result)
docker buildx imagetools inspect golang:1.26.1-alpine --format '{{json .Manifest.Digest}}'
docker buildx imagetools inspect alpine:3.20 --format '{{json .Manifest.Digest}}'
```

Add a Dependabot `docker` ecosystem entry to `.github/dependabot.yml` so digest updates are proposed automatically:

```yaml
# .github/dependabot.yml (add alongside existing go-modules entry)
- package-ecosystem: docker
  directory: "/"
  schedule:
    interval: weekly
```

#### Acceptance Criteria

- Both `FROM` lines include `@sha256:<digest>`.
- `docker build .` succeeds locally.
- `docker-publish.yml` CI job still builds and pushes the image.
- `.github/dependabot.yml` includes the `docker` ecosystem entry.

---

### ENH-01

**Title:** No cache hit/miss metrics
**Severity:** Medium | **Category:** Enhancement

#### Problem

`metricsCache` (`main.go:66-72`) has no observability. Operators cannot determine whether `METRICS_CACHE_TTL` is effective, how often requests are being coalesced via the `inFlight` channel, or what the cache hit rate is. This makes tuning `METRICS_CACHE_TTL` and `METRICS_MAX_INFLIGHT` entirely data-free.

#### Change

Add two counters to `metricsCache` and emit them in the handler output:

```go
// main.go — extend metricsCache struct
type metricsCache struct {
    ttl       time.Duration
    mu        sync.Mutex
    value     string
    expiresAt time.Time
    inFlight  chan struct{}
    hits      atomic.Int64  // incremented when cache value is served
    misses    atomic.Int64  // incremented when a new fetch is launched
}
```

In `getOrFetch`:

```go
// cache hit branch (line 187-190)
if c.ttl > 0 && c.value != "" && time.Now().Before(c.expiresAt) {
    value := c.value
    c.hits.Add(1)      // <-- add
    c.mu.Unlock()
    return value, nil
}
// fetch launch branch (line 192-195)
if c.inFlight == nil {
    c.inFlight = make(chan struct{})
    c.misses.Add(1)    // <-- add
    c.mu.Unlock()
    break
}
```

Emit in the handler (alongside `http_requests_total`):

```go
metrics += fmt.Sprintf(
    "# HELP metrics_aggregator_cache_hits_total Total number of /metrics requests served from cache.\n"+
        "# TYPE metrics_aggregator_cache_hits_total counter\n"+
        "metrics_aggregator_cache_hits_total %d\n"+
        "# HELP metrics_aggregator_cache_misses_total Total number of /metrics requests that triggered a new scrape.\n"+
        "# TYPE metrics_aggregator_cache_misses_total counter\n"+
        "metrics_aggregator_cache_misses_total %d\n",
    cache.hits.Load(),
    cache.misses.Load(),
)
```

Update `CLAUDE.md` self-instrumentation metrics list (add the two new counter families).

#### Acceptance Criteria

- `TestMetricsHandler_CacheHitsAndMisses`: two back-to-back requests within TTL produce `cache_hits_total=1` and `cache_misses_total=1`; a third request after TTL expiry produces `cache_misses_total=2`.
- Both metrics appear in the aggregator's `/metrics` output with correct `# HELP` and `# TYPE` headers.
- `go test -race ./...` passes.

---

### ENH-02

**Title:** No build version info metric
**Severity:** Medium | **Category:** Enhancement

#### Problem

The binary exposes no `metrics_aggregator_build_info` gauge. In a Kubernetes fleet with multiple pod versions running simultaneously (e.g., during a rolling upgrade), it is impossible to correlate an anomalous metric change with a specific release.

#### Change

**Step 1 — embed version at build time:**

```dockerfile
# Dockerfile:9 — replace the current go build line
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build \
    -ldflags="-X main.version=${VERSION} -X main.commitSHA=${COMMIT}" \
    -o metrics-aggregator .
```

In the `docker-publish.yml` workflow, pass the version:

```yaml
# .github/workflows/docker-publish.yml — add to docker/build-push-action inputs
build-args: |
  VERSION=${{ github.ref_name }}
  COMMIT=${{ github.sha }}
```

**Step 2 — expose in `main.go`:**

```go
// main.go — add package-level vars (set via -ldflags)
var (
    version   = "dev"
    commitSHA = "unknown"
)
```

**Step 3 — emit in the aggregator's self-instrumentation output (in `makeMetricsHandler`):**

```go
metrics += fmt.Sprintf(
    "# HELP metrics_aggregator_build_info Build information for the running binary.\n"+
        "# TYPE metrics_aggregator_build_info gauge\n"+
        "metrics_aggregator_build_info{version=%q,commit=%q} 1\n",
    version,
    commitSHA,
)
```

Update `CLAUDE.md` self-instrumentation metrics list and environment variables table (document `VERSION` and `COMMIT` as build args, not env vars).

#### Acceptance Criteria

- `go build -ldflags="-X main.version=v1.2.3 -X main.commitSHA=abc1234" .` produces a binary that serves `metrics_aggregator_build_info{version="v1.2.3",commit="abc1234"} 1`.
- `go build .` (no flags) serves `metrics_aggregator_build_info{version="dev",commit="unknown"} 1`.
- `docker build --build-arg VERSION=v1.2.3 --build-arg COMMIT=abc1234 .` produces an image that serves the correct build info.
- `TestMetricsHandler_BuildInfo`: verifies the metric appears in `/metrics` output.

---

## LOW

---

### BUG-02

**Title:** Oversized body incorrectly increments `scrape_errors_total`
**Severity:** Low | **Category:** Bug

#### Problem

When a scrape body exceeds `maxBodySize` (`aggregator.go:250-258`), the goroutine returns early with `failed` still `true` (its initialised value). The deferred block at lines 195-204 then calls `a.scrapeErrors[idx].Add(1)`. This conflates two distinct failure causes under the same counter: genuine HTTP errors vs. payload-too-large discards.

#### Change

Set `failed = false` for the oversized case and instead add a dedicated per-endpoint gauge (or use a separate log field). The simplest correct fix:

```go
// aggregator.go:250-258 — after the body size check
if len(body) > maxBodySize {
    logger.Warn().
        Str("endpoint", ep.Name).
        Str("url", sanitizedURL).
        Int("max_body_size", maxBodySize).
        Msg("scrape body exceeded size limit; discarding endpoint payload")
    failed = false  // not a scrape error — endpoint was reachable and responded
    results[idx] = scrapeResult{duration: time.Since(start)}
    return
}
```

If an operator wants to distinguish this case, add a `metrics_aggregator_scrape_body_too_large_total` counter (per-endpoint, same pattern as `scrape_errors_total`) and increment it here instead. Either approach is acceptable; choose based on whether the counter noise matters in practice.

#### Acceptance Criteria

- `TestAggregateMetrics_OversizedBody`: an endpoint that returns a body exactly 1 byte over `maxBodySize` does NOT increment `scrape_errors_total` for that endpoint.
- Existing oversized-body warn log still fires.
- `go test -race ./pkg/aggregator/...` passes.

---

### BUG-04

**Title:** `X-Request-Id` header not length/character validated
**Severity:** Low | **Category:** Bug/Security

#### Problem

`requestIDMiddleware` (`main.go:111-115`) accepts the inbound `X-Request-Id` verbatim:

```go
id := r.Header.Get("X-Request-Id")
if id == "" {
    id = generateRequestID()
}
```

While zerolog JSON-encodes the value (preventing log injection), an attacker with scraper access can send a header up to `defaultMaxHeaderBytes` (1 MiB) and cause 1 MiB log lines. In aggregate across many concurrent scrapers, this can fill disk.

#### Change

Sanitise the inbound ID before use:

```go
// main.go — in requestIDMiddleware
const maxRequestIDLen = 128
id := r.Header.Get("X-Request-Id")
if id == "" {
    id = generateRequestID()
} else {
    // Truncate and strip non-printable ASCII
    if len(id) > maxRequestIDLen {
        id = id[:maxRequestIDLen]
    }
    // Keep only printable ASCII (0x20-0x7E)
    var b strings.Builder
    for _, c := range id {
        if c >= 0x20 && c <= 0x7E {
            b.WriteRune(c)
        }
    }
    id = b.String()
    if id == "" {
        id = generateRequestID()
    }
}
```

#### Acceptance Criteria

- `TestRequestIDMiddleware_TruncatesLongHeader`: a 10000-char `X-Request-Id` is truncated to 128 chars in the response and logger context.
- `TestRequestIDMiddleware_StripsNonPrintable`: a header containing null bytes and control characters is sanitised.
- `TestRequestIDMiddleware_EmptySanitisedFallsBack`: a header composed entirely of non-printable characters generates a new ID.
- `go test -race ./...` passes.

---

### SEC-05

**Title:** JSON parse error log reflects config structure
**Severity:** Low | **Category:** Security

#### Problem

`aggregator.go:95-98`:

```go
log.Warn().
    Err(err).
    Int("input_length", len(endpointsConfig)).
    Msg("failed JSON parse, trying comma-separated list")
```

The `err` value from `json.Unmarshal` contains positional info (e.g., `"invalid character 'p' looking for beginning of value"`) and `input_length` hints at configuration size. Combined, these can reveal structural information about `METRICS_ENDPOINTS` to anyone with log access — particularly relevant if logs are shipped to a third-party SIEM.

#### Change

Remove the `Err` and `input_length` fields. The warning message alone is sufficient:

```go
log.Warn().Msg("METRICS_ENDPOINTS is not valid JSON; trying comma-separated URL list")
```

#### Acceptance Criteria

- `TestNewAggregator_ValidCSV_NoErrInLog`: calling `NewAggregator` with a CSV-format config captures no log field named `error` or `input_length` at the warn level.
- `go test -race ./pkg/aggregator/...` passes.

---

### SEC-06

**Title:** `METRICS_AGGREGATOR_PORT` not validated
**Severity:** Low | **Category:** Security

#### Problem

`main.go:401-405`:

```go
port := os.Getenv("METRICS_AGGREGATOR_PORT")
if port == "" {
    port = aggregator.DefaultAggregatorPort
}
addr := ":" + port
```

A non-numeric, out-of-range, or empty-after-trim value causes `ListenAndServe` to fail with an OS-level error message that may confuse operators. There is also no protection against port `0` (random port assignment, unhelpful for a server) or ports < 1024 (requires elevated privileges, fails silently in a non-root container).

#### Change

```go
// main.go — after reading the env var
port := strings.TrimSpace(os.Getenv("METRICS_AGGREGATOR_PORT"))
if port == "" {
    port = aggregator.DefaultAggregatorPort
}
portNum, err := strconv.Atoi(port)
if err != nil || portNum < 1 || portNum > 65535 {
    log.Fatal().Str("port", port).Msg("METRICS_AGGREGATOR_PORT must be a number between 1 and 65535")
}
addr := ":" + port
```

#### Acceptance Criteria

- `TestMain_InvalidPort`: calling `main` with `METRICS_AGGREGATOR_PORT=abc` logs a fatal error and exits non-zero (use `os.Exit` capture pattern or test via subprocess).
- `TestMain_PortZero`: `METRICS_AGGREGATOR_PORT=0` is rejected.
- `go test -race ./...` passes.

---

### SEC-07

**Title:** No listener bind-address restriction
**Severity:** Low | **Category:** Security

#### Problem

The server always binds to `0.0.0.0:PORT`. In environments where only `localhost` (or the Istio proxy sidecar) should reach the aggregator, there is no way to restrict the bind address without a network policy.

#### Change

Add `METRICS_BIND_ADDRESS` env var (default `""`, which means `0.0.0.0`):

```go
// main.go — after port validation
bindAddr := strings.TrimSpace(os.Getenv("METRICS_BIND_ADDRESS"))
addr := bindAddr + ":" + port
```

Document in `CLAUDE.md` environment variables table:

| `METRICS_BIND_ADDRESS` | `""` (all interfaces) | IP address to bind the HTTP server to (e.g. `127.0.0.1`) |

#### Acceptance Criteria

- `TestRun_BindAddressRespected`: server started with `METRICS_BIND_ADDRESS=127.0.0.1` rejects connections from a different address (test with a loopback-only dial).
- `go test -race ./...` passes.
- `CLAUDE.md` updated.

---

### ENH-03

**Title:** `Histogram.Observe` is O(N) in bucket count
**Severity:** Low | **Category:** Enhancement

#### Problem

`histogram.go:49-53` iterates all buckets unconditionally:

```go
for i, b := range h.bounds {
    if v <= b {
        h.counts[i].Add(1)
    }
}
```

With 11 default buckets (12 with +Inf) this is negligible, but the public API accepts arbitrary bucket lists. Using `sort.SearchFloat64s` finds the first matching boundary in O(log N), then the remaining upper buckets are incremented in a single forward pass.

**Note:** This must be implemented together with BUG-01's lock unification, since `counts` will be a plain `[]int64` under `h.mu`.

#### Change

```go
// histogram.go — inside Observe, under h.mu
func (h *Histogram) Observe(v float64) {
    h.mu.Lock()
    // Binary search: find index of first boundary >= v
    idx := sort.SearchFloat64s(h.bounds, v)
    // Increment all cumulative buckets from idx upward
    for i := idx; i < len(h.counts); i++ {
        h.counts[i]++
    }
    h.sum += v
    h.mu.Unlock()
}
```

#### Acceptance Criteria

- All existing `TestHistogram_*` tests pass unchanged.
- `TestHistogram_ObserveCorrectness`: values at exact bucket boundaries and between boundaries are counted in exactly the right cumulative buckets (unchanged from current behaviour).
- `BenchmarkHistogramObserve`: benchmark showing improvement for large bucket sets (e.g., 100 buckets) — `go test -bench=BenchmarkHistogramObserve -benchmem ./pkg/aggregator/`.

---

### ENH-04

**Title:** No stale-cache-on-error option
**Severity:** Low | **Category:** Enhancement

#### Problem

When `fetch` returns an error, `getOrFetch` propagates it to all callers, causing a `500` response. Prometheus treats `500` responses as scrape failures and sets `up=0`. Many operators would prefer "serve the last successful result with a staleness warning" during brief endpoint flaps — keeping Prometheus `up=1` while logging the upstream error.

#### Change

Add `METRICS_CACHE_SERVE_STALE_ON_ERROR` env var (`"true"` enables).

Extend `metricsCache`:

```go
type metricsCache struct {
    ...
    serveStaleOnError bool
    lastGoodValue     string  // protected by mu
}
```

In `getOrFetch`, after `fetch` returns an error:

```go
if err != nil {
    c.mu.Lock()
    if c.serveStaleOnError && c.lastGoodValue != "" {
        value := c.lastGoodValue
        // (close inFlight, etc.)
        c.mu.Unlock()
        log.Warn().Err(err).Msg("fetch failed; serving stale cache value")
        return value, nil
    }
    // ... existing error path
}
// On success, also update lastGoodValue:
if err == nil {
    c.lastGoodValue = value
    ...
}
```

Load the env var in `makeMetricsHandler`:

```go
cache := &metricsCache{
    ttl:               cfg.cacheTTL,
    serveStaleOnError: os.Getenv("METRICS_CACHE_SERVE_STALE_ON_ERROR") == "true",
}
```

Update `CLAUDE.md` environment variables table.

#### Acceptance Criteria

- `TestMetricsCache_ServeStaleOnError`: after a successful fetch, if `fetch` subsequently errors, the handler returns `200` with the previous value and logs a warning.
- `TestMetricsCache_NoStaleIfNeverSucceeded`: if the first fetch ever fails, the error is propagated even with stale mode enabled (no empty response served).
- Default behaviour (stale mode off) is unchanged.
- `go test -race ./...` passes.

---

### ENH-05

**Title:** Test gap: `validateEndpointURL` HTTPS path
**Severity:** Low | **Category:** Enhancement

#### Problem

All `TestNewAggregator` cases use `http://` URLs. The `https://` scheme branch in `validateEndpointURL` is allowed but completely untested. There is also no test for `AggregateMetrics` against an HTTPS endpoint.

#### Change

Add to `aggregator_test.go`:

```go
func TestNewAggregator_ValidHTTPS(t *testing.T) {
    agg, err := NewAggregator(`{"svc1":"https://a/metrics"}`)
    if err != nil {
        t.Fatalf("unexpected error for https: %v", err)
    }
    eps := agg.getEndpoints()
    if eps[0].URL != "https://a/metrics" {
        t.Fatalf("unexpected URL: %s", eps[0].URL)
    }
}

func TestAggregateMetrics_HTTPSEndpoint(t *testing.T) {
    ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        fmt.Fprintln(w, `# HELP test_gauge A test gauge.`)
        fmt.Fprintln(w, `# TYPE test_gauge gauge`)
        fmt.Fprintln(w, `test_gauge 1.0`)
    }))
    defer ts.Close()
    t.Setenv("METRICS_SCRAPE_TLS_INSECURE_SKIP_VERIFY", "true")
    // After SEC-02 is implemented, NewAggregator picks up TLS config from env
    agg, err := NewAggregator(fmt.Sprintf(`{"svc":"%s/metrics"}`, ts.URL))
    ...
}
```

**Dependency:** ENH-05 depends on SEC-02 being implemented first, since without custom TLS config, HTTPS to a test server with a self-signed cert will always fail.

#### Acceptance Criteria

- `TestNewAggregator_ValidHTTPS` passes.
- `TestAggregateMetrics_HTTPSEndpoint` passes with `METRICS_SCRAPE_TLS_INSECURE_SKIP_VERIFY=true`.
- `go test -race ./pkg/aggregator/...` passes.

---

### ENH-06

**Title:** Test gap: `parseDurationEnv` negative / `parsePositiveIntEnv` zero
**Severity:** Low | **Category:** Enhancement

#### Problem

Two guard conditions in `main.go` are not tested:
1. `parseDurationEnv` rejects `value < 0` (line 299) — there is no `TestParseDurationEnv_NegativeUsesDefault`.
2. `parsePositiveIntEnv` rejects `value <= 0` (line 312) — there is no `TestParsePositiveIntEnv_ZeroUsesDefault`.

Additionally, `METRICS_MAX_INFLIGHT=0` would be caught by `parsePositiveIntEnv` and fall back to `defaultMaxInflight=32`, but this is not verified.

#### Change

Add to `main_test.go`:

```go
func TestParseDurationEnv_NegativeUsesDefault(t *testing.T) {
    t.Setenv("TEST_DUR", "-5s")
    got := parseDurationEnv("TEST_DUR", 10*time.Second)
    if got != 10*time.Second {
        t.Fatalf("expected default 10s, got %v", got)
    }
}

func TestParsePositiveIntEnv_ZeroUsesDefault(t *testing.T) {
    t.Setenv("TEST_INT", "0")
    got := parsePositiveIntEnv("TEST_INT", 32)
    if got != 32 {
        t.Fatalf("expected default 32, got %d", got)
    }
}

func TestParsePositiveIntEnv_NegativeUsesDefault(t *testing.T) {
    t.Setenv("TEST_INT", "-1")
    got := parsePositiveIntEnv("TEST_INT", 32)
    if got != 32 {
        t.Fatalf("expected default 32, got %d", got)
    }
}
```

#### Acceptance Criteria

- All three new tests pass.
- `go test -race ./...` passes.

---

### ENH-07

**Title:** Context cancellation does not cancel in-flight fetch
**Severity:** Low | **Category:** Enhancement

#### Problem

When all waiting goroutines leave `getOrFetch` via context cancellation (the `case <-ctx.Done()` branch at `main.go:201-203`), the in-flight `fetch` started by the goroutine that won the lock continues to completion — up to 5 seconds for HTTP timeouts plus scrape processing time. Under sustained load with short-lived client contexts (e.g., Prometheus with a `scrape_timeout` shorter than `METRICS_CACHE_TTL`), goroutines accumulate.

#### Change

Track the context that launched the fetch and use it as the fetch context. When that context is cancelled and there are no remaining waiters, the fetch will also cancel:

The simplest correct approach is to pass the first requester's context to `fetch`, which already propagates cancellation through the HTTP client's `NewRequestWithContext`. Since the cache already passes `ctx` (the first requester's context) to `fetch` today via `value, err := fetch(ctx)`, the HTTP requests inside `AggregateMetrics` are already cancellable. The issue is only that subsequent waiters abandon but the first requester may also abandon — in that case the fetch continues with a cancelled context and the HTTP calls would time out on their own.

A minimal fix: detect if the fetch context is cancelled and skip caching the result:

```go
// main.go — in getOrFetch, after fetch returns
if ctx.Err() != nil && err != nil {
    // First requester context was cancelled; don't cache, notify waiters
    c.mu.Lock()
    wait := c.inFlight
    c.inFlight = nil
    close(wait)
    c.mu.Unlock()
    return "", ctx.Err()
}
```

This is a minimal fix. A more complete solution (not required for this spec) would use a detached context for the fetch so it outlives individual requesters — but that increases complexity and is only worthwhile if the stale-cache-on-error feature (ENH-04) is also implemented.

#### Acceptance Criteria

- `TestMetricsCache_ContextCancelledDuringFetch`: a fetch that blocks for longer than the requester's context deadline returns `ctx.Err()` to the caller, not a stale/nil result.
- No goroutine leak detected by `goleak` or equivalent in the test.
- `go test -race ./...` passes.

---

### ENH-08

**Title:** `promtool` lint warning filter too broad
**Severity:** Low | **Category:** Enhancement

#### Problem

`test/e2e/run.sh:190`:

```bash
if echo "$PROMTOOL_OUTPUT" | grep -v "no help text" | grep -q "."; then
    fail "promtool check metrics failed"
```

The filter `grep -v "no help text"` removes any line containing that substring — including potential real error messages that happen to also include those words. A `promtool` formatting error like `"metric has no help text but also has parse error..."` would be silently swallowed.

#### Change

Make the filter anchored to match only the specific warning format `promtool` emits:

```bash
# The exact promtool warning format is:
# "UNIT_NAME: no help text for metric: METRIC_NAME"
if echo "$PROMTOOL_OUTPUT" | grep -vE '^[^:]+: no help text for metric:' | grep -q '[^[:space:]]'; then
    fail "promtool check metrics failed"
```

The `[^[:space:]]` ensures we don't match lines that are entirely whitespace (avoids false fails on empty filtered output).

#### Acceptance Criteria

- `bash -n test/e2e/run.sh` passes (no syntax errors).
- The E2E `make e2e` job still passes.
- Manual test: injecting a line `"error: something went wrong"` into `PROMTOOL_OUTPUT` causes the check to fail; injecting `"app: no help text for metric: foo"` does not.

---

### ENH-09

**Title:** `docker-compose.yaml` uses `prom/prometheus:latest`
**Severity:** Low | **Category:** Enhancement

#### Problem

`docker-compose.yaml` lines 17 and 24:

```yaml
image: prom/prometheus:latest
```

A breaking change in a new Prometheus release could silently break the smoke test (`make smoke` / `compose-smoke.yml`) on the next CI run, without any code change in this repo.

#### Change

Pin to a specific version:

```yaml
image: prom/prometheus:v3.3.0
```

Update both occurrences. When upgrading Prometheus in the future, bump this version intentionally.

#### Acceptance Criteria

- `docker compose up --build` succeeds.
- `make smoke` passes.
- Both `prometheus1` and `prometheus2` services use the pinned tag.

---

## Implementation Notes

### Commit grouping

Each finding should be a separate commit following Conventional Commits:

| Finding(s) | Suggested commit prefix |
|---|---|
| SEC-01 | `fix(security): block link-local SSRF in legacy mode` |
| SEC-02 | `feat(tls): add custom TLS config for HTTPS scrape targets` |
| BUG-01 + ENH-03 | `fix(histogram): unify mutex and use binary search in Observe` |
| BUG-03 | `fix(regex): handle escaped } in sampleLine label values` |
| SEC-03 | `fix(e2e): verify checksums for downloaded binaries` |
| SEC-04 | `fix(docker): pin base images to digest` |
| ENH-01 | `feat(metrics): add cache hit/miss counters` |
| ENH-02 | `feat(metrics): add build_info metric with version and commit` |
| BUG-02 | `fix: don't count oversized body as scrape error` |
| BUG-04 | `fix: sanitise X-Request-Id header length and characters` |
| SEC-05 | `fix(log): remove parse error details from ENDPOINTS fallback warn` |
| SEC-06 | `fix: validate METRICS_AGGREGATOR_PORT on startup` |
| SEC-07 | `feat: add METRICS_BIND_ADDRESS env var` |
| ENH-04 | `feat(cache): add METRICS_CACHE_SERVE_STALE_ON_ERROR option` |
| ENH-05 | `test: add HTTPS endpoint coverage` |
| ENH-06 | `test: cover negative/zero values in env parsers` |
| ENH-07 | `fix(cache): skip caching result when fetch context is cancelled` |
| ENH-08 | `fix(e2e): tighten promtool lint warning filter` |
| ENH-09 | `chore: pin prom/prometheus to v3.3.0 in docker-compose` |

### Dependencies

```
SEC-02 must precede ENH-05
BUG-01 must be co-implemented with ENH-03
```

All other findings are independent.
