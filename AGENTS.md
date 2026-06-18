# Repository Rules

## Development Workflow

- Always use test-driven development for bug fixes, behavior changes, refactoring, and new features.
- Write the failing test first, run it to verify the expected failure, then implement the minimal fix and rerun the tests.
- Do not mark a review checklist item fixed until the relevant tests pass.
- Whenever Go coverage changes, update the README coverage badge to the exact one-decimal coverage value without changing the badge format. The line must remain:
  `![coverage](https://img.shields.io/badge/coverage-<value>%25-brightgreen?style=flat&logo=go)`
  Replace only `<value>` with the measured number, for example `82.2`; keep `%25` as the encoded percent sign.
