//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package bee

import (
	"errors"
	"os"
	"syscall"
	"time"
)

func duplicateInput(file *os.File) (*os.File, func() error, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	// /dev/null is a useful CLI input and always returns EOF without waiting.
	nullInfo, nullErr := os.Stat(os.DevNull)
	synchronous := info.Mode().IsRegular() || (nullErr == nil && os.SameFile(info, nullInfo))
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	var descriptor int
	var originalNonblock bool
	var operationErr error
	err = raw.Control(func(fd uintptr) {
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFL, 0)
		if errno != 0 {
			operationErr = errno
			return
		}
		originalNonblock = flags&syscall.O_NONBLOCK != 0
		syscall.ForkLock.RLock()
		descriptor, operationErr = syscall.Dup(int(fd))
		if operationErr == nil {
			syscall.CloseOnExec(descriptor)
		}
		syscall.ForkLock.RUnlock()
	})
	if err = errors.Join(err, operationErr); err != nil {
		return nil, nil, err
	}
	restore := func() error {
		var restoreErr error
		err := raw.Control(func(fd uintptr) { restoreErr = syscall.SetNonblock(int(fd), originalNonblock) })
		return errors.Join(err, restoreErr)
	}
	if !synchronous {
		if err := syscall.SetNonblock(descriptor, true); err != nil {
			return nil, nil, errors.Join(err, syscall.Close(descriptor))
		}
	}
	owned := os.NewFile(uintptr(descriptor), file.Name())
	if !synchronous {
		// NewFile registers an already nonblocking descriptor with the runtime.
		// Verify support rather than returning a reader whose Close could hang.
		if err := owned.SetReadDeadline(time.Time{}); err != nil {
			return nil, nil, errors.Join(err, owned.Close(), restore())
		}
	}
	return owned, restore, nil
}
