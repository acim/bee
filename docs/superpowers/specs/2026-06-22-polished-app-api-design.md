# Polished App API Design

Date: 2026-06-22

## Goal

Improve the public bee application API with breaking changes where they make the
API clearer, easier to use, and more capable, without expanding bee into a broad
CLI framework or adding a separate standalone parser API in this iteration.

This design keeps the focus on the application abstraction:

- typed config parsing from flags and environment variables;
- command registration and dispatch;
- runtime context, logging, supervised goroutines, and cleanup;
- HTTP server convenience helpers;
- simple struct-tag validation.

## Scope

This is the "Polished App API Only" iteration. It does not add a separate public
`Parse[T]` API, a standalone command tree package, custom validation callbacks,
or route-aware HTTP behavior.

The existing config field tags remain supported:

```go
flag
env
help
def
req
```

The supported config field types remain:

```go
string
bool
int
int64
uint
uint64
float64
time.Duration
bee.StringSlice
bee.IntSlice
bee.URL
bee.Time
nested struct
```

## Application Construction

`bee.New` should accept a config value rather than requiring callers to allocate
and pass a pointer:

```go
app := bee.New("maia", Config{},
	bee.WithLogger(logger),
	bee.WithShutdownTimeout(30*time.Second),
)
```

The app owns parsing and stores the parsed config. Handler code receives a
runtime context with the parsed config exposed directly as a field.

Logger injection should be explicit:

```go
bee.WithLogger(logger)
```

`WithLogLevel` may remain as a convenience, but the primary polished API should
allow callers to provide the full `*slog.Logger`.

## Command Tree

Commands should be represented as a tree rather than flat string paths.

```go
start := app.Command("start", "Start services")

start.Command("api", "Run HTTP API", runAPI)
start.Command("worker", "Run background workers", runWorker)

migrate := app.Command("migrate", "Database migrations")
migrate.Command("up", "Apply migrations", runMigrateUp)
migrate.Command("down", "Rollback migration", runMigrateDown)
```

Command nesting is unlimited in the data model. Documentation and examples
should favor shallow trees, usually one to three levels:

```text
start api
start worker
migrate up
migrate down
admin users import
```

Each command name is a single token. Empty names and names containing whitespace
are invalid. Duplicate sibling command names are invalid. The implementation may
panic for definition-time mistakes, matching the current duplicate-command
behavior.

Intermediate commands may have handlers. For example, `start` may run a default
start handler while `start api` runs the API-specific handler.

Apps without commands remain first-class. `Root` registers the handler for a
simple app that does not need command dispatch:

```go
app.Root("Run service", runService)
app.Run()
```

`Root` is not part of the command tree. It is the no-command app entrypoint. If
commands are registered, command selection is required unless a default command
is configured.

`Run` should remain argument-free and should use `os.Args[1:]` plus the
configured error handling:

```go
app.Run()
```

`RunE` remains the testable and embeddable variant for custom arguments:

```go
err := app.RunE("start", "api", "--http-addr", ":9090")
```

## Handler Context

Handlers should receive a runtime context, not the definition-time app:

```go
func(ctx bee.Context[Config]) error
```

Runtime state and resources should be exported fields:

```go
ctx.Config
ctx.Log
ctx.Context
```

Runtime actions should remain methods:

```go
ctx.Go(...)
ctx.Register(...)
ctx.HTTPServer(...)
ctx.Exit(...)
```

This separates app construction from command execution and avoids encouraging
handlers to mutate the command tree at runtime.

`ctx.Config` should be the parsed config value. If callers need mutable or shared
state, they can put pointers or concurrency-safe values inside their config.

## Graceful Shutdown

The public documentation should describe shutdown ordering clearly, because this
is part of how users decide where to place HTTP servers, database pools, queues,
and telemetry flushers.

`ctx.HTTPServer` starts the supplied server as a supervised goroutine. When the
app context is cancelled, it calls `server.Shutdown` with a fresh shutdown
context controlled by `WithShutdownTimeout`.

Registered closers run after supervised goroutines finish. This means HTTP
servers stop accepting new requests and drain in-flight requests before shared
dependencies are closed.

For example:

```go
ctx.HTTPServer("http api", server)
ctx.Register("database", closeDB)
ctx.Register("queue", closeQueue)
```

The shutdown order is:

```text
1. app context is cancelled
2. HTTP server starts graceful shutdown
3. app waits for supervised goroutines, including HTTP servers, to finish
4. registered closers run in reverse order: queue, then database
```

This ordering lets in-flight HTTP requests continue using dependencies such as
database pools while the HTTP server drains.

## Validation Tags

Validation runs after defaults, environment variables, and flags have been
parsed, and before the selected command handler runs. Help requests should not
run validation.

Validation failures reuse the configured `flag.ErrorHandling`, the same as parse
failures:

- `flag.ContinueOnError`: return an error from `RunE`;
- `flag.ExitOnError`: write the error and exit with code 2;
- `flag.PanicOnError`: panic with the validation error.

The first validation tag set is:

```go
min
max
oneof
len
minlen
maxlen
regex
prefix
suffix
nonzero
```

### Tag Semantics

`min` and `max` apply to numeric types and `time.Duration`.

```go
Port    int           `min:"1" max:"65535"`
Timeout time.Duration `min:"100ms" max:"30s"`
```

`oneof` applies to strings, numeric types, and `time.Duration`. Values are
comma-separated, with optional surrounding whitespace trimmed.

