package bee

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultShutdownGracePeriod = 5 * time.Second
	exitCode                   = 2
)

var osExit = os.Exit

// App is a typed application runner with config parsing, commands, context,
// supervised goroutines, and graceful shutdown.
type App[T any] struct {
	name        string
	cfg         *T
	commandLine *commandLine
	timeout     time.Duration
	logLevel    slog.Leveler
	log         *slog.Logger
	output      io.Writer
	defaultCmd  string
	parentUsage string
	commands    map[string]command[T]
	root        *command[T]
	closers     []c
	ctx         context.Context
	cancel      context.CancelFunc
	signalCh    chan os.Signal
	wg          sync.WaitGroup
	wgMu        sync.Mutex
	goroutines  int
	errMu       sync.Mutex
	runErr      error
}

type command[T any] struct {
	path        string
	description string
	handler     func(app *App[T]) error
}

type appOptions struct {
	timeout       time.Duration
	logLevel      slog.Leveler
	output        io.Writer
	lookupEnvFunc func(string) (string, bool)
	errorHandling flag.ErrorHandling
	defaultCmd    string
	parentUsage   string
}

// Option defines application option type.
type Option func(*appOptions)

// New creates a typed application.
func New[T any](name string, cfg *T, opts ...Option) *App[T] {
	options := appOptions{ //nolint:exhaustruct
		timeout:       defaultShutdownGracePeriod,
		output:        os.Stderr,
		lookupEnvFunc: os.LookupEnv,
		errorHandling: flag.ExitOnError,
	}
	for _, opt := range opts {
		opt(&options)
	}

	cl := newCommandLine(name)
	cl.output = options.output
	cl.lookupEnvFunc = options.lookupEnvFunc
	cl.errorHandling = options.errorHandling
	cl.flagSet.SetOutput(options.output)

	ctx, cancel := context.WithCancel(context.Background())
	app := &App[T]{ //nolint:exhaustruct
		name:        name,
		cfg:         cfg,
		commandLine: cl,
		timeout:     options.timeout,
		logLevel:    options.logLevel,
		output:      options.output,
		defaultCmd:  normalizeCommandPath(options.defaultCmd),
		parentUsage: options.parentUsage,
		commands:    map[string]command[T]{},
		ctx:         ctx,
		cancel:      cancel,
		signalCh:    make(chan os.Signal, 1),
	}
	app.log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: app.logLevel})) //nolint:exhaustruct

	if options.parentUsage != "" {
		app.commandLine.flagSet.Usage = func() {
			_, _ = fmt.Fprintf(app.output, "Usage of %s %s:\n", options.parentUsage, app.name)
			app.commandLine.flagSet.PrintDefaults()
		}
	}

	return app
}

// Config returns the parsed application config.
func (a *App[T]) Config() *T {
	return a.cfg
}

// Log returns the application logger.
func (a *App[T]) Log() *slog.Logger {
	return a.log
}

// Context returns the root application context.
func (a *App[T]) Context() context.Context {
	return a.ctx
}

// Command registers a command path.
func (a *App[T]) Command(path string, description string, handler func(app *App[T]) error) {
	path = normalizeCommandPath(path)
	if path == "" {
		a.Root(description, handler)

		return
	}

	if _, ok := a.commands[path]; ok {
		panic(fmt.Sprintf("duplicate command %q", path))
	}

	a.commands[path] = command[T]{path: path, description: description, handler: handler}
}

// Root registers the handler used when no commands are registered.
func (a *App[T]) Root(description string, handler func(app *App[T]) error) {
	a.root = &command[T]{description: description, handler: handler}
}

// Register registers closer to be called on graceful shutdown.
func (a *App[T]) Register(name string, closer func(ctx context.Context) error) {
	a.closers = append(a.closers, c{name: name, inner: closer})
}

// Go starts a supervised goroutine with the application context.
func (a *App[T]) Go(name string, fn func(context.Context) error) {
	a.wgMu.Lock()
	a.goroutines++
	a.wgMu.Unlock()

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()

		a.log.Debug("goroutine start", slog.String("name", name))
		if err := fn(a.ctx); err != nil {
			a.log.Error("goroutine error", slog.String("name", name), SlogError(err))
			a.recordErr(err)
			a.cancel()

			return
		}
		a.log.Debug("goroutine stop", slog.String("name", name))
	}()
}

