package bee

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var exitFuncMu sync.Mutex

type exitPanic int

func captureExit(t *testing.T, fn func()) (code int) {
	t.Helper()

	exitFuncMu.Lock()
	t.Cleanup(func() {
		osExit = os.Exit
		exitFuncMu.Unlock()
	})

	osExit = func(code int) {
		panic(exitPanic(code))
	}

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("want exit, got return")
		}

		exit, ok := got.(exitPanic)
		if !ok {
			panic(got)
		}

		code = int(exit)
	}()

	fn()

	return code
}

func TestServiceOptions(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	opts := appOptions{} //nolint:exhaustruct

	WithShutdownGracePeriod(3 * time.Second)(&opts)
	WithLogLevel("warn")(&opts)
	WithErrorHandling(flag.ContinueOnError)(&opts)
	WithOutput(&output)(&opts)
	WithLookupEnvFunc(func(string) (string, bool) {
		return "value", true
	})(&opts)
	WithUsage("parent")(&opts)

	if opts.timeout != 3*time.Second {
		t.Fatalf("want timeout 3s, got %s", opts.timeout)
	}

	if opts.logLevel.Level() != slog.LevelWarn {
		t.Fatalf("want warn log level, got %s", opts.logLevel.Level())
	}

	if opts.errorHandling != flag.ContinueOnError {
		t.Fatalf("want ContinueOnError, got %v", opts.errorHandling)
	}

	if opts.output != &output {
		t.Fatalf("want output to be overridden, got %v", opts.output)
	}

	value, ok := opts.lookupEnvFunc("ANY")
	if !ok || value != "value" {
		t.Fatalf("want lookup env override, got %q %t", value, ok)
	}

	if opts.parentUsage != "parent" {
		t.Fatalf("want parent usage, got %q", opts.parentUsage)
	}
}

func TestWithLogLevelDefaultsToDebug(t *testing.T) {
	t.Parallel()

	opts := appOptions{} //nolint:exhaustruct
	WithLogLevel("trace")(&opts)

	if opts.logLevel.Level() != slog.LevelDebug {
		t.Fatalf("want debug log level, got %s", opts.logLevel.Level())
	}
}

func TestWithLogLevel(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   string
		want slog.Level
	}{
		"info": {
			in:   "info",
			want: slog.LevelInfo,
		},
		"error": {
			in:   "ERROR",
			want: slog.LevelError,
		},
	}

	for name, tt := range tests {

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := appOptions{} //nolint:exhaustruct
			WithLogLevel(tt.in)(&opts)

			if opts.logLevel.Level() != tt.want {
				t.Fatalf("want %s log level, got %s", tt.want, opts.logLevel.Level())
			}
		})
	}
}

func TestServiceRunClosesRegisteredClosersInReverseOrder(t *testing.T) {
	t.Parallel()

	cfg := struct{}{}
	app := New(
		"test",
		&cfg,
		WithOutput(io.Discard),
		WithErrorHandling(flag.ContinueOnError),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	app.timeout = 100 * time.Millisecond

	var calls []string
	app.Root("Run app", func(ctx *Ctx[struct{}]) error {
		ctx.Register("first", func(run context.Context) error {
			if err := run.Err(); err != nil {
				t.Fatalf("first closer got expired context: %v", err)
			}

			calls = append(calls, "first")

			return nil
		})
		ctx.Register("second", func(run context.Context) error {
			if err := run.Err(); err != nil {
				t.Fatalf("second closer got expired context: %v", err)
			}

			calls = append(calls, "second")

			return errors.New("close second")
		})
		app.cancel()

		return nil
	})

	err := app.RunE()
	if err == nil || !strings.Contains(err.Error(), "close second") {
		t.Fatalf("want closer error, got %v", err)
	}

	want := []string{"second", "first"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("want closer calls %v, got %v", want, calls)
	}
}

func TestAppNewWithUsage(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	cfg := struct{}{}
	app := New("test", &cfg, WithOutput(&output), WithUsage("parent"), WithErrorHandling(flag.ContinueOnError))
	app.Root("Run app", func(ctx *Ctx[struct{}]) error {
		return nil
	})

	if err := app.RunE("-h"); err != nil {
		t.Fatal(err)
	}

	if got, want := output.String(), "Usage of parent test:\nRun app\n\nFlags:\n"; got != want {
		t.Fatalf("want usage %q, got %q", want, got)
	}
}

func TestAppRunDoesNotExitOnSuccess(t *testing.T) {
	exitFuncMu.Lock()
	args := os.Args
	t.Cleanup(func() {
		os.Args = args
		osExit = os.Exit
		exitFuncMu.Unlock()
	})

	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	app.Root("Run app", func(*Ctx[appTestConfig]) error {
		return nil
	})

	osExit = func(code int) {
		t.Fatalf("Run called osExit(%d) on success", code)
	}
	os.Args = []string{"test"}

	app.Run()
}

func TestAppRunExitsOnFailure(t *testing.T) {
	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	app.Root("Run app", func(*Ctx[appTestConfig]) error {
		return errors.New("boom")
	})

	args := os.Args
	os.Args = []string{"test"}
	t.Cleanup(func() {
		os.Args = args
	})

	gotCode := captureExit(t, app.Run)
	if gotCode != exitCode {
		t.Fatalf("want exit code %d, got %d", exitCode, gotCode)
	}
}

func TestSlogError(t *testing.T) {
	t.Parallel()

	attr := SlogError(flag.ErrHelp)
	if attr.Key != "error" || attr.Value.String() != flag.ErrHelp.Error() {
		t.Fatalf("want error attr for %q, got %s=%s", flag.ErrHelp, attr.Key, attr.Value)
	}
}

func TestExitWritesMessageAndExits(t *testing.T) {
	var output bytes.Buffer
	stderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		os.Stderr = stderr
		_ = r.Close()
	})

	os.Stderr = w

	gotCode := captureExit(t, func() {
		Exit("stopping", errors.New("boom"))
	})

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := output.ReadFrom(r); err != nil {
		t.Fatal(err)
	}

	if gotCode != exitCode {
		t.Fatalf("want exit code %d, got %d", exitCode, gotCode)
	}

	if got, want := output.String(), "stopping: boom\n"; got != want {
		t.Fatalf("want stderr %q, got %q", want, got)
	}
}

func TestExitWritesMessageWithoutError(t *testing.T) {
	var output bytes.Buffer
	stderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		os.Stderr = stderr
		_ = r.Close()
	})

	os.Stderr = w

	gotCode := captureExit(t, func() {
		Exit("stopping", nil)
	})

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := output.ReadFrom(r); err != nil {
		t.Fatal(err)
	}

	if gotCode != exitCode {
		t.Fatalf("want exit code %d, got %d", exitCode, gotCode)
	}

	if got, want := output.String(), "stopping\n"; got != want {
		t.Fatalf("want stderr %q, got %q", want, got)
	}
}

func TestRef(t *testing.T) {
	t.Parallel()

	value := Ref("bee")
	if value == nil || *value != "bee" {
		t.Fatalf("want ref to bee, got %v", value)
	}
}