```go
Env      string `oneof:"dev, staging, prod"`
LogLevel string `oneof:"debug,info,warn,error"`
Port     int    `oneof:"80,443,8080"`
```

`len`, `minlen`, and `maxlen` apply to strings and bee slice types.

```go
Token string          `len:"32"`
Name  string          `minlen:"3" maxlen:"64"`
Hosts bee.StringSlice `minlen:"1" maxlen:"8"`
```

`regex` applies to strings.

```go
Name string `regex:"^[a-z][a-z0-9-]*$"`
```

`prefix` and `suffix` apply to string-like values: `string` and `bee.URL`.
Values are comma-separated, with optional surrounding whitespace trimmed.

```go
DatabaseURL bee.URL `prefix:"postgres://, postgresql://"`
OutputFile  string  `suffix:".json, .yaml, .yml"`
```

`nonzero` applies to all supported final values and requires the parsed value to
be non-zero.

```go
HTTPAddr string `def:":8080" nonzero:""`
```

`req` keeps its current source-based meaning: the value must be supplied by an
environment variable or command-line flag. `nonzero` is final-value validation.

## Example

```go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.acim.net/bee"
)

type Config struct {
	Env       string        `def:"dev" oneof:"dev,staging,prod"`
	LogLevel  string        `def:"info" oneof:"debug,info,warn,error"`
	Name      string        `req:"" minlen:"3" maxlen:"32" regex:"^[a-z][a-z0-9-]*$"`
	Token     string        `req:"" len:"32"`
	Topic     string        `def:"maia.events" prefix:"maia." suffix:".events"`
	Enabled   bool          `def:"true"`
	Port      int           `def:"8080" min:"1" max:"65535"`
	Workers   uint          `def:"4" min:"1" max:"64"`
	MaxBytes  uint64        `def:"10485760" min:"1024"`
	Rate      float64       `def:"0.5" min:"0" max:"1"`
	Timeout   time.Duration `def:"5s" min:"100ms" max:"30s"`
	Hosts     bee.StringSlice `def:"api-1,api-2" minlen:"1" maxlen:"8"`
	Ports     bee.IntSlice    `def:"8080,8081" minlen:"1" maxlen:"8"`
	PublicURL bee.URL         `req:"" prefix:"https://"`
	StartAt   bee.Time        `def:"2026-01-01T00:00:00Z"`

	HTTP struct {
		Addr string `def:":8080" flag:"http-addr" env:"MAIA_HTTP_ADDR" help:"HTTP listen address" nonzero:""`
	}

	Database struct {
		URL         bee.URL       `req:"" prefix:"postgres://,postgresql://"`
		MaxConns    int           `def:"10" min:"1" max:"100"`
		ConnTimeout time.Duration `def:"3s" min:"100ms" max:"10s"`
	}
}

func main() {
	app := bee.New("maia", Config{},
		bee.WithLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil))),
		bee.WithShutdownTimeout(30*time.Second),
	)

	start := app.Command("start", "Start services")

	start.Command("api", "Run HTTP API", func(ctx bee.Context[Config]) error {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})

		var mws bee.Middlewares
		mws.Add(bee.SlogLogger(ctx.Log))

		server := &http.Server{
			Addr:              ctx.Config.HTTP.Addr,
			Handler:           mws.Wrap(mux),
			ReadHeaderTimeout: 5 * time.Second,
		}

		ctx.HTTPServer("http api", server)
		ctx.Log.Info("api started", "env", ctx.Config.Env, "addr", ctx.Config.HTTP.Addr)

		return nil
	})

	start.Command("worker", "Run background workers", func(ctx bee.Context[Config]) error {
		for i := uint(0); i < ctx.Config.Workers; i++ {
			index := i
			ctx.Go("worker", func(run context.Context) error {
				ticker := time.NewTicker(ctx.Config.Timeout)
				defer ticker.Stop()

				for {
					select {
					case <-run.Done():
						ctx.Log.Info("worker stopped", "index", index)
						return nil
					case <-ticker.C:
						ctx.Log.Info("worker tick", "index", index)
					}
				}
			})
		}

		return nil
	})

	migrate := app.Command("migrate", "Database migrations")
	migrate.Command("up", "Apply migrations", runMigrateUp)
	migrate.Command("down", "Rollback migration", runMigrateDown)

	app.Run()
}

func runMigrateUp(ctx bee.Context[Config]) error {
	ctx.Log.Info("applying migrations", "database", ctx.Config.Database.URL.String())
	return nil
}

func runMigrateDown(ctx bee.Context[Config]) error {
	ctx.Log.Info("rolling back migration", "database", ctx.Config.Database.URL.String())
	return nil
}
```

## Testing Strategy

Use test-driven development for implementation.

Key test areas:

- command tree registration, duplicate sibling rejection, invalid names, nested
  dispatch, intermediate handlers, unknown commands, and help output;
- handler context fields and lifecycle methods;
- `WithLogger` behavior;
- validation tag success and failure cases for each supported type;
- validation error handling through `flag.ContinueOnError`,
  `flag.ExitOnError`, and `flag.PanicOnError`;
- help requests bypassing validation;
- README examples compiling and matching the polished API.

## Open Questions

None. This spec intentionally defers standalone parsing, custom validators,
positional arguments, command aliases, hidden commands, and route-aware HTTP
middleware.
