package bee

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
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

func newTestApp(t *testing.T, cfg *appTestConfig, output *bytes.Buffer, opts ...Option) *App[appTestConfig] {
	t.Helper()

	allOpts := []Option{
		WithOutput(output),
		WithErrorHandling(flag.ContinueOnError),
	}
	allOpts = append(allOpts, opts...)

	app := New("maia", cfg, allOpts...)
	app.log = slog.New(slog.NewTextHandler(io.Discard, nil))

	return app
}

func TestAppCommandParsingAndConfig(t *testing.T) {
	t.Parallel()

	cfg := &appTestConfig{}
	output := &bytes.Buffer{}
	app := newTestApp(t, cfg, output, WithLookupEnvFunc(func(key string) (string, bool) {
		if key == "MAIA_PORT" {
			return "9090", true
		}

		return "", false
	}))

	var ran string
	app.Command("start", "Run starter", func(app *App[appTestConfig]) error {
		ran = "start"

		return nil
	})
	app.Command("start api", "Run API", func(app *App[appTestConfig]) error {
		ran = "start api"

		if app.Config() != cfg {
			t.Fatal("Config returned a different pointer")
		}

		if app.Log() == nil {
			t.Fatal("Log returned nil")
		}

		if app.Context() == nil {
			t.Fatal("Context returned nil")
		}

		return nil
	})

	if err := app.RunE("start", "api", "--port", "7070", "--http-host", "0.0.0.0"); err != nil {
		t.Fatal(err)
	}

	if ran != "start api" {
		t.Fatalf("want longest command to run start api, got %q", ran)
	}

	if cfg.Port != 7070 {
		t.Fatalf("want flag to override env/default with port 7070, got %d", cfg.Port)
	}

	if cfg.HTTP.Host != "0.0.0.0" {
		t.Fatalf("want nested flag after command to parse, got %q", cfg.HTTP.Host)
	}

	if cfg.LogLevel != "INFO" {
		t.Fatalf("want default log level INFO, got %q", cfg.LogLevel)
	}
}

func TestAppDefaultCommand(t *testing.T) {
	t.Parallel()

	cfg := &appTestConfig{}
	output := &bytes.Buffer{}
	app := newTestApp(t, cfg, output, WithDefaultCommand("start worker"))

	var ran string
	app.Command("start api", "Run API", func(app *App[appTestConfig]) error {
		ran = "api"

		return nil
	})
	app.Command("start worker", "Run worker", func(app *App[appTestConfig]) error {
		ran = "worker"

		return nil
	})

	if err := app.RunE("--port", "6060"); err != nil {
		t.Fatal(err)
	}

	if ran != "worker" {
		t.Fatalf("want default worker command, got %q", ran)
	}

	if cfg.Port != 6060 {
		t.Fatalf("want flags to parse for default command, got port %d", cfg.Port)
	}
}

func TestAppHTTPServerShutsDownWhenContextIsCancelled(t *testing.T) {
	t.Parallel()

	cfg := &appTestConfig{}
	output := &bytes.Buffer{}
	app := newTestApp(t, cfg, output)
	app.timeout = 50 * time.Millisecond

	server := &http.Server{
		Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	}

	app.Root("Run app", func(app *App[appTestConfig]) error {
		app.HTTPServer("http server", server)
		app.cancel()

		return nil
	})

	if err := app.RunE(); err != nil {
		t.Fatalf("RunE() error = %v, want nil", err)
	}
}

func TestAppDuplicateCommandPanicsClearly(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, &appTestConfig{}, &bytes.Buffer{})
	app.Command(" start   api ", "Run API", func(app *App[appTestConfig]) error {
		return nil
	})

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("want duplicate command panic")
		}

		if !strings.Contains(got.(string), `duplicate command "start api"`) {
			t.Fatalf("want clear duplicate command panic, got %v", got)
		}
	}()

	app.Command("start api", "Run API again", func(app *App[appTestConfig]) error {
		return nil
	})
}

