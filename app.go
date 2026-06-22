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
	"unicode"
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
	Cfg         *T
	commandLine *commandLine
	timeout     time.Duration
	logLevel    slog.Leveler
	Log         *slog.Logger
	output      io.Writer
	defaultCmd  string
	parentUsage string
	commands    map[string]*Cmd[T]
	root        *Cmd[T]
	closers     []c
	Ctx         context.Context
	cancel      context.CancelFunc
	signalCh    chan os.Signal
	wg          sync.WaitGroup
	wgMu        sync.Mutex
	goroutines  int
	errMu       sync.Mutex
	runErr      error
}

// Handler is an application or command handler.
type Handler[T any] func(Ctx[T]) error

// Ctx provides runtime access to application config and services.
type Ctx[T any] struct {
	Cfg *T
	Log *slog.Logger
	Ctx context.Context

	app *App[T]
}

// Cmd is an application command node.
type Cmd[T any] struct {
	name        string
	path        string
	description string
	handler     Handler[T]
	parent      *Cmd[T]
	children    map[string]*Cmd[T]
	app         *App[T]
}

type appOptions struct {
	timeout       time.Duration
	logLevel      slog.Leveler
	log           *slog.Logger
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
	if cfg == nil {
		panic("bee: invalid nil config")
	}

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
		Cfg:         cfg,
		commandLine: cl,
		timeout:     options.timeout,
		logLevel:    options.logLevel,
		output:      options.output,
		defaultCmd:  normalizeCommandPath(options.defaultCmd),
		parentUsage: options.parentUsage,
		commands:    map[string]*Cmd[T]{},
		Ctx:         ctx,
		cancel:      cancel,
		signalCh:    make(chan os.Signal, 1),
	}
	app.Log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: app.logLevel})) //nolint:exhaustruct
	if options.log != nil {
		app.Log = options.log
	}

	if options.parentUsage != "" {
		app.commandLine.flagSet.Usage = func() {
			_, _ = fmt.Fprintf(app.output, "Usage of %s %s:\n", options.parentUsage, app.name)
			app.commandLine.flagSet.PrintDefaults()
		}
	}

	return app
}

// Register registers closer to be called on graceful shutdown.
func (c Ctx[T]) Register(name string, closer func(ctx context.Context) error) {
	c.appRuntime().Register(name, closer)
}

// Go starts a supervised goroutine with the application context.
func (c Ctx[T]) Go(name string, fn func(context.Context) error) {
	c.appRuntime().Go(name, fn)
}

// HTTPServer starts an HTTP server as a supervised goroutine.
func (c Ctx[T]) HTTPServer(name string, server *http.Server) {
	c.appRuntime().HTTPServer(name, server)
}

// Exit records a fatal application result and cancels the application context.
func (c Ctx[T]) Exit(message string, err error) {
	c.appRuntime().Exit(message, err)
}

func (c Ctx[T]) appRuntime() *App[T] {
	if c.app == nil {
		panic("bee: runtime method called on context not created by App")
	}

	return c.app
}

func (a *App[T]) runtimeContext() Ctx[T] {
	return Ctx[T]{
		Cfg: a.Cfg,
		Log: a.Log,
		Ctx: a.Ctx,
		app: a,
	}
}

// Cmd registers a root command.
func (a *App[T]) Cmd(name string, description string, handler ...Handler[T]) *Cmd[T] {
	return a.addCommand(nil, name, description, handler...)
}

// Cmd registers a nested command.
func (c *Cmd[T]) Cmd(name string, description string, handler ...Handler[T]) *Cmd[T] {
	return c.appRuntime().addCommand(c, name, description, handler...)
}

func (c *Cmd[T]) appRuntime() *App[T] {
	if c == nil || c.app == nil {
		panic("bee: command method called on command not created by App")
	}

	return c.app
}

// Root registers the handler used when no commands are registered.
func (a *App[T]) Root(description string, handler Handler[T]) {
	a.root = &Cmd[T]{description: description, handler: handler, app: a}
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

		a.Log.Debug("goroutine start", slog.String("name", name))
		if err := fn(a.Ctx); err != nil {
			a.Log.Error("goroutine error", slog.String("name", name), SlogError(err))
			a.recordErr(err)
			a.cancel()

			return
		}
		a.Log.Debug("goroutine stop", slog.String("name", name))
	}()
}

// HTTPServer starts an HTTP server as a supervised goroutine and shuts it down
// when the application context is cancelled.
func (a *App[T]) HTTPServer(name string, server *http.Server) {
	a.Go(name, func(ctx context.Context) error {
		shutdownStarted := make(chan struct{})
		shutdownErr := make(chan error, 1)
		serveDone := make(chan struct{})

		go func() {
			select {
			case <-ctx.Done():
				close(shutdownStarted)
				shutdownCtx, cancel := context.WithTimeout(context.Background(), a.timeout)
				defer cancel()
				shutdownErr <- server.Shutdown(shutdownCtx)
			case <-serveDone:
			}
		}()

		err := server.ListenAndServe()
		close(serveDone)
		if errors.Is(err, http.ErrServerClosed) {
			select {
			case <-shutdownStarted:
				if err := <-shutdownErr; err != nil {
					return err
				}
			default:
			}

			return nil
		}

		return err
	})
}

