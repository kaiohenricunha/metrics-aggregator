Run all three static analysis tools:

```bash
make lint
```

This installs staticcheck and revive into `.bin/` if not present, then runs each against `./...`. For vulnerability scanning, use `/vulncheck`. Address every reported issue before merging.
