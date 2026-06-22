# Polished App API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current flat command/app-handler API with a polished typed app API that uses config values, command trees, runtime handler contexts, logger injection, documented graceful shutdown ordering, and struct-tag validation.

**Architecture:** Keep `service.go` as the app runtime owner, but split command-tree and handler-context concepts into focused types so app definition and command execution are separate. Keep `command_line.go` responsible for reflection-based config parsing and add a validation pass after flag parsing, before command execution. Update examples and README only after the new API is covered by tests.

**Tech Stack:** Go 1.23, standard library `context`, `flag`, `log/slog`, `net/http`, `os/signal`, `reflect`, `regexp`, `sync`, existing bee config parser and value types.

---

## File Structure

- Modify `service.go`
  - Change `New` to accept a config value.
  - Add `Context[T]` runtime handler type.
  - Add public command node type for command trees.
  - Move runtime methods used by handlers onto `Context[T]`.
  - Keep `Run()` argument-free and `RunE(args ...string) error`.
  - Preserve root/no-command apps.
  - Preserve HTTP server graceful shutdown before registered closers.

- Modify `command_line.go`
  - Keep parsing defaults/env/flags.
  - Add post-parse validation tag handling.
  - Route validation errors through existing `flag.ErrorHandling`.

- Modify `app_test.go`
  - Replace old flat command tests with value-config, context-field, root, and command-tree tests.
  - Add HTTP shutdown ordering tests.

- Modify `command_line_internal_test.go`
  - Add parser-level validation tests for all supported validation tags.
  - Add validation error handling tests for `ContinueOnError`, `ExitOnError`, and `PanicOnError`.
  - Add help bypass tests.

- Modify `service_internal_test.go`
  - Update option tests for value config and `WithLogger`.
  - Keep process-exit tests for `Run`/`Exit`.

- Modify `example_test.go`
  - Update package examples to the polished API.

- Modify `README.md`
  - Document the new app shape, command tree, handler context fields, validation tags, and graceful shutdown order.
  - Update coverage badge only if measured coverage changes.

- Do not commit during execution unless the human explicitly asks. This repository's AGENTS.md overrides the generic plan-template preference for frequent commits.

---

### Task 1: Value Config, Handler Context, Logger Injection, And Root Apps

**Files:**
- Modify: `service.go`
- Modify: `app_test.go`
- Modify: `service_internal_test.go`

- [ ] **Step 1: Write failing app tests for value config, exported context fields, injected logger, runtime methods, and root apps**

Add these tests to `app_test.go`. Replace helper usage only as needed for this task.

```go
func newTestApp(t *testing.T, cfg appTestConfig, output *bytes.Buffer, opts ...Option) *App[appTestConfig] {
	t.Helper()

	allOpts := []Option{
		WithOutput(output),
		WithErrorHandling(flag.ContinueOnError),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	allOpts = append(allOpts, opts...)

	return New("maia", cfg, allOpts...)
}

func TestAppRootReceivesRuntimeContextWithFields(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := newTestApp(t, appTestConfig{}, output, WithLogger(logger))

	var gotConfig appTestConfig
	var gotLog *slog.Logger
	var gotContext context.Context
	var goRan bool

	app.Root("Run service", func(ctx Context[appTestConfig]) error {
		gotConfig = ctx.Config
		gotLog = ctx.Log
		gotContext = ctx.Context
		ctx.Go("short task", func(run context.Context) error {
			goRan = run == ctx.Context
			return nil
		})
		return nil
	})

	if err := app.RunE("--port", "9090", "--http-host", "0.0.0.0"); err != nil {
		t.Fatal(err)
	}

	if gotConfig.Port != 9090 {
		t.Fatalf("want parsed value config port 9090, got %d", gotConfig.Port)
	}
	if gotConfig.HTTP.Host != "0.0.0.0" {
		t.Fatalf("want parsed nested host, got %q", gotConfig.HTTP.Host)
	}
	if gotLog != logger {
		t.Fatal("want handler context to expose injected logger")
	}
	if gotContext == nil {
		t.Fatal("want handler context to expose application context")
	}
	if !goRan {
		t.Fatal("want ctx.Go to pass the application context")
	}
}

func TestAppWithoutCommandsRequiresRoot(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, appTestConfig{}, output)

	err := app.RunE()
	if err == nil {
		t.Fatal("want missing root error")
	}
	if err.Error() != "no root command registered" {
		t.Fatalf("want missing root error, got %v", err)
	}
}
```

- [ ] **Step 2: Run focused tests and verify they fail to compile**

Run outside the sandbox because Go may need the normal build cache:

```bash
go test -count=1 -run 'TestAppRootReceivesRuntimeContextWithFields|TestAppWithoutCommandsRequiresRoot' ./...
```

Expected: FAIL to compile with errors like:

```text
cannot use appTestConfig{} as *appTestConfig value in argument to New
undefined: Context
undefined: WithLogger
```

- [ ] **Step 3: Implement the new public handler context and value-config constructor**

In `service.go`, replace the app config pointer field and command handler shape with these definitions. Keep existing private fields that are not shown.

```go
type Handler[T any] func(Context[T]) error

type Context[T any] struct {
	Config  T
	Log     *slog.Logger
	Context context.Context

	app *App[T]
}

type App[T any] struct {
	name        string
	cfg         T
	commandLine *commandLine
	timeout     time.Duration
	logLevel    slog.Leveler
	log         *slog.Logger
	output      io.Writer
	defaultCmd  string
	parentUsage string
	commands    map[string]*Command[T]
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
	handler     Handler[T]
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
```

Change `New` and add `WithLogger`:

