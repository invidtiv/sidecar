package overview

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The Activity board and remote hosts.
//
// The claim these tests pin is the one nobody could previously point at: a
// remote agent-backed shell reaches the same lane a local agent would, and it
// sorts after every local project rather than interleaving with them. The
// second test is the other half of the same answer — a remote shell with no
// agent is absent for exactly the reason a local plain shell is, so "remote
// shells are missing from Activity" is answered by the shell's provider, not by
// its machine.

// seedHost folds one host's snapshot into a model the way handleHostUpdate
// does, without a registry, a connection, or ssh.
func seedHost(m *Model, id string, snapshot hostproto.Snapshot) {
	if m.hostHealth == nil {
		m.hostHealth = map[string]hosts.Health{}
		m.hostResults = map[string][]workspaceinventory.ProjectResult{}
		m.hostProjects = map[string][]Project{}
	}
	m.hostHealth[id] = hosts.Health{State: hosts.StateOnline}
	results := hosts.ProjectResults(id, snapshot, false)
	m.hostResults[id] = results
	projects := make([]Project, 0, len(results))
	for index, result := range results {
		projects = append(projects, Project{Name: result.ProjectName, Path: result.ProjectRoot, Key: result.ProjectKey, Index: index})
	}
	m.hostProjects[id] = projects
}

func laneCards(t *testing.T, m *Model, lane agentstatus.LaneID) []kanban.Card {
	t.Helper()
	for _, built := range m.board.Board().Lanes {
		if built.ID == kanban.LaneID(lane) {
			return built.Cards
		}
	}
	t.Fatalf("no %s lane on the board", lane)
	return nil
}

// TestRemoteAgentShellLandsInItsLaneAfterLocalProjects. Remote agents share the
// lanes with local ones — "is anything blocked?" is a question about every
// machine at once — and the cardOrder offset is what keeps a machine's rows
// together below the local projects instead of interleaved with them.
func TestRemoteAgentShellLandsInItsLaneAfterLocalProjects(t *testing.T) {
	m := catalogModel(t)
	seedHost(m, "mac-mini", *remoteSnapshot("blocked"))
	m.syncBoard()

	remoteID := hosts.ScopedKey("mac-mini", "/home/me/api:shell:s1")
	workspace, catalogued := m.cards[remoteID]
	if !catalogued {
		t.Fatalf("the remote agent shell never became a card; cards=%d", len(m.cards))
	}
	if !workspace.HasAgent() {
		t.Fatalf("the remote shell lost its agent evidence: provider=%q", workspace.Provider)
	}

	cards := laneCards(t, m, agentstatus.LaneBlocked)
	position := -1
	for index, card := range cards {
		if card.ID == remoteID {
			position = index
		}
	}
	if position < 0 {
		t.Fatalf("the remote blocked agent is not in the blocked lane; lane=%v", cards)
	}
	// catalogModel's local blocked agent is "s1" in the sidecar project. The
	// remote card must sort below it: remote ordinals start at len(m.projects).
	if position == 0 {
		t.Errorf("the remote card sorted above the local ones; lane=%v", cards)
	}
	if got := cards[0].ID; got != "s1" {
		t.Errorf("first blocked card = %q, want the local one", got)
	}
	if len(cards) != 2 {
		t.Fatalf("blocked lane holds %d cards, want the local one and the remote one", len(cards))
	}
}

// TestRemotePlainShellIsAbsentLikeALocalOne is the diagnosis, written down: a
// shell with no agent is not on an agent board, and the machine it lives on
// changes nothing about that. It still reaches the Sessions list, which is
// where a plain shell belongs.
func TestRemotePlainShellIsAbsentLikeALocalOne(t *testing.T) {
	snapshot := hostproto.Snapshot{
		Generation: 1,
		ObservedAt: time.Now(),
		Projects: []hostproto.Project{{
			Key: "/home/me/api", Name: "api", Root: "/home/me/api",
			Items: []hostproto.Item{{
				ID: "/home/me/api:shell:plain", ProjectKey: "/home/me/api", ProjectName: "api", ProjectRoot: "/home/me/api",
				Kind: "shell", Key: "plain", Name: "Plain shell", Session: "api-plain", PaneID: "%9", Live: true,
			}},
		}},
	}
	m := catalogModel(t)
	seedHost(m, "mac-mini", snapshot)
	m.syncBoard()

	plainID := hosts.ScopedKey("mac-mini", "/home/me/api:shell:plain")
	if _, onBoard := m.cards[plainID]; onBoard {
		t.Error("a remote shell with no agent became an Activity card")
	}
	// The local plain shell in catalogModel ("s2") is absent for the same
	// reason. If that ever changes, it must change for both.
	if _, onBoard := m.cards["s2"]; onBoard {
		t.Error("a local shell with no agent became an Activity card")
	}
	if _, listed := m.catalog[plainID]; !listed {
		t.Errorf("the remote plain shell is missing from the Sessions catalog too; catalog=%d", len(m.catalog))
	}
}
