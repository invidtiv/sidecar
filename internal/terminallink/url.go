package terminallink

import (
	"os/exec"
	"runtime"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
)

// SafeHTTPURL trims trailing prose punctuation and accepts only http(s) URLs
// with a host and no control characters.
func SafeHTTPURL(raw string) (string, bool) {
	return contentlink.SafeHTTPURL(raw)
}

// OpenHTTP opens a validated http(s) URL in the system browser.
func OpenHTTP(raw string) tea.Cmd {
	safeURL, ok := SafeHTTPURL(raw)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", safeURL)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", safeURL)
		case "linux":
			cmd = exec.Command("xdg-open", safeURL)
		default:
			return nil
		}
		_ = cmd.Start()
		return nil
	}
}
