package resource

import (
	"testing"

	"github.com/marcus/sidecar/internal/terminallink"
)

// internal/terminallink restates two of the protocol's scanner bounds because
// this package depends on it for OSC stripping, so it cannot depend back. This
// is the guard against those copies drifting.
func TestScannerBoundsMatchTheProtocol(t *testing.T) {
	if terminallink.MaxResourceLocatorChars != MaxLocatorChars {
		t.Errorf("locator bound drifted: terminallink has %d, protocol has %d",
			terminallink.MaxResourceLocatorChars, MaxLocatorChars)
	}
	if terminallink.MaxResourceMatchesPerLine != MaxMatchesPerLine {
		t.Errorf("matches-per-line bound drifted: terminallink has %d, protocol has %d",
			terminallink.MaxResourceMatchesPerLine, MaxMatchesPerLine)
	}
}