```go
func New[T any](name string, cfg T, opts ...Option) *App[T] {
	options := appOptions{
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
	app := &App[T]{
		name:        name,
		cfg:         cfg,
		commandLine: cl,
		timeout:     options.timeout,
		logLevel:    options.logLevel,
		log:         options.log,
		output:      options.output,
		defaultCmd:  normalizeCommandPath(options.defaultCmd),
		parentUsage: options.parentUsage,
		commands:    map[string]*Command[T]{},
		ctx:         ctx,
		cancel:      cancel,
		signalCh:    make(chan os.Signal, 1),
	}
	if app.log == nil {
		app.log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: app.logLevel}))
	}

	if options.parentUsage != "" {
		app.commandLine.flagSet.Usage = func() {
			_, _ = fmt.Fprintf(app.output, "Usage of %s %s:\n", options.parentUsage, app.name)
			app.commandLine.flagSet.PrintDefaults()
		}
	}

	return app
}

func WithLogger(log *slog.Logger) Option {
	return func(o *appOptions) {
		o.log = log
	}
}
```

Add context methods by moving current app action behavior behind `Context[T]`:

```go
func (c Context[T]) Register(name string, closer func(ctx context.Context) error) {
	c.app.Register(name, closer)
}

func (c Context[T]) Go(name string, fn func(context.Context) error) {
	c.app.Go(name, fn)
}

func (c Context[T]) HTTPServer(name string, server *http.Server) {
	c.app.HTTPServer(name, server)
}

func (c Context[T]) Exit(message string, err error) {
	c.app.Exit(message, err)
}

func (a *App[T]) runtimeContext() Context[T] {
	return Context[T]{
		Config:  a.cfg,
		Log:     a.log,
		Context: a.ctx,
		app:     a,
	}
}
```

Keep `App.Register`, `App.Go`, `App.HTTPServer`, and `App.Exit` as unexported-or-deprecated-compatible internals for now if that minimizes churn, but new tests and docs should use `Context[T]`.

- [ ] **Step 4: Update root execution to call handlers with `Context[T]`**

In `RunE`, parse using `&a.cfg` and call the selected handler with `a.runtimeContext()`:

```go
if err := a.commandLine.parse(&a.cfg, flags); err != nil {
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
```

Change `Root` to accept `Handler[T]`:

```go
func (a *App[T]) Root(description string, handler Handler[T]) {
	a.root = &command[T]{description: description, handler: handler}
}
```

Update or remove old `Config()`, `Log()`, and `Context()` tests. If keeping the methods temporarily, make them return `&a.cfg`, `a.log`, and `a.ctx` so old internal tests can be migrated in later tasks without hiding failures in the new API.

- [ ] **Step 5: Run focused tests and verify they pass**

Run:

```bash
go test -count=1 -run 'TestAppRootReceivesRuntimeContextWithFields|TestAppWithoutCommandsRequiresRoot|TestServiceOptions|TestWithLogLevel|TestWithLogLevelDefaultsToDebug' ./...
```

Expected: PASS.

- [ ] **Step 6: Inspect diff; do not commit unless explicitly asked**

Run:

```bash
git diff --stat
git diff -- service.go app_test.go service_internal_test.go
```

Expected: only constructor/context/logger/root-app changes.

---

### Task 2: Command Tree API And Dispatch

**Files:**
- Modify: `service.go`
- Modify: `app_test.go`

- [ ] **Step 1: Write failing tests for tree registration, nested dispatch, intermediate handlers, invalid command names, duplicate siblings, unknown commands, default command, and help**

Add these tests to `app_test.go`:

```go
func TestAppCommandTreeDispatchesNestedCommand(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, appTestConfig{}, output)

	var ran string
	start := app.Command("start", "Start services")
	start.Command("api", "Run API", func(ctx Context[appTestConfig]) error {
		ran = "start api"
		return nil
	})
	start.Command("worker", "Run worker", func(ctx Context[appTestConfig]) error {
		ran = "start worker"
		return nil
	})

	if err := app.RunE("start", "api"); err != nil {
		t.Fatal(err)
	}
	if ran != "start api" {
		t.Fatalf("want start api, got %q", ran)
	}
}

func TestAppCommandTreeAllowsIntermediateHandler(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, appTestConfig{}, output)

	var ran string
	start := app.Command("start", "Start services", func(ctx Context[appTestConfig]) error {
		ran = "start"
		return nil
	})
	start.Command("api", "Run API", func(ctx Context[appTestConfig]) error {
		ran = "start api"
		return nil
	})

	if err := app.RunE("start"); err != nil {
		t.Fatal(err)
	}
	if ran != "start" {
		t.Fatalf("want intermediate start handler, got %q", ran)
	}
}

func TestAppCommandTreeRejectsDuplicateSibling(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	start := app.Command("start", "Start services")
	start.Command("api", "Run API", func(ctx Context[appTestConfig]) error { return nil })

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("want duplicate sibling panic")
		}
		if !strings.Contains(fmt.Sprint(got), `duplicate command "api"`) {
			t.Fatalf("want duplicate command panic, got %v", got)
		}
	}()

	start.Command("api", "Run API again", func(ctx Context[appTestConfig]) error { return nil })
}

func TestAppCommandTreeRejectsInvalidName(t *testing.T) {
	t.Parallel()

	tests := []string{"", "start api", " start ", "\t"}
	for _, name := range tests {
		name := name
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			t.Parallel()

			app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
			defer func() {
				got := recover()
				if got == nil {
					t.Fatal("want invalid command name panic")
				}
				if !strings.Contains(fmt.Sprint(got), "invalid command name") {
					t.Fatalf("want invalid command name panic, got %v", got)
				}
			}()

			app.Command(name, "Bad command")
		})
	}
}

func TestAppCommandTreeUnknownCommandShowsUsage(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, appTestConfig{}, output)
	app.Command("start", "Start services").
		Command("api", "Run API", func(ctx Context[appTestConfig]) error { return nil })
	app.Command("migrate", "Run migrations", func(ctx Context[appTestConfig]) error { return nil })

	err := app.RunE("start", "sender")
	if err == nil {
		t.Fatal("want unknown command error")
	}
	if err.Error() != `unknown command "start sender"` {
		t.Fatalf("want unknown nested command error, got %v", err)
	}

	got := output.String()
	for _, want := range []string{"Commands:", "start api", "migrate"} {
		if !strings.Contains(got, want) {
			t.Fatalf("want usage to contain %q, got:\n%s", want, got)
		}
	}
}

func TestAppCommandTreeDefaultCommand(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, appTestConfig{}, output, WithDefaultCommand("start worker"))

	var ran string
	start := app.Command("start", "Start services")
	start.Command("api", "Run API", func(ctx Context[appTestConfig]) error {
		ran = "api"
		return nil
	})
	start.Command("worker", "Run worker", func(ctx Context[appTestConfig]) error {
		ran = "worker"
		return nil
	})

	if err := app.RunE("--port", "6060"); err != nil {
		t.Fatal(err)
	}
	if ran != "worker" {
		t.Fatalf("want default worker command, got %q", ran)
	}
}
```

