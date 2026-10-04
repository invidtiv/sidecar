package uiapi

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

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
			switch address.Name {
			case filepath.Join(Dir(opts.StateDir), localSocketName):
				kind = ListenerLocal
			case filepath.Join(Dir(opts.StateDir), tailnetSockName):
				if opts.Tailnet != nil && opts.Tailnet.Port == 0 {
					kind = ListenerTailnet
				}
			}
			if kind != "" {
				info, err := os.Lstat(address.Name)
				if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
					return nil, fmt.Errorf("ui api: activated Unix socket %s must exist with mode 0600", address.Name)
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
