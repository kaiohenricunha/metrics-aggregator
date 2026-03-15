Run the full pre-PR verification suite:

```bash
make check
```

This runs build, vet, race-detector tests, and all three static analysis tools (staticcheck, revive, govulncheck) in sequence. Fix any errors before opening a pull request.
