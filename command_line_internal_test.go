package bee

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func assertError(t *testing.T, err error, want string) {
	t.Helper()

	if want == "" {
		if err != nil {
			t.Fatalf("want no error, got %v", err)
		}

		return
	}

	if err == nil {
		t.Fatalf("want error %q, got nil", want)
	}

	if err.Error() != want {
		t.Fatalf("want error %q, got %q", want, err.Error())
	}
}

func TestParse_errors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in      any
		flags   []string
		wantErr string
	}{
		"config-not-pointer-to-struct": {
			in:      []string{},
			flags:   []string{""},
			wantErr: "invalid config type",
		},
		"config-nil": {
			in:      nil,
			flags:   []string{""},
			wantErr: "invalid config type",
		},
		"config-typed-nil": {
			in: (*struct {
				Port int
			})(nil),
			flags:   []string{""},
			wantErr: "invalid config type",
		},
		"unsupported-field-type": {
			in: &struct {
				Port int16
			}{},
			flags:   []string{""},
			wantErr: "Port def: parsing value: type not supported: int16",
		},
		"---help": {
			in: &struct {
				Port int
			}{},
			flags:   []string{"---help"},
			wantErr: "bad flag syntax: ---help",
		},
	}

	for n, tt := range tests { //nolint:paralleltest

		t.Run(n, func(t *testing.T) {
			t.Parallel()

			cl := newCommandLine("test")
			cl.errorHandling = flag.ContinueOnError

			err := cl.parse(tt.in, tt.flags)
			assertError(t, err, tt.wantErr)
		})
	}
}

func TestParse_unexportedFieldReturnsError(t *testing.T) {
	t.Parallel()

	cl := newCommandLine("test")
	cl.errorHandling = flag.ContinueOnError

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("parse panicked for unexported field: %v", r)
		}
	}()

	err := cl.parse(&struct {
		Public  string
		private string
	}{}, []string{})

	if err == nil {
		t.Fatal("want error for unexported field, got nil")
	}
}

func TestParse_duplicateFlagReturnsError(t *testing.T) {
	t.Parallel()

	cl := newCommandLine("test")
	cl.errorHandling = flag.ContinueOnError

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("parse panicked for duplicate flag: %v", r)
		}
	}()

	err := cl.parse(&struct {
		First  string `flag:"same"`
		Second string `flag:"same"`
	}{}, []string{})

	assertError(t, err, `Second def: duplicate flag "same": invalid config type`)
}

func TestParse_requiredTagReturnsErrorWhenEnvAndFlagAreMissing(t *testing.T) {
	t.Parallel()

	cl := newCommandLine("test")
	cl.errorHandling = flag.ContinueOnError
	cl.lookupEnvFunc = func(string) (string, bool) {
		return "", false
	}

	err := cl.parse(&struct {
		DatabaseURL string `req:"true"`
	}{}, []string{})

	assertError(t, err, `DatabaseURL req: required value missing; set TEST_DATABASE_URL or -database-url`)
}

func TestParse_requiredTagIsSatisfiedByEnv(t *testing.T) {
	t.Parallel()

	cfg := &struct {
		DatabaseURL string `req:"true"`
	}{}
	cl := newCommandLine("test")
	cl.errorHandling = flag.ContinueOnError
	cl.lookupEnvFunc = func(env string) (string, bool) {
		if env == "TEST_DATABASE_URL" {
			return "postgres://env", true
		}

		return "", false
	}

	err := cl.parse(cfg, []string{})
	assertError(t, err, "")

	if cfg.DatabaseURL != "postgres://env" {
		t.Fatalf("want env required value, got %q", cfg.DatabaseURL)
	}
}

func TestParse_requiredTagIsSatisfiedByFlag(t *testing.T) {
	t.Parallel()

	cfg := &struct {
		DatabaseURL string `req:"true"`
	}{}
	cl := newCommandLine("test")
	cl.errorHandling = flag.ContinueOnError
	cl.lookupEnvFunc = func(string) (string, bool) {
		return "", false
	}

	err := cl.parse(cfg, []string{"--database-url", "postgres://flag"})
	assertError(t, err, "")

	if cfg.DatabaseURL != "postgres://flag" {
		t.Fatalf("want flag required value, got %q", cfg.DatabaseURL)
	}
}

