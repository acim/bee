# Repository Rules

## Development Workflow

- Always use test-driven development for bug fixes, behavior changes, refactoring, and new features.
- Write the failing test first, run it to verify the expected failure, then implement the minimal fix and rerun the tests.
- Do not mark a review checklist item fixed until the relevant tests pass.
- Whenever Go coverage changes, update the README coverage badge to the exact one-decimal coverage value without changing the badge format. The line must remain:
  `![coverage](https://img.shields.io/badge/coverage-<value>%25-brightgreen?style=flat&logo=go)`
  Replace only `<value>` with the measured number, for example `82.2`; keep `%25` as the encoded percent sign.

## GitHub Actions

- Every `ectobit/*` action and reusable workflow reference tracks `@main` and never overrides shared `go-version`, `golangci-lint-version`, or `govulncheck-version` values.
- Third-party actions use a bare major version tag, such as `actions/checkout@v7`.
- Preserve local `./...` and `docker://...` references. Resolve SHA pins from upstream release/tag evidence and preserve the published major; report references without a major alias for a decision.
- Standard Go jobs select `stable` with `check-latest: true`; shared workflows own this selection upstream. Keep `go.mod`/`go.work` minimum versions separate from CI toolchain selection, and preserve module files in cache keys.
- Keep any explicit minimum-version or compatibility checks separately named, running their promised version with `GOTOOLCHAIN=local`.

## CI and Security Notes

- Keep the early blocking `actionlint` job in `pipeline.yml`, using `raven-actions/actionlint@v2`, and make Go checks depend on it. Also validate workflow changes locally with `actionlint`.
- `ectobit/reusable-workflows/.github/workflows/go-check.yaml` already runs `golangci-lint run` and `govulncheck ./...`.
- GitHub CodeQL/code scanning is enabled in the repository settings.
- This repository is a Go library and does not build or publish container images. Container image vulnerability scanning is not applicable unless a Dockerfile or image publishing workflow is added.
