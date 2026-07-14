# Contributing

Issues and pull requests are welcome. For behavior changes, open an issue first so safety and compatibility expectations can be agreed on.

## Development

Install Go 1.25.12 and `just`. The task runner installs pinned development tools into `.cache/bin`. Run repository tasks through `just`:

```sh
just fmt
just test
just ci
```

Use conventional commit messages such as `fix: retry a failed migration trigger`. Add tests for changed behavior. Do not use a production cluster for development drains.

The guarded lab harness is documented in [docs/lab-e2e.md](docs/lab-e2e.md).

By participating, you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
