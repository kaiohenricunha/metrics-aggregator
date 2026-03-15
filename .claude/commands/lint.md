Run all three static analysis tools:

```bash
make lint
```

This installs staticcheck and revive into `.bin/` if not present, then runs each against `./...`. For vulnerability scanning run `make vulncheck` separately. Address every reported issue before merging.