func TestParse_requiredTagWorksForNestedConfig(t *testing.T) {
	t.Parallel()

	cl := newCommandLine("test")
	cl.errorHandling = flag.ContinueOnError
	cl.lookupEnvFunc = func(string) (string, bool) {
		return "", false
	}

	err := cl.parse(&struct {
		HTTP struct {
			Port int `req:"true"`
		}
	}{}, []string{})

	assertError(t, err, `Port req: required value missing; set TEST_HTTP_PORT or -http-port`)
}

func TestParse_requiredTagRejectsDefaultTag(t *testing.T) {
	t.Parallel()

	cl := newCommandLine("test")
	cl.errorHandling = flag.ContinueOnError

	err := cl.parse(&struct {
		DatabaseURL string `def:"postgres://default" req:"true"`
	}{}, []string{})

	assertError(t, err, `DatabaseURL req: cannot combine req and def tags`)
}

func TestExitOnErrorWritesErrorAndExits(t *testing.T) {
	var output bytes.Buffer
	cl := newCommandLine("test")
	cl.output = &output
	cl.errorHandling = flag.ExitOnError

	gotCode := captureExit(t, func() {
		_ = cl.exit(errors.New("bad flag"))
	})

	if gotCode != exitCode {
		t.Fatalf("want exit code %d, got %d", exitCode, gotCode)
	}

	if got, want := output.String(), "bee: bad flag\n"; got != want {
		t.Fatalf("want output %q, got %q", want, got)
	}
}

func TestExitOnErrorHelpExitsSuccessfully(t *testing.T) {
	cl := newCommandLine("test")
	cl.errorHandling = flag.ExitOnError
	cl.help = true

	gotCode := captureExit(t, func() {
		_ = cl.exit(flag.ErrHelp)
	})

	if gotCode != 0 {
		t.Fatalf("want exit code 0, got %d", gotCode)
	}
}

func TestPanicOnErrorPanics(t *testing.T) {
	t.Parallel()

	cl := newCommandLine("test")
	cl.errorHandling = flag.PanicOnError

	defer func() {
		if got := recover(); got != flag.ErrHelp {
			t.Fatalf("want panic %v, got %v", flag.ErrHelp, got)
		}
	}()

	_ = cl.exit(flag.ErrHelp)
}

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

