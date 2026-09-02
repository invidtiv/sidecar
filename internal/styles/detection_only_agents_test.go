package styles

import (
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
// not a crash or an empty style. AgentLabel answers the bare provider name where
// there is no icon. Both are the graceful degradation the plan claims, and both
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
// so AgentIcon answers for it while the other nine fall through to the bare
// name. Nothing was added for it here and nothing needs to be: both answers
// render.
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
		if got := AgentLabel(family.ID); got != family.ID {
			t.Errorf("AgentLabel(%q) = %q, want the bare name", family.ID, got)
		}
	}
}
