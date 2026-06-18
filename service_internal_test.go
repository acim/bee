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

	s := &Service{name: "test", commandLine: newCommandLine("test")} //nolint:exhaustruct

	WithShutdownGracePeriod(3 * time.Second)(s)
	WithLogLevel("warn")(s)
	WithErrorHandling(flag.ContinueOnError)(s)
	WithOutput(&output)(s)
	WithLookupEnvFunc(func(string) (string, bool) {
		return "value", true
	})(s)
	WithUsage("parent")(s)

	if s.timeout != 3*time.Second {
		t.Fatalf("want timeout 3s, got %s", s.timeout)
	}

	if s.logLevel.Level() != slog.LevelWarn {
		t.Fatalf("want warn log level, got %s", s.logLevel.Level())
	}

	if s.commandLine.errorHandling != flag.ContinueOnError {
		t.Fatalf("want ContinueOnError, got %v", s.commandLine.errorHandling)
	}

	if got := s.commandLine.flagSet.Output(); got != &output {
		t.Fatalf("want flag set output to be overridden, got %v", got)
	}

	value, ok := s.commandLine.lookupEnvFunc("ANY")
	if !ok || value != "value" {
		t.Fatalf("want lookup env override, got %q %t", value, ok)
	}

	s.commandLine.flagSet.Usage()
	if got, want := output.String(), "Usage of parent test:\n"; got != want {
		t.Fatalf("want usage %q, got %q", want, got)
	}
}

func TestWithLogLevelDefaultsToDebug(t *testing.T) {
	t.Parallel()

	s := &Service{} //nolint:exhaustruct
	WithLogLevel("trace")(s)

	if s.logLevel.Level() != slog.LevelDebug {
		t.Fatalf("want debug log level, got %s", s.logLevel.Level())
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
		name := name
		tt := tt

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := &Service{} //nolint:exhaustruct
			WithLogLevel(tt.in)(s)

			if s.logLevel.Level() != tt.want {
				t.Fatalf("want %s log level, got %s", tt.want, s.logLevel.Level())
			}
		})
	}
}

func TestServiceRunClosesRegisteredClosersInReverseOrder(t *testing.T) {
	t.Parallel()

	stop := make(chan os.Signal, 1)
	s := &Service{ //nolint:exhaustruct
		stop:    stop,
		timeout: 100 * time.Millisecond,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	var calls []string
	s.Register("first", func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			t.Fatalf("first closer got expired context: %v", err)
		}

		calls = append(calls, "first")

		return nil
	})
	s.Register("second", func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			t.Fatalf("second closer got expired context: %v", err)
		}

		calls = append(calls, "second")

		return errors.New("close second")
	})

	stop <- os.Interrupt
	s.Run()

	want := []string{"second", "first"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("want closer calls %v, got %v", want, calls)
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

func TestNewServiceExitsOnParseError(t *testing.T) {
	var output bytes.Buffer
	args := os.Args
	stderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		os.Args = args
		os.Stderr = stderr
		_ = r.Close()
	})

	os.Args = []string{"test"}
	os.Stderr = w

	gotCode := captureExit(t, func() {
		_ = NewService("test", []string{}, WithErrorHandling(flag.ContinueOnError))
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

	if got, want := output.String(), "failed parsing command line: invalid config type\n"; got != want {
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
