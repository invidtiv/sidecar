//go:build darwin || linux

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mobileproto"
)

// Measure the actual owner process, rather than counting timers or requests:
// any idle hot loop (scanner, encoder, or an adapter) consumes the same budget.
func TestMobileOwnerIdleCPU(t *testing.T) {
	if os.Getenv("SIDECAR_TEST_OWNER_CPU") == "1" {
		config.SetConfigPath(os.Getenv("SIDECAR_TEST_OWNER_CONFIG"))
		measurement := os.NewFile(3, "owner-cpu")
		go func() {
			time.Sleep(250 * time.Millisecond) // let the completed hello settle
			cpu := func() time.Duration {
				var usage syscall.Rusage
				if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
					panic(err)
				}
				return time.Duration(usage.Utime.Sec+usage.Stime.Sec)*time.Second + time.Duration(usage.Utime.Usec+usage.Stime.Usec)*time.Microsecond
			}
			before := cpu()
			time.Sleep(2 * time.Second)
			_, _ = fmt.Fprintln(measurement, int64(cpu()-before))
			_ = measurement.Close()
		}()
		os.Exit(runMobileServe(Env{Ctx: context.Background(), Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, StateDir: config.StateDir()}, []string{"--stdio", "--owner-only"}))
	}
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	defer func() { _ = write.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMobileOwnerIdleCPU$")
	cmd.Env = append(os.Environ(), "SIDECAR_TEST_OWNER_CPU=1", "SIDECAR_TEST_OWNER_CONFIG="+configPath,
		"SIDECAR_ISOLATED_STATE=1", "XDG_STATE_HOME="+filepath.Join(root, "state"), "TMUX_TMPDIR="+filepath.Join(root, "tmux"), "TMUX=", "TMUX_PANE=")
	cmd.ExtraFiles = []*os.File{write}
	cmd.Stderr = os.Stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	_ = write.Close()
	_, _ = fmt.Fprintln(input, `{"version":0,"type":"hello","request_id":"hub-owner-hello"}`)
	var hello mobileproto.Response
	if err := json.NewDecoder(output).Decode(&hello); err != nil || hello.Type != mobileproto.ResponseHello {
		t.Fatalf("owner hello = %+v, %v", hello, err)
	}
	line, err := bufio.NewReader(read).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	ns, err := strconv.ParseInt(line[:len(line)-1], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	used := time.Duration(ns)
	t.Logf("idle owner CPU = %s / 2s", used)
	// 100ms is 5% of a core; the reported regression burns about 480ms.
	if used > 100*time.Millisecond {
		t.Fatalf("idle owner consumed %s CPU during a fixed 2s window (maximum 100ms)", used)
	}
	_ = input.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("owner did not exit cleanly after stdin EOF: %v", err)
	}
}
