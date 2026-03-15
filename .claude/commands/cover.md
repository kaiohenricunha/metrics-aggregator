Run the race-detector test suite and generate a coverage report:

```bash
make test-race
make cover-html
```

`make test-race` writes `cover.out`. `make cover-html` renders `cover.html`. Coverage percentage appears in the `make test-race` output, e.g. `coverage: 87.3% of statements`.
