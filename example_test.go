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
		cfg,
		bee.WithErrorHandling(flag.ContinueOnError),
		bee.WithOutput(io.Discard),
	)
	app.Root("Run app", func(ctx bee.Context[config]) error {
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
		cfg,
		bee.WithErrorHandling(flag.ContinueOnError),
		bee.WithOutput(io.Discard),
	)
	app.Root("Run app", func(ctx bee.Context[config]) error {
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

	app := bee.New(
		"maia",
		config{}, //nolint:exhaustruct
		bee.WithErrorHandling(flag.ContinueOnError),
		bee.WithOutput(io.Discard),
		bee.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	start := app.Command("start", "Start services")
	start.Command("api", "Run HTTP API", func(ctx bee.Context[config]) error {
		ctx.Log.Info("api starting", "addr", ctx.Config.HTTP.Addr)

		return nil
	})
	start.Command("worker", "Run worker", func(ctx bee.Context[config]) error {
		return nil
	})

	app.Command("migrate", "Run migrations").
		Command("up", "Apply migrations", func(ctx bee.Context[config]) error {
			return nil
		})

	_ = app.RunE(os.Args[1:]...)

	// Output:
}
