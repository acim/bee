//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package bee

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestInputCancellationUnblocksPipeAndPreservesOriginal(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	}()
	defer func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := Ctx[struct{}]{Ctx: ctx}
	input, err := runtime.Input(reader)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := input.Close(); err != nil {
			t.Error(err)
		}
	}()
	done := make(chan error, 1)
	go func() { var b [1]byte; _, err := input.Read(b[:]); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("read = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("read did not unblock")
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := reader.Read(b[:]); err != nil || b[0] != 'x' {
		t.Fatalf("original read = %q, %v", b, err)
	}
}

func TestInputCloseUnblocksRead(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	}()
	defer func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	}()
	input, err := (Ctx[struct{}]{Ctx: context.Background()}).Input(reader)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { var b [1]byte; _, err := input.Read(b[:]); done <- err }()
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("read = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not unblock read")
	}
}

func TestInputReadsRedirectedFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := file.WriteString("hello\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	input, err := (Ctx[struct{}]{Ctx: context.Background()}).Input(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := input.Close(); err != nil {
			t.Error(err)
		}
	}()
	data, err := io.ReadAll(input)
	if err != nil || string(data) != "hello\n" {
		t.Fatalf("read = %q, %v", data, err)
	}
}

func TestInputRejectsInvalidArguments(t *testing.T) {
	if _, err := (Ctx[struct{}]{}).Input(os.Stdin); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := (Ctx[struct{}]{Ctx: context.Background()}).Input(nil); err == nil {
		t.Fatal("nil file accepted")
	}
}

func TestInputAcceptsDevNull(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	input, err := (Ctx[struct{}]{Ctx: context.Background()}).Input(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	var b [1]byte
	if n, err := input.Read(b[:]); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("null read = %d, %v", n, err)
	}
}

func TestInputRejectsCancelledContextAndClosedFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Ctx[struct{}]{Ctx: ctx}).Input(os.Stdin); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled input = %v", err)
	}
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := (Ctx[struct{}]{Ctx: context.Background()}).Input(file); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed input = %v", err)
	}
}

func TestInputPipeDataAndEOF(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	input, err := (Ctx[struct{}]{Ctx: context.Background()}).Input(reader)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	if _, err := writer.WriteString("hello\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(input)
	if err != nil || string(data) != "hello\n" {
		t.Fatalf("pipe read = %q, %v", data, err)
	}
}
