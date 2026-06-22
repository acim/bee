package bee

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

type appTestConfig struct {
	LogLevel string `def:"INFO"`
	Port     int    `def:"8080"`
	HTTP     struct {
		Host string `def:"127.0.0.1"`
	}
}

func newTestApp(t *testing.T, cfg appTestConfig, output *bytes.Buffer, opts ...Option) *App[appTestConfig] {
	t.Helper()

	allOpts := []Option{
		WithOutput(output),
		WithErrorHandling(flag.ContinueOnError),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	allOpts = append(allOpts, opts...)

	return New("maia", &cfg, allOpts...)
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
	started := make(chan struct{})

	app.Root("Run service", func(ctx *Ctx[appTestConfig]) error {
		gotConfig = *ctx.Cfg
		gotLog = ctx.Log
		gotContext = ctx.Ctx
		ctx.Go("short task", func(run context.Context) error {
			goRan = run == ctx.Ctx
			close(started)
			<-run.Done()

			return nil
		})
		<-started
		app.cancel()

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

func TestNewRejectsNilConfig(t *testing.T) {
	t.Parallel()

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("want nil config panic")
		}
		if got != "bee: invalid nil config" {
			t.Fatalf("want clear nil config panic, got %v", got)
		}
	}()

	New("maia", (*appTestConfig)(nil))
}

func TestNewExposesRuntimeFields(t *testing.T) {
	t.Parallel()

	cfg := appTestConfig{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := New("maia", &cfg, WithLogger(logger))

	if app.Cfg != &cfg {
		t.Fatal("want app to expose config pointer")
	}
	if app.Log != logger {
		t.Fatal("want app to expose logger")
	}
	if app.Ctx == nil {
		t.Fatal("want app to expose context")
	}
}

func TestContextRuntimeMethodPanicsWithoutApp(t *testing.T) {
	t.Parallel()

	ctx := Ctx[appTestConfig]{}

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("want panic")
		}
		if got != "bee: runtime method called on context not created by App" {
			t.Fatalf("want clear context runtime panic, got %v", got)
		}
	}()

	ctx.Go("manual context", func(context.Context) error {
		return nil
	})
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

func TestAppCommandParsingAndConfig(t *testing.T) {
	t.Parallel()

	cfg := appTestConfig{}
	output := &bytes.Buffer{}
	app := newTestApp(t, cfg, output, WithLookupEnvFunc(func(key string) (string, bool) {
		if key == "MAIA_PORT" {
			return "9090", true
		}

		return "", false
	}))

	var ran string
	start := app.Cmd("start", "Run starter", func(ctx *Ctx[appTestConfig]) error {
		ran = "start"

		return nil
	})
	start.Cmd("api", "Run API", func(ctx *Ctx[appTestConfig]) error {
		ran = "start api"

		if ctx.Cfg.Port != 7070 {
			t.Fatalf("want context config port 7070, got %d", ctx.Cfg.Port)
		}

		if ctx.Log == nil {
			t.Fatal("context Log is nil")
		}

		if ctx.Ctx == nil {
			t.Fatal("context Context is nil")
		}

		return nil
	})

	if err := app.RunE("start", "api", "--port", "7070", "--http-host", "0.0.0.0"); err != nil {
		t.Fatal(err)
	}

	if ran != "start api" {
		t.Fatalf("want longest command to run start api, got %q", ran)
	}

	if app.Cfg.Port != 7070 {
		t.Fatalf("want flag to override env/default with port 7070, got %d", app.Cfg.Port)
	}

	if app.Cfg.HTTP.Host != "0.0.0.0" {
		t.Fatalf("want nested flag after command to parse, got %q", app.Cfg.HTTP.Host)
	}

	if app.Cfg.LogLevel != "INFO" {
		t.Fatalf("want default log level INFO, got %q", app.Cfg.LogLevel)
	}
}

func TestAppDefaultCommand(t *testing.T) {
	t.Parallel()

	cfg := appTestConfig{}
	output := &bytes.Buffer{}
	app := newTestApp(t, cfg, output, WithDefaultCommand("start worker"))

	var ran string
	start := app.Cmd("start", "Run services")
	start.Cmd("api", "Run API", func(ctx *Ctx[appTestConfig]) error {
		ran = "api"

		return nil
	})
	start.Cmd("worker", "Run worker", func(ctx *Ctx[appTestConfig]) error {
		ran = "worker"

		return nil
	})

	if err := app.RunE("--port", "6060"); err != nil {
		t.Fatal(err)
	}

	if ran != "worker" {
		t.Fatalf("want default worker command, got %q", ran)
	}

	if app.Cfg.Port != 6060 {
		t.Fatalf("want flags to parse for default command, got port %d", app.Cfg.Port)
	}
}

func TestAppCommandTreeDispatchesNestedCommand(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, appTestConfig{}, output)

	var ran string
	start := app.Cmd("start", "Start services")
	start.Cmd("api", "Run API", func(ctx *Ctx[appTestConfig]) error {
		ran = "start api"

		return nil
	})
	start.Cmd("worker", "Run worker", func(ctx *Ctx[appTestConfig]) error {
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
	start := app.Cmd("start", "Start services", func(ctx *Ctx[appTestConfig]) error {
		ran = "start"

		return nil
	})
	start.Cmd("api", "Run API", func(ctx *Ctx[appTestConfig]) error {
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
	start := app.Cmd("start", "Start services")
	start.Cmd("api", "Run API", func(ctx *Ctx[appTestConfig]) error { return nil })

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("want duplicate sibling panic")
		}
		if !strings.Contains(fmt.Sprint(got), `duplicate command "api"`) {
			t.Fatalf("want duplicate command panic, got %v", got)
		}
	}()

	start.Cmd("api", "Run API again", func(ctx *Ctx[appTestConfig]) error { return nil })
}

func TestAppCommandTreeRejectsInvalidName(t *testing.T) {
	t.Parallel()

	tests := []string{"", "start api", " start ", "\t"}
	for _, name := range tests {
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

			app.Cmd(name, "Bad command")
		})
	}
}

func TestAppCommandTreeUnknownCommandShowsUsage(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, appTestConfig{}, output)
	app.Cmd("start", "Start services").
		Cmd("api", "Run API", func(ctx *Ctx[appTestConfig]) error { return nil })
	app.Cmd("migrate", "Run migrations", func(ctx *Ctx[appTestConfig]) error { return nil })

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
	start := app.Cmd("start", "Start services")
	start.Cmd("api", "Run API", func(ctx *Ctx[appTestConfig]) error {
		ran = "api"

		return nil
	})
	start.Cmd("worker", "Run worker", func(ctx *Ctx[appTestConfig]) error {
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

func TestAppCommandGroupHelpListsChildren(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, appTestConfig{}, output)
	start := app.Cmd("start", "Start services")
	start.Cmd("api", "Run API", func(ctx *Ctx[appTestConfig]) error { return nil })
	start.Cmd("worker", "Run worker", func(ctx *Ctx[appTestConfig]) error { return nil })

	if err := app.RunE("start", "--help"); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	for _, want := range []string{
		"Usage of maia start:",
		"Start services",
		"Commands:",
		"start api",
		"start worker",
		"Run API",
		"Run worker",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("want group help to contain %q, got:\n%s", want, got)
		}
	}
}

func TestCommandRuntimeMethodPanicsWithoutApp(t *testing.T) {
	t.Parallel()

	var cmd Cmd[appTestConfig]
	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("want panic")
		}
		if got != "bee: command method called on command not created by App" {
			t.Fatalf("want clear command runtime panic, got %v", got)
		}
	}()

	cmd.Cmd("api", "Run API")
}

