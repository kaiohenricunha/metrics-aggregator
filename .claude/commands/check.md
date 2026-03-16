Run the full pre-PR verification suite:

```bash
make check
```

This runs build, vet, tests, and static analysis (staticcheck + revive) in sequence. For vulnerability scanning, use `/vulncheck`. Fix any errors before opening a pull request.
