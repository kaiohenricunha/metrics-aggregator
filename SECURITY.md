# Security Policy

This project accepts responsible disclosure reports for security vulnerabilities affecting `metrics-aggregator`.

## Reporting a Vulnerability

**Preferred:** [Open a private security advisory](https://github.com/kaiohenricunha/metrics-aggregator/security/advisories/new) on this repository.

If private advisory tooling is unavailable, open an issue requesting a private contact channel and avoid posting exploit details publicly.

Please include:
- Affected version or commit SHA
- Reproduction steps
- Expected impact and severity estimate
- Any proof-of-concept artifacts

Do **not** post credential material or sensitive environment data in public issues.

### Response timeline

- **Initial acknowledgment:** within 7 days
- **Triage and severity assessment:** within 14 days
- **Fix or mitigation:** depends on severity; critical issues are fast-tracked

These are best-effort targets for a maintainer-run project.

## Scope

**In scope:**
- The aggregator binary and its dependencies
- The container image (`ghcr.io/kaiohenricunha/metrics-aggregator`)
- CI/CD supply chain (workflows, release signing, image scanning)
- Configuration parsing and endpoint validation logic

**Out of scope:**
- Example manifests and demo configurations in `examples/`
- Third-party tools (Prometheus, Istio, kind) used in E2E tests
- Issues that require physical access or pre-existing privileged access to the host

## Supported Versions

Security fixes are applied to:

- the latest release tag;
- the default `main` branch.

Older, unmaintained versions may not receive backported security patches.

## Security Posture

The project maintains several supply-chain and runtime hardening measures:

- **Image signing** — container images are signed with [Cosign](https://docs.sigstore.dev/cosign/overview/) via keyless OIDC (GitHub Actions identity)
- **CVE scanning** — [Trivy](https://trivy.dev/) scans every published image for CRITICAL and HIGH vulnerabilities
- **SBOM + SLSA provenance** — releases include Software Bill of Materials and SLSA provenance attestations via GoReleaser
- **Static analysis** — `govulncheck` runs in CI on every push and PR to detect known Go vulnerabilities
- **Strict endpoint validation** — the default `strict` security mode rejects unsafe URL forms (unsupported schemes, embedded credentials, non-metrics paths) and blocks redirect following
- **Bounded concurrency** — configurable inflight request limits and explicit HTTP server timeouts prevent resource exhaustion

## Disclosure Process

1. Maintainers validate and triage the report.
2. A fix is prepared with regression tests.
3. Security-impacting fixes are released and documented.
4. Public disclosure follows once a patched version is available.

For severe issues, maintainers may accelerate release and disclosure timelines.

## Acknowledgments

Reporters who responsibly disclose valid vulnerabilities will be credited in the release notes and security advisory, unless they prefer to remain anonymous.
