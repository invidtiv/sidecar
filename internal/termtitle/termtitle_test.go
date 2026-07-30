package termtitle

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		vars Vars
		want string
	}{
		{
			name: "empty template disables the feature",
			tmpl: "",
			vars: Vars{Project: "sidecar"},
			want: "",
		},
		{
			name: "project only on the main worktree",
			tmpl: "{project}{worktree}",
			vars: Vars{Project: "sidecar", Dir: "sidecar"},
			want: "sidecar",
		},
		{
			name: "worktree brings its own brackets",
			tmpl: "{project}{worktree}",
			vars: Vars{Project: "td", Worktree: "charm"},
			want: "td [charm]",
		},
		{
			name: "plugin and dir",
			tmpl: "{project} · {plugin} ({dir})",
			vars: Vars{Project: "sidecar", Plugin: "workspaces", Dir: "sidecar"},
			want: "sidecar · workspaces (sidecar)",
		},
		{
			name: "unknown placeholders are left alone",
			tmpl: "{project} {branch}",
			vars: Vars{Project: "sidecar"},
			want: "sidecar {branch}",
		},
		{
			name: "empty substitution does not leave dangling whitespace",
			tmpl: "{project} {plugin}",
			vars: Vars{Project: "sidecar"},
			want: "sidecar",
		},
		{
			name: "escape sequences in a branch name are stripped",
			tmpl: "{project}{worktree}",
			vars: Vars{Project: "sidecar", Worktree: "fix\x1b]0;pwned\x07me"},
			want: "sidecar [fix]0;pwnedme]",
		},
		{
			name: "newlines collapse to a single space",
			tmpl: "{project}",
			vars: Vars{Project: "side\n\ncar"},
			want: "side car",
		},
		{
			// Display spoofing rather than injection, but a title that renders
			// backwards is worse than one missing a character.
			name: "bidi overrides are stripped",
			tmpl: "{project}",
			vars: Vars{Project: "main‮gnp.exe"},
			want: "maingnp.exe",
		},
		{
			name: "zero width joiner survives so emoji stay intact",
			tmpl: "{project}",
			vars: Vars{Project: "👩‍💻"},
			want: "👩‍💻",
		},
		{
			// U+009B, the C1 form of CSI — invisible in a diff, dangerous in a
			// title, and not covered by stripping ESC alone.
			name: "C1 control characters are stripped",
			tmpl: "{project}",
			vars: Vars{Project: "sidecar"},
			want: "sidecar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Render(tt.tmpl, tt.vars); got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderTruncatesLongTitles(t *testing.T) {
	got := Render("{project}", Vars{Project: strings.Repeat("x", 500)})
	if len([]rune(got)) != maxLen {
		t.Errorf("Render() length = %d, want %d", len([]rune(got)), maxLen)
	}
}

func TestRenderKeepsMultibyteRunesIntact(t *testing.T) {
	got := Render("{project}", Vars{Project: strings.Repeat("日", 200)})
	if runes := []rune(got); len(runes) != maxLen {
		t.Fatalf("Render() length = %d runes, want %d", len(runes), maxLen)
	}
	if strings.ContainsRune(got, '�') {
		t.Error("Render() truncated in the middle of a rune")
	}
}

func TestSequences(t *testing.T) {
	// OSC 0 rather than OSC 1 or 2 alone: it sets the icon name and the window
	// title together, and terminals disagree about which one labels a tab.
	if got, want := Set("sidecar"), "\x1b]0;sidecar\x07"; got != want {
		t.Errorf("Set() = %q, want %q", got, want)
	}
	// Parameter 0 covers icon name and window title. Parameter 2 would push and
	// pop only the window title, leaving the icon name — the iTerm2 tab label —
	// stuck on the last project after sidecar exits.
	if got, want := Save(), "\x1b[22;0t"; got != want {
		t.Errorf("Save() = %q, want %q", got, want)
	}
	if got, want := Restore(), "\x1b[23;0t"; got != want {
		t.Errorf("Restore() = %q, want %q", got, want)
	}
}
