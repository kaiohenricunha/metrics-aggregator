# Contributing

Thanks for taking the time to contribute. This guide covers everything you need to open a useful issue or a mergeable PR.

## Reporting issues

- **Bug:** include the version (`metrics-aggregator --version` or the image tag), the `METRICS_ENDPOINTS` format you used, and the relevant log output.
- **Feature request:** describe the problem you are trying to solve, not just the solution you have in mind.
- **Security vulnerability:** do **not** open a public issue — follow the [security policy](SECURITY.md).

## Development setup

```bash
# Prerequisites: Go 1.26+, Docker, kind (for E2E)
git clone https://github.com/kaiohenricunha/metrics-aggregator.git
cd metrics-aggregator

go build ./...
go test ./...
```

See [`CLAUDE.md`](CLAUDE.md) for the full command reference, including lint, coverage, and E2E targets.

## Making changes

1. **Fork** the repository and create a branch from `main`.
2. **Write tests** for any new behaviour. Coverage on touched paths must not drop.
3. **Run the pre-PR gate** before pushing:
   ```bash
   make check   # build + vet + race tests + staticcheck + revive + govulncheck
   ```
4. **Open a pull request** against `main`.

## Commit style

This project uses [Conventional Commits](https://www.conventionalcommits.org/) because releases are automated via semantic-release:

| Prefix | When to use |
|--------|-------------|
| `feat:` | New user-visible behaviour |
| `fix:` | Bug fix |
| `chore:` | Tooling, deps, CI, docs |
| `ci:` | Workflow-only changes |

Breaking changes must include a `BREAKING CHANGE:` footer in the commit body. The first breaking change bumps the minor version (pre-1.0) or major version (post-1.0).

## Code style

- `gofmt` is enforced automatically (a post-edit hook runs it in the dev environment).
- `go vet`, `staticcheck`, and `revive` must all pass with zero warnings.
- Prefer editing existing files over adding new ones; avoid over-abstracting for single-use cases.

## License

By submitting a pull request you agree that your contribution will be licensed under the [Apache 2.0 License](LICENSE).
