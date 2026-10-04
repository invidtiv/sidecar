package workspaceops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/marcus/sidecar/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

func TestConcurrentShellAllocationIgnoresIdenticalStalePreviews(t *testing.T) {
	testenv.RequireTmux(t)
	root := filepath.Join(t.TempDir(), "project")
	startThrowawaySession(t, "allocation-anchor", t.TempDir())
	// Each caller already rendered the same stale preview. Even if scheduling
	// serializes every subprocess, these requests must receive distinct identities.
	preview, session := ShellNames(root, nil)
	const count = 8
	start := make(chan struct{})
	results := make([]ShellResult, count)
	errs := make([]error, count)
	var workers sync.WaitGroup
	for i := range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			display := ""
			if i%2 == 0 {
				display = fmt.Sprintf("Parallel %d", i)
			}
			results[i], errs[i] = (Service{}).CreateShell(ManagedShellSpec{Allocate: true, ProjectRoot: root, ShellSpec: ShellSpec{SessionName: session, DisplayName: display, WorkDir: filepath.Dir(root)}})
		}()
	}
	close(start)
	workers.Wait()
	sessions, names := map[string]bool{}, map[string]bool{}
	for i, result := range results {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if result.SessionName == "" || result.PaneID == "" || result.DisplayName == "" || sessions[result.SessionName] || names[result.DisplayName] {
			t.Fatalf("caller %d reused an identity: %+v (preview %s)", i, result, preview)
		}
		sessions[result.SessionName], names[result.DisplayName] = true, true
		t.Cleanup(func() { _, _ = tmuxTest(t, "kill-session", "-t", "="+result.SessionName) })
	}
	dir, err := projectdir.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	defs, err := shellstate.ListAtPath(filepath.Join(dir, "shells.json"))
	if err != nil || len(defs) != count {
		t.Fatalf("durable records: %+v err=%v", defs, err)
	}
	for _, def := range defs {
		if !sessions[def.TmuxName] || !names[def.DisplayName] || def.Restore == nil || !def.Restore.Eligible {
			t.Fatalf("durable identity mismatch: %+v", def)
		}
	}
}

func TestFreshShellSkipsOccupiedAndForgottenIdentitiesButReconnectReuses(t *testing.T) {
	testenv.RequireTmux(t)
	root := t.TempDir()
	_, occupied := ShellNames(root, nil)
	startThrowawaySession(t, occupied, root)
	spec := ManagedShellSpec{Allocate: true, ProjectRoot: root, ShellSpec: ShellSpec{WorkDir: root}}
	first, err := (Service{}).CreateShell(spec)
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionName == occupied {
		t.Fatal("fresh create adopted unrecorded session")
	}
	t.Cleanup(func() { _, _ = tmuxTest(t, "kill-session", "-t", "="+first.SessionName) })
	if err := ForgetManagedShell(root, first.SessionName, tmuxenv.Namespace(), time.Time{}); err != nil {
		t.Fatal(err)
	}
	// Remove the live session too: the tombstone alone must reserve its identity.
	_, _ = tmuxTest(t, "kill-session", "-t", "="+first.SessionName)
	second, err := (Service{}).CreateShell(spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tmuxTest(t, "kill-session", "-t", "="+second.SessionName) })
	if second.SessionName == first.SessionName {
		t.Fatal("fresh create consumed restore identity")
	}
	if _, err := RestoreManagedShell(root, first.SessionName, tmuxenv.Namespace()); err != nil {
		t.Fatal(err)
	}
	restored, err := CreateShell(ShellSpec{SessionName: first.SessionName, WorkDir: root})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := CreateShell(ShellSpec{SessionName: first.SessionName, WorkDir: root})
	if err != nil || retry.PaneID != restored.PaneID {
		t.Fatalf("reconnect changed pane: %+v %+v %v", restored, retry, err)
	}
}

func TestShellAllocationRefusesDuplicateNameBeforeCreating(t *testing.T) {
	testenv.RequireTmux(t)
	root := t.TempDir()
	spec := ManagedShellSpec{Allocate: true, ProjectRoot: root, ShellSpec: ShellSpec{WorkDir: root, DisplayName: "Same name"}}
	first, err := (Service{}).CreateShell(spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tmuxTest(t, "kill-session", "-t", "="+first.SessionName) })
	_, err = (Service{}).CreateShell(spec)
	var named *ShellCreateError
	if !errors.As(err, &named) || named.Code != "shell_name_in_use" {
		t.Fatalf("unnamed refusal: %v", err)
	}
	dir, _ := projectdir.Resolve(root)
	defs, err := shellstate.ListAtPath(filepath.Join(dir, "shells.json"))
	if err != nil || len(defs) != 1 || defs[0].TmuxName != first.SessionName || !SessionExists(first.SessionName) {
		t.Fatalf("refusal changed winning shell: %+v %v", defs, err)
	}
}

