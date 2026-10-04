// uiapiserviceproof is a fake socket supervisor, never launchd or systemd.
// It execs real Sidecar children with ExtraFiles while retaining parent copies.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/marcus/sidecar/internal/apiservice"
	"github.com/marcus/sidecar/internal/uiapi"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--exec" {
		// systemd supplies the destination PID; exec preserves this helper's PID.
		for key, value := range map[string]string{"LISTEN_PID": strconv.Itoa(os.Getpid()), "LISTEN_FDS": "2", "LISTEN_FDNAMES": "browser:local", apiservice.ActivationRequired: "1"} {
			if err := os.Setenv(key, value); err != nil {
				panic(err)
			}
		}
		binary := os.Args[2]
		if err := syscall.Exec(binary, append([]string{binary}, os.Args[3:]...), os.Environ()); err != nil {
			panic(err)
		}
	}
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: uiapiserviceproof ROOT CONFIG FIRST SECOND")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2], os.Args[3], os.Args[4]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type child struct {
	command *exec.Cmd
	output  io.ReadCloser
	errors  *os.File
	exited  chan error
}

func run(root, config, first, second string) error {
	if os.Getenv("SIDECAR_ISOLATED_STATE") != "1" || !strings.HasPrefix(root, "/private/tmp/") && !strings.HasPrefix(root, "/tmp/") {
		return errors.New("proof requires isolated state and a /tmp root")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	state := filepath.Join(os.Getenv("XDG_STATE_HOME"), "sidecar")
	if err := os.MkdirAll(uiapi.Dir(state), 0o700); err != nil {
		return err
	}
	browser, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		return err
	}
	defer func() { _ = browser.Close() }()
	localPath := filepath.Join(uiapi.Dir(state), "api.sock")
	local, err := net.ListenUnix("unix", &net.UnixAddr{Name: localPath, Net: "unix"})
	if err != nil {
		return err
	}
	defer func() { _ = local.Close() }()
	if err := os.Chmod(localPath, 0o600); err != nil {
		return err
	}
	browserFile, err := browser.File()
	if err != nil {
		return err
	}
	defer func() { _ = browserFile.Close() }()
	localFile, err := local.File()
	if err != nil {
		return err
	}
	defer func() { _ = localFile.Close() }()

	address := browser.Addr().String()
	var attempts atomic.Int64
	monitorErr := make(chan error, 1)
	monitorDone := make(chan struct{})
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	defer func() { stopMonitor(); <-monitorDone }()
	go func() {
		defer close(monitorDone)
		timer := time.NewTicker(10 * time.Millisecond)
		defer timer.Stop()
		for {
			if listener, err := net.Listen("tcp", address); err == nil {
				_ = listener.Close()
				monitorErr <- errors.New("origin takeover: second listener bound the supervised port")
				return
			} else if !errors.Is(err, syscall.EADDRINUSE) {
				monitorErr <- fmt.Errorf("second bind failed for an unexpected reason: %w", err)
				return
			}
			attempts.Add(1)
			select {
			case <-monitorCtx.Done():
				return
			case <-timer.C:
			}
		}
	}()
	stable := filepath.Join(root, "bin", "activated-sidecar")
	if err := os.Symlink(first, stable); err != nil {
		return err
	}
	files := []*os.File{browserFile, localFile}
	launch := func() (*child, error) {
		log, err := os.CreateTemp(root, "activation-stderr-*")
		if err != nil {
			return nil, err
		}
		// The helper supplies LISTEN_PID, then execs the real CLI without a shell.
		command := exec.CommandContext(ctx, os.Args[0], "--exec", stable, "-config", config, "api", "serve", "--port", "0", "--json", "--fixtures", "testdata/ui-api/v0")
		command.Env = os.Environ()
		command.ExtraFiles = files
		command.Stderr = log
		stdout, err := command.StdoutPipe()
		if err != nil {
			_ = log.Close()
			return nil, err
		}
		if err := command.Start(); err != nil {
			_ = log.Close()
			return nil, err
		}
		running := &child{command: command, output: stdout, errors: log, exited: make(chan error, 1)}
		// Wait runs only after stdout is drained, as required by StdoutPipe.
		var endpoint uiapi.Endpoint
		if err := json.NewDecoder(stdout).Decode(&endpoint); err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			_ = log.Close()
			data, _ := os.ReadFile(log.Name())
			return nil, fmt.Errorf("activated start: %w: %s", err, data)
		}
		go func() { _, _ = io.Copy(io.Discard, stdout); running.exited <- command.Wait(); close(running.exited) }()
		if endpoint.TCP != address || endpoint.UnixSocket != localPath {
			_ = command.Process.Kill()
			<-running.exited
			_ = log.Close()
			return nil, fmt.Errorf("server rebound: %+v", endpoint)
		}
		return running, nil
	}
	stop := func(running *child) {
		if running != nil {
			_ = running.command.Process.Signal(syscall.SIGTERM)
			select {
			case <-running.exited:
			case <-time.After(3 * time.Second):
				_ = running.command.Process.Kill()
				<-running.exited
			}
			_ = running.errors.Close()
		}
	}
	checkUI := func() error {
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://" + address + "/")
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		if response.StatusCode != 200 || !strings.Contains(string(body), "CONFIG_UI_PROOF") {
			return errors.New("activated service did not serve configured UI")
		}
		return nil
	}
	running, err := launch()
	if err != nil {
		return err
	}
	defer func() { stop(running) }()
	if err := checkUI(); err != nil {
		return err
	}
	if err := os.Symlink(second, stable+".next"); err != nil {
		return err
	}
	if err := os.Rename(stable+".next", stable); err != nil {
		return err
	}
	select {
	case err := <-running.exited:
		if err != nil {
			return fmt.Errorf("replacement shutdown: %w", err)
		}
	case <-ctx.Done():
		return errors.New("binary replacement did not exit")
	}
	logName := running.errors.Name()
	_ = running.errors.Close()
	running = nil
	log, err := os.ReadFile(logName)
	if err != nil || !strings.Contains(string(log), "executable changed") {
		return errors.New("binary-change shutdown was not logged")
	}
	if _, err := os.Stat(uiapi.EndpointPath(state)); !errors.Is(err, os.ErrNotExist) {
		return errors.New("discovery survived shutdown")
	}
	if _, err := os.Stat(localPath); err != nil {
		return fmt.Errorf("manager Unix socket unlinked: %w", err)
	}
	// Deliberately reproduce the manager's entire RestartSec/ThrottleInterval gap.
	select {
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}
	running, err = launch()
	if err != nil {
		return err
	}
	if err := checkUI(); err != nil {
		return err
	}
	select {
	case <-time.After(2200 * time.Millisecond):
	case err := <-running.exited:
		running = nil
		return fmt.Errorf("replacement restart loop: %v", err)
	case <-ctx.Done():
		return ctx.Err()
	}
	stop(running)
	running = nil
	select {
	case err := <-monitorErr:
		return err
	default:
	}
	if attempts.Load() < 500 {
		return fmt.Errorf("insufficient second-listener attempts: %d", attempts.Load())
	}
	fmt.Printf("socket activation: PASS (%d second-listener binds refused across binary exit, 5s gap and restart at %s)\n", attempts.Load(), address)
	return nil
}
