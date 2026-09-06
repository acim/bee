package bee

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
)

// Input is a cancellation-aware reader owned by a command. Close releases its
// descriptor without closing the original file. Do not read, close, or change
// the original file's flags while Input is open; duplicates share offsets and
// may share file status flags. Always Close Input, including after cancellation.
// Pipes and terminals unblock on cancellation. Regular files are also accepted,
// but cancellation cannot interrupt an operating-system disk read in progress.
type Input struct {
	ctx      context.Context
	file     *os.File
	restore  func() error
	stop     func() bool
	once     sync.Once
	closeErr error
}

// Input opens a reader tied to the command context, leaving file open. Reads
// interrupted by cancellation return the context error. It requires a regular
// file or a descriptor supported by the platform's runtime poller.
func (c Ctx[T]) Input(file *os.File) (*Input, error) {
	if c.Ctx == nil {
		return nil, errors.New("bee: input context is nil")
	}
	if file == nil {
		return nil, errors.New("bee: input file is nil")
	}
	if err := c.Ctx.Err(); err != nil {
		return nil, err
	}
	owned, restore, err := duplicateInput(file)
	if err != nil {
		return nil, fmt.Errorf("bee: open input: %w", err)
	}
	input := &Input{ctx: c.Ctx, file: owned, restore: restore}
	input.stop = context.AfterFunc(c.Ctx, func() { input.closeFile() })
	return input, nil
}

// Read reads from the file, returning the context error when cancelled.
func (in *Input) Read(p []byte) (int, error) {
	if err := in.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := in.file.Read(p)
	if err != nil && in.ctx.Err() != nil {
		return n, in.ctx.Err()
	}
	return n, err
}

// Close unblocks pending reads and releases Input's resources. It is idempotent.
func (in *Input) Close() error {
	in.stop()
	in.closeFile()
	return in.closeErr
}

func (in *Input) closeFile() {
	in.once.Do(func() { in.closeErr = errors.Join(in.file.Close(), in.restore()) })
}