func TestParse_usage(t *testing.T) { //nolint:funlen
	t.Parallel()

	ws := regexp.MustCompile(`\s+`)

	tests := map[string]struct {
		config  any
		want    string
		wantErr string
	}{
		"string-help-without-def": {
			config: &struct {
				LogLevel string
			}{},
			want: "Usage of test: -log-level string log level (env TEST_LOG_LEVEL)",
		},
		"string-help-with-def": {
			config: &struct {
				LogLevel string `def:"debug"`
			}{},
			want: `Usage of test: -log-level string log level (env TEST_LOG_LEVEL) (default "debug")`,
		},
		"bool-help-without-def": {
			config: &struct {
				Verbose bool
			}{},
			want: "Usage of test: -verbose verbose (env TEST_VERBOSE)",
		},
		"bool-help-with-def": {
			config: &struct {
				Verbose bool `def:"true"`
			}{},
			want: "Usage of test: -verbose verbose (env TEST_VERBOSE) (default true)",
		},
		"bool-help-with-invalid-def": {
			config: &struct {
				Verbose bool `def:"a"`
			}{},
			wantErr: `Verbose def: parsing bool "a": strconv.ParseBool: parsing "a": invalid syntax`,
		},
		"number-help-without-def": {
			config: &struct {
				Port       uint
				Distance   uint64
				Degrees    int
				Difference int64
				Balance    float64
			}{},
			want: `Usage of test:
-balance float balance (env TEST_BALANCE)
-degrees int degrees (env TEST_DEGREES)
-difference int difference (env TEST_DIFFERENCE)
-distance uint distance (env TEST_DISTANCE)
-port uint port (env TEST_PORT)`,
		},
		"number-help-with-def": {
			config: &struct {
				Port       uint    `def:"1"`
				Distance   uint64  `def:"1"`
				Degrees    int     `def:"1"`
				Difference int64   `def:"1"`
				Balance    float64 `def:"1"`
			}{},
			want: `Usage of test:
-balance float balance (env TEST_BALANCE) (default 1)
-degrees int degrees (env TEST_DEGREES) (default 1)
-difference int difference (env TEST_DIFFERENCE) (default 1)
-distance uint distance (env TEST_DISTANCE) (default 1)
-port uint port (env TEST_PORT) (default 1)`,
		},
		"uint-help-with-invalid-def": {
			config: &struct {
				Port uint `def:"a"`
			}{},
			wantErr: `Port def: parsing uint "a": strconv.ParseUint: parsing "a": invalid syntax`,
		},
		"uint64-help-with-invalid-def": {
			config: &struct {
				Difference uint64 `def:"a"`
			}{},
			wantErr: `Difference def: parsing uint64 "a": strconv.ParseUint: parsing "a": invalid syntax`,
		},
		"int-help-with-invalid-def": {
			config: &struct {
				Temperature int `def:"a"`
			}{},
			wantErr: `Temperature def: parsing int "a": strconv.Atoi: parsing "a": invalid syntax`,
		},
		"int64-help-with-invalid-def": {
			config: &struct {
				Distance int64 `def:"a"`
			}{},
			wantErr: `Distance def: parsing int64 "a": strconv.ParseInt: parsing "a": invalid syntax`,
		},
		"float64-help-with-invalid-def": {
			config: &struct {
				BlackHole float64 `def:"a"`
			}{},
			wantErr: `BlackHole def: parsing float64 "a": strconv.ParseFloat: parsing "a": invalid syntax`,
		},
		"duration-help-without-def": {
			config: &struct {
				Timeout time.Duration
			}{},
			want: "Usage of test: -timeout duration timeout (env TEST_TIMEOUT)",
		},
		"duration-help-with-def": {
			config: &struct {
				Timeout time.Duration `def:"1s"`
			}{},
			want: "Usage of test: -timeout duration timeout (env TEST_TIMEOUT) (default 1s)",
		},
		"duration-help-with-invalid-def": {
			config: &struct {
				Timeout time.Duration `def:"a"`
			}{},
			wantErr: `Timeout def: parsing duration "a": time: invalid duration "a"`,
		},
		"url-help-without-def": {
			config: &struct {
				Endpoint URL
			}{},
			want: "Usage of test: -endpoint value endpoint (env TEST_ENDPOINT)",
		},
		"url-help-with-def": {
			config: &struct {
				Endpoint URL `def:"http://localhost"`
			}{},
			want: "Usage of test: -endpoint value endpoint (env TEST_ENDPOINT) (default http://localhost)",
		},
		"url-help-with-invalid-def": {
			config: &struct {
				Endpoint URL `def:"%"`
			}{},
			wantErr: `Endpoint def: parsing url: parse "%": invalid URL escape "%"`,
		},
		"string-slice-help-without-def": {
			config: &struct {
				Buckets StringSlice
			}{},
			want: "Usage of test: -buckets value buckets (env TEST_BUCKETS)",
		},
		"string-slice-help-with-def": {
			config: &struct {
				Buckets StringSlice `def:"foo,bar,baz"`
			}{},
			want: "Usage of test: -buckets value buckets (env TEST_BUCKETS) (default ['foo','bar','baz'])",
		},
		"int-slice-help-without-def": {
			config: &struct {
				DailyTemperatures IntSlice
			}{},
			want: "Usage of test: -daily-temperatures value daily temperatures (env TEST_DAILY_TEMPERATURES)",
		},
		"int-slice-help-with-def": {
			config: &struct {
				DailyTemperatures IntSlice `def:"10,-5,0"`
			}{},
			want: "Usage of test: -daily-temperatures value daily temperatures (env TEST_DAILY_TEMPERATURES) (default [10,-5,0])", //nolint:lll
		},
		"int-slice-help-with-invalid-def": {
			config: &struct {
				DailyTemperatures IntSlice `def:"10,-5,a"`
			}{},
			wantErr: `DailyTemperatures def: parsing int: strconv.Atoi: parsing "a": invalid syntax`,
		},
		"number-in-child-struct-help-with-invalid-def": {
			config: &struct {
				DB struct {
					Port uint `def:"a"`
				}
			}{},
			wantErr: `Port def: parsing uint "a": strconv.ParseUint: parsing "a": invalid syntax`,
		},
		"override-flag-and-env": {
			config: &struct {
				Log struct {
					LogLevel  string `flag:"foo"`
					Verbose   bool   `env:"FOO"`
					Something struct {
						Why StringSlice
						Not IntSlice
						URL URL
					}
				}
				Expiration time.Duration `help:"bar"`
				Number1    int
				Number2    int64 `def:"5"`
				Number3    float64
				Number4    uint
				Number5    uint64
			}{},
			want: `Usage of test:
-expiration duration bar (env TEST_EXPIRATION)
-foo string log log level (env TEST_LOG_LOG_LEVEL)
-log-something-not value log something not (env TEST_LOG_SOMETHING_NOT)
-log-something-url value log something url (env TEST_LOG_SOMETHING_URL)
-log-something-why value log something why (env TEST_LOG_SOMETHING_WHY)
-log-verbose log verbose (env FOO) -number-1 int number 1 (env TEST_NUMBER_1)
-number-2 int number 2 (env TEST_NUMBER_2) (default 5)
-number-3 float number 3 (env TEST_NUMBER_3)
-number-4 uint number 4 (env TEST_NUMBER_4)
-number-5 uint number 5 (env TEST_NUMBER_5)`,
		},
		"time-invalid-def": {
			config: &struct {
				Start Time `def:"a"`
			}{},
			wantErr: `Start def: parsing time: parsing time "a" as "2006-01-02T15:04:05Z07:00": cannot parse "a" as "2006"`,
		},
		"time-valid-def": {
			config: &struct {
				Start Time `def:"2002-10-02T10:00:00-05:00"`
			}{},
			want: "Usage of test: -start value start (env TEST_START) (default 2002-10-02T10:00:00-05:00)",
		},
	}

	for n, tt := range tests { //nolint:paralleltest

		t.Run(n, func(t *testing.T) {
			t.Parallel()

			b := &bytes.Buffer{}

			cl := newCommandLine("test")
			cl.errorHandling = flag.ContinueOnError
			cl.output = b
			cl.flagSet.SetOutput(b)

			err := cl.parse(tt.config, []string{"-h"})
			assertError(t, err, tt.wantErr)
			if tt.wantErr != "" {
				return
			}

			got := strings.TrimSpace(ws.ReplaceAllString(b.String(), " "))
			want := ws.ReplaceAllString(tt.want, " ")
			if got != want {
				t.Errorf("\ngot %q\nwant %q\n", got, want)
			}
		})
	}
}