func TestAppHTTPServerShutsDownWhenContextIsCancelled(t *testing.T) {
	t.Parallel()

	cfg := appTestConfig{}
	output := &bytes.Buffer{}
	app := newTestApp(t, cfg, output)
	app.timeout = 50 * time.Millisecond

	server := &http.Server{
		Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	}

	app.Root("Run app", func(ctx *Ctx[appTestConfig]) error {
		ctx.HTTPServer("http server", server)
		app.cancel()

		return nil
	})

	if err := app.RunE(); err != nil {
		t.Fatalf("RunE() error = %v, want nil", err)
	}
}

func TestAppHTTPServerDrainsBeforeRegisteredClosers(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, appTestConfig{}, output)
	app.timeout = time.Second

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}

	requestStarted := make(chan struct{})
	allowRequestFinish := make(chan struct{})
	events := make(chan string, 4)
	clientDone := make(chan error, 1)
	exitCalled := make(chan struct{})

	server := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(requestStarted)
			<-allowRequestFinish
			events <- "handler finished"
			w.WriteHeader(http.StatusNoContent)
		}),
	}

	app.Root("Run service", func(ctx *Ctx[appTestConfig]) error {
		ctx.HTTPServer("http api", server)
		ctx.Register("database", func(context.Context) error {
			events <- "database closed"

			return nil
		})
		ctx.Register("queue", func(context.Context) error {
			events <- "queue closed"

			return nil
		})

		go func() {
			clientDone <- getUntilOK("http://" + addr)
		}()

		select {
		case <-requestStarted:
		case <-time.After(time.Second):
			return errors.New("timed out waiting for request to start")
		}

		ctx.Exit("stop", nil)
		close(exitCalled)

		return nil
	})

	runDone := make(chan error, 1)
	go func() {
		runDone <- app.RunE()
	}()

	select {
	case <-exitCalled:
	case err := <-runDone:
		t.Fatalf("RunE returned before request started: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ctx.Exit")
	}

	select {
	case event := <-events:
		t.Fatalf("closer ran before handler was allowed to finish: %s", event)
	case <-time.After(50 * time.Millisecond):
	}
	close(allowRequestFinish)

	select {
	case err := <-clientDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for client")
	}

	select {
	case err = <-runDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for app shutdown")
	}
	if err == nil {
		t.Fatal("want ctx.Exit to record an exit error")
	}

	got := []string{
		receiveString(t, events, time.Second, "handler finish event"),
		receiveString(t, events, time.Second, "queue closer event"),
		receiveString(t, events, time.Second, "database closer event"),
	}
	want := []string{"handler finished", "queue closed", "database closed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want shutdown order %v, got %v", want, got)
	}
}