- [ ] **Step 2: Run command-tree tests and verify they fail**

Run:

```bash
go test -count=1 -run 'TestAppCommandTree' ./...
```

Expected: FAIL to compile or fail behaviorally because `Command` still expects flat paths and does not return a command node.

- [ ] **Step 3: Implement public `Command[T]` node and registration**

In `service.go`, add:

```go
type Command[T any] struct {
	name        string
	path        string
	description string
	handler     Handler[T]
	parent      *Command[T]
	children    map[string]*Command[T]
	app         *App[T]
}

func (a *App[T]) Command(name string, description string, handler ...Handler[T]) *Command[T] {
	name = validateCommandName(name)
	if _, ok := a.commands[name]; ok {
		panic(fmt.Sprintf("duplicate command %q", name))
	}

	cmd := &Command[T]{
		name:        name,
		path:        name,
		description: description,
		children:    map[string]*Command[T]{},
		app:         a,
	}
	if len(handler) > 0 {
		cmd.handler = handler[0]
	}
	a.commands[name] = cmd

	return cmd
}

func (c *Command[T]) Command(name string, description string, handler ...Handler[T]) *Command[T] {
	name = validateCommandName(name)
	if _, ok := c.children[name]; ok {
		panic(fmt.Sprintf("duplicate command %q", name))
	}

	cmd := &Command[T]{
		name:        name,
		path:        c.path + " " + name,
		description: description,
		parent:      c,
		children:    map[string]*Command[T]{},
		app:         c.app,
	}
	if len(handler) > 0 {
		cmd.handler = handler[0]
	}
	c.children[name] = cmd

	return cmd
}

func validateCommandName(name string) string {
	if strings.TrimSpace(name) != name || name == "" || strings.ContainsAny(name, " \t\n\r") {
		panic(fmt.Sprintf("invalid command name %q", name))
	}

	return name
}
```

Keep variadic handler support so both group commands and executable intermediate commands use the same method name.

- [ ] **Step 4: Implement tree command selection and command listing**

Replace flat `longestCommand` logic with tree traversal:

```go
func (a *App[T]) selectCommand(args []string) (*Command[T], []string, error) {
	if hasHelp(args) && len(commandTokens(args)) == 0 && len(a.commands) > 0 {
		a.setUsage(nil)
		return &Command[T]{handler: func(Context[T]) error { return nil }}, args, nil
	}

	if len(a.commands) == 0 {
		if a.root == nil {
			return nil, args, nil
		}
		return &Command[T]{description: a.root.description, handler: a.root.handler}, args, nil
	}

	tokens := commandTokens(args)
	if len(tokens) == 0 {
		if a.defaultCmd == "" {
			return nil, args, nil
		}
		cmd, consumed, ok := a.findCommand(strings.Fields(a.defaultCmd))
		if !ok || consumed != len(strings.Fields(a.defaultCmd)) || cmd.handler == nil {
			return nil, args, fmt.Errorf("unknown default command %q", a.defaultCmd)
		}
		return cmd, args, nil
	}

	cmd, consumed, ok := a.findCommand(tokens)
	if !ok || cmd.handler == nil {
		return nil, args, fmt.Errorf("unknown command %q", strings.Join(tokens, " "))
	}

	return cmd, args[consumed:], nil
}

func (a *App[T]) findCommand(tokens []string) (*Command[T], int, bool) {
	if len(tokens) == 0 {
		return nil, 0, false
	}

	cmd, ok := a.commands[tokens[0]]
	if !ok {
		return nil, 0, false
	}

	consumed := 1
	for consumed < len(tokens) {
		next, ok := cmd.children[tokens[consumed]]
		if !ok {
			return nil, 0, false
		}
		cmd = next
		consumed++
	}

	return cmd, consumed, true
}
```

Replace command path collection with recursive leaf/executable listing:

```go
func (a *App[T]) commandPaths() []string {
	paths := make([]string, 0)
	for _, cmd := range a.commands {
		paths = appendCommandPaths(paths, cmd)
	}
	sort.Strings(paths)
	return paths
}

func appendCommandPaths(paths []string, cmd *Command[T]) []string {
	if cmd.handler != nil {
		paths = append(paths, cmd.path)
	}
	for _, child := range cmd.children {
		paths = appendCommandPaths(paths, child)
	}
	return paths
}
```

Update `writeUsage` to look up descriptions by path:

```go
func (a *App[T]) commandByPath(path string) *Command[T] {
	cmd, consumed, ok := a.findCommand(strings.Fields(path))
	if !ok || consumed != len(strings.Fields(path)) {
		return nil
	}
	return cmd
}
```

- [ ] **Step 5: Update `RunE` and usage helpers to use `*Command[T]`**

Change `setUsage` and `writeUsage` signatures:

```go
func (a *App[T]) setUsage(cmd *Command[T]) {
	a.commandLine.flagSet.Usage = func() {
		a.writeUsage(cmd)
	}
}

func (a *App[T]) writeUsage(cmd *Command[T]) {
	name := a.name
	if a.parentUsage != "" {
		name = a.parentUsage + " " + a.name
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
			c := a.commandByPath(path)
			if c != nil {
				_, _ = fmt.Fprintf(a.output, "  %-18s %s\n", c.path, c.description)
			}
		}
	}

	if a.commandLine.flagSet != nil {
		_, _ = fmt.Fprintln(a.output, "\nFlags:")
		a.commandLine.flagSet.PrintDefaults()
	}
}
```