func TestParse_valid(t *testing.T) { //nolint:funlen
	t.Parallel()

	tests := map[string]struct {
		in any
	}{
		"empty-config": {
			in: &struct{}{},
		},
		"without-tags": {
			in: &struct {
				Host string
				Port int
				DB   struct {
					Kind     string
					Postgres struct {
						Host string
					}
					Mongo struct {
						Host StringSlice
					}
				}
				Start Time
			}{},
		},
		"with-tags": {
			in: &struct {
				Env   string `help:"environment [development|production]" def:"development"`
				Port  uint   `def:"3000"`
				Mongo struct {
					Hosts             StringSlice   `def:"mongo"`
					ConnectionTimeout time.Duration `def:"10s"`
					ReplicaSet        string
					MaxPoolSize       uint64 `def:"100"`
					TLS               bool
					Username          string
					Password          string
					Database          string `def:"cool"`
				}
				JWT struct {
					Secret                 string
					TokenExpiration        time.Duration `def:"24h"`
					RefreshTokenExpiration time.Duration `def:"168h"`
				}
				AWS struct {
					Region string `def:"eu-central-1"`
				}
				Start Time `def:"2002-10-02T10:00:00-05:00"`
			}{},
		},
	}

	for n, tt := range tests { //nolint:paralleltest

		t.Run(n, func(t *testing.T) {
			t.Parallel()

			a := newCommandLine("test")

			err := a.parse(tt.in, []string{})
			if err != nil {
				t.Error(err)
			}
		})
	}
}

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
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cl := newCommandLine("test")
			cl.errorHandling = flag.ContinueOnError
			err := cl.parse(tt.cfg, tt.flags)
			assertError(t, err, tt.wantErr)
		})
	}
}

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
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cl := newCommandLine("test")
			cl.errorHandling = flag.ContinueOnError
			err := cl.parse(tt.cfg, tt.flags)
			assertError(t, err, tt.wantErr)
		})
	}
}

