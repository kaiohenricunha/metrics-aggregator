Run all three static analysis tools:

```bash
make lint
```

This installs staticcheck, revive, and govulncheck into `.bin/` if not present, then runs each against `./...`. Address every reported issue before merging.