- [ ] **Step 6: Run command-tree tests and verify they pass**

Run:

```bash
go test -count=1 -run 'TestAppCommandTree|TestAppHelp|TestAppCommandHelp|TestAppUnknownCommand|TestAppDefaultCommand' ./...
```

Expected: PASS after replacing old flat-command tests with tree API equivalents.

- [ ] **Step 7: Inspect diff; do not commit unless explicitly asked**

Run:

```bash
git diff -- service.go app_test.go
```

Expected: command registry and command dispatch changed from flat map paths to tree nodes.

---

### Task 3: Validation Engine For Numeric Tags And `oneof`

**Files:**
- Modify: `command_line.go`
- Modify: `command_line_internal_test.go`

- [ ] **Step 1: Write failing tests for `min`, `max`, and `oneof`**

Add these tests to `command_line_internal_test.go`:

```go
func TestParseValidationMinMax(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg     any
		flags   []string
		wantErr string
	}{
		"int below min": {
			cfg: &struct {
				Port int `min:"1"`
			}{},
			flags:   []string{"--port", "0"},
			wantErr: `Port min: value 0 must be >= 1`,
		},
		"uint above max": {
			cfg: &struct {
				Workers uint `max:"4"`
			}{},
			flags:   []string{"--workers", "5"},
			wantErr: `Workers max: value 5 must be <= 4`,
		},
		"float valid": {
			cfg: &struct {
				Rate float64 `min:"0" max:"1"`
			}{},
			flags: []string{"--rate", "0.5"},
		},
		"duration below min": {
			cfg: &struct {
				Timeout time.Duration `min:"100ms"`
			}{},
			flags:   []string{"--timeout", "50ms"},
			wantErr: `Timeout min: value 50ms must be >= 100ms`,
		},
	}

	for name, tt := range tests {
		name := name
		tt := tt
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cl := newCommandLine("test")
			cl.errorHandling = flag.ContinueOnError
			err := cl.parse(tt.cfg, tt.flags)
			assertError(t, err, tt.wantErr)
		})
	}
}

func TestParseValidationOneOf(t *testing.T) {
	t.Parallel()

	cfg := &struct {
		Env     string        `oneof:"dev, staging, prod"`
		Port    int           `oneof:"80,443,8080"`
		Timeout time.Duration `oneof:"1s, 5s"`
	}{}

	cl := newCommandLine("test")
	cl.errorHandling = flag.ContinueOnError

	err := cl.parse(cfg, []string{"--env", "qa", "--port", "80", "--timeout", "1s"})
	assertError(t, err, `Env oneof: value "qa" must be one of dev, staging, prod`)
}
```

- [ ] **Step 2: Run validation tests and verify they fail**

Run:

```bash
go test -count=1 -run 'TestParseValidationMinMax|TestParseValidationOneOf' ./...
```

Expected: FAIL because validation tags are ignored.

- [ ] **Step 3: Add validation traversal after flag parsing**

In `command_line.go`, call validation after required validation:

```go
func (cl *commandLine) parse(config any, flags []string) error {
	cl.required = nil
	cl.help = false

	if err := cl.subParse(config, flags, ""); err != nil {
		return cl.exit(err)
	}

	if err := cl.flagSet.Parse(flags); err != nil {
		return cl.exit(err)
	}

	if err := cl.validateRequired(); err != nil {
		return cl.exit(err)
	}

	if err := cl.validate(config); err != nil {
		return cl.exit(err)
	}

	return nil
}
```

Add validation traversal:

```go
func (cl *commandLine) validate(config any) error {
	if cl.help {
		return nil
	}

	v := reflect.ValueOf(config)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return ErrInvalidConfigType
	}

	return cl.validateStruct(v.Elem())
}

func (cl *commandLine) validateStruct(v reflect.Value) error {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i)
		if field.PkgPath != "" {
			return ErrInvalidConfigType
		}

		if value.Kind() == reflect.Struct && !isSpecialStructValue(value) {
			if err := cl.validateStruct(value); err != nil {
				return err
			}
			continue
		}

		if err := cl.validateField(field, value); err != nil {
			return err
		}
	}

	return nil
}

func isSpecialStructValue(v reflect.Value) bool {
	if v.CanAddr() {
		switch v.Addr().Interface().(type) {
		case *URL, *Time:
			return true
		}
	}

	return false
}
```

- [ ] **Step 4: Implement numeric min/max validation**

Add helpers to `command_line.go`:

```go
func (cl *commandLine) validateField(field reflect.StructField, value reflect.Value) error {
	if err := validateMinMax(field, value); err != nil {
		return err
	}
	if err := validateOneOf(field, value); err != nil {
		return err
	}

	return nil
}

func validateMinMax(field reflect.StructField, value reflect.Value) error {
	if min := field.Tag.Get("min"); min != "" {
		if err := compareMinimum(field, value, min); err != nil {
			return err
		}
	}
	if max := field.Tag.Get("max"); max != "" {
		if err := compareMaximum(field, value, max); err != nil {
			return err
		}
	}

	return nil
}
```

Implement comparisons for `int`, `int64`, `uint`, `uint64`, `float64`, and `time.Duration`. Use `value.Interface().(time.Duration)` when `value.Type() == reflect.TypeOf(time.Duration(0))` before generic `int64` handling.