func TestParseValidationNonzeroSpecialTypes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg     any
		flags   []string
		wantErr string
	}{
		"url empty": {
			cfg: &struct {
				Database URL `nonzero:""`
			}{},
			flags:   []string{"--database", ""},
			wantErr: `Database nonzero: value must not be zero`,
		},
		"time zero default": {
			cfg: &struct {
				Start Time `nonzero:""`
			}{},
			flags:   []string{},
			wantErr: `Start nonzero: value must not be zero`,
		},
		"time zero flag": {
			cfg: &struct {
				Start Time `nonzero:""`
			}{},
			flags:   []string{"--start", "0001-01-01T00:00:00Z"},
			wantErr: `Start nonzero: value must not be zero`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cl := newCommandLine("test")
			cl.errorHandling = flag.ContinueOnError
			err := cl.parse(tt.cfg, tt.flags)
			assertError(t, err, tt.wantErr)
		})
	}
}

func TestParseValidationRejectsEmptyLists(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg     any
		flags   []string
		wantErr string
	}{
		"oneof": {
			cfg: &struct {
				Env string `oneof:""`
			}{},
			flags:   []string{"--env", "dev"},
			wantErr: `Env oneof: empty value list`,
		},
		"prefix": {
			cfg: &struct {
				Topic string `prefix:""`
			}{},
			flags:   []string{"--topic", "maia.events"},
			wantErr: `Topic prefix: empty value list`,
		},
		"suffix": {
			cfg: &struct {
				File string `suffix:""`
			}{},
			flags:   []string{"--file", "config.json"},
			wantErr: `File suffix: empty value list`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cl := newCommandLine("test")
			cl.errorHandling = flag.ContinueOnError
			err := cl.parse(tt.cfg, tt.flags)
			assertError(t, err, tt.wantErr)
		})
	}
}

func TestParseValidationMinMaxRejectsNaN(t *testing.T) {
	t.Parallel()

	cl := newCommandLine("test")
	cl.errorHandling = flag.ContinueOnError

	err := cl.parse(&struct {
		Rate float64 `min:"0" max:"1"`
	}{}, []string{"--rate", "NaN"})

	assertError(t, err, `Rate min: value NaN must be a number`)
}

func TestParseValidationMinMaxRejectsUnsupportedType(t *testing.T) {
	t.Parallel()

	cl := newCommandLine("test")
	cl.errorHandling = flag.ContinueOnError

	err := cl.parse(&struct {
		Name string `min:"3"`
	}{}, []string{"--name", "maia"})

	assertError(t, err, `Name min: unsupported type string`)
}

func TestParse_environment_errors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		config        any
		lookupEnvFunc func(string) (string, bool)
		wantErr       string
	}{
		"number-invalid-env": {
			config: &struct {
				Port uint `def:"1"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "a", true
			},
			wantErr: `Port env: parsing uint "a": strconv.ParseUint: parsing "a": invalid syntax`,
		},
		"duration-invalid-env": {
			config: &struct {
				Timeout time.Duration `def:"1s"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "a", true
			},
			wantErr: `Timeout env: parsing duration "a": time: invalid duration "a"`,
		},
		"url-invalid-env": {
			config: &struct {
				Endpoint URL `def:"http://localhost"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "%", true
			},
			wantErr: `Endpoint env: parsing url: parse "%": invalid URL escape "%"`,
		},
		"int-slice-invalid-env": {
			config: &struct {
				DailyTemperatures IntSlice `def:"10,-5,0"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "a,-2,3", true
			},
			wantErr: `DailyTemperatures env: parsing int: strconv.Atoi: parsing "a": invalid syntax`,
		},
	}

	for n, tt := range tests { //nolint:paralleltest

		t.Run(n, func(t *testing.T) {
			t.Parallel()

			cl := newCommandLine("test")
			cl.errorHandling = flag.ContinueOnError
			cl.lookupEnvFunc = tt.lookupEnvFunc

			err := cl.parse(tt.config, []string{})
			assertError(t, err, tt.wantErr)
		})
	}
}

