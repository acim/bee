# bee

Microservices oriented [12-factor](https://12factor.net) Go library for parsing environment variables and command line flags to arbitrary config struct using struct tags to define default values and to override flag names and environment variables' names.

[![pipeline](https://github.com/acim/bee/actions/workflows/pipeline.yml/badge.svg)](https://github.com/acim/bee/actions/workflows/pipeline.yml)
[![reference](https://pkg.go.dev/badge/go.acim.net/bee.svg)](https://pkg.go.dev/go.acim.net/bee)
[![report](https://goreportcard.com/badge/go.acim.net/bee)](https://goreportcard.com/report/go.acim.net/bee)
![coverage](https://img.shields.io/badge/coverage-96.8%25-brightgreen?style=flat&logo=go)

This package in intended to be used to parse command line arguments and environment variables into an arbitrary config struct.
This struct may contain multiple nested structs, they all will be processed recursively. Names of the flags and environment
variables are automatically generated. Flags will be kebab case of the field name eventually preceded by parent fields
in case of nested structs. Names of environment variables will be similar, but additionally prefixed with command name
and then snake and upper cased. Description of each flag will also be automatically generated in a human friendly way
as much as possible. Additionally, you may override these auto-generated names using the struct tags and you also may
define default value.

- **flag** - override generated flag name
- **env** - override generated environment variable name
- **help** - override generated flag description
- **def** - override default (zero) value
- **req** - require the value to be supplied by environment variable or command line flag

## Important: all struct fields should be exported.

## Custom flag types

Besides the types supported by flag package, this package provides additional types:

- **bee.StringSlice** - doesn't support multiple flags but instead supports comma separated strings, i.e. "foo,bar"
- **bee.IntSlice** - doesn't support multiple flags but instead supports comma separated integers, i.e. "5,-8,0"
- **bee.URL**
- **bee.Time** - RFC3339 time

User-defined named scalar types, such as `type Level string`, are not supported.
Fields of these types that previously panicked during parsing now return an error
wrapping `bee.ErrUnsupportedType`. Supported types such as `time.Duration` and
the `bee` types listed above remain supported.

## Order of precedence:

- command line options
- environment variables
- default values

Fields tagged with `req` must be supplied by the user through either an
environment variable or command line flag. A field cannot use both `req` and
`def`, because a default value would satisfy the field without user input.
Validation failures return parse errors through the configured
`flag.ErrorHandling` mode.

`req` means the value must be supplied by environment variable or flag.
`nonzero` means the final parsed value, after defaults/env/flags, must not be zero.

| Tag | Applies To | Meaning |
| --- | --- | --- |
| `min` | numbers, `time.Duration` | Minimum final value |
| `max` | numbers, `time.Duration` | Maximum final value |
| `oneof` | strings, numbers, `time.Duration` | Comma-separated allowed values; whitespace is trimmed |
| `len` | strings, `bee.StringSlice`, `bee.IntSlice` | Exact length |
| `minlen` | strings, `bee.StringSlice`, `bee.IntSlice` | Minimum length |
| `maxlen` | strings, `bee.StringSlice`, `bee.IntSlice` | Maximum length |
| `regex` | strings | Regular expression the value must match |
| `prefix` | strings, `bee.URL` | Comma-separated allowed prefixes; whitespace is trimmed |
| `suffix` | strings, `bee.URL` | Comma-separated allowed suffixes; whitespace is trimmed |
| `nonzero` | all supported types | Final parsed value must not be the zero value |

## [Examples](example_test.go)

Run `go test -v` to see examples output.

## App setup

`bee.New` is the entry point for services and operational commands. It creates a
typed `*bee.App[T]` that owns config parsing, logging, application context,
supervised goroutines, signal handling, and shutdown cleanup. A typical app:

- defines a config struct with `flag`, `env`, `help`, and `def` tags;
- calls `bee.New(name, &cfg, options...)`;
- accesses app-level runtime state through `app.Cfg`, `app.Log`, and `app.Ctx`;
- registers command trees with `app.Cmd(...).Cmd(...)`;
- uses `ctx.Cfg`, `ctx.Log`, and `ctx.Ctx` inside handlers;
- starts long-running work with `ctx.Go`;
- starts HTTP servers with `ctx.HTTPServer`;
- registers cleanup with `ctx.Register`;
- stops the app with `ctx.Exit`;
- calls `app.Run()`.

```go
package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.acim.net/bee"
)

type Config struct {
	HTTP struct {
		Addr string `def:":8080"`
	}
}

func main() {
	cfg := Config{}
	app := bee.New("maia", &cfg,
		bee.WithLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil))),
		bee.WithShutdownTimeout(30*time.Second),
	)

	start := app.Cmd("start", "Start services")

	start.Cmd("api", "Run HTTP API", func(ctx *bee.Ctx[Config]) error {
		server := &http.Server{
			Addr: ctx.Cfg.HTTP.Addr,
		}
		ctx.HTTPServer("http api", server)
		ctx.Log.Info("api started", "addr", ctx.Cfg.HTTP.Addr)

		return nil
	})

	app.Run()
}
```

Command nodes can be nested to build command trees:

```go
start := app.Cmd("start", "Start services")
start.Cmd("api", "Run HTTP API", runAPI)
start.Cmd("worker", "Run worker", runWorker)
app.Cmd("migrate", "Run migrations").Cmd("up", "Apply migrations", migrateUp)
```

Users then run:

```text
maia start api
maia start worker
maia migrate up
```

Register a root handler for invocations without a subcommand:

```go
app.Root("Run service", runService)
app.Run()
```

The root handler can be used alongside named commands:

```go
app := bee.New("maia", &cfg)
app.Root("Run service", runService)
app.Cmd("admin", "Run an admin operation", runAdmin)
app.Run()
```

Empty and flags-only invocations run the root handler, while named commands
continue to dispatch normally. Top-level help still prints usage without
running the root handler. If `WithDefaultCommand` is configured, its named
command takes precedence over the root handler.

### Graceful shutdown

`ctx.HTTPServer` starts the server as a supervised goroutine. Cancellation stops
intake and drains active requests using a fresh context. The drain timeout
falls back to `WithShutdownTimeout` (five seconds by default); give HTTP its
own budget when requests need longer:

```go
app := bee.New("api", &cfg, bee.WithShutdownTimeout(5*time.Second))
app.Root("serve", func(ctx *bee.Ctx[Config]) error {
    // Construct server and register dependencies here.
    ctx.HTTPServer("http api", server, bee.WithHTTPDrainTimeout(130*time.Second))
    return nil
})
```

If the HTTP drain deadline expires, Bee cancels remaining request contexts,
closes their connections, and **waits for handlers to return before closing
registered dependencies**. The shutdown error is returned by `RunE`.
Request context values and client cancellation remain intact. Late requests
cannot enter the application handler once forced shutdown begins. Hijacked
connections remain the application's responsibility.

Registered closers run in reverse order after supervised work finishes. Each
closer receives its own `WithShutdownTimeout` budget; it is not a global process
deadline and does not forcibly stop a closer that ignores its context. Budget
for HTTP draining, finalization, and all sequential closers in the process
supervisor's termination grace. Handlers that ignore cancellation can keep Bee
waiting; the process supervisor owns the hard termination deadline.

### Cancellation-aware command input

Use `ctx.Input(os.Stdin)` instead of scanning stdin directly. It returns an
`*Input` implementing `io.ReadCloser`; cancellation unblocks pending pipe and
terminal reads and returns the context error. No additional signal handler or
reader goroutine is needed in the application.

```go
input, err := ctx.Input(os.Stdin)
if err != nil {
    return err
}
defer func() {
    if err := input.Close(); err != nil {
        ctx.Log.Warn("close input", bee.SlogError(err))
    }
}()
scanner := bufio.NewScanner(input)
for scanner.Scan() {
    // Handle scanner.Text().
}
if errors.Is(scanner.Err(), context.Canceled) {
    return nil // Treat cancellation while waiting for input as a clean exit.
}
return scanner.Err()
```

`Close` is idempotent and leaves the original file open. Input temporarily owns
reading and file-status flags: do not read, close, or change the original file
until Input has been closed. Duplicate descriptors share their read offset.
Linux, macOS, FreeBSD, OpenBSD, NetBSD, and DragonFly are supported; other
platforms return `errors.ErrUnsupported`. Regular files and `/dev/null` work
for redirected input, but a kernel disk read in progress cannot be interrupted.
Unsupported blocking devices are rejected instead of promising cancellation.
The pipe tests run in Go; terminal signal integration tests additionally use
Python 3's standard `pty` module and report a skip when Python is unavailable.

### Migration from `NewService`

`NewService` has been removed in favor of the typed `bee.New[T]` API. Move
startup code into a `Root` or `Command` handler, replace direct config access
with `ctx.Cfg`, replace service logs with `ctx.Log`, replace manually owned
contexts with `ctx.Ctx`, replace manual goroutines with `ctx.Go`, and keep
shutdown callbacks on `ctx.Register`.

## HTTP Middlewares

`bee.Middlewares` is a small helper for standard Go HTTP middleware:

```go
type Middlewares []func(http.Handler) http.Handler
```

`Add` appends middleware to the stack. `Wrap` starts with the supplied `*http.ServeMux`
and applies middleware in reverse index order, so requests execute in the same order
the middleware was added. If the stack is empty, `Wrap` returns the original mux.
For `Add(first)` followed by `Add(second)`, request execution is:
`first before -> second before -> handler -> second after -> first after`.

```go
var mws bee.Middlewares

mws.Add(func(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("first before")
		next.ServeHTTP(w, r)
		fmt.Println("first after")
	})
})

mws.Add(func(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("second before")
		next.ServeHTTP(w, r)
		fmt.Println("second after")
	})
})

mux := http.NewServeMux()
mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
	fmt.Println("handler")
})

http.ListenAndServe(":8080", mws.Wrap(mux))
```

For one request, this prints:

```text
first before
second before
handler
second after
first after
```

Middlewares are plain `net/http` middleware functions, not bee-specific
route-aware middleware.

## Global and route-local middleware

Global middleware should be used for cross-cutting behavior such as logging,
security headers, recovery, and request IDs. Anything passed through
`mws.Wrap(mux)` sits in front of the whole mux and runs before `net/http`
`ServeMux` route selection.

```go
app.Root("Run service", func(ctx *bee.Ctx[Config]) error {
	var mws bee.Middlewares

	mws.Add(bee.SlogLogger(ctx.Log))
	mws.Add(securityHeadersMiddleware)
	mws.Add(recoveryMiddleware)
	mws.Add(requestIDMiddleware)

	mux := http.NewServeMux()

	server := &http.Server{
		Addr:    ":8080",
		Handler: mws.Wrap(mux),
	}

	ctx.HTTPServer("http api", server)

	return nil
})
```

Route-specific behavior, such as auth or CORS for only part of the service,
should usually wrap a sub-mux or handler before mounting it on the root mux:

```go
mux := http.NewServeMux()

apiMux := http.NewServeMux()
apiMux.HandleFunc("GET /api/users", usersHandler)

mux.Handle("/api/", authMiddleware(apiMux))

server := &http.Server{
	Addr:    ":8080",
	Handler: mws.Wrap(mux),
}
```

## License

Licensed under either of

- Apache License, Version 2.0
  ([LICENSE-APACHE](LICENSE-APACHE) or http://www.apache.org/licenses/LICENSE-2.0)
- MIT license
  ([LICENSE-MIT](LICENSE-MIT) or http://opensource.org/licenses/MIT)

at your option.

## Contribution

Unless you explicitly state otherwise, any contribution intentionally submitted
for inclusion in the work by you, as defined in the Apache-2.0 license, shall be
dual licensed as above, without any additional terms or conditions.
