package agentcontrol

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/marcus/sidecar/internal/testenv"
)

func TestMain(m *testing.M) {
	_, teardown, err := testenv.IsolateTmux()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stop := testenv.OnSignal(teardown)

	// Keep one session alive for the package lifetime. Individual integration
	// tests create and kill their own sessions; without this keeper, killing the
	// last one lets tmux begin shutting down while the next test is starting a
	// session on the same private socket. tmux then intermittently reports
	// "server exited unexpectedly". The package-level teardown kills this
	// private server by its explicit socket path.
	if _, lookErr := exec.LookPath("tmux"); lookErr == nil {
		if out, startErr := exec.Command("tmux", "-f", "/dev/null", "new-session", "-d", "-s", "__sidecar_agentcontrol_keeper").CombinedOutput(); startErr != nil {
			fmt.Fprintf(os.Stderr, "start agentcontrol test tmux server: %v: %s\n", startErr, out)
			stop()
			teardown()
			os.Exit(1)
		}
	}

	code := m.Run()
	stop()
	teardown()
	os.Exit(code)
}
