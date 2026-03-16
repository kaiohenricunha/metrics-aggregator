Run the Go vulnerability scanner:

```bash
make vulncheck
```

This installs `govulncheck` into `.bin/` if not present, then scans all packages against the Go vulnerability database. CI runs this in the `lint.yml` workflow. Address any reported vulnerabilities before merging.
