package bee

import (
	"bytes"
	"flag"
	"reflect"
	"strings"
	"testing"
)

type scopedConfig struct {
	Global string `def:"global"`
	Key    string `cmd:"serve" req:"" nonzero:""`
	Shared string `cmd:" serve, sender " def:"shared"`
	Server struct {
		Port int `def:"8080" min:"1" max:"65535"`
		Deep struct {
			Name string `def:"deep"`
			Only string `cmd:"serve" def:"only"`
		}
	} `cmd:"serve,sender"`
	Sender int `cmd:"sender" req:"" nonzero:""`
}

func scopeApp[T any](cfg *T, output *bytes.Buffer, opts ...Option) *App[T] {
	opts = append([]Option{WithOutput(output), WithErrorHandling(flag.ContinueOnError), WithLookupEnvFunc(func(string) (string, bool) { return "", false })}, opts...)
	app := New("scope", cfg, opts...)
	handler := func(*Ctx[T]) error { return nil }
	serve := app.Cmd("serve", "Serve")
	serve.Cmd("something", "First", handler)
	serve.Cmd("something-else", "Second", handler)
	app.Cmd("sender", "Send", handler)
	app.Cmd("migrate", "Migrate", handler)
	app.Root("Root", handler)
	return app
}

func TestCommandScopesSelection(t *testing.T) {
	for _, tt := range []struct {
		name  string
		args  []string
		def   string
		group string
	}{
		{name: "first descendant", args: []string{"serve", "something", "-key=secret"}, group: "serve"},
		{name: "second descendant", args: []string{"serve", "something-else", "-key=secret"}, group: "serve"},
		{name: "nested default", args: []string{"-key=secret"}, def: "serve something", group: "serve"},
		{name: "explicit overrides default", args: []string{"migrate"}, def: "serve something"},
		{name: "sender", args: []string{"sender", "-sender=2"}, group: "sender"},
		{name: "direct default", args: []string{"-sender=2"}, def: "sender", group: "sender"},
		{name: "root"},
		{name: "unrelated", args: []string{"migrate"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := scopedConfig{Key: "untouched", Shared: "untouched", Sender: 99}
			cfg.Server.Port = 42
			cfg.Server.Deep.Name = "untouched"
			cfg.Server.Deep.Only = "untouched"
			want := cfg
			want.Global = "global"
			if tt.group != "" {
				want.Shared = "shared"
				want.Server.Port = 8080
				want.Server.Deep.Name = "deep"
			}
			if tt.group == "serve" {
				want.Key = "secret"
				want.Server.Deep.Only = "only"
			}
			if tt.group == "sender" {
				want.Sender = 2
			}
			app := scopeApp(&cfg, &bytes.Buffer{}, WithDefaultCommand(tt.def))
			if err := app.RunE(tt.args...); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg, want) {
				t.Fatalf("got %+v, want %+v", cfg, want)
			}
		})
	}
}

func TestCommandScopesOverridesAndValidation(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		env     map[string]string
		wantErr string
		port    int
	}{
		{name: "environment", args: []string{"serve", "something"}, env: map[string]string{"SCOPE_KEY": "secret", "SCOPE_SERVER_PORT": "9000"}, port: 9000},
		{name: "flag overrides environment", args: []string{"serve", "something", "-server-port=9001"}, env: map[string]string{"SCOPE_KEY": "secret", "SCOPE_SERVER_PORT": "9000"}, port: 9001},
		{name: "missing required", args: []string{"serve", "something"}, wantErr: "Key req"},
		{name: "zero required", args: []string{"serve", "something", "-key="}, wantErr: "Key nonzero"},
		{name: "range validation", args: []string{"serve", "something", "-key=secret", "-server-port=0"}, wantErr: "Port min"},
		{name: "inactive flag", args: []string{"migrate", "-key=secret"}, wantErr: "flag provided but not defined"},
		{name: "inactive malformed environment", args: []string{"migrate"}, env: map[string]string{"SCOPE_SENDER": "bad", "SCOPE_SERVER_PORT": "bad"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := scopedConfig{}
			app := scopeApp(&cfg, &bytes.Buffer{}, WithLookupEnvFunc(func(key string) (string, bool) {
				if key == "SCOPE_SENDER" || (tt.name == "inactive malformed environment" && key != "SCOPE_GLOBAL") {
					t.Errorf("read inactive env %s", key)
				}
				v, ok := tt.env[key]
				return v, ok
			}))
			err := app.RunE(tt.args...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got %v, want %s", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Server.Port != tt.port {
				t.Fatalf("port %d, want %d", cfg.Server.Port, tt.port)
			}
		})
	}
}

func TestCommandScopesHelp(t *testing.T) {
	for _, args := range [][]string{{"-help"}, {"serve", "-help"}, {"serve", "something", "-help"}, {"serve", "something-else", "-help"}, {"sender", "-help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			output := &bytes.Buffer{}
			cfg := scopedConfig{}
			app := scopeApp(&cfg, output, WithDefaultCommand("serve something"))
			if err := app.RunE(args...); err != nil {
				t.Fatal(err)
			}
			got := output.String()
			if !strings.Contains(got, "-global") {
				t.Fatalf("missing global flag: %s", got)
			}
			serve := args[0] == "serve"
			sender := args[0] == "sender"
			for flagName, want := range map[string]bool{"-key": serve, "-sender": sender, "-server-port": serve || sender, "-server-deep-only": serve} {
				if strings.Contains(got, flagName) != want {
					t.Errorf("flag %s presence, want %v: %s", flagName, want, got)
				}
			}
			if args[0] == "-help" && !strings.Contains(got, "serve something-else") {
				t.Errorf("missing command overview: %s", got)
			}
		})
	}
}

