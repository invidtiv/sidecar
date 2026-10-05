package apiservice

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestActivationProtocolChild(t *testing.T) {
	scenario := os.Getenv("SC_FD_CHILD")
	if scenario == "" {
		return
	}
	_ = os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	switch scenario {
	case "wrong-pid":
		_ = os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()+1))
	case "bad-pid":
		_ = os.Setenv("LISTEN_PID", "garbage")
	case "bad-pidfd":
		_ = os.Setenv("LISTEN_PIDFDID", "garbage")
	case "bad-count":
		_ = os.Setenv("LISTEN_FDS", "-1")
	case "too-many":
		_ = os.Setenv("LISTEN_FDS", "10000000")
	case "bad-names":
		_ = os.Setenv("LISTEN_FDNAMES", "browser:extra")
	}
	listeners, err := Activate()
	switch scenario {
	case "wrong-pid":
		if err != nil || len(listeners) != 0 {
			t.Fatalf("pid mismatch consumed fds: %v %v", listeners, err)
		}
	case "valid":
		if err != nil || len(listeners) != 1 {
			t.Fatalf("valid fds: %v %v", listeners, err)
		}
	default:
		if err == nil {
			t.Fatal("malformed activation accepted")
		}
	}
	CloseActivated(listeners)
	for _, name := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES", "LISTEN_PIDFDID"} {
		if os.Getenv(name) != "" {
			t.Fatalf("leaked %s", name)
		}
	}
	if scenario == "valid" || scenario == "bad-names" || scenario == "regular-file" {
		file := os.NewFile(3, "closed-original")
		if _, err := file.Stat(); err == nil {
			_ = file.Close()
			t.Fatal("original fd 3 leaked")
		}
	}
}

func TestActivationProtocol(t *testing.T) {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	file, err := listener.File()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	regular, err := os.CreateTemp(t.TempDir(), "regular")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = regular.Close() }()
	for _, scenario := range []string{"valid", "wrong-pid", "bad-pid", "bad-count", "bad-pidfd", "too-many", "bad-names", "regular-file"} {
		t.Run(scenario, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestActivationProtocolChild$", "-test.timeout=5s")
			cmd.Env = append(os.Environ(), "SC_FD_CHILD="+scenario, "LISTEN_FDS=1", "LISTEN_FDNAMES=browser")
			cmd.ExtraFiles = []*os.File{file}
			if scenario == "regular-file" {
				cmd.ExtraFiles = []*os.File{regular}
			}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", scenario, err, output)
			}
		})
	}
}

func TestActivationRejectsUnsupportedSockets(t *testing.T) {
	for _, scenario := range []string{"ipv6-mapped", "seqpacket"} {
		t.Run(scenario, func(t *testing.T) {
			family, socketType := unix.AF_INET6, unix.SOCK_STREAM
			if scenario == "seqpacket" {
				family, socketType = unix.AF_UNIX, unix.SOCK_SEQPACKET
			}
			fd, err := unix.Socket(family, socketType, 0)
			if err != nil {
				if errors.Is(err, unix.EPROTONOSUPPORT) || errors.Is(err, unix.EAFNOSUPPORT) {
					t.Skipf("socket unavailable: %v", err)
				}
				t.Fatal(err)
			}
			file := os.NewFile(uintptr(fd), scenario)
			defer func() { _ = file.Close() }()
			if scenario == "ipv6-mapped" {
				if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, 0); err != nil {
					t.Fatal(err)
				}
				address := &unix.SockaddrInet6{}
				copy(address.Addr[:], net.ParseIP("::ffff:127.0.0.1").To16())
				err = unix.Bind(fd, address)
			} else {
				root, mkdirErr := os.MkdirTemp("/tmp", "sc-seq-*")
				if mkdirErr != nil {
					t.Fatal(mkdirErr)
				}
				defer func() { _ = os.RemoveAll(root) }()
				err = unix.Bind(fd, &unix.SockaddrUnix{Name: root + "/socket"})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := unix.Listen(fd, 1); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestActivationProtocolChild$", "-test.timeout=5s")
			cmd.Env = append(os.Environ(), "SC_FD_CHILD="+scenario, "LISTEN_FDS=1", "LISTEN_FDNAMES=browser")
			cmd.ExtraFiles = []*os.File{file}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", scenario, err, output)
			}
		})
	}
}

