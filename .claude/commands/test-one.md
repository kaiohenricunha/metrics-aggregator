Run a single test by exact name. Usage: /test-one <TestName>

```bash
make test-one TEST='^$ARGUMENTS$'
```

If no argument is provided, the Makefile will print a usage hint and exit 1. Examples:

  /test-one TestAggregateMetrics
  /test-one TestAddCustomLabel
