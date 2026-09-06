//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package bee

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestInputUnsupportedPlatform(t *testing.T) {
	_, err := (Ctx[struct{}]{Ctx: context.Background()}).Input(os.Stdin)
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Input error = %v", err)
	}
}
