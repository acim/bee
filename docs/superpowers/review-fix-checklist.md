# Review Fix Checklist

Repository review performed at `ad0d13fc6d1bf1117563126fd87dfd744b42f2a6`.

Use this as the fix tracker for the review findings. Mark each item complete only after the fix is implemented and verified.

## P1

- [x] `command_line.go`: unexported config fields can panic at `Addr().Interface()` instead of returning a clear invalid-config error.
  - Suggested fix: check `field.PkgPath`, `CanAddr`, or `CanInterface` before taking the interface.

- [x] `value.go`: `URL.Get()` and `Time.Get()` dereference nil embedded pointers for zero/default values.
  - Suggested fix: return `url.URL{}` / `time.Time{}` for nil receivers or nil embedded pointers.

- [x] `README.md`: subcommand example does not compile.
  - Problems: `bee.NewCommandLine` is not exported, `ms.New` is undefined, and the delete example uses the wrong command name.

- [x] `.github/workflows/pipeline.yml`: missing blocking `actionlint` CI job.
  - Decision: do not add a CI job for now because the pipeline is small; validate workflow changes locally with `actionlint`.

## P2

- [x] `command_line.go`: typed nil config pointers can panic after passing the pointer-to-struct check.
  - Suggested fix: check `v.IsNil()` before `v.Elem()` and return `ErrInvalidConfigType`.

- [x] `service.go`: `WithOutput` updates `commandLine.output` but not `flagSet.SetOutput(w)`.
  - Suggested fix: update both outputs and add coverage for redirected flag usage/diagnostics.

- [x] `command_line.go`: duplicate generated or tagged flag names can panic during registration.
  - Suggested fix: track registered flag names or convert duplicate registration into a returned config error.

- [x] `command_line_internal_test.go`: several error assertions do not fail when `err == nil` and `wantErr` is set.
  - Locations noted during review: around lines 54, 293, and 432.

- [x] `.github/workflows/pipeline.yml` and `.github/workflows/update-deps.yaml`: reusable workflows are pinned to mutable `@main`.
  - Resolution: accepted as fine for this repository; leave the reusable workflow refs on `@main`.

- [ ] `.github/workflows/pipeline.yml`: CI runs `make test` but not `make lint`.
  - Current local result: `make lint` fails on a govet inline finding and stale `gomnd` nolint directives.

- [ ] `.github/dependabot.yml`: Dependabot covers GitHub Actions only, not Go modules.
  - Suggested fix: add a `gomod` ecosystem entry for `/`.

- [x] `README.md`: coverage badge says `82.2%`, but current coverage is `77.9%`.
  - Suggested fix: update the badge or generate it automatically from CI.

## P3

- [ ] `example_test.go`: examples are commented out while the README links to them.
  - Suggested fix: restore compile-checked examples or remove the examples claim.

- [ ] `value.go`: `Time.Set` comment says it parses a provided URL, but it parses RFC3339 time.

## Repository Reminders

- [ ] Consider enabling GitHub CodeQL/code scanning for this Go repository.

- [ ] Consider adding `govulncheck ./...` to CI. `govulncheck` was not installed locally during the review.

- [ ] No Dockerfile was present during the review, so the container image vulnerability scanning reminder did not apply at that time.