// Exit records a fatal application result and cancels the application context.
func (a *App[T]) Exit(message string, err error) {
	if err != nil {
		a.Log.Error(message, SlogError(err))
		a.recordErr(err)
	} else {
		a.Log.Info(message)
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
		case <-a.Ctx.Done():
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
	if err := a.commandLine.parse(a.Cfg, flags); err != nil {
		return err
	}

	if a.commandLine.help {
		return nil
	}

	if err := cmd.handler(a.runtimeContext()); err != nil {
		a.recordErr(err)
		a.cancel()
	} else if a.goroutineCount() == 0 {
		a.cancel()
	}

	<-a.Ctx.Done()
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

// WithLogger can be used to inject an application logger.
func WithLogger(log *slog.Logger) Option {
	return func(o *appOptions) {
		o.log = log
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

func (a *App[T]) selectCommand(args []string) (*Cmd[T], []string, error) {
	if hasHelp(args) && len(commandTokens(args)) == 0 && len(a.commands) > 0 {
		a.setUsage(nil)

		return &Cmd[T]{handler: func(Ctx[T]) error { return nil }}, args, nil
	}

	if len(a.commands) == 0 {
		return a.root, args, nil
	}

	tokens := commandTokens(args)
	if len(tokens) == 0 {
		if a.defaultCmd == "" {
			return nil, args, nil
		}

		cmd, _, ok := a.findCommand(strings.Fields(a.defaultCmd))
		if !ok || cmd.handler == nil {
			return nil, args, fmt.Errorf("unknown default command %q", a.defaultCmd)
		}

		return cmd, args, nil
	}

	cmd, consumed, ok := a.findCommand(tokens)
	if !ok || (cmd.handler == nil && !hasHelp(args)) {
		return nil, args, fmt.Errorf("unknown command %q", strings.Join(tokens, " "))
	}

	flags := args[consumed:]

	return cmd, flags, nil
}

func (a *App[T]) findCommand(tokens []string) (*Cmd[T], int, bool) {
	if len(tokens) == 0 {
		return nil, 0, false
	}

	cmd, ok := a.commands[tokens[0]]
	if !ok {
		return nil, 0, false
	}

	for i := 1; i < len(tokens); i++ {
		next, ok := cmd.children[tokens[i]]
		if !ok {
			return nil, 0, false
		}
		cmd = next
	}

	return cmd, len(tokens), true
}

func (a *App[T]) setUsage(cmd *Cmd[T]) {
	a.commandLine.flagSet.Usage = func() {
		a.writeUsage(cmd)
	}
}

func (a *App[T]) writeUsage(cmd *Cmd[T]) {
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

	if paths := a.commandPathsFrom(cmd); len(paths) > 0 {
		_, _ = fmt.Fprintln(a.output, "\nCommands:")
		for _, path := range paths {
			c, _, _ := a.findCommand(strings.Fields(path))
			_, _ = fmt.Fprintf(a.output, "  %-18s %s\n", c.path, c.description)
		}
	}

	if a.commandLine.flagSet != nil {
		_, _ = fmt.Fprintln(a.output, "\nFlags:")
		a.commandLine.flagSet.PrintDefaults()
	}
}

func (a *App[T]) commandPaths() []string {
	paths := []string{}
	for _, cmd := range a.commands {
		paths = appendExecutableCommandPaths(paths, cmd)
	}

	sort.Strings(paths)

	return paths
}

func (a *App[T]) commandPathsFrom(cmd *Cmd[T]) []string {
	if cmd == nil || cmd.path == "" {
		return a.commandPaths()
	}

	paths := []string{}
	for _, child := range cmd.children {
		paths = appendExecutableCommandPaths(paths, child)
	}

	sort.Strings(paths)

	return paths
}

func appendExecutableCommandPaths[T any](paths []string, cmd *Cmd[T]) []string {
	if cmd.handler != nil {
		paths = append(paths, cmd.path)
	}

	for _, child := range cmd.children {
		paths = appendExecutableCommandPaths(paths, child)
	}

	return paths
}

func (a *App[T]) addCommand(parent *Cmd[T], name string, description string, handlers ...Handler[T]) *Cmd[T] {
	validateCommandName(name)
	if len(handlers) > 1 {
		panic(fmt.Sprintf("command %q has multiple handlers", name))
	}

	siblings := a.commands
	path := name
	if parent != nil {
		siblings = parent.children
		path = parent.path + " " + name
	}

	if _, ok := siblings[name]; ok {
		panic(fmt.Sprintf("duplicate command %q", name))
	}

	cmd := &Cmd[T]{ //nolint:exhaustruct
		name:        name,
		path:        path,
		description: description,
		parent:      parent,
		children:    map[string]*Cmd[T]{},
		app:         a,
	}
	if len(handlers) == 1 {
		cmd.handler = handlers[0]
	}

	siblings[name] = cmd

	return cmd
}

func validateCommandName(name string) {
	if name == "" || strings.TrimSpace(name) != name || strings.ContainsFunc(name, unicode.IsSpace) {
		panic(fmt.Sprintf("invalid command name %q", name))
	}
}

func (a *App[T]) runClosers() {
	closers := slices.Clone(a.closers)
	slices.Reverse(closers)
	if len(closers) > 0 {
		a.Log.Info("graceful shutdown", slog.Duration("grace period", a.timeout))
	}

	for _, f := range closers {
		a.Log.Debug("closing " + f.name)

		ctx, cancel := context.WithTimeout(context.Background(), a.timeout)
		err := f.inner(ctx)
		cancel()
		if err != nil {
			a.Log.Warn("closer "+f.name, SlogError(err))
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