// Each worker is an independent process with its own Go mutexes. A ready gate
// releases them together, all carrying the same stale identity preview.
func TestShellAllocationAcrossProcesses(t *testing.T) {
	if root := os.Getenv("SIDECAR_ALLOC_TEST_ROOT"); root != "" {
		t.Setenv("TMUX_TMPDIR", os.Getenv("SIDECAR_ALLOC_TEST_TMUX"))
		config.SetTestStateDir(os.Getenv("SIDECAR_ALLOC_TEST_STATE"))
		worker := os.Getenv("SIDECAR_ALLOC_TEST_WORKER")
		ready := filepath.Join(root, "ready-"+worker)
		if err := os.WriteFile(ready, nil, 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(root, "go")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("allocation gate timed out")
			}
			time.Sleep(5 * time.Millisecond)
		}
		result, err := (Service{}).CreateShell(ManagedShellSpec{Allocate: true, ProjectRoot: root, ShellSpec: ShellSpec{SessionName: "stale-preview", DisplayName: "Process " + worker, WorkDir: root}})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "result-"+worker), data, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	testenv.RequireTmux(t)
	root := t.TempDir()
	startThrowawaySession(t, "allocation-process-anchor", root)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const count = 8
	var workers sync.WaitGroup
	errs := make([]error, count)
	outputs := make([][]byte, count)
	for i := range count {
		command := exec.CommandContext(ctx, exe, "-test.run=^TestShellAllocationAcrossProcesses$")
		command.Env = append(os.Environ(), "SIDECAR_ALLOC_TEST_ROOT="+root, "SIDECAR_ALLOC_TEST_TMUX="+os.Getenv("TMUX_TMPDIR"), "SIDECAR_ALLOC_TEST_STATE="+config.StateDir(), fmt.Sprintf("SIDECAR_ALLOC_TEST_WORKER=%d", i))
		workers.Add(1)
		go func() { defer workers.Done(); outputs[i], errs[i] = command.CombinedOutput() }()
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		ready, err := filepath.Glob(filepath.Join(root, "ready-*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(ready) == count {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			workers.Wait()
			t.Fatalf("only %d workers ready", len(ready))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(root, "go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	seen := map[string]bool{}
	for i := range count {
		if errs[i] != nil {
			t.Fatalf("process %d: %v: %s", i, errs[i], outputs[i])
		}
		data, err := os.ReadFile(filepath.Join(root, fmt.Sprintf("result-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		var result ShellResult
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if result.SessionName == "" || seen[result.SessionName] || !SessionExists(result.SessionName) {
			t.Fatalf("process %d reused or lost identity: %+v", i, result)
		}
		seen[result.SessionName] = true
		t.Cleanup(func() { _, _ = tmuxTest(t, "kill-session", "-t", "="+result.SessionName) })
	}
	dir, err := projectdir.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	defs, err := shellstate.ListAtPath(filepath.Join(dir, "shells.json"))
	if err != nil || len(defs) != count {
		t.Fatalf("durable process records: %+v %v", defs, err)
	}
	for _, def := range defs {
		if !seen[def.TmuxName] {
			t.Fatalf("wrong durable identity: %+v", def)
		}
	}
}

func TestConcurrentExplicitShellReconnectKeepsOnePane(t *testing.T) {
	testenv.RequireTmux(t)
	root := t.TempDir()
	startThrowawaySession(t, "reconnect-allocation-anchor", root)
	spec := ShellSpec{SessionName: "explicit-reconnect-proof", WorkDir: root}
	t.Cleanup(func() { _, _ = tmuxTest(t, "kill-session", "-t", "="+spec.SessionName) })
	const count = 8
	start := make(chan struct{})
	results, errs := make([]ShellResult, count), make([]error, count)
	var workers sync.WaitGroup
	for i := range count {
		workers.Add(1)
		go func() { defer workers.Done(); <-start; results[i], errs[i] = CreateShell(spec) }()
	}
	close(start)
	workers.Wait()
	for i, result := range results {
		if errs[i] != nil || result.PaneID == "" || result.PaneID != results[0].PaneID {
			t.Fatalf("retry %d: %+v %v", i, result, errs[i])
		}
	}
}

type failingShellEditor struct{}

func (failingShellEditor) AddShell(shellstate.Definition) error {
	return errors.New("unexpected unlocked write")
}
func (failingShellEditor) RemoveShell(string) error { return errors.New("unexpected removal") }
func (failingShellEditor) EditShells(apply func(*shellstate.Snapshot) (bool, error)) error {
	if _, err := apply(&shellstate.Snapshot{}); err != nil {
		return err
	}
	return errors.New("disk write failed")
}
func TestShellAllocationWriteFailureRollsBackOnlyNewSession(t *testing.T) {
	testenv.RequireTmux(t)
	root := t.TempDir()
	_, occupied := ShellNames(root, nil)
	startThrowawaySession(t, occupied, root)
	result, err := (Service{Shells: failingShellEditor{}}).CreateShell(ManagedShellSpec{Allocate: true, ProjectRoot: root, ShellSpec: ShellSpec{WorkDir: root}})
	var named *ShellCreateError
	if !errors.As(err, &named) || named.Code != "shell_state" {
		t.Fatalf("unnamed write failure: %v", err)
	}
	if SessionExists(result.SessionName) || !SessionExists(occupied) {
		t.Fatalf("rollback affected wrong identity: %+v", result)
	}
}

func TestShellAllocationHungTmuxReleasesWriterLock(t *testing.T) {
	testenv.RequireTmux(t)
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	startThrowawaySession(t, "hung-allocation-anchor", t.TempDir())
	for _, phase := range []string{"has-session", "display-message", "start-server", "show-options", "new-session", "new-session-after-create", "list-panes"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			dir, err := projectdir.Resolve(root)
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "hung")
			shim := t.TempDir()
			script := "#!/bin/sh\nif [ \"$HUNG_PHASE\" = new-session-after-create ] && [ \"$1\" = new-session ]; then \"$REAL_TMUX\" \"$@\" || exit $?; touch \"$HUNG_MARKER\"; sleep 3; exit 1; fi\nif [ \"$1\" = \"$HUNG_PHASE\" ]; then touch \"$HUNG_MARKER\"; sleep 3; exit 1; fi\nexec \"$REAL_TMUX\" \"$@\"\n"
			if err := os.WriteFile(filepath.Join(shim, "tmux"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("REAL_TMUX", realTmux)
			t.Setenv("HUNG_PHASE", phase)
			t.Setenv("HUNG_MARKER", marker)
			t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
			started := time.Now()
			done := make(chan error, 1)
			go func() {
				_, err := (Service{}).CreateShell(ManagedShellSpec{Allocate: true, ProjectRoot: root, ShellSpec: ShellSpec{WorkDir: root}})
				done <- err
			}()
			deadline := time.Now().Add(4 * time.Second)
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("shim was not reached")
				}
				time.Sleep(5 * time.Millisecond)
			}
			writerStart := time.Now()
			writerErr := shellstate.AddAtPath(filepath.Join(dir, "shells.json"), shellstate.Definition{TmuxName: "other-writer", DisplayName: "Other writer"})
			createErr := <-done
			t.Logf("hung %s: writer wait %v, create returned in %v", phase, time.Since(writerStart), time.Since(started))
			var named *ShellCreateError
			if !errors.As(createErr, &named) || named.Code != "shell_create_failed" || !strings.Contains(named.Message, "deadline") {
				t.Errorf("hung %s was not a named timeout: %v", phase, createErr)
			}
			if elapsed := time.Since(started); elapsed > 3*time.Second {
				t.Errorf("lock held across hung %s for %v", phase, elapsed)
			}
			if writerErr != nil || time.Since(writerStart) > 3*time.Second {
				t.Errorf("other writer blocked: %v (%v)", writerErr, time.Since(writerStart))
			}
			t.Setenv("HUNG_PHASE", "")
			defs, err := shellstate.ListAtPath(filepath.Join(dir, "shells.json"))
			if err != nil || len(defs) != 1 || defs[0].TmuxName != "other-writer" {
				t.Errorf("timeout published a shell: %+v %v", defs, err)
			}
			_, session := ShellNames(root, nil)
			if SessionExists(session) {
				_, _ = tmuxTest(t, "kill-session", "-t", "="+session)
				t.Error("post-create timeout leaked its new session")
			}
		})
	}
}