func TestCommandScopesInvalidDeclarations(t *testing.T) {
	for _, scope := range []string{"", " ", "missing", "serve something", "serve/something", "*", "serve,*", "serve,", ",serve"} {
		t.Run(scope, func(t *testing.T) {
			typ := reflect.StructOf([]reflect.StructField{{Name: "Outer", Type: reflect.StructOf([]reflect.StructField{{Name: "Value", Type: reflect.TypeFor[string](), Tag: reflect.StructTag(`cmd:"` + scope + `"`)}})}})
			cfg := reflect.New(typ).Interface()
			cl := newCommandLine("scope")
			cl.errorHandling = flag.ContinueOnError
			cl.commandGroups = []string{"serve", "sender"}
			err := cl.parse(cfg, nil)
			if err == nil || !strings.Contains(err.Error(), "Outer.Value cmd") || !strings.Contains(err.Error(), scope) {
				t.Fatalf("got %v for scope %q", err, scope)
			}
		})
	}
}

func TestCommandScopesInvalidChildBeforeParsing(t *testing.T) {
	for _, args := range [][]string{{"migrate"}, {"serve", "something"}, {"-help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cfg := struct {
				Global string `def:"changed"`
				Server struct {
					Deep struct {
						Key string `cmd:"sender"`
					}
				} `cmd:"serve"`
			}{Global: "original"}
			app := scopeApp(&cfg, &bytes.Buffer{}, WithLookupEnvFunc(func(key string) (string, bool) {
				t.Errorf("read %s before declaration validation", key)
				return "", false
			}))
			err := app.RunE(args...)
			if err == nil || !strings.Contains(err.Error(), "Server.Deep.Key cmd") || !strings.Contains(err.Error(), "outside parent scope") {
				t.Fatalf("got %v", err)
			}
			if cfg.Global != "original" {
				t.Fatalf("mutated config before declaration validation: %+v", cfg)
			}
		})
	}
}

func TestCommandScopesRequiredDefaultConflict(t *testing.T) {
	for _, args := range [][]string{{"migrate"}, {"serve", "something"}, {"serve", "-help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cfg := struct {
				Value string `cmd:"serve" req:"" def:"invalid"`
			}{Value: "original"}
			app := scopeApp(&cfg, &bytes.Buffer{})
			err := app.RunE(args...)
			if args[0] == "migrate" {
				if err != nil || cfg.Value != "original" {
					t.Fatalf("inactive field: config %+v, error %v", cfg, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "cannot combine req and def") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestCommandScopesRunnableParent(t *testing.T) {
	type config struct {
		Key string `cmd:"serve" req:"" nonzero:""`
	}
	cfg := config{}
	app := New("scope", &cfg, WithOutput(&bytes.Buffer{}), WithErrorHandling(flag.ContinueOnError), WithLookupEnvFunc(func(string) (string, bool) { return "", false }))
	called := false
	app.Cmd("serve", "Serve", func(*Ctx[config]) error {
		called = true
		return nil
	})
	if err := app.RunE("serve", "-key=secret"); err != nil {
		t.Fatal(err)
	}
	if !called || cfg.Key != "secret" {
		t.Fatalf("called %v, config %+v", called, cfg)
	}
}

func TestCommandScopesSpecialTypesAndNames(t *testing.T) {
	type config struct {
		URL    URL         `cmd:"serve" def:"https://example.com" nonzero:""`
		When   Time        `cmd:"serve" nonzero:""`
		Names  StringSlice `cmd:"serve" def:"a,b" minlen:"1"`
		Custom struct {
			Value int `flag:"custom" env:"CUSTOM_VALUE" def:"3" min:"1"`
		} `cmd:"serve"`
	}
	for _, args := range [][]string{{"migrate"}, {"serve", "something", "-when=2026-09-10T00:00:00Z", "-custom=5"}} {
		t.Run(args[0], func(t *testing.T) {
			cfg := config{Names: StringSlice{"original"}}
			app := scopeApp(&cfg, &bytes.Buffer{}, WithLookupEnvFunc(func(key string) (string, bool) {
				if key == "CUSTOM_VALUE" {
					return "4", true
				}
				return "", false
			}))
			if err := app.RunE(args...); err != nil {
				t.Fatal(err)
			}
			if args[0] == "migrate" {
				if cfg.URL.URL != nil || cfg.When.Time != nil || cfg.Names[0] != "original" || cfg.Custom.Value != 0 {
					t.Fatalf("inactive values changed: %+v", cfg)
				}
				return
			}
			if cfg.URL.String() != "https://example.com" || cfg.When.Time == nil || len(cfg.Names) != 2 || cfg.Custom.Value != 5 {
				t.Fatalf("active config: %+v", cfg)
			}
		})
	}
}
