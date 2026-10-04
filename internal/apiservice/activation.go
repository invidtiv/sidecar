package apiservice

import (
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// ActivationRequired marks jobs that must never fall back to binding the
// browser port themselves. A broken supervisor configuration fails closed.
const ActivationRequired = "SIDECAR_API_SOCKET_ACTIVATION"

// ActivatedListener is one descriptor received from the service manager.
// Empty names (sd_listen_fds) and sidecar-api names are classified by address.
type ActivatedListener struct {
	Name     string
	Listener net.Listener
}

func CloseActivated(listeners []ActivatedListener) {
	for _, listener := range listeners {
		_ = listener.Listener.Close()
	}
}

// Activate consumes manager descriptors and clears the systemd environment so
// children cannot mistake them for their own. The manager retains its copies.
// The systemd protocol is also available on macOS for isolated ExtraFiles proofs.
func Activate() ([]ActivatedListener, error) {
	if os.Getenv("LISTEN_PID") != "" {
		return systemdListeners()
	}
	return launchdListeners()
}

func systemdListeners() (listeners []ActivatedListener, err error) {
	pidfdID := os.Getenv("LISTEN_PIDFDID")
	pidText, countText, namesText := os.Getenv("LISTEN_PID"), os.Getenv("LISTEN_FDS"), os.Getenv("LISTEN_FDNAMES")
	for _, name := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES", "LISTEN_PIDFDID"} {
		_ = os.Unsetenv(name)
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return nil, errors.New("socket activation: invalid LISTEN_PID")
	}
	if pid != os.Getpid() {
		return nil, nil
	} // sd_listen_fds ignores another process's fds.
	if pidfdID != "" {
		matches, checkErr := matchesPIDFD(pidfdID)
		if checkErr != nil {
			return nil, checkErr
		}
		if !matches {
			return nil, nil
		}
	}
	count, err := strconv.Atoi(countText)
	if err != nil || count < 0 || count > 16 {
		return nil, errors.New("socket activation: invalid LISTEN_FDS (expected 0..16)")
	}
	names := strings.Split(namesText, ":")
	// Own all advertised fds even when metadata is bad; don't leak them on error.
	files := make([]*os.File, count)
	for i := range files {
		fd := 3 + i
		unix.CloseOnExec(fd)
		files[i] = os.NewFile(uintptr(fd), "activated-listener")
	}
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
		if err != nil {
			CloseActivated(listeners)
			listeners = nil
		}
	}()
	if namesText != "" && len(names) != count {
		return nil, errors.New("socket activation: LISTEN_FDNAMES does not match LISTEN_FDS")
	}
	for i, file := range files {
		listener, convertErr := listenerFromFile(file)
		if convertErr != nil {
			return listeners, convertErr
		}
		name := ""
		if namesText != "" {
			name = names[i]
		}
		listeners = append(listeners, ActivatedListener{Name: name, Listener: listener})
	}
	return listeners, nil
}

func listenerFromFile(file *os.File) (net.Listener, error) {
	accepting, err := unix.GetsockoptInt(int(file.Fd()), unix.SOL_SOCKET, unix.SO_ACCEPTCONN)
	if runtime.GOOS == "darwin" && errors.Is(err, unix.ENOPROTOOPT) {
		// Darwin defines SO_ACCEPTCONN but does not implement getsockopt for it.
		// Require an unconnected stream; launchd's SockPassive creates the listener.
		socketType, typeErr := unix.GetsockoptInt(int(file.Fd()), unix.SOL_SOCKET, unix.SO_TYPE)
		_, peerErr := unix.Getpeername(int(file.Fd()))
		if typeErr == nil && socketType == unix.SOCK_STREAM && errors.Is(peerErr, unix.ENOTCONN) {
			accepting, err = 1, nil
		}
	}
	if err != nil || accepting != 1 {
		return nil, fmt.Errorf("socket activation: fd %d is not a listening stream socket (%v)", file.Fd(), err)
	}
	listener, err := net.FileListener(file) // duplicates with CLOEXEC; file can close.
	if err != nil {
		return nil, fmt.Errorf("socket activation: fd %d: %w", file.Fd(), err)
	}
	if local, ok := listener.(*net.UnixListener); ok {
		local.SetUnlinkOnClose(false)
	}
	return listener, nil
}
