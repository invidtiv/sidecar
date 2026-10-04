package agentcatalog

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCapabilityProbeUsesDestinationDirectory(t *testing.T) {
	dir := t.TempDir()
	oldDir, newDir := filepath.Join(dir, "older"), filepath.Join(dir, "newer")
	for _, path := range []string{oldDir, newDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\nif [ \"$PWD\" = '" + newDir + "' ]; then printf '%s' '--no-daemon'; else printf '%s' 'older usage'; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	for _, target := range []string{oldDir, newDir} {
		want := []string{"codex"}
		if target == newDir {
			want = append(want, "--no-daemon")
		}
		argv, err := BuildLaunchInDir(target, "codex", nil, false)
		if err != nil || !slices.Equal(argv, want) {
			t.Fatalf("launch in %s = %v, %v; want %v", target, argv, err, want)
		}
		resume, err := BuildResumeInDir(target, "codex", "id", "session", nil)
		if err != nil || !slices.Equal(resume, append(want, "resume", "session")) {
			t.Fatalf("resume in %s = %v, %v", target, resume, err)
		}
	}
}

func TestCodexExplicitRemoteDoesNotReceiveDaemonBypass(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nprintf '%s' '--no-daemon'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	for _, extra := range [][]string{{"--remote", "unix:///remote.sock"}, {"--remote=unix:///remote.sock"}} {
		launch, err := BuildLaunch("codex", extra, false)
		if err != nil || !slices.Equal(launch, append([]string{"codex"}, extra...)) {
			t.Fatalf("explicit remote launch = %v, %v", launch, err)
		}
		resume, err := BuildResume("codex", "id", "session", extra)
		if err != nil || !slices.Equal(resume, append([]string{"codex", "resume", "session"}, extra...)) {
			t.Fatalf("explicit remote resume = %v, %v", resume, err)
		}
	}
}

func TestCapabilityProbeBoundsDescendantsHoldingOutput(t *testing.T) {
	dir := t.TempDir()
	var fifos []*os.File
	for _, name := range []string{"ready", "release"} {
		path := filepath.Join(dir, name)
		if output, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
			t.Fatalf("create probe barrier: %v\n%s", err, output)
		}
		file, err := os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		fifos = append(fifos, file)
		t.Cleanup(func() { _ = file.Close() })
	}
	ready, release := fifos[0], fifos[1]
	t.Cleanup(func() { _, _ = release.WriteString("done\n") })
	script := "#!/bin/sh\n(printf 'started\\n' > '" + ready.Name() + "'; IFS= read -r release < '" + release.Name() + "') &\nprintf '%s' '--no-daemon'\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	started := make(chan error, 1)
	go func() { _, err := bufio.NewReader(ready).ReadString('\n'); started <- err }()
	done := make(chan error, 1)
	go func() { _, err := BuildLaunch("codex", nil, false); done <- err }()
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("help fixture never held the output pipe")
	}
	select {
	case err := <-done:
		if !errors.Is(err, exec.ErrWaitDelay) {
			t.Fatalf("held output must refuse an incomplete capability probe: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("capability probe waited for an unrelated descendant instead of its bound")
	}
}

func TestNativeCapabilityCacheAndRenamedShim(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "help.go")
	program := `package main
import "os"
func main() {
 if len(os.Args) != 2 || os.Args[1] != "--help" { os.Exit(90) }
 f, err := os.OpenFile(os.Getenv("HELP_PROBE_COUNT"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
 if err != nil { os.Exit(91) }; f.WriteString("x"); f.Close()
 if os.Getenv("HELP_PROBE_SUPPORTED") == "1" { os.Stdout.WriteString("--no-daemon") } else { os.Stdout.WriteString("older usage") }
}`
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := filepath.Join(dir, "codex")
	if output, err := exec.Command("go", "build", "-o", provider, source).CombinedOutput(); err != nil {
		t.Fatalf("build native help fixture: %v\n%s", err, output)
	}
	count := filepath.Join(dir, "count")
	t.Setenv("HELP_PROBE_COUNT", count)
	t.Setenv("HELP_PROBE_SUPPORTED", "1")
	t.Setenv("PATH", dir)
	if argv, err := BuildLaunch("codex", nil, false); err != nil || !slices.Equal(argv, []string{"codex", "--no-daemon"}) {
		t.Fatalf("native launch = %v, %v", argv, err)
	}
	if argv, err := BuildResume("codex", "id", "session", nil); err != nil || !slices.Equal(argv, []string{"codex", "--no-daemon", "resume", "session"}) {
		t.Fatalf("native resume = %v, %v", argv, err)
	}
	if probes, err := os.ReadFile(count); err != nil || string(probes) != "x" {
		t.Fatalf("native executable help was not cached: %q, %v", probes, err)
	}
	// The actual installed command is codex -> mise: its native target has a
	// different name and can switch provider versions without changing itself.
	shim := filepath.Join(dir, "mise")
	if err := os.Rename(provider, shim); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shim, provider); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HELP_PROBE_SUPPORTED", "0")
	if argv, err := BuildLaunch("codex", nil, false); err != nil || !slices.Equal(argv, []string{"codex"}) {
		t.Fatalf("native shim legacy launch = %v, %v", argv, err)
	}
	t.Setenv("HELP_PROBE_SUPPORTED", "1")
	if argv, err := BuildLaunch("codex", nil, false); err != nil || !slices.Equal(argv, []string{"codex", "--no-daemon"}) {
		t.Fatalf("native shim cached another selected provider: %v, %v", argv, err)
	}
}

