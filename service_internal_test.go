package bee

import (
	"bytes"
	"flag"
	"log/slog"
	"testing"
	"time"
)

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

func TestSlogError(t *testing.T) {
	t.Parallel()

	attr := SlogError(flag.ErrHelp)
	if attr.Key != "error" || attr.Value.String() != flag.ErrHelp.Error() {
		t.Fatalf("want error attr for %q, got %s=%s", flag.ErrHelp, attr.Key, attr.Value)
	}
}

func TestRef(t *testing.T) {
	t.Parallel()

	value := Ref("bee")
	if value == nil || *value != "bee" {
		t.Fatalf("want ref to bee, got %v", value)
	}
}
