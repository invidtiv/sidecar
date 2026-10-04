package apiservice

import (
	"net"
	"syscall"
	"testing"
)

func TestLaunchdActivationAdapter(t *testing.T) {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	for _, scenario := range []string{"foreground", "missing", "valid", "extra", "already"} {
		t.Run(scenario, func(t *testing.T) {
			activated, err := collectLaunchd(func(name string) ([]int, error) {
				if scenario == "foreground" {
					return nil, syscall.ESRCH
				}
				if name != "browser" || scenario == "missing" {
					return nil, syscall.ENOENT
				}
				if scenario == "already" {
					return nil, syscall.EALREADY
				}
				file, err := listener.File()
				if err != nil {
					return nil, err
				}
				// collectLaunchd owns and closes these descriptors.
				fd, err := syscall.Dup(int(file.Fd()))
				_ = file.Close()
				if err != nil {
					return nil, err
				}
				if scenario == "extra" {
					second, err := syscall.Dup(fd)
					if err != nil {
						_ = syscall.Close(fd)
						return nil, err
					}
					return []int{fd, second}, nil
				}
				return []int{fd}, nil
			})
			defer CloseActivated(activated)
			if scenario == "extra" || scenario == "already" {
				if err == nil {
					t.Fatal("accepted bad activation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "valid" && (len(activated) != 1 || activated[0].Name != "browser") {
				t.Fatal(activated)
			}
			if scenario != "valid" && len(activated) != 0 {
				t.Fatal(activated)
			}
		})
	}
}

// Exercises real libSystem symbol lookup and ABI without loading any job.
func TestLaunchdActivationUnsupervised(t *testing.T) {
	listeners, err := launchdListeners()
	defer CloseActivated(listeners)
	if err != nil || len(listeners) != 0 {
		t.Fatalf("unsupervised: %v %v", listeners, err)
	}
}