func getUntilOK(url string) error {
	deadline := time.Now().Add(time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()

			return nil
		}
		if time.Now().After(deadline) {
			return err
		}

		time.Sleep(time.Millisecond)
	}
}

func TestAppDuplicateCommandPanicsClearly(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	app.Cmd("api", "Run API", func(ctx *Ctx[appTestConfig]) error {
		return nil
	})

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("want duplicate command panic")
		}

		if !strings.Contains(fmt.Sprint(got), `duplicate command "api"`) {
			t.Fatalf("want clear duplicate command panic, got %v", got)
		}
	}()

	app.Cmd("api", "Run API again", func(ctx *Ctx[appTestConfig]) error {
		return nil
	})
}

func TestAppUnknownCommandShowsUsage(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, appTestConfig{}, output)
	app.Cmd("start", "Start services").Cmd("api", "Run API", func(ctx *Ctx[appTestConfig]) error {
		return nil
	})
	app.Cmd("migrate", "Run migrations", func(ctx *Ctx[appTestConfig]) error {
		return nil
	})

	err := app.RunE("start", "sender")
	if err == nil {
		t.Fatal("want unknown command error")
	}

	if !strings.Contains(err.Error(), `unknown command "start sender"`) {
		t.Fatalf("want unknown command error, got %v", err)
	}

	got := output.String()
	for _, want := range []string{"Commands:", "start api", "migrate"} {
		if !strings.Contains(got, want) {
			t.Fatalf("want usage to contain %q, got:\n%s", want, got)
		}
	}
}

func TestAppHelpListsCommandsAndFlags(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, appTestConfig{}, output, WithDefaultCommand("start api"))
	start := app.Cmd("start", "Start services")
	start.Cmd("api", "Run API", func(ctx *Ctx[appTestConfig]) error {
		return nil
	})
	start.Cmd("worker", "Run worker", func(ctx *Ctx[appTestConfig]) error {
		return nil
	})

	if err := app.RunE("--help"); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	for _, want := range []string{
		"Usage of maia:",
		"Default command: start api",
		"Commands:",
		"start api",
		"Run API",
		"-port int",
		"-http-host string",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("want root help to contain %q, got:\n%s", want, got)
		}
	}
}

func TestAppCommandHelpListsSelectedCommandAndFlags(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, appTestConfig{}, output)
	app.Cmd("start", "Start services").Cmd("worker", "Run worker", func(ctx *Ctx[appTestConfig]) error {
		return nil
	})

	if err := app.RunE("start", "worker", "--help"); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	for _, want := range []string{
		"Usage of maia start worker:",
		"Run worker",
		"-port int",
		"-http-host string",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("want command help to contain %q, got:\n%s", want, got)
		}
	}
}

func TestAppRootCommand(t *testing.T) {
	t.Parallel()

	cfg := appTestConfig{}
	output := &bytes.Buffer{}
	app := newTestApp(t, cfg, output)

	var ran bool
	app.Root("Run app", func(ctx *Ctx[appTestConfig]) error {
		ran = true

		return nil
	})

	if err := app.RunE("--port", "9091"); err != nil {
		t.Fatal(err)
	}

	if !ran {
		t.Fatal("want root handler to run")
	}

	if app.Cfg.Port != 9091 {
		t.Fatalf("want root flags parsed, got %d", app.Cfg.Port)
	}
}

func TestAppRunEReturnsConfigParseError(t *testing.T) {
	t.Parallel()

	cfg := []string{}
	app := New("maia", &cfg, WithOutput(io.Discard), WithErrorHandling(flag.ContinueOnError))
	app.Root("Run app", func(ctx *Ctx[[]string]) error {
		return nil
	})

	err := app.RunE()
	if !errors.Is(err, ErrInvalidConfigType) {
		t.Fatalf("want invalid config error, got %v", err)
	}
}

