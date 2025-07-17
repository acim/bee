package bee_test

import (
	"flag"
	"log"
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

	cfg := &config{} //nolint:exhaustruct

	svc := bee.NewService("mycmd")

	if err := svc.ParseCommandLine(cfg, bee.WithErrorHandling(flag.ContinueOnError)); err != nil {
		log.Println(err)
	}

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
		Start bee.Time `def:"2002-10-02T10:00:00-05:00"`
	}

	cfg := &config{} //nolint:exhaustruct

	svc := bee.NewService("cool")

	if err := svc.ParseCommandLine(cfg, bee.WithErrorHandling(flag.ContinueOnError)); err != nil {
		log.Println(err)
	}

	// Output:
}