// HTTPServer starts an HTTP server as a supervised goroutine and shuts it down
// when the application context is cancelled.
func (a *App[T]) HTTPServer(name string, server *http.Server) {
	a.Go(name, func(ctx context.Context) error {
		shutdownDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), a.timeout)
				defer cancel()
				_ = server.Shutdown(shutdownCtx)
			case <-shutdownDone:
			}
		}()

		err := server.ListenAndServe()
		close(shutdownDone)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	})
}

// Exit records a fatal application result and cancels the application context.
func (a *App[T]) Exit(message string, err error) {
	if err != nil {
		a.log.Error(message, SlogError(err))
		a.recordErr(err)
	} else {
		a.log.Info(message)
		if message == "" {
			message = "application exit"
		}
		a.recordErr(errors.New(message))
	}

	a.cancel()
}

// Run runs the application and exits the process on failure.
func (a *App[T]) Run() {
	if err := a.RunE(os.Args[1:]...); err != nil {
		osExit(exitCode)
	}
}

// RunE runs the application and returns a testable error instead of exiting.
func (a *App[T]) RunE(args ...string) error {
	signal.Notify(a.signalCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(a.signalCh)
	defer a.cancel()

	go func() {
		select {
		case <-a.ctx.Done():
		case <-a.signalCh:
			a.cancel()
		}
	}()

	cmd, flags, err := a.selectCommand(args)
	if err != nil {
		a.writeUsage(nil)

		return err
	}

	if cmd == nil {
		if len(a.commands) == 0 {
			a.writeUsage(nil)

			return errors.New("no root command registered")
		}

		a.writeUsage(nil)

		return errors.New("no command supplied")
	}

	a.setUsage(cmd)
	if err := a.commandLine.parse(a.cfg, flags); err != nil {
		return err
	}

	if a.commandLine.help {
		return nil
	}

	if err := cmd.handler(a); err != nil {
		a.recordErr(err)
		a.cancel()
	} else if a.goroutineCount() == 0 {
		a.cancel()
	}

	<-a.ctx.Done()
	a.wg.Wait()
	a.runClosers()

	return a.err()
}

// WithShutdownTimeout can be used to set the shutdown timeout.
func WithShutdownTimeout(d time.Duration) Option {
	return func(o *appOptions) {
		o.timeout = d
	}
}

// WithShutdownGracePeriod can be used to set the shutdown grace period.
func WithShutdownGracePeriod(d time.Duration) Option {
	return WithShutdownTimeout(d)
}

// WithDefaultCommand configures the command used when no command is supplied.
func WithDefaultCommand(path string) Option {
	return func(o *appOptions) {
		o.defaultCmd = path
	}
}

// WithLogLevel can be used to set default log level.
func WithLogLevel(l string) Option {
	return func(o *appOptions) {
		switch strings.ToUpper(l) {
		case "INFO":
			o.logLevel = slog.LevelInfo
		case "WARN":
			o.logLevel = slog.LevelWarn
		case "ERROR":
			o.logLevel = slog.LevelError
		default:
			o.logLevel = slog.LevelDebug
		}
	}
}

// WithErrorHandling is an option to change error handling similar to flag package.
func WithErrorHandling(errorHandling flag.ErrorHandling) Option {
	return func(o *appOptions) {
		o.errorHandling = errorHandling
	}
}

// WithOutput is an option to change the output writer similar as flag.SetOutput does.
func WithOutput(w io.Writer) Option {
	return func(o *appOptions) {
		o.output = w
	}
}

// WithLookupEnvFunc may be used to override default os.LookupEnv function to read environment variables values.
func WithLookupEnvFunc(fn func(string) (string, bool)) Option {
	return func(o *appOptions) {
		o.lookupEnvFunc = fn
	}
}

// WithUsage allows to prefix your command name with a parent command name.
func WithUsage(parentCmdName string) Option {
	return func(o *appOptions) {
		o.parentUsage = parentCmdName
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

	osExit(exitCode)
}

// SlogError create slog string attribute to handle error logs.
func SlogError(err error) slog.Attr {
	return slog.String("error", err.Error())
}

type c struct {
	name  string
	inner func(ctx context.Context) error
}

func (a *App[T]) selectCommand(args []string) (*command[T], []string, error) {
	if hasHelp(args) && len(commandTokens(args)) == 0 && len(a.commands) > 0 {
		a.setUsage(nil)

		return &command[T]{handler: func(app *App[T]) error { return nil }}, args, nil
	}

	if len(a.commands) == 0 {
		return a.root, args, nil
	}

	tokens := commandTokens(args)
	if len(tokens) == 0 {
		if a.defaultCmd == "" {
			return nil, args, nil
		}

		cmd, ok := a.commands[a.defaultCmd]
		if !ok {
			return nil, args, fmt.Errorf("unknown default command %q", a.defaultCmd)
		}

		return &cmd, args, nil
	}

	path, consumed, ok := a.longestCommand(tokens)
	if !ok {
		return nil, args, fmt.Errorf("unknown command %q", strings.Join(tokens, " "))
	}

	cmd := a.commands[path]
	flags := args[consumed:]

	return &cmd, flags, nil
}

func (a *App[T]) longestCommand(tokens []string) (string, int, bool) {
	for i := len(tokens); i > 0; i-- {
		path := strings.Join(tokens[:i], " ")
		if _, ok := a.commands[path]; ok {
			if i != len(tokens) {
				return "", 0, false
			}

			return path, i, true
		}
	}

	return "", 0, false
}

func (a *App[T]) setUsage(cmd *command[T]) {
	a.commandLine.flagSet.Usage = func() {
		a.writeUsage(cmd)
	}
}

func (a *App[T]) writeUsage(cmd *command[T]) {
	name := a.name
	if a.parentUsage != "" {
		name = a.parentUsage + " " + name
	}
	if cmd != nil && cmd.path != "" {
		name += " " + cmd.path
	}

	_, _ = fmt.Fprintf(a.output, "Usage of %s:\n", name)
	if cmd != nil && cmd.description != "" {
		_, _ = fmt.Fprintln(a.output, cmd.description)
	}

	if a.defaultCmd != "" && (cmd == nil || cmd.path == "") {
		_, _ = fmt.Fprintf(a.output, "\nDefault command: %s\n", a.defaultCmd)
	}

	if (cmd == nil || cmd.path == "") && len(a.commands) > 0 {
		_, _ = fmt.Fprintln(a.output, "\nCommands:")
		for _, path := range a.commandPaths() {
			c := a.commands[path]
			_, _ = fmt.Fprintf(a.output, "  %-18s %s\n", c.path, c.description)
		}
	}

	if a.commandLine.flagSet != nil {
		_, _ = fmt.Fprintln(a.output, "\nFlags:")
		a.commandLine.flagSet.PrintDefaults()
	}
}

func (a *App[T]) commandPaths() []string {
	paths := make([]string, 0, len(a.commands))
	for path := range a.commands {
		paths = append(paths, path)
	}

	sort.Strings(paths)

	return paths
}

func (a *App[T]) runClosers() {
	closers := slices.Clone(a.closers)
	slices.Reverse(closers)
	if len(closers) > 0 {
		a.log.Info("graceful shutdown", slog.Duration("grace period", a.timeout))
	}

	for _, f := range closers {
		a.log.Debug("closing " + f.name)

		ctx, cancel := context.WithTimeout(context.Background(), a.timeout)
		err := f.inner(ctx)
		cancel()
		if err != nil {
			a.log.Warn("closer "+f.name, SlogError(err))
			a.recordErr(err)
		}
	}
}

func (a *App[T]) recordErr(err error) {
	if err == nil {
		return
	}

	a.errMu.Lock()
	defer a.errMu.Unlock()

	a.runErr = errors.Join(a.runErr, err)
}

func (a *App[T]) goroutineCount() int {
	a.wgMu.Lock()
	defer a.wgMu.Unlock()

	return a.goroutines
}

func (a *App[T]) err() error {
	a.errMu.Lock()
	defer a.errMu.Unlock()

	return a.runErr
}

func normalizeCommandPath(path string) string {
	return strings.Join(strings.Fields(path), " ")
}

func commandTokens(args []string) []string {
	tokens := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			break
		}

		tokens = append(tokens, arg)
	}

	return tokens
}

func hasHelp(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--help", "-help", "--h", "-h":
			return true
		}
	}

	return false
}

// Ref returns a reference to a given value.
func Ref[T any](v T) *T {
	return &v
}