func TestAppContextCancelledOnSignal(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	entered := make(chan struct{})
	done := make(chan struct{})
	app.Root("Run app", func(ctx *Ctx[appTestConfig]) error {
		close(entered)
		<-ctx.Ctx.Done()
		close(done)

		return nil
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.RunE()
	}()

	<-entered
	app.signalCh <- testSignal{}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("context was not cancelled after signal")
	}

	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestAppGoReceivesAppContextAndFailsFast(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	boom := errors.New("boom")
	gotContext := make(chan bool, 1)
	app.Root("Run app", func(ctx *Ctx[appTestConfig]) error {
		ctx.Go("worker", func(run context.Context) error {
			gotContext <- run == ctx.Ctx

			return boom
		})

		<-ctx.Ctx.Done()

		return nil
	})

	err := app.RunE()
	if err == nil {
		t.Fatal("want goroutine error")
	}

	if !errors.Is(err, boom) {
		t.Fatalf("want goroutine error %v, got %v", boom, err)
	}

	if !<-gotContext {
		t.Fatal("Go did not receive app context")
	}

	if app.Ctx.Err() == nil {
		t.Fatal("want app context cancelled after goroutine failure")
	}
}

func TestAppGoExitsCleanlyOnContextCancellation(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	stopped := make(chan struct{})
	app.Root("Run app", func(ctx *Ctx[appTestConfig]) error {
		ctx.Go("worker", func(run context.Context) error {
			<-run.Done()
			close(stopped)

			return nil
		})

		app.cancel()

		return nil
	})

	if err := app.RunE(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
}

func TestAppClosersRunAfterGoroutinesStopInReverseOrder(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	var calls []string
	app.Root("Run app", func(ctx *Ctx[appTestConfig]) error {
		ctx.Register("first", func(run context.Context) error {
			if run.Err() != nil {
				t.Fatalf("first closer got cancelled shutdown context: %v", run.Err())
			}

			calls = append(calls, "first")

			return nil
		})
		ctx.Register("second", func(run context.Context) error {
			if run.Err() != nil {
				t.Fatalf("second closer got cancelled shutdown context: %v", run.Err())
			}

			calls = append(calls, "second")

			return errors.New("close second")
		})
		ctx.Go("worker", func(run context.Context) error {
			<-run.Done()
			calls = append(calls, "worker stopped")

			return nil
		})

		app.cancel()

		return nil
	})

	err := app.RunE()
	if err == nil {
		t.Fatal("want closer error")
	}

	if !strings.Contains(err.Error(), "close second") {
		t.Fatalf("want closer error recorded, got %v", err)
	}

	want := []string{"worker stopped", "second", "first"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("want calls %v, got %v", want, calls)
	}
}

func TestAppExitRecordsFatalError(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	app.Root("Run app", func(ctx *Ctx[appTestConfig]) error {
		ctx.Exit("fatal stop", nil)

		return nil
	})

	err := app.RunE()
	if err == nil {
		t.Fatal("want fatal exit error")
	}

	if !strings.Contains(err.Error(), "fatal stop") {
		t.Fatalf("want fatal exit message recorded, got %v", err)
	}
}

func TestAppExitRecordsProvidedError(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	exitErr := errors.New("database unavailable")
	app.Root("Run app", func(ctx *Ctx[appTestConfig]) error {
		ctx.Exit("fatal stop", exitErr)

		return nil
	})

	err := app.RunE()
	if !errors.Is(err, exitErr) {
		t.Fatalf("want provided exit error recorded, got %v", err)
	}
}

func TestAppExitUsesDefaultMessageForEmptyExit(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	app.Root("Run app", func(ctx *Ctx[appTestConfig]) error {
		ctx.Exit("", nil)

		return nil
	})

	err := app.RunE()
	if err == nil {
		t.Fatal("want fatal exit error")
	}

	if !strings.Contains(err.Error(), "application exit") {
		t.Fatalf("want default exit message recorded, got %v", err)
	}
}

func TestAppCommandSetupErrorRunsRegisteredClosers(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	setupErr := errors.New("setup failed")
	var closed bool
	app.Root("Run app", func(ctx *Ctx[appTestConfig]) error {
		ctx.Register("resource", func(run context.Context) error {
			closed = true

			return nil
		})

		return setupErr
	})

	err := app.RunE()
	if !errors.Is(err, setupErr) {
		t.Fatalf("want setup error %v, got %v", setupErr, err)
	}

	if !closed {
		t.Fatal("registered closer did not run after setup error")
	}
}

func receiveString(t *testing.T, ch <-chan string, timeout time.Duration, name string) string {
	t.Helper()

	select {
	case value := <-ch:
		return value
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %s", name)

		return ""
	}
}

type testSignal struct{}

func (testSignal) String() string {
	return "test signal"
}

func (testSignal) Signal() {}
