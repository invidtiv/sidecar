package styles

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentcatalog"
)

// Phase 4 of docs/plans/active/herdr-detection-parity.md registered ten
// detection-only agent families, and its whole scope argument is that they cost
// no presentation work: no curated colour in twenty themes, no website palette
// entry, no icon that has to match a conversation adapter. That argument rests
// on how this package answers for a provider nothing here has heard of, so it is
// pinned rather than assumed.
//
// AgentColor answers TextMuted, in every theme, which is a real chip colour and
// not a crash or an empty style. AgentLabel answers the family's own name, with
// a glyph only where one already exists. Both are the graceful degradation the
// plan claims, and both
// stop being true the moment somebody adds a partial entry -- a colour in one
// theme and not the rest -- which is exactly what this catches.
func TestDetectionOnlyAgentsRenderWithNoThemeWork(t *testing.T) {
	original := GetCurrentThemeName()
	t.Cleanup(func() { ApplyTheme(original) })

	families := agentcatalog.DetectionFamilies()
	if len(families) == 0 {
		t.Fatal("no detection-only families to check")
	}

	for _, theme := range ListThemes() {
		ApplyTheme(theme)
		for _, family := range families {
			if got := AgentColor(family.ID); got != TextMuted {
				t.Errorf("theme %s: AgentColor(%q) = %v, want TextMuted.\n"+
					"A detection-only family has no curated colour by design. A colour here means one theme "+
					"gained an entry the other %d did not, which is the drift the curated-palette tests exist to stop.",
					theme, family.ID, got, len(ListThemes())-1)
			}
			if AgentLabel(family.ID) == "" {
				t.Errorf("theme %s: AgentLabel(%q) is empty; a detected pane would render no agent chip at all", theme, family.ID)
			}
		}
	}
}

// The one detection-only family that already has an icon, recorded so it is not
// mistaken for the rule. Kiro has a conversation-history adapter
// (internal/adapter/kiro) that predates this phase and shipped a glyph with it,
// so AgentIcon answers for it while the other nine render with no glyph.
// Nothing was added for it here and nothing needs to be: both answers render.
func TestOnlyKiroAmongDetectionOnlyFamiliesAlreadyHasAnIcon(t *testing.T) {
	for _, family := range agentcatalog.DetectionFamilies() {
		icon := AgentIcon(family.ID)
		if family.ID == "kiro" {
			if icon != "κ" {
				t.Errorf("AgentIcon(kiro) = %q, want κ from the Kiro conversations adapter", icon)
			}
			if got := AgentLabel("kiro"); got != "κ kiro" {
				t.Errorf("AgentLabel(kiro) = %q, want %q", got, "κ kiro")
			}
			continue
		}
		if icon != "" {
			t.Errorf("AgentIcon(%q) = %q; a detection-only family with an icon and no conversations adapter "+
				"breaks the rule TestAgentIconMatchesConversationsAdapters keeps", family.ID, icon)
		}
	}
}

// A detected pane renders the family's own name, not the id its manifest file
// happens to be called.
//
// This is the half the first pass missed. Ten families were registered with a
// Name and a Short and nothing read either: agentcatalog.Label knew only the
// launchable list and fell through to the raw id, and AgentLabel rendered the
// raw id too. Qoder is the family that shows it, because it is the only one
// whose id is not a name -- `qodercli` is Herdr's manifest label, kept as the id
// on purpose so the vendored file, the alias table and `agent explain --agent`
// all agree with no mapping. A chip is a width-constrained lowercase token, so
// it takes Short; a settings row or any other prose surface takes Label.
func TestDetectionOnlyPanesRenderTheirDisplayNameNotTheirManifestID(t *testing.T) {
	if got, want := AgentLabel("qodercli"), "qoder"; got != want {
		t.Errorf("AgentLabel(qodercli) = %q, want %q; the chip is naming a manifest file rather than a program", got, want)
	}
	if got, want := agentcatalog.Label("qodercli"), "Qoder CLI"; got != want {
		t.Errorf("agentcatalog.Label(qodercli) = %q, want %q", got, want)
	}
	for _, family := range agentcatalog.DetectionFamilies() {
		if got, want := AgentLabel(family.ID), strings.ToLower(family.Short); !strings.HasSuffix(got, want) {
			t.Errorf("AgentLabel(%q) = %q, want it to end in the family's short name %q", family.ID, got, want)
		}
		if got := agentcatalog.Label(family.ID); got != family.Name {
			t.Errorf("agentcatalog.Label(%q) = %q, want %q; the display name registered for this family is read by nothing",
				family.ID, got, family.Name)
		}
	}
}

// The launchable ten keep the token they have always rendered. Routing
// AgentLabel through the catalog was only safe because Short lowercased is
// already the id for every one of them, and that equality is an accident of the
// data rather than a rule, so it is pinned: a family that breaks it renames a
// chip on every workspace row and every Sessions card.
func TestLaunchableFamiliesKeepTheirChipToken(t *testing.T) {
	for _, family := range agentcatalog.Families() {
		want := family.ID
		if icon := AgentIcon(family.ID); icon != "" {
			want = icon + " " + family.ID
		}
		if got := AgentLabel(family.ID); got != want {
			t.Errorf("AgentLabel(%q) = %q, want %q", family.ID, got, want)
		}
	}
}