```go
var durationType = reflect.TypeOf(time.Duration(0))

func compareMinimum(field reflect.StructField, value reflect.Value, min string) error {
	if value.Type() == durationType {
		limit, err := time.ParseDuration(min)
		if err != nil {
			return fmt.Errorf("%s min: parsing duration %q: %w", field.Name, min, err)
		}
		got := value.Interface().(time.Duration)
		if got < limit {
			return fmt.Errorf("%s min: value %s must be >= %s", field.Name, got, limit)
		}
		return nil
	}

	switch value.Kind() { //nolint:exhaustive
	case reflect.Int, reflect.Int64:
		limit, err := strconv.ParseInt(min, 10, 64)
		if err != nil {
			return fmt.Errorf("%s min: parsing int %q: %w", field.Name, min, err)
		}
		got := value.Int()
		if got < limit {
			return fmt.Errorf("%s min: value %d must be >= %d", field.Name, got, limit)
		}
	case reflect.Uint, reflect.Uint64:
		limit, err := strconv.ParseUint(min, 10, 64)
		if err != nil {
			return fmt.Errorf("%s min: parsing uint %q: %w", field.Name, min, err)
		}
		got := value.Uint()
		if got < limit {
			return fmt.Errorf("%s min: value %d must be >= %d", field.Name, got, limit)
		}
	case reflect.Float64:
		limit, err := strconv.ParseFloat(min, 64)
		if err != nil {
			return fmt.Errorf("%s min: parsing float %q: %w", field.Name, min, err)
		}
		got := value.Float()
		if got < limit {
			return fmt.Errorf("%s min: value %g must be >= %g", field.Name, got, limit)
		}
	}

	return nil
}
```

Implement `compareMaximum` with the same type handling and messages using `max`, `<=`, and `>` comparisons:

```go
return fmt.Errorf("%s max: value %d must be <= %d", field.Name, got, limit)
```

- [ ] **Step 5: Implement comma-separated `oneof` validation**

Add:

```go
func validateOneOf(field reflect.StructField, value reflect.Value) error {
	tag := field.Tag.Get("oneof")
	if tag == "" {
		return nil
	}

	allowed := splitTagList(tag)
	got := validationString(value)
	for _, option := range allowed {
		if got == option {
			return nil
		}
	}

	return fmt.Errorf("%s oneof: value %q must be one of %s", field.Name, got, strings.Join(allowed, ", "))
}

func splitTagList(tag string) []string {
	parts := strings.Split(tag, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}

func validationString(value reflect.Value) string {
	if value.Type() == durationType {
		return value.Interface().(time.Duration).String()
	}
	if value.CanAddr() {
		switch v := value.Addr().Interface().(type) {
		case *URL:
			return v.String()
		case *Time:
			return v.String()
		}
	}

	switch value.Kind() { //nolint:exhaustive
	case reflect.String:
		return value.String()
	case reflect.Bool:
		return strconv.FormatBool(value.Bool())
	case reflect.Int, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10)
	case reflect.Uint, reflect.Uint64:
		return strconv.FormatUint(value.Uint(), 10)
	case reflect.Float64:
		return strconv.FormatFloat(value.Float(), 'g', -1, 64)
	}

	return fmt.Sprint(value.Interface())
}
```

- [ ] **Step 6: Run focused validation tests**

Run:

```bash
go test -count=1 -run 'TestParseValidationMinMax|TestParseValidationOneOf' ./...
```

Expected: PASS.

- [ ] **Step 7: Inspect diff; do not commit unless explicitly asked**

Run:

```bash
git diff -- command_line.go command_line_internal_test.go
```

Expected: validation traversal plus numeric and `oneof` helpers.

---

### Task 4: Validation Tags For Length, Regex, Prefix, Suffix, And Nonzero

**Files:**
- Modify: `command_line.go`
- Modify: `command_line_internal_test.go`

- [ ] **Step 1: Write failing tests for string/slice length validation**

Add to `command_line_internal_test.go`:

```go
func TestParseValidationLengthTags(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg     any
		flags   []string
		wantErr string
	}{
		"string len": {
			cfg: &struct {
				Token string `len:"4"`
			}{},
			flags:   []string{"--token", "abc"},
			wantErr: `Token len: length 3 must equal 4`,
		},
		"string minlen": {
			cfg: &struct {
				Name string `minlen:"3"`
			}{},
			flags:   []string{"--name", "ab"},
			wantErr: `Name minlen: length 2 must be >= 3`,
		},
		"string maxlen": {
			cfg: &struct {
				Name string `maxlen:"3"`
			}{},
			flags:   []string{"--name", "abcd"},
			wantErr: `Name maxlen: length 4 must be <= 3`,
		},
		"string slice valid": {
			cfg: &struct {
				Hosts StringSlice `minlen:"1" maxlen:"2"`
			}{},
			flags: []string{"--hosts", "api-1,api-2"},
		},
		"int slice maxlen": {
			cfg: &struct {
				Ports IntSlice `maxlen:"1"`
			}{},
			flags:   []string{"--ports", "8080,8081"},
			wantErr: `Ports maxlen: length 2 must be <= 1`,
		},
	}

	for name, tt := range tests {
		name := name
		tt := tt
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cl := newCommandLine("test")
			cl.errorHandling = flag.ContinueOnError
			err := cl.parse(tt.cfg, tt.flags)
			assertError(t, err, tt.wantErr)
		})
	}
}
```

- [ ] **Step 2: Write failing tests for `regex`, `prefix`, `suffix`, and `nonzero`**

Add:

```go
func TestParseValidationStringLikeTags(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg     any
		flags   []string
		wantErr string
	}{
		"regex mismatch": {
			cfg: &struct {
				Name string `regex:"^[a-z][a-z0-9-]*$"`
			}{},
			flags:   []string{"--name", "Bad_Name"},
			wantErr: `Name regex: value "Bad_Name" must match ^[a-z][a-z0-9-]*$`,
		},
		"prefix string": {
			cfg: &struct {
				Topic string `prefix:"maia., bee."`
			}{},
			flags:   []string{"--topic", "other.events"},
			wantErr: `Topic prefix: value "other.events" must start with one of maia., bee.`,
		},
		"suffix string": {
			cfg: &struct {
				File string `suffix:".json, .yaml"`
			}{},
			flags:   []string{"--file", "config.toml"},
			wantErr: `File suffix: value "config.toml" must end with one of .json, .yaml`,
		},
		"prefix url valid": {
			cfg: &struct {
				Database URL `prefix:"postgres://, postgresql://"`
			}{},
			flags: []string{"--database", "postgres://localhost/db"},
		},
		"nonzero string": {
			cfg: &struct {
				Addr string `nonzero:""`
			}{},
			flags:   []string{},
			wantErr: `Addr nonzero: value must not be zero`,
		},
		"nonzero int": {
			cfg: &struct {
				Port int `nonzero:""`
			}{},
			flags:   []string{},
			wantErr: `Port nonzero: value must not be zero`,
		},
	}

	for name, tt := range tests {
		name := name
		tt := tt
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cl := newCommandLine("test")
			cl.errorHandling = flag.ContinueOnError
			err := cl.parse(tt.cfg, tt.flags)
			assertError(t, err, tt.wantErr)
		})
	}
}
```

