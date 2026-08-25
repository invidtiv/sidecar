package app

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/version"
)

// plainText strips ANSI styling so content assertions survive glamour's
// per-span markup.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func plainText(s string) string { return ansiRe.ReplaceAllString(s, "") }

// The section shows the release body of the first planned product that
// published notes, and says whose notes they are.
func TestUpdateNotes_PerProductSelection(t *testing.T) {
	m := &Model{width: 100, height: 42}
	sidecar := target(version.ProductSidecar, "Sidecar", "0.95.0", "v9.9.9", true)
	td := target(version.ProductTd, "td", "1.0.0", "v1.1.0", true)
	sidecar.Notes = "- sidecar released feature"
	td.Notes = "- td released feature"
	m.products = []version.Target{sidecar, td}
	m.openUpdateModal()
	renderUpdatePhase(m)

	u := m.updateUIState()
	if u.notesTarget.Product != version.ProductSidecar {
		t.Fatalf("expected the first planned product's notes, got %s", u.notesTarget.Product)
	}

	// A release without published notes yields to one with them.
	sidecar.Notes = ""
	m.setProductStatus(version.ProductStatusMsg{Target: sidecar})
	out := plainText(renderUpdatePhase(m))
	if u.notesTarget.Product != version.ProductTd {
		t.Fatalf("a product without notes must not hide another's, got %s", u.notesTarget.Product)
	}
	if !strings.Contains(out, "td v1.1.0") {
		t.Errorf("the header should say whose notes these are:\n%s", out)
	}
	if !strings.Contains(out, "td released feature") {
		t.Errorf("the body should be that product's release notes:\n%s", out)
	}
}

// Expanding drives a tag-pinned changelog fetch whose result replaces the
// release-body teaser; failures render a styled error plus a working Retry;
// responses for other tags are dropped as stale.
func TestUpdateChangelog_ExpandFetchRetryMachine(t *testing.T) {
	config.SetTestConfigPath(t.TempDir() + "/config.json")
	t.Cleanup(config.ResetTestConfigPath)

	m := &Model{width: 100, height: 42}
	td := target(version.ProductTd, "td", "1.0.0", "v1.1.0", true)
	td.Notes = "- td released feature"
	m.products = []version.Target{td}
	m.openUpdateModal()
	renderUpdatePhase(m)
	u := m.updateUIState()

	_, fetch := m.applyUpdateAction("toggle-notes", nil)
	if u.changelogState != changelogLoading {
		t.Fatalf("expanding should start the fetch, state %v", u.changelogState)
	}
	if u.changelogTag != "v1.1.0" {
		t.Fatalf("request tag = %q", u.changelogTag)
	}
	if fetch == nil {
		t.Fatal("expanding should return the fetch command")
	}

	// A stale response for another tag changes nothing.
	m.handleUpdateChangelogMsg(version.ChangelogMsg{Repo: "td", Tag: "v9.8.8", Body: "stale"})
	if u.changelogState != changelogLoading {
		t.Fatalf("a stale response must be dropped, state %v", u.changelogState)
	}

	// Failure renders the styled error and a retry affordance.
	m.handleUpdateChangelogMsg(version.ChangelogMsg{Repo: "td", Tag: "v1.1.0",
		Err: errors.New("HTTP 404")})
	if u.changelogState != changelogFailed {
		t.Fatalf("expected failed, got %v", u.changelogState)
	}
	out := strings.ToLower(plainText(renderUpdatePhase(m)))
	if !strings.Contains(out, "couldn't load the full changelog") {
		t.Errorf("failure should render as styled copy, not raw body text:\n%s", out)
	}
	if !strings.Contains(out, "[ retry ]") {
		t.Errorf("failure should offer Retry:\n%s", out)
	}

	if _, retry := m.applyUpdateAction("retry-changelog", nil); retry == nil {
		t.Error("retry should re-issue the fetch command")
	}
	if u.changelogState != changelogLoading {
		t.Fatalf("retry should restart the fetch, state %v", u.changelogState)
	}
	// Success on retry swaps the window over to the fetched changelog.
	m.handleUpdateChangelogMsg(version.ChangelogMsg{Repo: "td", Tag: "v1.1.0",
		Body: "# Changelog\n\n- full history entry"})
	if u.changelogState != changelogLoaded {
		t.Fatalf("expected loaded, got %v", u.changelogState)
	}
	out = plainText(renderUpdatePhase(m))
	if !strings.Contains(out, "full history entry") {
		t.Errorf("expanded section should show the fetched changelog:\n%s", out)
	}
	if strings.Contains(out, "td released feature") {
		t.Errorf("the teaser should give way to the full changelog:\n%s", out)
	}
}

// About's Open-updater affordance stays visible while any product's update is
// pending, not only Sidecar's own.
func TestConfigUpdateStatus_AnyPending(t *testing.T) {
	m := &Model{}
	m.products = []version.Target{target(version.ProductTd, "td", "1.0.0", "1.1.0", true)}
	status := m.configUpdateStatus()
	if !status.AnyPending {
		t.Error("a pending td update must mark the status as any-pending")
	}
	m.products = nil
	if status := m.configUpdateStatus(); status.Available || status.AnyPending {
		t.Errorf("nothing pending should not claim work: %+v", status)
	}
}
