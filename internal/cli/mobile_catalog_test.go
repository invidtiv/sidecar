package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hostserve"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestParseMobileCatalogArgs(t *testing.T) {
	var stderr bytes.Buffer
	env := Env{Stdout: &bytes.Buffer{}, Stderr: &stderr, Ctx: context.Background()}
	query, code := parseMobileCatalogArgs(env, []string{
		"--json", "--sort=recent", "--search", "sidecar blocked", "--host", "local:aerie", "--host=remote:studio",
		"--provider", "codex", "--state", "working", "--state=ready",
	}, "help")
	want := mobileproto.CatalogQuery{Sort: "recent", Search: "sidecar blocked", Hosts: []string{"local:aerie", "remote:studio"}, Providers: []string{"codex"}, States: []string{"working", "ready"}}
	if code != 0 || stderr.Len() != 0 || !reflect.DeepEqual(query, want) {
		t.Fatalf("parse = %+v code=%d stderr=%q, want %+v", query, code, stderr.String(), want)
	}
}

func TestParseMobileCatalogArgsRequiresStructuredOutputAndValues(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "structured output", args: []string{"--sort", "name"}, want: "--json is required"},
		{name: "missing value", args: []string{"--json", "--host"}, want: "--host requires a value"},
		{name: "unknown", args: []string{"--json", "--wat=one"}, want: "unknown mobile sessions flag"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			_, code := parseMobileCatalogArgs(Env{Stderr: &stderr}, test.args, "help")
			if code != 2 || !bytes.Contains(stderr.Bytes(), []byte(test.want)) {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestMobileSessionsCommandPublishesCatalogContract(t *testing.T) {
	command := mobileCommand().FindSubcommand("sessions")
	if command == nil || command.Agent.Invocation != "sidecar mobile sessions --json" {
		t.Fatalf("sessions command = %+v", command)
	}
	for _, name := range []string{"--sort", "--search", "--host", "--provider", "--state"} {
		found := false
		for _, flag := range command.Flags {
			found = found || flag.Name == name
		}
		if !found {
			t.Fatalf("sessions command omits %s", name)
		}
	}
}

func TestMobileCatalogProviderPreservesUnknownActivityAgeAcrossReads(t *testing.T) {
	stateDir := t.TempDir()
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	projectState, err := projectdir.ResolveWithBase(stateDir, root)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 7, 16, 0, 0, 0, time.UTC)
	manifest := fmt.Sprintf(`{"version":3,"shells":[{"tmuxName":"sidecar-sh-repo-1","displayName":"Agent","namespace":%q,"createdAt":%q,"agentType":"codex"}]}`, tmuxenv.Namespace(), created.Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(projectState, "shells.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC)
	screen := "› Write tests for @filename"
	runner := mobileCatalogRunner{root: root}
	collector := workspaceinventory.Collector{
		Runner: runner,
		Capture: func(string, int) (string, tty.PaneState, error) {
			return screen, tty.PaneState{}, nil
		},
		Now: func() time.Time { return clock },
	}.WithDefaults()
	provider := newMobileCatalogProvider(Env{StateDir: stateDir}, func() ([]hostserve.Project, error) {
		return []hostserve.Project{{Name: "Repo", Path: root}}, nil
	}, collector, nil, func() time.Time { return clock })
	first, err := provider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(134 * time.Millisecond)
	second, err := provider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	firstChanged, secondChanged := shellChangedAt(t, first), shellChangedAt(t, second)
	if !firstChanged.IsZero() || !secondChanged.IsZero() {
		t.Fatalf("provider invented activity age without shared history: %v -> %v", firstChanged, secondChanged)
	}
	screen = "• Working (1s • esc to interrupt)"
	clock = clock.Add(time.Second)
	third, err := provider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if changed := shellChangedAt(t, third); !changed.Equal(clock) {
		t.Fatalf("provider hid an observed activity transition at %v, want %v", changed, clock)
	}
}

func TestMobileCatalogProviderColdProcessesKeepGenerationStable(t *testing.T) {
	stateDir := t.TempDir()
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	projectState, err := projectdir.ResolveWithBase(stateDir, root)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 7, 16, 0, 0, 0, time.UTC)
	manifest := fmt.Sprintf(`{"version":3,"shells":[{"tmuxName":"sidecar-sh-repo-1","displayName":"Agent","namespace":%q,"createdAt":%q,"agentType":"codex"}]}`, tmuxenv.Namespace(), created.Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(projectState, "shells.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	identity := mobile.CatalogIdentity{HubID: host, OwnerHostID: "local:" + host, OwnerConfigGeneration: "config-stable"}
	resolver := func(context.Context, string) (mobile.ResolvedTarget, error) {
		return mobile.ResolvedTarget{
			WorkspaceID: "repo", ProjectRoot: root, WorkspaceKind: string(workspaceinventory.KindShell),
			Session: "sidecar-sh-repo-1", Pane: "%1", DurableSessionCreated: created.Format(time.RFC3339Nano),
			ServerPID: 101, SessionID: "$1", SessionCreated: "1700000000",
		}, nil
	}
	read := func(clock time.Time) mobileproto.CatalogSnapshot {
		collector := workspaceinventory.Collector{
			Runner: mobileCatalogRunner{root: root},
			Capture: func(string, int) (string, tty.PaneState, error) {
				return "› Write tests for @filename", tty.PaneState{}, nil
			},
			Now: func() time.Time { return clock },
		}.WithDefaults()
		provider := newMobileCatalogProvider(Env{StateDir: stateDir}, func() ([]hostserve.Project, error) {
			return []hostserve.Project{{Name: "Repo", Path: root}}, nil
		}, collector, nil, func() time.Time { return clock })
		snapshot, err := mobile.QueryCatalog(context.Background(), provider, resolver, mobileproto.CatalogQuery{}, identity)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	first := read(time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC))
	second := read(time.Date(2026, 9, 7, 18, 0, 1, 0, time.UTC))
	if first.Generation != second.Generation {
		t.Fatalf("cold read generation drifted without activity history: %q -> %q", first.Generation, second.Generation)
	}
	firstChanged := first.Sections[0].Rows[0].ChangedAt
	secondChanged := second.Sections[0].Rows[0].ChangedAt
	if firstChanged != "" || secondChanged != "" {
		t.Fatalf("cold reads invented activity age: %q -> %q", firstChanged, secondChanged)
	}
}

type mobileCatalogRunner struct{ root string }

func (r mobileCatalogRunner) Output(_ context.Context, name string, _ ...string) ([]byte, error) {
	switch name {
	case "tmux":
		return []byte(fmt.Sprintf("%%1\tsidecar-sh-repo-1\t%s\tcodex\trepo\t0\t101\t202\t24\n", r.root)), nil
	case "git":
		return []byte(fmt.Sprintf("worktree %s\nHEAD 0123456789abcdef\nbranch refs/heads/main\n", r.root)), nil
	default:
		return nil, fmt.Errorf("unexpected command %s", name)
	}
}

func shellChangedAt(t *testing.T, input mobile.CatalogInput) time.Time {
	t.Helper()
	for _, project := range input.Projects {
		for _, workspace := range project.Result.Workspaces {
			if workspace.Kind == workspaceinventory.KindShell {
				return workspace.Presentation.ChangedAt
			}
		}
	}
	t.Fatal("catalog provider omitted managed shell")
	return time.Time{}
}
