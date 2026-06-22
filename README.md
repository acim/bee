# bee

Microservices oriented [12-factor](https://12factor.net) Go library for parsing environment variables and command line flags to arbitrary config struct using struct tags to define default values and to override flag names and environment variables' names.

[![pipeline](https://github.com/acim/bee/actions/workflows/pipeline.yml/badge.svg)](https://github.com/acim/bee/actions/workflows/pipeline.yml)
[![reference](https://pkg.go.dev/badge/go.acim.net/bee.svg)](https://pkg.go.dev/go.acim.net/bee)
[![report](https://goreportcard.com/badge/go.acim.net/bee)](https://goreportcard.com/report/go.acim.net/bee)
![coverage](https://img.shields.io/badge/coverage-95.4%25-brightgreen?style=flat&logo=go)

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

## Order of precedence:

- command line options
- environment variables
- default values

Fields tagged with `req` must be supplied by the user through either an
environment variable or command line flag. A field cannot use both `req` and
`def`, because a default value would satisfy the field without user input.

## [Examples](example_test.go)

Run `go test -v` to see examples output.

## App setup

`bee.New` is the entry point for services and operational commands. It creates a
typed `*bee.App[T]` that owns config parsing, logging, application context,
supervised goroutines, signal handling, and shutdown cleanup. A typical app:

- defines a config struct with `flag`, `env`, `help`, and `def` tags;
- calls `bee.New(name, &cfg, options...)`;
- registers one or more commands with `app.Command`;
- uses `app.Config()`, `app.Log()`, and `app.Context()` inside handlers;
- starts long-running work with `app.Go`;
- registers cleanup with `app.Register`;
- calls `app.Run()`.

Commands are flat, normalized paths such as `start api`, `start worker`, and
`migrate`. Command tokens come before flags, so `maia start api --port :8080`
selects `start api` and parses `--port` into config. If no command is supplied,
`bee.WithDefaultCommand("start api")` selects the default. Apps without a command
set can use `app.Root("Run app", handler)`.

`app.Context()` is cancelled on SIGINT/SIGTERM, `app.Exit`, and fail-fast
supervised goroutine errors. After cancellation, `app.Run()` waits for
supervised goroutines to stop, then runs registered closers in reverse
registration order. Closers receive a fresh shutdown context controlled by
`bee.WithShutdownTimeout`.

```go
package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.acim.net/bee"
)

type config struct {
	Port     string `def:":8080" flag:"port"`
	LogLevel string `def:"INFO"`
}

func main() {
	cfg := config{}
	app := bee.New("maia", &cfg,
		bee.WithDefaultCommand("start api"),
		bee.WithShutdownTimeout(30*time.Second),
	)

	app.Command("start api", "Run the HTTP API server", func(app *bee.App[config]) error {
		cfg := app.Config()
		log := app.Log()

		mux := http.NewServeMux()
		mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})

		var mws bee.Middlewares
		mws.Add(bee.SlogLogger(log))

		server := &http.Server{
			Addr:    cfg.Port,
			Handler: mws.Wrap(mux),
		}

		app.Register("http server", server.Shutdown)
		app.Go("http server", func(ctx context.Context) error {
			if err := server.ListenAndServe(); err != nil &&
				!errors.Is(err, http.ErrServerClosed) {
				return err
			}

			return nil
		})

		log.Info("starting API", "port", cfg.Port)

		return nil
	})

	app.Command("start worker", "Run the background worker", func(app *bee.App[config]) error {
		app.Go("worker", func(ctx context.Context) error {
			<-ctx.Done()

			return nil
		})

		app.Log().Info("starting worker")

		return nil
	})

	app.Run()
}
```

### Migration from `NewService`

`NewService` has been removed in favor of the typed `bee.New[T]` API. Move
startup code into a `Root` or `Command` handler, replace direct config access
with `app.Config()`, replace `svc.Log` with `app.Log()`, replace manually owned
contexts with `app.Context()`, replace manual goroutines with `app.Go`, and keep
shutdown callbacks on `app.Register`.

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
var mws bee.Middlewares

mws.Add(bee.SlogLogger(svc.Log))
mws.Add(securityHeadersMiddleware)
mws.Add(recoveryMiddleware)
mws.Add(requestIDMiddleware)

mux := http.NewServeMux()

server := &http.Server{
	Addr:    ":8080",
	Handler: mws.Wrap(mux),
}
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
