package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAssertIsolatedPath(t *testing.T) {
	tests := []struct {
		name      string
		isolated  bool
		path      func(home string) string
		wantError bool
	}{
		{
			name:     "isolated run may not touch the real state tree",
			isolated: true,
			path: func(home string) string {
				return filepath.Join(home, ".local/state/sidecar/projects/sidecar/shells.json")
			},
			wantError: true,
		},
		{
			name:      "isolated run may not touch the real config tree",
			isolated:  true,
			path:      func(home string) string { return filepath.Join(home, ".config/sidecar/config.json") },
			wantError: true,
		},
		{
			name:      "the state root itself is inside the state root",
			isolated:  true,
			path:      func(home string) string { return filepath.Join(home, ".local/state/sidecar") },
			wantError: true,
		},
		{
			name:      "isolated run writing to its temp dir is fine",
			isolated:  true,
			path:      func(string) string { return filepath.Join(t.TempDir(), "state/sidecar/projects/sidecar/shells.json") },
			wantError: false,
		},
		{
			// This case is the guarantee that a normal user run still writes
			// its real manifest: without the isolation promise the guard is
			// inert, whatever path it is handed.
			name:     "normal run is never blocked from the real manifest",
			isolated: false,
			path: func(home string) string {
				return filepath.Join(home, ".local/state/sidecar/projects/sidecar/shells.json")
			},
			wantError: false,
		},
		{
			name:      "sibling directory is not inside the state root",
			isolated:  true,
			path:      func(home string) string { return filepath.Join(home, ".local/state/sidecar-other/shells.json") },
			wantError: false,
		},
		{
			name:      "sibling directory is not inside the config root",
			isolated:  true,
			path:      func(home string) string { return filepath.Join(home, ".config/sidecar-other/config.json") },
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if tt.isolated {
				t.Setenv(IsolationEnv, "1")
			} else {
				t.Setenv(IsolationEnv, "")
			}

			err := AssertIsolatedPath(tt.path(home))
			if tt.wantError && err == nil {
				t.Fatalf("AssertIsolatedPath(%s) = nil, want error", tt.path(home))
			}
			if !tt.wantError && err != nil {
				t.Fatalf("AssertIsolatedPath(%s) = %v, want nil", tt.path(home), err)
			}
		})
	}
}

func TestAssertIsolatedPathErrorNamesTheFix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(IsolationEnv, "1")

	path := filepath.Join(home, ".local/state/sidecar/projects/sidecar/shells.json")
	err := AssertIsolatedPath(path)
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{IsolationEnv, path, RealUserStateDir(), "XDG_STATE_HOME", "-config"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestCheckStateIsolation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(IsolationEnv, "1")

	// Isolation asserted but XDG_STATE_HOME left alone: StateDir() falls back
	// to the real tree and the check must fail closed.
	t.Setenv("XDG_STATE_HOME", "")
	if err := CheckStateIsolation(); err == nil {
		t.Fatal("CheckStateIsolation() = nil with the real state dir resolved, want error")
	}

	// Both axes moved into temp dirs: the check passes.
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmp, "state"))
	SetConfigPath(filepath.Join(tmp, "config", "config.json"))
	t.Cleanup(func() { SetConfigPath("") })
	if err := CheckStateIsolation(); err != nil {
		t.Fatalf("CheckStateIsolation() = %v, want nil", err)
	}
}

func TestIsolationAsserted(t *testing.T) {
	t.Setenv(IsolationEnv, "")
	if IsolationAsserted() {
		t.Fatal("IsolationAsserted() = true with no env and no test override")
	}
	t.Setenv(IsolationEnv, "1")
	if !IsolationAsserted() {
		t.Fatal("IsolationAsserted() = false with the env set")
	}

	t.Setenv(IsolationEnv, "")
	SetTestStateDir(t.TempDir())
	t.Cleanup(ResetTestStateDir)
	if !IsolationAsserted() {
		t.Fatal("IsolationAsserted() = false with a test state dir set")
	}
}