- [ ] **Step 3: Run focused tests and verify they fail**

Run:

```bash
go test -count=1 -run 'TestParseValidationLengthTags|TestParseValidationStringLikeTags' ./...
```

Expected: FAIL because these validation tags are ignored.

- [ ] **Step 4: Implement length helpers**

Add calls in `validateField`:

```go
if err := validateLengths(field, value); err != nil {
	return err
}
if err := validateRegex(field, value); err != nil {
	return err
}
if err := validatePrefixSuffix(field, value); err != nil {
	return err
}
if err := validateNonzero(field, value); err != nil {
	return err
}
```

Add length helpers:

```go
func validateLengths(field reflect.StructField, value reflect.Value) error {
	length, ok := validationLength(value)
	if !ok {
		return nil
	}

	if tag := field.Tag.Get("len"); tag != "" {
		want, err := strconv.Atoi(tag)
		if err != nil {
			return fmt.Errorf("%s len: parsing length %q: %w", field.Name, tag, err)
		}
		if length != want {
			return fmt.Errorf("%s len: length %d must equal %d", field.Name, length, want)
		}
	}
	if tag := field.Tag.Get("minlen"); tag != "" {
		want, err := strconv.Atoi(tag)
		if err != nil {
			return fmt.Errorf("%s minlen: parsing length %q: %w", field.Name, tag, err)
		}
		if length < want {
			return fmt.Errorf("%s minlen: length %d must be >= %d", field.Name, length, want)
		}
	}
	if tag := field.Tag.Get("maxlen"); tag != "" {
		want, err := strconv.Atoi(tag)
		if err != nil {
			return fmt.Errorf("%s maxlen: parsing length %q: %w", field.Name, tag, err)
		}
		if length > want {
			return fmt.Errorf("%s maxlen: length %d must be <= %d", field.Name, length, want)
		}
	}

	return nil
}

func validationLength(value reflect.Value) (int, bool) {
	switch value.Kind() { //nolint:exhaustive
	case reflect.String:
		return len(value.String()), true
	case reflect.Slice:
		switch value.Interface().(type) {
		case StringSlice, IntSlice:
			return value.Len(), true
		}
	}

	return 0, false
}
```

- [ ] **Step 5: Implement regex, prefix, suffix, and nonzero helpers**

Add:

```go
func validateRegex(field reflect.StructField, value reflect.Value) error {
	tag := field.Tag.Get("regex")
	if tag == "" || value.Kind() != reflect.String {
		return nil
	}

	matched, err := regexp.MatchString(tag, value.String())
	if err != nil {
		return fmt.Errorf("%s regex: compiling %q: %w", field.Name, tag, err)
	}
	if !matched {
		return fmt.Errorf("%s regex: value %q must match %s", field.Name, value.String(), tag)
	}

	return nil
}

func validatePrefixSuffix(field reflect.StructField, value reflect.Value) error {
	got, ok := stringLikeValue(value)
	if !ok {
		return nil
	}

	if tag := field.Tag.Get("prefix"); tag != "" {
		allowed := splitTagList(tag)
		if !hasAnyPrefix(got, allowed) {
			return fmt.Errorf("%s prefix: value %q must start with one of %s", field.Name, got, strings.Join(allowed, ", "))
		}
	}

	if tag := field.Tag.Get("suffix"); tag != "" {
		allowed := splitTagList(tag)
		if !hasAnySuffix(got, allowed) {
			return fmt.Errorf("%s suffix: value %q must end with one of %s", field.Name, got, strings.Join(allowed, ", "))
		}
	}

	return nil
}

func stringLikeValue(value reflect.Value) (string, bool) {
	if value.Kind() == reflect.String {
		return value.String(), true
	}
	if value.CanAddr() {
		if u, ok := value.Addr().Interface().(*URL); ok {
			return u.String(), true
		}
	}
	return "", false
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func hasAnySuffix(s string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

func validateNonzero(field reflect.StructField, value reflect.Value) error {
	if _, ok := field.Tag.Lookup("nonzero"); !ok {
		return nil
	}
	if value.IsZero() {
		return fmt.Errorf("%s nonzero: value must not be zero", field.Name)
	}
	return nil
}
```

- [ ] **Step 6: Run focused tests and verify they pass**

Run:

```bash
go test -count=1 -run 'TestParseValidationLengthTags|TestParseValidationStringLikeTags' ./...
```

Expected: PASS.

- [ ] **Step 7: Inspect diff; do not commit unless explicitly asked**

Run:

```bash
git diff -- command_line.go command_line_internal_test.go
```

Expected: length, regex, prefix, suffix, and nonzero validation added.

---

### Task 5: Validation Error Handling And Help Bypass

**Files:**
- Modify: `command_line.go`
- Modify: `command_line_internal_test.go`

- [ ] **Step 1: Write failing tests for validation error handling modes**

Add to `command_line_internal_test.go`:

