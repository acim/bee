//go:build darwin || linux

package bee

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
)

func TestInputTerminalHelper(t *testing.T) {
	if os.Getenv("BEE_TERMINAL_HELPER") != "1" {
		return
	}
	app := New("terminal", &struct{}{})
	app.Root("read", func(ctx *Ctx[struct{}]) error {
		input, err := ctx.Input(os.Stdin)
		if err != nil {
			return err
		}
		defer func() {
			if err := input.Close(); err != nil {
				t.Error(err)
			}
		}()
		fmt.Println("ready")
		_, err = io.Copy(io.Discard, input)
		if errors.Is(err, ctx.Ctx.Err()) {
			return nil
		}
		return err
	})
	if err := app.RunE(); err != nil {
		t.Fatal(err)
	}
}

func TestInputTerminalSignals(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("terminal integration test requires Python 3's standard pty module")
	}
	cmd := exec.Command(python, "-c", `
import os, pty, select, signal, subprocess, sys
for sig in (signal.SIGINT, signal.SIGTERM):
    master, slave = pty.openpty()
    child = subprocess.Popen([sys.argv[1], "-test.run=^TestInputTerminalHelper$"],
        stdin=slave, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        env={"BEE_TERMINAL_HELPER": "1"})
    os.close(slave)
    try:
        assert select.select([child.stdout], [], [], 5)[0], "terminal startup timed out"
        line = child.stdout.readline()
        assert line == b"ready\n", (line, child.communicate(timeout=3))
        child.send_signal(sig)
        out, err = child.communicate(timeout=3)
        assert child.returncode == 0, (sig, out, err)
    finally:
        if child.poll() is None:
            child.kill()
            child.wait()
        os.close(master)
`, os.Args[0])
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("terminal signals: %v\n%s", err, output)
	}
}
