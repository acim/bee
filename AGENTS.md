# Repository Rules

## Development Workflow

- Always use test-driven development for bug fixes, behavior changes, refactoring, and new features.
- Write the failing test first, run it to verify the expected failure, then implement the minimal fix and rerun the tests.
- Do not mark a review checklist item fixed until the relevant tests pass.
- Whenever Go coverage changes, update the README coverage badge to the exact one-decimal coverage value without changing the badge format. The line must remain:
  `![coverage](https://img.shields.io/badge/coverage-<value>%25-brightgreen?style=flat&logo=go)`
  Replace only `<value>` with the measured number, for example `82.2`; keep `%25` as the encoded percent sign.

## CI and Security Notes

- The GitHub Actions pipeline intentionally uses `ectobit/reusable-workflows` refs at `@main`.
- Do not add an `actionlint` CI job unless explicitly requested; validate workflow changes locally with `actionlint`.
- `ectobit/reusable-workflows/.github/workflows/go-check.yaml` already runs `golangci-lint run` and `govulncheck ./...`.
- GitHub CodeQL/code scanning is enabled in the repository settings.
- This repository is a Go library and does not build or publish container images. Container image vulnerability scanning is not applicable unless a Dockerfile or image publishing workflow is added.
