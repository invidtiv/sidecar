package termnotify

import (
	"strings"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		want       Terminal
		wantMarker string
	}{
		{
			name:       "ghostty by TERM_PROGRAM",
			env:        map[string]string{"TERM_PROGRAM": "ghostty"},
			want:       Ghostty,
			wantMarker: "TERM_PROGRAM",
		},
		{
			// TERM is the marker that matters over SSH: ssh forwards it, and it
			// is often the only thing left after the hop.
			name:       "ghostty by forwarded TERM",
			env:        map[string]string{"TERM": "xterm-ghostty"},
			want:       Ghostty,
			wantMarker: "TERM",
		},
		{
			name:       "ghostty by resources dir",
			env:        map[string]string{"GHOSTTY_RESOURCES_DIR": "/opt/ghostty"},
			want:       Ghostty,
			wantMarker: "GHOSTTY_RESOURCES_DIR",
		},
		{
			name:       "iterm2 by TERM_PROGRAM",
			env:        map[string]string{"TERM_PROGRAM": "iTerm.app"},
			want:       ITerm2,
			wantMarker: "TERM_PROGRAM",
		},
		{
			name:       "iterm2 by its SSH-forwarded LC_TERMINAL",
			env:        map[string]string{"LC_TERMINAL": "iTerm2", "TERM": "xterm-256color"},
			want:       ITerm2,
			wantMarker: "LC_TERMINAL",
		},
		{
			name:       "iterm2 by session id",
			env:        map[string]string{"ITERM_SESSION_ID": "w0t0p0"},
			want:       ITerm2,
			wantMarker: "ITERM_SESSION_ID",
		},
		{
			name:       "wezterm by TERM_PROGRAM",
			env:        map[string]string{"TERM_PROGRAM": "WezTerm"},
			want:       WezTerm,
			wantMarker: "TERM_PROGRAM",
		},
		{
			name:       "wezterm by pane",
			env:        map[string]string{"WEZTERM_PANE": "3"},
			want:       WezTerm,
			wantMarker: "WEZTERM_PANE",
		},
		{
			name:       "kitty by forwarded TERM",
			env:        map[string]string{"TERM": "xterm-kitty"},
			want:       Kitty,
			wantMarker: "TERM",
		},
		{
			name:       "kitty by window id",
			env:        map[string]string{"KITTY_WINDOW_ID": "1"},
			want:       Kitty,
			wantMarker: "KITTY_WINDOW_ID",
		},
		{
			name:       "the explicit override wins outright",
			env:        map[string]string{"SIDECAR_TERM_PROGRAM": "WezTerm", "TERM": "xterm-kitty"},
			want:       WezTerm,
			wantMarker: overrideVar,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Detect(env(tt.env))
			if !got.OK() || got.Terminal != tt.want {
				t.Fatalf("Detect() = %+v, want %q", got, tt.want)
			}
			if got.Marker != tt.wantMarker {
				t.Errorf("Detect().Marker = %q, want %q", got.Marker, tt.wantMarker)
			}
			if got.Reason != "" {
				t.Errorf("Detect().Reason = %q, want empty on success", got.Reason)
			}
		})
	}
}

// Auto-detection has to be allowed to fail. SSH drops TERM_PROGRAM and tmux
// replaces TERM, so a plain remote shell frequently carries no marker at all —
// reporting unavailable is the honest answer, and the fixed terminal choice in
// notifications.ssh.terminal is the way out.
func TestDetectReportsUnavailableRatherThanGuessing(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		wantReason string
	}{
		{
			name:       "no markers at all",
			env:        nil,
			wantReason: "no supported terminal",
		},
		{
			name:       "a plain SSH shell",
			env:        map[string]string{"TERM": "xterm-256color", "SSH_CONNECTION": "10.0.0.1 22 10.0.0.2 22"},
			wantReason: "no supported terminal",
		},
		{
			name:       "inside tmux, which replaces TERM and claims TERM_PROGRAM",
			env:        map[string]string{"TERM": "tmux-256color", "TERM_PROGRAM": "tmux", "TMUX": "/tmp/tmux-501/default,1,0"},
			wantReason: "no supported terminal",
		},
		{
			name:       "an unsupported terminal is never approximated",
			env:        map[string]string{"TERM_PROGRAM": "Apple_Terminal", "TERM": "xterm-256color"},
			wantReason: "no supported terminal",
		},
		{
			name:       "alacritty has no encoder here",
			env:        map[string]string{"ALACRITTY_SOCKET": "/tmp/a", "TERM": "alacritty"},
			wantReason: "no supported terminal",
		},
		{
			name:       "two terminals claim the process at once",
			env:        map[string]string{"TERM": "xterm-ghostty", "KITTY_WINDOW_ID": "1"},
			wantReason: "ghostty (TERM) and kitty (KITTY_WINDOW_ID)",
		},
		{
			name:       "the override names something with no encoder",
			env:        map[string]string{"SIDECAR_TERM_PROGRAM": "alacritty", "TERM": "xterm-kitty"},
			wantReason: "is not a terminal with a Sidecar encoder",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Detect(env(tt.env))
			if got.OK() {
				t.Fatalf("Detect() = %+v, want unavailable", got)
			}
			if !strings.Contains(got.Reason, tt.wantReason) {
				t.Errorf("Detect().Reason = %q, want it to contain %q", got.Reason, tt.wantReason)
			}
		})
	}
}

func TestDetectWithoutAnEnvironment(t *testing.T) {
	if got := Detect(nil); got.OK() || got.Reason == "" {
		t.Errorf("Detect(nil) = %+v, want an unavailable answer with a reason", got)
	}
}

func TestInsideTmux(t *testing.T) {
	if InsideTmux(env(map[string]string{"TMUX": "/tmp/tmux-501/default,42,0"})) != true {
		t.Error("InsideTmux() = false with TMUX set")
	}
	if InsideTmux(env(map[string]string{"TMUX": "  "})) {
		t.Error("InsideTmux() = true for a blank TMUX")
	}
	if InsideTmux(env(nil)) || InsideTmux(nil) {
		t.Error("InsideTmux() = true outside tmux")
	}
}