func TestCodexLocalLaunchAndResumeUseSupportedIsolation(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "older", true: "supported"}[supported], func(t *testing.T) {
			dir := t.TempDir()
			help := "usage: codex\n"
			if supported {
				help += "  --no-daemon  Run without the shared background server\n"
			}
			path := filepath.Join(dir, "codex")
			count := filepath.Join(dir, "probes")
			script := "#!/bin/sh\n[ \"$1\" = --help ] || exit 90\nprintf x >> '" + count + "'\nprintf '%s' '" + help + "'\n"
			if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			prefix := []string{"codex"}
			if supported {
				prefix = append(prefix, "--no-daemon")
			}
			launch, err := BuildLaunch("codex", []string{"--model", "space value"}, true)
			wantLaunch := append(slices.Clone(prefix), "--dangerously-bypass-approvals-and-sandbox", "--model", "space value")
			if err != nil || !slices.Equal(launch, wantLaunch) {
				t.Fatalf("launch = %v, %v; want %v", launch, err, wantLaunch)
			}
			resume, err := BuildResume("codex", "id", "session-id", nil)
			wantResume := append(slices.Clone(prefix), "resume", "session-id")
			if err != nil || !slices.Equal(resume, wantResume) {
				t.Fatalf("resume = %v, %v; want %v", resume, err, wantResume)
			}
			if supported {
				explicit, err := BuildLaunch("codex", []string{"--no-daemon"}, false)
				if err != nil || !slices.Equal(explicit, []string{"codex", "--no-daemon"}) {
					t.Fatalf("explicit flag must not be duplicated: %v, %v", explicit, err)
				}
			}
			probes, err := os.ReadFile(count)
			wantProbes := "xx"
			if supported {
				wantProbes += "x"
			}
			if err != nil || string(probes) != wantProbes {
				t.Fatalf("script wrappers must recheck capabilities: %q, %v", probes, err)
			}
			if supported {
				// Replacing the installed executable invalidates an advertised flag.
				if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' 'usage: older codex'\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				argv, err := BuildLaunch("codex", nil, false)
				if err != nil || !slices.Equal(argv, []string{"codex"}) {
					t.Fatalf("replaced executable retained old capabilities: %v, %v", argv, err)
				}
			}
		})
	}
}

func TestCapabilityWrapperDoesNotCacheItsSelectedProvider(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "new-provider")
	path := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nif [ -f '" + marker + "' ]; then printf '%s' '--no-daemon'; else printf '%s' 'older usage'; fi\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	argv, err := BuildLaunch("codex", nil, false)
	if err != nil || !slices.Equal(argv, []string{"codex"}) {
		t.Fatalf("legacy wrapper = %v, %v", argv, err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	argv, err = BuildLaunch("codex", nil, false)
	if err != nil || !slices.Equal(argv, []string{"codex", "--no-daemon"}) {
		t.Fatalf("wrapper reused capability from another selected provider: %v, %v", argv, err)
	}
	after, err := os.Stat(path)
	if err != nil || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		t.Fatalf("proof accidentally changed wrapper identity: %v", err)
	}
}

func TestFailedCapabilityProbeRefusesLaunchAndCanRecover(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "recover")
	path := filepath.Join(dir, "codex")
	script := "#!/bin/sh\n[ -f '" + marker + "' ] || exit 8\nprintf '%s' '--no-daemon'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if _, err := BuildLaunch("codex", nil, false); err == nil || !strings.Contains(err.Error(), "probe provider") {
		t.Fatalf("unknown daemon capability must refuse launch: %v", err)
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	argv, err := BuildLaunch("codex", nil, false)
	if err != nil || !slices.Equal(argv, []string{"codex", "--no-daemon"}) {
		t.Fatalf("temporary failure was cached: %v, %v", argv, err)
	}
}