```go
func TestParseValidationExitOnErrorWritesAndExits(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	code := captureExit(t, func() {
		cl := newCommandLine("test")
		cl.output = output
		cl.flagSet.SetOutput(output)
		cl.errorHandling = flag.ExitOnError

		_ = cl.parse(&struct {
			Port int `max:"10"`
		}{}, []string{"--port", "11"})
	})

	if code != 2 {
		t.Fatalf("want exit code 2, got %d", code)
	}
	if !strings.Contains(output.String(), "bee: Port max: value 11 must be <= 10") {
		t.Fatalf("want validation error in output, got %q", output.String())
	}
}

func TestParseValidationPanicOnErrorPanics(t *testing.T) {
	t.Parallel()

	cl := newCommandLine("test")
	cl.errorHandling = flag.PanicOnError

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("want validation panic")
		}
		if fmt.Sprint(got) != `Port max: value 11 must be <= 10` {
			t.Fatalf("want validation panic, got %v", got)
		}
	}()

	_ = cl.parse(&struct {
		Port int `max:"10"`
	}{}, []string{"--port", "11"})
}
```

- [ ] **Step 2: Write failing test for help bypassing validation**

Add:

```go
func TestParseHelpBypassesValidation(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	cl := newCommandLine("test")
	cl.output = output
	cl.flagSet.SetOutput(output)
	cl.errorHandling = flag.ContinueOnError

	err := cl.parse(&struct {
		DatabaseURL string `req:"" nonzero:"" prefix:"postgres://"`
		Port        int    `min:"1"`
	}{}, []string{"--help"})

	assertError(t, err, "")
	if !strings.Contains(output.String(), "-database-url") {
		t.Fatalf("want help output, got %q", output.String())
	}
}
```

- [ ] **Step 3: Run focused tests and verify they fail if validation bypass/error routing is incomplete**

Run:

```bash
go test -count=1 -run 'TestParseValidationExitOnErrorWritesAndExits|TestParseValidationPanicOnErrorPanics|TestParseHelpBypassesValidation' ./...
```

Expected: PASS if Tasks 3-4 already routed through `cl.exit` and skipped validation on help; otherwise FAIL with direct returned validation errors or help validation errors.

- [ ] **Step 4: Fix validation routing and help bypass if needed**

Ensure `commandLine.parse` has this exact order:

```go
if err := cl.subParse(config, flags, ""); err != nil {
	return cl.exit(err)
}
if err := cl.flagSet.Parse(flags); err != nil {
	return cl.exit(err)
}
if err := cl.validateRequired(); err != nil {
	return cl.exit(err)
}
if err := cl.validate(config); err != nil {
	return cl.exit(err)
}
return nil
```

Ensure both `validateRequired` and `validate` start with:

```go
if cl.help {
	return nil
}
```

- [ ] **Step 5: Run focused tests and verify they pass**

Run:

```bash
go test -count=1 -run 'TestParseValidationExitOnErrorWritesAndExits|TestParseValidationPanicOnErrorPanics|TestParseHelpBypassesValidation' ./...
```

Expected: PASS.

- [ ] **Step 6: Inspect diff; do not commit unless explicitly asked**

Run:

```bash
git diff -- command_line.go command_line_internal_test.go
```

Expected: only validation ordering/error behavior adjustments beyond prior validation tasks.

---

### Task 6: Graceful Shutdown Ordering With HTTPServer And Register

**Files:**
- Modify: `service.go`
- Modify: `app_test.go`

- [ ] **Step 1: Write failing or regression test for HTTP server shutdown before registered closers**

Add to `app_test.go`:

```go
func TestAppHTTPServerDrainsBeforeRegisteredClosers(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, appTestConfig{}, output)
	app.timeout = time.Second

	requestStarted := make(chan struct{})
	allowRequestFinish := make(chan struct{})
	events := make(chan string, 4)

	server := &http.Server{
		Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(requestStarted)
			<-allowRequestFinish
			events <- "handler finished"
			w.WriteHeader(http.StatusNoContent)
		}),
	}

	app.Root("Run service", func(ctx Context[appTestConfig]) error {
		listener, err := net.Listen("tcp", server.Addr)
		if err != nil {
			return err
		}
		addr := listener.Addr().String()

		ctx.Go("http api", func(run context.Context) error {
			shutdownDone := make(chan struct{})
			go func() {
				select {
				case <-run.Done():
					shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					_ = server.Shutdown(shutdownCtx)
				case <-shutdownDone:
				}
			}()

			err := server.Serve(listener)
			close(shutdownDone)
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		})

		ctx.Register("database", func(context.Context) error {
			events <- "database closed"
			return nil
		})
		ctx.Register("queue", func(context.Context) error {
			events <- "queue closed"
			return nil
		})

		go func() {
			resp, err := http.Get("http://" + addr)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()

		<-requestStarted
		ctx.Exit("stop", nil)
		close(allowRequestFinish)

		return nil
	})

	if err := app.RunE(); err == nil {
		t.Fatal("want ctx.Exit to record an exit error")
	}

	got := []string{<-events, <-events, <-events}
	want := []string{"handler finished", "queue closed", "database closed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want shutdown order %v, got %v", want, got)
	}
}
```

Add `net` to the test imports.

- [ ] **Step 2: Run the shutdown ordering test**

Run:

```bash
go test -count=1 -run TestAppHTTPServerDrainsBeforeRegisteredClosers ./...
```

Expected: PASS if existing lifecycle already waits for supervised goroutines before closers. If it fails, the order is wrong and must be fixed before docs are updated.

- [ ] **Step 3: Fix lifecycle order only if the test fails**

In `RunE`, ensure this order remains:

```go
<-a.ctx.Done()
a.wg.Wait()
a.runClosers()

return a.err()
```

Do not move `runClosers` before `wg.Wait`.

- [ ] **Step 4: Run lifecycle tests**

Run:

```bash
go test -count=1 -run 'TestAppHTTPServer|TestAppClosers|TestAppCommandSetup|TestAppGo' ./...
```

Expected: PASS.

- [ ] **Step 5: Inspect diff; do not commit unless explicitly asked**

Run:

```bash
git diff -- service.go app_test.go
```

Expected: shutdown order test plus any minimal lifecycle correction.

---

### Task 7: Examples And README Public Documentation

