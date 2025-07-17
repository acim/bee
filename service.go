package bee

import (
	"context"
	"flag"
	"fmt"
	"io"
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
	name        string
	commandLine *comandLine
	stop        chan os.Signal
	timeout     time.Duration
	closers     []c
	logLevel    slog.Leveler
	Log         *slog.Logger
}

// NewService creates microservice.
func NewService(name string, config interface{}, opts ...Option) *Service {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	s := &Service{ //nolint:exhaustruct
		name:        name,
		commandLine: newCommandLine(name),
		timeout:     defaultShutdownGracePeriod,
		stop:        stop,
	}

	for _, opt := range opts {
		opt(s)
	}

	if err := s.commandLine.parse(config, os.Args[1:]); err != nil {
		Exit("failed parsing command line", err)
	}

	s.Log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: s.logLevel})) //nolint:exhaustruct

	return s
}

// Register registers closer to be called on graceful shutdown.
func (s *Service) Register(name string, closer func(ctx context.Context) error) {
	s.closers = append(s.closers, c{name: name, inner: closer})
}

// Run runs the microservice and takes care of the graceful shutdown.
func (s *Service) Run() {
	<-s.stop

	signal.Stop(s.stop)

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	s.Log.Info("graceful shutdown", slog.Duration("grace period", s.timeout))

	slices.Reverse(s.closers)

	for _, f := range s.closers {
		s.Log.Debug("closing " + f.name)

		if err := f.inner(ctx); err != nil {
			s.Log.Warn("closer "+f.name, SlogError(err))
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
	return func(s *Service) {
		switch strings.ToUpper(l) {
		case "INFO":
			s.logLevel = slog.LevelInfo
		case "WARN":
			s.logLevel = slog.LevelWarn
		case "ERROR":
			s.logLevel = slog.LevelError
		default:
			s.logLevel = slog.LevelDebug
		}
	}
}

// WithErrorHandling is an option to change error handling similar to flag package.
func WithErrorHandling(errorHandling flag.ErrorHandling) Option {
	return func(s *Service) {
		s.commandLine.errorHandling = errorHandling
	}
}

// WithOutput is an option to change the output writer similar as flag.SetOutput does.
func WithOutput(w io.Writer) Option {
	return func(s *Service) {
		s.commandLine.output = w
	}
}

// WithLookupEnvFunc may be used to override default os.LookupEnv function to read environment variables values.
func WithLookupEnvFunc(fn func(string) (string, bool)) Option {
	return func(s *Service) {
		s.commandLine.lookupEnvFunc = fn
	}
}

// WithUsage allows to prefix your command name with a parent command name.
func WithUsage(parentCmdName string) Option {
	return func(s *Service) {
		s.commandLine.flagSet.Usage = func() {
			_, _ = fmt.Fprintf(s.commandLine.output, "Usage of %s %s:\n", parentCmdName, s.name)
			s.commandLine.flagSet.PrintDefaults()
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
