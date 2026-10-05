package uiapi

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/marcus/sidecar/internal/apiservice"
)

func acquireListener(inherited net.Listener, network, address string) (net.Listener, error) {
	if inherited != nil {
		return inherited, nil
	}
	if network == "unix" {
		return listenPrivateUnix(address)
	}
	return net.Listen(network, address)
}

// Classify by exact address before associating a trust surface. A manager name
// is not sufficient authority to turn an arbitrary socket into a Local socket.
func validateActivated(opts Options, activated []apiservice.ActivatedListener) (map[Listener]net.Listener, error) {
	inherited := map[Listener]net.Listener{}
	for _, item := range activated {
		kind := Listener("")
		switch address := item.Listener.Addr().(type) {
		case *net.TCPAddr:
			if !address.IP.Equal(net.ParseIP("127.0.0.1")) {
				return nil, fmt.Errorf("ui api: activated listener must bind 127.0.0.1, got %s", address)
			}
			if opts.Port == address.Port || (opts.Port == 0 && (opts.Tailnet == nil || opts.Tailnet.Port != address.Port)) {
				kind = ListenerBrowser
			}
			if opts.Tailnet != nil && opts.Tailnet.Port > 0 && opts.Tailnet.Port == address.Port {
				if kind != "" {
					return nil, fmt.Errorf("ui api: activated TCP listener %s has ambiguous trust", address)
				}
				kind = ListenerTailnet
			}
		case *net.UnixAddr:
			localPath := filepath.Join(Dir(opts.StateDir), localSocketName)
			tailnetPath := filepath.Join(Dir(opts.StateDir), tailnetSockName)
			tailnetSocket := opts.Tailnet != nil && opts.Tailnet.Port == 0
			path := address.Name
			switch {
			case path == localPath:
				kind = ListenerLocal
			case path == tailnetPath && tailnetSocket:
				kind = ListenerTailnet
			case item.Name == string(ListenerLocal) || (item.Name == string(ListenerTailnet) && tailnetSocket):
				// launchd on recent macOS binds the socket under its own
				// private directory and moves it to SockPathName, so the
				// descriptor reports the bind-time path. The name alone is not
				// authority: the expected path must route to this listener.
				path = localPath
				if item.Name == string(ListenerTailnet) {
					path = tailnetPath
				}
				if !unixPathReaches(item.Listener, path, activationProbeTimeout) {
					return nil, fmt.Errorf("ui api: activated %s listener %s is not the socket at %s", item.Name, address, path)
				}
				kind = Listener(item.Name)
			}
			if kind != "" {
				info, err := os.Lstat(path)
				if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
					return nil, fmt.Errorf("ui api: activated Unix socket %s must exist with mode 0600", path)
				}
			}
		}
		if kind == "" {
			return nil, fmt.Errorf("ui api: unexpected activated listener %s", item.Listener.Addr())
		}
		if item.Name != "" && item.Name != "sidecar-api" && item.Name != "sidecar-api.socket" && item.Name != string(kind) {
			return nil, fmt.Errorf("ui api: activated listener name %q disagrees with %s address", item.Name, kind)
		}
		if inherited[kind] != nil {
			return nil, fmt.Errorf("ui api: duplicate activated %s listener", kind)
		}
		inherited[kind] = item.Listener
	}
	return inherited, nil
}

const (
	activationProbeTimeout    = 2 * time.Second
	activationProbeClientWait = 250 * time.Millisecond
)

// unixPathReaches proves that dialing path arrives at listener by sending a
// random token through it. Connections that arrive first without the token
// (an early client during startup) are closed; that client retries.
func unixPathReaches(listener net.Listener, path string, timeout time.Duration) bool {
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		return false
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return false
	}
	deadline := time.Now().Add(timeout)
	if err := unixListener.SetDeadline(deadline); err != nil {
		return false
	}
	defer func() { _ = unixListener.SetDeadline(time.Time{}) }()
	go func() {
		conn, err := net.DialTimeout("unix", path, timeout)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(deadline)
		_, _ = conn.Write(token)
		_, _ = conn.Read(make([]byte, 1))
	}()
	for {
		conn, err := unixListener.Accept()
		if err != nil {
			return false
		}
		// The probe writes its token at once. Anything slower is an early
		// client, which must not spend the probe's whole budget.
		readBy := time.Now().Add(activationProbeClientWait)
		if readBy.After(deadline) {
			readBy = deadline
		}
		_ = conn.SetReadDeadline(readBy)
		got := make([]byte, len(token))
		_, readErr := io.ReadFull(conn, got)
		_ = conn.Close()
		if readErr == nil && bytes.Equal(got, token) {
			return true
		}
	}
}