func TestAppUnknownCommandShowsUsage(t *testing.T) {
	t.Parallel()

	output := &bytes.Buffer{}
	app := newTestApp(t, &appTestConfig{}, output)
	app.Command("start api", "Run API", func(app *App[appTestConfig]) error {
		return nil
	})
	app.Command("migrate", "Run migrations", func(app *App[appTestConfig]) error {
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
	app := newTestApp(t, &appTestConfig{}, output, WithDefaultCommand("start api"))
	app.Command("start api", "Run API", func(app *App[appTestConfig]) error {
		return nil
	})
	app.Command("start worker", "Run worker", func(app *App[appTestConfig]) error {
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
	app := newTestApp(t, &appTestConfig{}, output)
	app.Command("start worker", "Run worker", func(app *App[appTestConfig]) error {
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

	cfg := &appTestConfig{}
	output := &bytes.Buffer{}
	app := newTestApp(t, cfg, output)

	var ran bool
	app.Root("Run app", func(app *App[appTestConfig]) error {
		ran = true

		return nil
	})

	if err := app.RunE("--port", "9091"); err != nil {
		t.Fatal(err)
	}

	if !ran {
		t.Fatal("want root handler to run")
	}

	if cfg.Port != 9091 {
		t.Fatalf("want root flags parsed, got %d", cfg.Port)
	}
}

func TestAppRunEReturnsConfigParseError(t *testing.T) {
	t.Parallel()

	cfg := []string{}
	app := New("maia", &cfg, WithOutput(io.Discard), WithErrorHandling(flag.ContinueOnError))
	app.Root("Run app", func(app *App[[]string]) error {
		return nil
	})

	err := app.RunE()
	if !errors.Is(err, ErrInvalidConfigType) {
		t.Fatalf("want invalid config error, got %v", err)
	}
}

func TestAppContextCancelledOnSignal(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, &appTestConfig{}, &bytes.Buffer{})
	entered := make(chan struct{})
	done := make(chan struct{})
	app.Root("Run app", func(app *App[appTestConfig]) error {
		close(entered)
		<-app.Context().Done()
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

	app := newTestApp(t, &appTestConfig{}, &bytes.Buffer{})
	boom := errors.New("boom")
	gotContext := make(chan bool, 1)
	app.Root("Run app", func(app *App[appTestConfig]) error {
		app.Go("worker", func(ctx context.Context) error {
			gotContext <- ctx == app.Context()

			return boom
		})

		<-app.Context().Done()

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

	if app.Context().Err() == nil {
		t.Fatal("want app context cancelled after goroutine failure")
	}
}

func TestAppGoExitsCleanlyOnContextCancellation(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, &appTestConfig{}, &bytes.Buffer{})
	stopped := make(chan struct{})
	app.Root("Run app", func(app *App[appTestConfig]) error {
		app.Go("worker", func(ctx context.Context) error {
			<-ctx.Done()
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

	app := newTestApp(t, &appTestConfig{}, &bytes.Buffer{})
	var calls []string
	app.Root("Run app", func(app *App[appTestConfig]) error {
		app.Register("first", func(ctx context.Context) error {
			if ctx.Err() != nil {
				t.Fatalf("first closer got cancelled shutdown context: %v", ctx.Err())
			}

			calls = append(calls, "first")

			return nil
		})
		app.Register("second", func(ctx context.Context) error {
			if ctx.Err() != nil {
				t.Fatalf("second closer got cancelled shutdown context: %v", ctx.Err())
			}

			calls = append(calls, "second")

			return errors.New("close second")
		})
		app.Go("worker", func(ctx context.Context) error {
			<-ctx.Done()
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

	app := newTestApp(t, &appTestConfig{}, &bytes.Buffer{})
	app.Root("Run app", func(app *App[appTestConfig]) error {
		app.Exit("fatal stop", nil)

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

func TestAppCommandSetupErrorRunsRegisteredClosers(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, &appTestConfig{}, &bytes.Buffer{})
	setupErr := errors.New("setup failed")
	var closed bool
	app.Root("Run app", func(app *App[appTestConfig]) error {
		app.Register("resource", func(ctx context.Context) error {
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

type testSignal struct{}

func (testSignal) String() string {
	return "test signal"
}

func (testSignal) Signal() {}