**Files:**
- Modify: `example_test.go`
- Modify: `README.md`

- [ ] **Step 1: Update examples to compile with the polished app API**

In `example_test.go`, update examples to use value config and `Context[T]`:

```go
app := bee.New(
	"mycmd",
	config{},
	bee.WithErrorHandling(flag.ContinueOnError),
	bee.WithOutput(io.Discard),
)
app.Root("Run app", func(ctx bee.Context[config]) error {
	return nil
})
_ = app.RunE()
```

For advanced examples, use:

```go
app.Command("start", "Start services").
	Command("api", "Run API", func(ctx bee.Context[config]) error {
		_ = ctx.Config
		return nil
	})
```

- [ ] **Step 2: Run example tests and verify failures are gone**

Run:

```bash
go test -count=1 -run Example ./...
```

Expected: PASS.

- [ ] **Step 3: Update README app setup section**

Replace the existing app setup example with a polished API example containing:

```go
app := bee.New("maia", Config{},
	bee.WithLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil))),
	bee.WithShutdownTimeout(30*time.Second),
)

start := app.Command("start", "Start services")

start.Command("api", "Run HTTP API", func(ctx bee.Context[Config]) error {
	server := &http.Server{
		Addr: ctx.Config.HTTP.Addr,
	}
	ctx.HTTPServer("http api", server)
	ctx.Log.Info("api started", "addr", ctx.Config.HTTP.Addr)
	return nil
})

app.Run()
```

Document that commands are trees:

```text
maia start api
maia start worker
maia migrate up
```

Document that simple apps can use root:

```go
app.Root("Run service", runService)
app.Run()
```

- [ ] **Step 4: Add README validation tag table**

Add a table with these exact rows:

```markdown
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
```

Document the distinction:

```markdown
`req` means the value must be supplied by environment variable or flag.
`nonzero` means the final parsed value, after defaults/env/flags, must not be zero.
```

- [ ] **Step 5: Add README graceful shutdown section**

Add this public behavior text:

```markdown
`ctx.HTTPServer` starts the server as a supervised goroutine. When the app
context is cancelled, bee calls `server.Shutdown` with a fresh shutdown context
controlled by `WithShutdownTimeout`.

Registered closers run after supervised goroutines finish. This means HTTP
servers stop accepting new requests and drain in-flight requests before shared
dependencies are closed.

For `ctx.HTTPServer("http api", server)`, `ctx.Register("database", closeDB)`,
and `ctx.Register("queue", closeQueue)`, shutdown order is:

1. app context is cancelled
2. HTTP server starts graceful shutdown
3. bee waits for supervised goroutines, including HTTP servers, to finish
4. registered closers run in reverse order: queue, then database
```

- [ ] **Step 6: Run docs/examples verification**

Run:

```bash
go test -count=1 ./...
```

Expected: PASS.

- [ ] **Step 7: Inspect README and examples diff; do not commit unless explicitly asked**

Run:

```bash
git diff -- README.md example_test.go
```

Expected: examples and public docs use only the polished API.

---

### Task 8: Full Verification, Coverage Badge, And Final Review

**Files:**
- Modify: `README.md` only if coverage value changes.
- Check: all modified Go files and docs.

- [ ] **Step 1: Run gofmt**

Run:

```bash
gofmt -w *.go
```

Expected: no output.

- [ ] **Step 2: Run full race test with coverage**

Run outside the sandbox because Go may need the normal build cache:

```bash
go test -race -coverprofile=coverage.out -count=1 ./...
```

Expected: PASS.

- [ ] **Step 3: Compute exact coverage**

Run:

```bash
go tool cover -func=coverage.out
```

Expected: output ending with:

```text
total:	(statements)	NN.N%
```

- [ ] **Step 4: Update README coverage badge only if the measured value differs**

If Step 3 reports a value other than the README badge value, update only the numeric value in this line:

```markdown
![coverage](https://img.shields.io/badge/coverage-95.3%25-brightgreen?style=flat&logo=go)
```

For example, if coverage is `94.8%`, the line becomes:

```markdown
![coverage](https://img.shields.io/badge/coverage-94.8%25-brightgreen?style=flat&logo=go)
```

- [ ] **Step 5: Run full tests again if README changed only for coverage**

Run:

```bash
go test -count=1 ./...
```

Expected: PASS.

- [ ] **Step 6: Inspect full diff**

Run:

```bash
git diff --stat
git diff
```

Expected: scoped changes to app API, command tree, validation, examples, README, and tests.

- [ ] **Step 7: Self-review against the design spec**

Check each item manually:

```text
[ ] New accepts Config{} value.
[ ] Handler signature is func(ctx bee.Context[Config]) error.
[ ] ctx.Config, ctx.Log, and ctx.Context are exported fields.
[ ] Runtime actions are ctx.Go, ctx.Register, ctx.HTTPServer, and ctx.Exit.
[ ] Commands are registered as app.Command(...).Command(...).
[ ] Command nesting is not hard-coded to a depth limit.
[ ] Root apps without commands still work.
[ ] Run() has no args and RunE(args ...string) remains.
[ ] WithLogger injects *slog.Logger.
[ ] Validation tags implemented: min, max, oneof, len, minlen, maxlen, regex, prefix, suffix, nonzero.
[ ] oneof/prefix/suffix split comma-separated values and trim whitespace.
[ ] Validation reuses flag.ErrorHandling.
[ ] Help bypasses validation.
[ ] HTTPServer drains before registered closers run.
[ ] README documents graceful shutdown ordering.
```

- [ ] **Step 8: Final repository reminders**

When reporting completion, include these reminders if applicable:

```text
This repository contains Go code, and the project notes say GitHub CodeQL/code scanning is already enabled.
This repository currently has no Dockerfile, so the container image vulnerability scanning reminder is not applicable.
The repository uses GitHub Actions, but project notes explicitly say not to add an actionlint CI job unless requested; validate workflow changes locally with actionlint if workflows are touched.
```

Do not stage, commit, push, or open a PR unless the human explicitly asks.
