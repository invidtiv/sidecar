package terminallink

import (
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// SafeHTTPURL trims trailing prose punctuation and accepts only http(s) URLs
// with a host and no control characters.
func SafeHTTPURL(raw string) (string, bool) {
	raw = strings.TrimRight(raw, ".,;!?) ]}")
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", false
	}
	return raw, true
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
