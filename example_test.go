package bee_test

import (
	"flag"
	"io"
	"log/slog"
	"os"
	"time"

	"go.acim.net/bee"
)

func Example_basic() {
	type config struct {
		Host string
		Port int
		DB   struct {
			Kind     string
			Postgres struct {
				Host string
			}
			Mongo struct {
				Host bee.StringSlice
			}
		}
		Start bee.Time
	}

	cfg := config{} //nolint:exhaustruct

	args := os.Args
	defer func() {
		os.Args = args
	}()
	os.Args = []string{"mycmd"}

	app := bee.New(
		"mycmd",
		&cfg,
		bee.WithErrorHandling(flag.ContinueOnError),
		bee.WithOutput(io.Discard),
	)
	app.Root("Run app", func(ctx *bee.Ctx[config]) error {
		return nil
	})
	_ = app.RunE()

	// Output:
}

func Example_advanced() {
	type config struct {
		Env   string `help:"environment [development|production]" def:"development"`
		Port  uint   `def:"3000"`
		Mongo struct {
			Hosts             bee.StringSlice `def:"mongo"`
			ConnectionTimeout time.Duration   `def:"10s"`
			ReplicaSet        string
			MaxPoolSize       uint64 `def:"100"`
			TLS               bool
			Username          string
			Password          string `env:"MONGO_PASSWORD"`
			Database          string `def:"cool"`
		}
		JWT struct {
			Secret                 string        `env:"JWT_SECRET"`
			TokenExpiration        time.Duration `def:"24h"`
			RefreshTokenExpiration time.Duration `def:"168h"`
		}
		AWS struct {
			Region string `def:"eu-central-1"`
		}
		Start bee.Time `def:"2002-10-02T10:00:00-05:00"`
	}

	cfg := config{} //nolint:exhaustruct

	args := os.Args
	defer func() {
		os.Args = args
	}()
	os.Args = []string{"cool"}

	app := bee.New(
		"cool",
		&cfg,
		bee.WithErrorHandling(flag.ContinueOnError),
		bee.WithOutput(io.Discard),
	)
	app.Root("Run app", func(ctx *bee.Ctx[config]) error {
		return nil
	})
	_ = app.RunE()

	// Output:
}

func Example_commandTree() {
	type config struct {
		HTTP struct {
			Addr string `def:":8080"`
		}
	}

	args := os.Args
	defer func() {
		os.Args = args
	}()
	os.Args = []string{"maia", "start", "api"}

	cfg := config{} //nolint:exhaustruct
	app := bee.New(
		"maia",
		&cfg,
		bee.WithErrorHandling(flag.ContinueOnError),
		bee.WithOutput(io.Discard),
		bee.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	start := app.Cmd("start", "Start services")
	start.Cmd("api", "Run HTTP API", func(ctx *bee.Ctx[config]) error {
		ctx.Log.Info("api starting", "addr", ctx.Cfg.HTTP.Addr)

		return nil
	})
	start.Cmd("worker", "Run worker", func(ctx *bee.Ctx[config]) error {
		return nil
	})

	app.Cmd("migrate", "Run migrations").
		Cmd("up", "Apply migrations", func(ctx *bee.Ctx[config]) error {
			return nil
		})

	_ = app.RunE(os.Args[1:]...)

	// Output:
}