func TestSocketUnitsLifecycleAndDefinition(t *testing.T) {
	manager, runner := testManager(t, "linux")
	if err := manager.Install(t.Context()); err != nil {
		t.Fatal(err)
	}
	service, _ := os.ReadFile(manager.file)
	socket, _ := os.ReadFile(manager.socketFile)
	for _, part := range []string{"Requires=sidecar-api.socket", "After=sidecar-api.socket", "Sockets=sidecar-api.socket", ActivationRequired + "=1"} {
		if !strings.Contains(string(service), part) {
			t.Fatalf("missing %s\n%s", part, service)
		}
	}
	for _, part := range []string{"ListenStream=127.0.0.1:7861", "SocketMode=0600", "DirectoryMode=0700", "Accept=no", "Service=sidecar-api.service", "FileDescriptorName=sidecar-api"} {
		if !strings.Contains(string(socket), part) {
			t.Fatalf("missing %s\n%s", part, socket)
		}
	}
	info, err := os.Stat(manager.socketFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket unit mode: %v %v", info, err)
	}
	if !strings.Contains(strings.Join(runner.calls, "\n"), "enable --now "+SocketUnit+" "+Unit) {
		t.Fatal(runner.calls)
	}
	if err := manager.Uninstall(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manager.socketFile); !os.IsNotExist(err) {
		t.Fatalf("socket unit survived uninstall: %v", err)
	}
	// ListenStream is an entire address (not ExecStart argv); quoting it makes
	// systemd reject it. Only % specifiers expand here; spaces and $ stay literal.
	manager.options.StateDir = `/tmp/with spaces/%/$name/"quoted"`
	want := fmt.Sprintf("ListenStream=%s/api/api.sock", strings.ReplaceAll(manager.options.StateDir, "%", "%%"))
	if !strings.Contains(string(manager.socketDefinition()), want) {
		t.Fatal(string(manager.socketDefinition()))
	}
}

func TestLaunchdSocketDefinition(t *testing.T) {
	manager, _ := testManager(t, "darwin")
	definition := string(manager.definition())
	for _, part := range []string{"<key>Sockets</key>", "<key>browser</key>", "<key>local</key>", "<key>SockNodeName</key><string>127.0.0.1</string>", "<key>SockFamily</key><string>IPv4</string>", "<key>SockPathMode</key><integer>384</integer>", "<key>" + ActivationRequired + "</key><string>1</string>"} {
		if !strings.Contains(definition, part) {
			t.Fatalf("missing %s: %s", part, definition)
		}
	}
}

func TestSocketStatusAndLegacyMigration(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			manager, _ := testManager(t, platform)
			legacy := true
			stopped := false
			calls := []string{}
			manager.options.Run = func(_ context.Context, command string, args ...string) ([]byte, error) {
				call := command + " " + strings.Join(args, " ")
				calls = append(calls, call)
				if command == "launchctl" && args[0] == "print" {
					text := "\tstate = running\n\tpid = 42\n"
					if stopped {
						text = "\tstate = waiting\n"
					}
					if !legacy {
						text += "\tsockets = {\n\t\t\"browser\" = {\n\t\t\tstate = active\n\t\t}\n\t}\n"
					}
					return []byte(text), nil
				}
				if strings.Contains(call, "show "+Unit) {
					text := "LoadState=loaded\nActiveState=active\nMainPID=42\n"
					if stopped {
						text = "LoadState=loaded\nActiveState=inactive\nMainPID=0\n"
					}
					return []byte(text), nil
				}
				if strings.Contains(call, "show "+SocketUnit) {
					if legacy {
						return []byte("LoadState=not-found\n"), errors.New("exit 1")
					}
					return []byte("LoadState=loaded\nActiveState=active\nSubState=listening\n"), nil
				}
				if legacy && strings.Contains(call, "disable --now "+SocketUnit) {
					return []byte("Unit not found"), errors.New("exit 1")
				}
				return nil, nil
			}
			// A legacy running job has no socket definition. Migration must not try
			// to disable a nonexistent unit (systemctl would fail before install).
			status, err := manager.Status(t.Context())
			if err != nil || !status.Running || status.Socket.Listening || !strings.Contains(status.Message, "without a manager-held") {
				t.Fatalf("legacy: %+v %v", status, err)
			}
			if err := manager.Install(t.Context()); err != nil {
				t.Fatal(err)
			}
			if platform == "linux" && !strings.Contains(strings.Join(calls, "\n"), "disable --now "+Unit) {
				t.Fatal(calls)
			}
			legacy = false
			stopped = true
			status, err = manager.Status(t.Context())
			if err != nil || status.Running || !status.Socket.Installed || !status.Socket.Loaded || !status.Socket.Listening {
				t.Fatalf("restart gap: %+v %v", status, err)
			}
		})
	}
}
