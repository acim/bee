package bee

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
)

const (
	defaultShutdownGracePeriod = 5 * time.Second
	exitCode                   = 2
)

// Service is an microservice abstraction providing graceful shutdown.
type Service struct {
	name     string
	stop     chan os.Signal
	timeout  time.Duration
	closers  []c
	logLevel slog.Leveler
	Log      *slog.Logger
}

// NewService creates microservice.
func NewService(name string, opts ...Option) *Service {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	s := &Service{ //nolint:exhaustruct
		name:    name,
		timeout: defaultShutdownGracePeriod,
		stop:    stop,
	}

	for _, opt := range opts {
		opt(s)
	}

	s.Log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: s.logLevel})) //nolint:exhaustruct

	return s
}

func (a *Service) ParseCommandLine(config interface{}, opts ...CommandLineOption) error {
	cl := newCommandLine(a.name, opts)

	return cl.parse(config, os.Args[1:])
}

// Register registers closer to be called on graceful shutdown.
func (a *Service) Register(name string, closer func(ctx context.Context) error) {
	a.closers = append(a.closers, c{name: name, inner: closer})
}

// Run runs the microservice and takes care of the graceful shutdown.
func (a *Service) Run() {
	<-a.stop

	signal.Stop(a.stop)

	ctx, cancel := context.WithTimeout(context.Background(), a.timeout)
	defer cancel()

	a.Log.Info("graceful shutdown", slog.Duration("grace period", a.timeout))

	slices.Reverse(a.closers)

	for _, f := range a.closers {
		a.Log.Debug("closing " + f.name)

		if err := f.inner(ctx); err != nil {
			a.Log.Warn("closer "+f.name, SlogError(err))
		}
	}
}

// Option defines application option type.
type Option func(*Service)

// WithShutdownGracePeriod can be used to set the shutdown grace period.
func WithShutdownGracePeriod(d time.Duration) Option {
	return func(a *Service) {
		a.timeout = d
	}
}

// WithLogLevel can be used to set default log level.
func WithLogLevel(l string) Option {
	return func(a *Service) {
		switch strings.ToUpper(l) {
		case "INFO":
			a.logLevel = slog.LevelInfo
		case "WARN":
			a.logLevel = slog.LevelWarn
		case "ERROR":
			a.logLevel = slog.LevelError
		default:
			a.logLevel = slog.LevelDebug
		}
	}
}

// Exit logs exit reason using standard library log package and exits process with the default exit code.
func Exit(m string, err error) {
	switch err {
	case nil:
		_, _ = fmt.Fprintln(os.Stderr, m)
	default:
		_, _ = fmt.Fprintf(os.Stderr, "%s: %s\n", m, err)
	}

	os.Exit(exitCode)
}

// SlogError create slog string attribute to handle error logs.
func SlogError(err error) slog.Attr {
	return slog.String("error", err.Error())
}

type c struct {
	name  string
	inner func(ctx context.Context) error
}
