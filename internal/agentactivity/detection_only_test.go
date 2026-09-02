package agentactivity

import (
	"sort"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity/manifests"
	"github.com/marcus/sidecar/internal/agentcatalog"
)

// unregisteredManifests are the vendored Herdr manifests Sidecar deliberately
// has no family for. Each entry carries the reason, because "we have not got to
// it yet" and "we decided not to" are different facts and only the second one
// should survive a sync without being re-argued.
var unregisteredManifests = map[string]string{
	"gemini": "Decision 4: Antigravity replaced it and `agy` is already a full family. " +
		"The manifest stays vendored because the sync mirrors the whole catalog, so registering it later is one alias line.",
}

// The vendored manifest set and the registered families are one relationship
// held in two files, and a sync moves the first without touching the second.
// This is what makes an addition upstream a decision here rather than a manifest
// nobody notices: a new agent in Herdr's catalog fails this test until someone
// either registers it or records why not.
//
// It is also the exit gate of Phase 4, stated as a test. Herdr vendors 21 screen
// manifests; Sidecar registers 20 of them, ten launchable and ten
// detection-only, and declines exactly one.
func TestEveryVendoredManifestIsRegisteredOrDeclaredUnregistered(t *testing.T) {
	lock, err := manifests.LoadLock()
	if err != nil {
		t.Fatalf("load upstream.lock.json: %v", err)
	}

	registered := map[string]string{}
	for _, family := range agentcatalog.Families() {
		registered[HerdrAgentLabel(family.ID)] = "launchable"
	}
	for _, family := range agentcatalog.DetectionFamilies() {
		registered[HerdrAgentLabel(family.ID)] = "detection-only"
	}

	vendored := map[string]bool{}
	for _, agent := range lock.Agents {
		vendored[agent.ID] = true
		kind, ok := registered[agent.ID]
		if ok {
			if !Supports(sidecarFamilyForLabel(t, agent.ID)) {
				t.Errorf("%s manifest is vendored and registered as %s, but Supports says no", agent.ID, kind)
			}
			continue
		}
		if _, declared := unregisteredManifests[agent.ID]; !declared {
			t.Errorf("vendored manifest %q has no Sidecar family and no entry in unregisteredManifests.\n"+
				"A synced-in agent is a decision: register it in agentcatalog (detection-only is one line and\n"+
				"costs no theme work), or record here why Sidecar declines it.", agent.ID)
		}
	}

	for label, kind := range registered {
		if !vendored[label] {
			t.Errorf("family %q (%s) has no vendored manifest; its panes would report %s.manifest-unavailable", label, kind, label)
		}
	}
	for id := range unregisteredManifests {
		if !vendored[id] {
			t.Errorf("unregisteredManifests names %q, which is no longer vendored; drop the entry", id)
		}
	}

	if got, want := len(lock.Agents), len(registered)+len(unregisteredManifests); got != want {
		t.Errorf("lock pins %d manifests; %d registered plus %d declared unregistered = %d", got, len(registered), len(unregisteredManifests), want)
	}
}

// sidecarFamilyForLabel inverts HerdrAgentLabel over both halves of the catalog.
func sidecarFamilyForLabel(t *testing.T, label string) string {
	t.Helper()
	for _, family := range append(agentcatalog.Families(), agentcatalog.DetectionFamilies()...) {
		if HerdrAgentLabel(family.ID) == label {
			return family.ID
		}
	}
	t.Fatalf("no Sidecar family has Herdr label %q", label)
	return ""
}

// The whole of a detection-only family's live path, end to end: the process name
// resolves to the family, the family is supported, the gate admits it, and the
// vendored manifest produces a verdict. Journey 4 of the plan is this test.
func TestDetectionOnlyFamiliesClassifyThroughTheirVendoredManifest(t *testing.T) {
	for _, family := range agentcatalog.DetectionFamilies() {
		t.Run(family.ID, func(t *testing.T) {
			if got := Identify(Observation{CurrentCommand: family.ID}); got != family.ID {
				t.Fatalf("Identify(%q) = %q", family.ID, got)
			}
			ob := Observation{Agent: family.ID, CurrentCommand: family.ID, Screen: "\n"}
			result, explain := DetectManifest(ob)
			if explain == nil {
				t.Fatalf("no manifest evaluated: %s", result.Evidence)
			}
			if len(explain.EvaluatedRules) == 0 {
				t.Fatalf("%s.toml evaluated no rules", family.ID)
			}
			// A blank screen matches nothing, so every one of them lands on the
			// conservative fallback rather than on an invented verdict.
			if result.State != StateIdle || result.Evidence != family.ID+".known-live-fallback" || !result.FallbackIdle {
				t.Fatalf("blank screen got %+v, want the low-evidence idle fallback", result)
			}
		})
	}
}

// The gate is the only refusal a detection-only family has, so it has to be a
// real one: a pane running a shell, or running another agent, must not have this
// family's manifest evaluated against it.
func TestDetectionOnlyFamiliesRefuseAPaneRunningSomethingElse(t *testing.T) {
	for _, family := range agentcatalog.DetectionFamilies() {
		for _, command := range []string{"zsh", "node", "claude", ""} {
			got := Detect(Observation{Agent: family.ID, CurrentCommand: command, Screen: "⠋ Working\n"})
			if got.State != StateUnknown || got.Evidence != family.ID+".process-mismatch" {
				t.Errorf("Detect(%s on %q) = %+v, want %s.process-mismatch", family.ID, command, got, family.ID)
			}
		}
	}
}

// Two mappings and one vocabulary: a detection-only family's id is Herdr's own
// label, so neither mapping may need a case for it. A family that did would be
// carrying a Sidecar-only spelling for no gain, and the loader keys on the file
// name, so a missed case reads the wrong manifest rather than failing.
func TestDetectionOnlyFamilyIDsAreHerdrsOwnLabels(t *testing.T) {
	for _, family := range agentcatalog.DetectionFamilies() {
		if got := ManifestAgentID(family.ID); got != family.ID {
			t.Errorf("ManifestAgentID(%q) = %q; a detection-only family must not need a manifest-id mapping", family.ID, got)
		}
		if got := HerdrAgentLabel(family.ID); got != family.ID {
			t.Errorf("HerdrAgentLabel(%q) = %q; a detection-only family must not need a label mapping", family.ID, got)
		}
		if !HasVendoredManifest(family.ID) {
			t.Errorf("no vendored manifest compiles for %q", family.ID)
		}
	}
}

// The ids and display names, printed once so a reviewer can read the twenty
// families and the one declared exclusion without opening three files.
func TestDetectionOnlyRoster(t *testing.T) {
	var rows []string
	for _, family := range agentcatalog.Families() {
		rows = append(rows, "  launchable      "+pad(family.ID)+family.Name)
	}
	for _, family := range agentcatalog.DetectionFamilies() {
		rows = append(rows, "  detection-only  "+pad(family.ID)+family.Name)
	}
	sort.Strings(rows)
	for id, why := range unregisteredManifests {
		rows = append(rows, "  unregistered    "+pad(id)+why)
	}
	t.Log("Herdr screen-manifest agents and what Sidecar does with each:\n" + strings.Join(rows, "\n"))
}

func pad(id string) string {
	if len(id) >= 14 {
		return id + " "
	}
	return id + strings.Repeat(" ", 14-len(id))
}
