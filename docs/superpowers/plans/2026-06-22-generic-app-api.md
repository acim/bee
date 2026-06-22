# Generic App API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Replace the service-first public API with a typed `App[T]` that owns config parsing, command dispatch, application context, supervised goroutines, and shutdown cleanup.

**Architecture:** Keep the existing reflection-based config parser in `command_line.go`, but move parsing from construction time to `RunE` so commands can be registered first. Implement lifecycle in `service.go` around `App[T]`, with options applied to shared app state, a testable `RunE(args ...string) error`, and `Run()` as the `os.Exit` wrapper. Use flat normalized command paths and longest-prefix matching.

**Tech Stack:** Go 1.23, standard library `context`, `flag`, `log/slog`, `os/signal`, `sync`, existing `commandLine` parser.

**Superseded note:** This completed plan records the first generic app API implementation. Its flat command-path examples and `func(app *bee.App[T]) error` handlers are historical, not current API guidance. The current app API is specified and implemented by `docs/superpowers/plans/2026-06-22-polished-app-api.md`: value config, `bee.Context[T]` handlers, command trees, validation tags, and graceful HTTP shutdown ordering.

---

### Task 1: Command And Config Tests

**Files:**
- Create: `app_test.go`
- Modify: `service.go`

- [x] **Step 1: Write failing tests for typed config access, default/env/flag precedence, nested config, multi-token command matching, longest command matching, default command, flags after commands, duplicate commands, unknown commands, root help, and command help.**

```go
func TestAppCommandParsingAndConfig(t *testing.T) {
    type config struct {
        LogLevel string `def:"INFO"`
        Port int `def:"8080"`
        HTTP struct { Host string `def:"127.0.0.1"` }
    }
    cfg := &config{}
    output := &bytes.Buffer{}
    app := bee.New("maia", cfg, bee.WithOutput(output), bee.WithErrorHandling(flag.ContinueOnError), bee.WithLookupEnvFunc(func(key string) (string, bool) {
        if key == "MAIA_PORT" { return "9090", true }
        return "", false
    }))
    app.Command("start", "Run default starter", func(app *bee.App[config]) error { return nil })
    app.Command("start api", "Run API", func(app *bee.App[config]) error {
        if app.Config() != cfg { t.Fatal("Config returned wrong pointer") }
        if cfg.Port != 7070 || cfg.HTTP.Host != "0.0.0.0" { t.Fatalf("config not parsed: %+v", cfg) }
        return nil
    })
    if err := app.RunE("start", "api", "--port", "7070", "--http-host", "0.0.0.0"); err != nil { t.Fatal(err) }
}
```

- [x] **Step 2: Run the focused test and verify it fails to compile because `bee.New`, `App[T]`, `Command`, `Config`, and `RunE` do not exist.**

Run: `go test -count=1 -run TestAppCommandParsingAndConfig ./...`
Expected: compile failure for missing new API.

- [x] **Step 3: Implement the minimal typed `App[T]`, options, command registry, command matching, config parsing after command tokens, and usage output.**

- [x] **Step 4: Run command/config tests until they pass.**

Run: `go test -count=1 -run 'TestAppCommand|TestAppDefault|TestAppHelp|TestAppDuplicate|TestAppUnknown' ./...`
Expected: PASS.

### Task 2: Lifecycle Tests

**Files:**
- Modify: `app_test.go`
- Modify: `service.go`

- [x] **Step 1: Write failing tests for app context cancellation on signal, cancellation on supervised goroutine error, `Go` receiving app context, non-zero run result on goroutine error, clean goroutine exit on cancellation, reverse closer order, non-cancelled shutdown contexts, goroutines stopping before closers, closer errors being recorded while later closers still run, and command setup error cleanup.**

- [x] **Step 2: Run lifecycle tests and verify they fail because supervision and cleanup are not implemented.**

Run: `go test -count=1 -run 'TestAppContext|TestAppGo|TestAppClosers|TestAppCommandSetup' ./...`
Expected: FAIL.

- [x] **Step 3: Implement supervised goroutine wait group, fail-fast cancellation, signal watcher, `Exit`, reverse-order cleanup with fresh timeout context, and error aggregation.**

- [x] **Step 4: Run lifecycle tests until they pass.**

Run: `go test -count=1 -run 'TestAppContext|TestAppGo|TestAppClosers|TestAppCommandSetup' ./...`
Expected: PASS.

### Task 3: Compatibility, Examples, And Documentation

**Files:**
- Modify: `example_test.go`
- Modify: `service_internal_test.go`
- Modify: `README.md`

- [x] **Step 1: Update examples and existing service tests to use `bee.New`, `RunE`, `Root`, `Command`, `Register`, `Go`, and `Log`.**

- [x] **Step 2: Update README with the new API, a `start api` / `start worker` example, migration note from `NewService`, context/goroutine/register docs, and shutdown ordering.**

- [x] **Step 3: Run all tests with coverage and update the README badge if coverage changes.**

Run: `go test -race -coverprofile=coverage.out -count=1 ./...`
Expected: PASS. Then compute coverage with `go tool cover -func=coverage.out`.

- [x] **Step 4: Run `gofmt`, inspect `git diff`, and make only scoped cleanups.**

Run: `gofmt -w *.go`
Run: `git diff --stat`
Expected: App API, tests, and docs only.
