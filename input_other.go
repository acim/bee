//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package bee

import (
	"errors"
	"os"
)

func duplicateInput(*os.File) (*os.File, func() error, error) {
	return nil, nil, errors.ErrUnsupported
}
