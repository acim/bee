# bee

Microservices oriented [12-factor](https://12factor.net) Go library for parsing environment variables and command line flags to arbitrary config struct using struct tags to define default values and to override flag names and environment variables' names.

[![pipeline](https://github.com/acim/bee/actions/workflows/pipeline.yml/badge.svg)](https://github.com/acim/bee/actions/workflows/pipeline.yml)
[![reference](https://pkg.go.dev/badge/go.acim.net/bee.svg)](https://pkg.go.dev/go.acim.net/bee)
[![report](https://goreportcard.com/badge/go.acim.net/bee)](https://goreportcard.com/report/go.acim.net/bee)
![coverage](https://img.shields.io/badge/coverage-99.0%25-brightgreen?style=flat&logo=go)

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

## [Examples](example_test.go)

Run `go test -v` to see examples output.

## Service setup

```go
package main

import (
	"net/http"

	"go.acim.net/bee"
)

type config struct {
	Port     string `def:":8080"`
	LogLevel string `def:"DEBUG"`
}

func main() {
	cfg := config{}
	svc := bee.NewService("myCoolApp", &cfg, bee.WithLogLevel(cfg.LogLevel))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	server := &http.Server{
		Addr:    cfg.Port,
		Handler: mux,
	}

	svc.Register("http server", server.Shutdown)

	go func() {
		if err := server.ListenAndServe(); err != nil {
			bee.Exit("failed to run HTTP server", err)
		}
	}()

	svc.Log.Info("starting myCoolApp", "port", cfg.Port)
	svc.Run()
}
```

## HTTP Middlewares

`bee.Middlewares` is a small helper for standard Go HTTP middleware:

```go
type Middlewares []func(http.Handler) http.Handler
```

`Add` appends middleware to the stack. `Wrap` starts with the supplied `*http.ServeMux`
and applies middleware in reverse index order, so requests execute in the same order
the middleware was added. If the stack is empty, `Wrap` returns the original mux.

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

Global middleware, such as logging, security headers, and recovery, should be added
to a shared stack and used as the server handler:

```go
var mws bee.Middlewares

mws.Add(loggerMiddleware)
mws.Add(securityHeadersMiddleware)
mws.Add(recoveryMiddleware)

mux := http.NewServeMux()

server := &http.Server{
	Addr:    ":8080",
	Handler: mws.Wrap(mux),
}
```

Middlewares are not bee-specific or route-aware. Anything passed through
`mws.Wrap(mux)` sits in front of the whole mux and runs before `net/http`
`ServeMux` route selection. Route-specific behavior should usually wrap a
sub-router or handler before it is mounted:

```go
mux := http.NewServeMux()

apiMux := http.NewServeMux()
apiMux.HandleFunc("GET /users", usersHandler)

mux.Handle("/api/", http.StripPrefix("/api", authMiddleware(apiMux)))

server := &http.Server{
	Addr:    ":8080",
	Handler: mws.Wrap(mux),
}
```

## TODO

- support req struct tag to mark required values

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