func TestParse_environment(t *testing.T) { //nolint:cyclop,gocognit,funlen,maintidx
	t.Parallel()

	tests := map[string]struct {
		config          any
		lookupEnvFunc   func(string) (string, bool)
		wantString      string
		wantBool        bool
		wantUint        uint
		wantUint64      uint64
		wantInt         int
		wantInt64       int64
		wantFloat64     float64
		wantDuration    time.Duration
		wantURL         URL
		wantStringSlice StringSlice
		wantIntSlice    IntSlice
	}{
		"string-env-not-set-def-not-set": {
			config: &struct {
				LogLevel string
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantString: "",
		},
		"string-env-not-set-def-set": {
			config: &struct {
				LogLevel string `def:"debug"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantString: "debug",
		},
		"string-env-set": {
			config: &struct {
				LogLevel string `def:"debug"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "info", true
			},
			wantString: "info",
		},
		"bool-env-not-set-def-not-set": {
			config: &struct {
				Verbose bool
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantBool: false,
		},
		"bool-env-not-set-def-set": {
			config: &struct {
				Verbose bool `def:"true"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantBool: true,
		},
		"bool-env-set": {
			config: &struct {
				Verbose bool `def:"true"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "false", true
			},
			wantBool: false,
		},
		"uint-env-not-set-def-not-set": {
			config: &struct {
				Port uint
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantUint: 0,
		},
		"uint-env-not-set-def-set": {
			config: &struct {
				Port uint `def:"1"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantUint: 1,
		},
		"uint-env-set": {
			config: &struct {
				Port uint `def:"1"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "2", true
			},
			wantUint: 2,
		},
		"uint64-env-not-set-def-not-set": {
			config: &struct {
				Distance uint64
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantUint64: 0,
		},
		"uint64-env-not-set-def-set": {
			config: &struct {
				Distance uint64 `def:"1"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantUint64: 1,
		},
		"uint64-env-set": {
			config: &struct {
				Distance uint64 `def:"1"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "2", true
			},
			wantUint64: 2,
		},
		"int-env-not-set-def-not-set": {
			config: &struct {
				Degrees int
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantInt: 0,
		},
		"int-env-not-set-def-set": {
			config: &struct {
				Degrees int `def:"1"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantInt: 1,
		},
		"int-env-set": {
			config: &struct {
				Degrees int `def:"1"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "2", true
			},
			wantInt: 2,
		},
		"int64-env-not-set-def-not-set": {
			config: &struct {
				Difference int64
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantInt64: 0,
		},
		"int64-env-not-set-def-set": {
			config: &struct {
				Difference int64 `def:"1"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantInt64: 1,
		},
		"int64-env-set": {
			config: &struct {
				Difference int64 `def:"1"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "2", true
			},
			wantInt64: 2,
		},
		"float64-env-not-set-def-not-set": {
			config: &struct {
				Balance float64
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantFloat64: 0,
		},
		"float64-env-not-set-def-set": {
			config: &struct {
				Balance float64 `def:"1.2"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantFloat64: 1.2,
		},
		"float64-env-set": {
			config: &struct {
				Balance float64 `def:"1.2"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "2.3", true
			},
			wantFloat64: 2.3,
		},
		"duration-env-not-set-def-not-set": {
			config: &struct {
				Timeout time.Duration
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantDuration: time.Duration(0),
		},
		"duration-env-not-set-def-set": {
			config: &struct {
				Timeout time.Duration `def:"1s"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantDuration: time.Second,
		},
		"duration-env-set": {
			config: &struct {
				Timeout time.Duration `def:"1s"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "2s", true
			},
			wantDuration: 2 * time.Second,
		},
		"url-env-not-set-def-not-set": {
			config: &struct {
				Endpoint URL
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantURL: URL{}, //nolint:exhaustruct
		},
		"url-env-not-set-def-set": {
			config: &struct {
				Endpoint URL `def:"http://localhost"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantURL: URL{URL: &url.URL{Scheme: "http", Host: "localhost"}}, //nolint:exhaustruct
		},
		"url-env-set": {
			config: &struct {
				Endpoint URL `def:"http://localhost"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "https://api.example.com/v1", true
			},
			wantURL: URL{URL: &url.URL{Scheme: "https", Host: "api.example.com", Path: "/v1"}}, //nolint:exhaustruct
		},
		"string-slice-env-not-set-def-not-set": {
			config: &struct {
				Buckets StringSlice
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantStringSlice: StringSlice{},
		},
		"string-slice-env-not-set-def-set": {
			config: &struct {
				Buckets StringSlice `def:"foo,bar,baz"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantStringSlice: StringSlice{"foo", "bar", "baz"},
		},
		"string-slice-env-set": {
			config: &struct {
				Buckets StringSlice `def:"foo,bar,baz"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "qux,bar", true
			},
			wantStringSlice: StringSlice{"qux", "bar"},
		},
		"int-slice-env-not-set-def-not-set": {
			config: &struct {
				DailyTemperatures IntSlice
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantIntSlice: IntSlice{},
		},
		"int-slice-env-not-set-def-set": {
			config: &struct {
				DailyTemperatures IntSlice `def:"10,-5,0"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "", false
			},
			wantIntSlice: IntSlice{10, -5, 0},
		},
		"int-slice-env-set": {
			config: &struct {
				DailyTemperatures IntSlice `def:"10,-5,0"`
			}{},
			lookupEnvFunc: func(env string) (string, bool) {
				return "-2,3,-1", true
			},
			wantIntSlice: IntSlice{-2, 3, -1},
		},
	}

	for n, tt := range tests { //nolint:paralleltest

		t.Run(n, func(t *testing.T) {
			t.Parallel()

			cl := newCommandLine("test")
			cl.errorHandling = flag.ContinueOnError
			cl.lookupEnvFunc = tt.lookupEnvFunc

			if err := cl.parse(tt.config, []string{}); err != nil {
				t.Error(err)
			}

			v := reflect.ValueOf(tt.config).Elem()

			p := v.FieldByIndex([]int{0}).Addr().Interface()

			switch p := p.(type) {
			case *string:
				if *p != tt.wantString {
					t.Errorf("want %q, got %q", tt.wantString, *p)
				}
			case *bool:
				if *p != tt.wantBool {
					t.Errorf("want %t, got %t", tt.wantBool, *p)
				}
			case *uint:
				if *p != tt.wantUint {
					t.Errorf("want %d, got %d", tt.wantUint, *p)
				}
			case *uint64:
				if *p != tt.wantUint64 {
					t.Errorf("want %d, got %d", tt.wantUint64, *p)
				}
			case *int:
				if *p != tt.wantInt {
					t.Errorf("want %d, got %d", tt.wantInt, *p)
				}
			case *int64:
				if *p != tt.wantInt64 {
					t.Errorf("want %d, got %d", tt.wantInt64, *p)
				}
			case *float64:
				if *p != tt.wantFloat64 {
					t.Errorf("want %f, got %f", tt.wantFloat64, *p)
				}
			case *time.Duration:
				if *p != tt.wantDuration {
					t.Errorf("want %v, got %v", tt.wantDuration, *p)
				}
			case *URL:
				if !reflect.DeepEqual(*p, tt.wantURL) {
					t.Errorf("want %#v, got %#v", tt.wantURL, *p)
				}
			case *StringSlice:
				if !reflect.DeepEqual(*p, tt.wantStringSlice) {
					t.Errorf("want %#v, got %#v", tt.wantStringSlice, *p)
				}
			case *IntSlice:
				if !reflect.DeepEqual(*p, tt.wantIntSlice) {
					t.Errorf("want %#v, got %#v", tt.wantIntSlice, *p)
				}
			default:
				t.Errorf("type %T not supported", p)
			}
		})
	}
}

func TestWithUsage(t *testing.T) {
	t.Parallel()

	b := &bytes.Buffer{}

	cl := newCommandLine("me")
	cl.errorHandling = flag.ContinueOnError
	cl.output = b
	cl.flagSet.Usage = func() {
		_, _ = fmt.Fprintf(cl.output, "Usage of %s %s:\n", "test", cl.name)
		cl.flagSet.PrintDefaults()
	}

	_ = cl.parse(&struct{}{}, []string{"-h"})

	if want := "Usage of test me:\n"; want != b.String() {
		t.Errorf("want %q got %q", want, b.String())
	}
}
