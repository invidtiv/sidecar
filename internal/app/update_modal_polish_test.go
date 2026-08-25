package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/version"
)

// --- columns (modal-redesign.md column behaviour) ---------------------------

func TestTwoColumnRow(t *testing.T) {
	fits := twoColumnRow("td 1.0.0 → 1.1.0", "Homebrew", 40)
	if w := lipgloss.Width(fits); w != 40 {
		t.Fatalf("fitting row width = %d, want 40: %q", w, fits)
	}
	if !strings.HasSuffix(fits, "Homebrew") {
		t.Errorf("secondary must sit flush right: %q", fits)
	}
	if !strings.HasPrefix(fits, "td 1.0.0 → 1.1.0") {
		t.Errorf("primary must stay whole when there is room: %q", fits)
	}

	tight := twoColumnRow(strings.Repeat("a", 60), "manual", 30)
	if lipgloss.Width(tight) != 30 {
		t.Errorf("tight row must hold width, got %d: %q", lipgloss.Width(tight), tight)
	}
	if !strings.Contains(tight, "…") {
		t.Errorf("the left side truncates before the right is sacrificed: %q", tight)
	}
	if !strings.HasSuffix(tight, "manual") {
		t.Errorf("the secondary survives truncation: %q", tight)
	}

	narrow := twoColumnRow("sidecar 0.95.0 → 0.96.0", "Homebrew", 14)
	if strings.Count(narrow, "\n") != 1 {
		t.Errorf("below the minimum left width the row stacks instead of clipping: %q", narrow)
	}
	if !strings.Contains(narrow, "sidecar 0.95.0 → 0.96.0") {
		t.Errorf("the stacked form keeps the primary value whole: %q", narrow)
	}
}

// --- one-line key-chip actions ----------------------------------------------

// Every phase collapses its buttons and hint line into one footer-styled chip
// line. Installing keeps the honest no-cancel wording and offers no update
// affordance at all — the batch owns the surface until it settles.
func TestUpdateChips_PerPhase(t *testing.T) {
	u := &updateUIState{phase: UpdateModalPreview, anyManaged: true}
	chips, suffix := updateChips(u)
	if suffix != "" || len(chips) != 2 ||
		chips[0].ID != "update" || chips[0].Keys != "[enter/u]" ||
		chips[1].ID != "cancel" {
		t.Fatalf("preview chips = %+v suffix %q", chips, suffix)
	}

	u = &updateUIState{phase: UpdateModalProgress, retryBatch: true}
	chips, suffix = updateChips(u)
	if len(chips) != 1 || chips[0].ID != "cancel" {
		t.Fatalf("installing must offer only the hide chip, got %+v", chips)
	}
	if !strings.Contains(suffix, "update continues") {
		t.Errorf("installing suffix should say the update continues: %q", suffix)
	}

	u.phase = UpdateModalComplete
	u.restartRequired = true
	chips, _ = updateChips(u)
	if len(chips) != 2 || chips[0].ID != "quit" || !strings.Contains(chips[0].Label, "Quit & Restart") {
		t.Fatalf("restart-required complete = %+v", chips)
	}

	u.restartRequired = false
	chips, _ = updateChips(u)
	if len(chips) != 1 || chips[0].ID != "cancel" {
		t.Fatalf("plain complete should close only, got %+v", chips)
	}

	u.phase = UpdateModalError
	u.retryCount = 1
	chips, _ = updateChips(u)
	if len(chips) != 2 || chips[0].ID != "retry" {
		t.Fatalf("error with failures should pair Retry with Close, got %+v", chips)
	}
}

// The rendered line carries the footer chip style verbatim, registers real
// hit regions per action, and never says cancel during an install.
func TestUpdateChips_RenderedLineAndRegions(t *testing.T) {
	m := &Model{width: 100, height: 40}
	m.products = []version.Target{target(version.ProductTd, "td", "1.0.0", "1.1.0", true)}
	m.openUpdateModal()
	out := plainText(renderUpdatePhase(m))
	if !strings.Contains(out, "[enter/u]") || !strings.Contains(out, "Update") {
		t.Errorf("preview line missing its chips:\n%s", out)
	}
	regions := map[string]bool{}
	for _, r := range m.updateMouseHandler.HitMap.Regions() {
		regions[r.ID] = true
	}
	if !regions["update"] || !regions["cancel"] {
		t.Errorf("preview must register clickable regions for both chips: %v", regions)
	}

	inst := modelWithBatch([]version.Target{target(version.ProductTasks, "Tasks", "1.5.0", "1.6.0", true)})
	lowered := strings.ToLower(plainText(renderUpdatePhase(inst)))
	if !strings.Contains(lowered, "[esc]") || !strings.Contains(lowered, "hide") ||
		!strings.Contains(lowered, "update continues") {
		t.Errorf("installing line = the honest no-cancel one-liner:\n%s", lowered)
	}
	for _, region := range inst.updateMouseHandler.HitMap.Regions() {
		if region.ID == "update" || region.ID == "retry" {
			t.Errorf("installing registered an update affordance: %s", region.ID)
		}
	}
}

// --- library-derived width ---------------------------------------------------

func TestUpdateModal_WidthFromLibrary(t *testing.T) {
	all := []version.Target{
		target(version.ProductSidecar, "Sidecar", "0.95.0", "0.96.0", true),
		target(version.ProductTd, "td", "1.0.0", "1.1.0", true),
	}
	phases := []UpdateModalState{
		UpdateModalPreview, UpdateModalProgress, UpdateModalComplete, UpdateModalError,
	}

	var want int
	for i, phase := range phases {
		m := &Model{width: 200, height: 50, products: all}
		m.updateModalState = phase
		if phase == UpdateModalComplete {
			m.updateCarried = []version.Result{{Target: all[0], Status: version.StatusUpdated, Version: "0.96.0"}}
			m.needsRestart = true
		}
		out := renderUpdatePhase(m)
		w := lipgloss.Width(out)
		if w > modal.MaxModalWidth {
			t.Errorf("%v renders %d wide, over the library maximum %d", phase, w, modal.MaxModalWidth)
		}
		if w < modal.MinModalWidth {
			t.Errorf("%v renders %d wide, under the library minimum %d", phase, w, modal.MinModalWidth)
		}
		if i == 0 {
			want = w
		} else if w != want {
			t.Errorf("%v width = %d, but the journey started at %d: one width for the whole journey", phase, w, want)
		}
	}

	small := &Model{width: 44, height: 24}
	if got := updateModalWidthFor(small.width); got > small.width-2*modal.DefaultMarginX && got != 1 {
		t.Errorf("narrow terminal width %d leaves no margin ring: box wants %d", small.width, got)
	}
}

// --- one action line, no false cancel ----------------------------------------

// The chip line replaces both the stacked buttons and the old muted hint
// line: every phase states its actions in the footer style, and none of them
// — installing above all — may promise a cancel that does not exist.
func TestUpdateChips_NoFalseCancelFooterStyle(t *testing.T) {
	plan := []version.Target{target(version.ProductTd, "td", "1.0.0", "1.1.0", true)}
	phases := map[UpdateModalState][]string{
		UpdateModalPreview:  {"[enter/u]", "Update", "[esc]", "Close"},
		UpdateModalProgress: {"[esc]", "hide", "update continues"},
		UpdateModalComplete: {"[esc]", "Close"},
		UpdateModalError:    {"[enter]", "Retry", "[esc]", "Close"},
	}
	for phase := range phases {
		m := &Model{width: 100, height: 40, products: plan}
		m.updateModalState = phase
		if phase == UpdateModalError {
			m.updateCarried = []version.Result{{Target: plan[0], Status: version.StatusFailed,
				Err: errors.New("boom")}}
		}
		out := strings.ToLower(plainText(renderUpdatePhase(m)))
		for _, want := range phases[phase] {
			if !strings.Contains(out, strings.ToLower(want)) {
				t.Errorf("%v line missing %q:\n%s", phase, want, out)
			}
		}
		if strings.Contains(out, "cancel") {
			t.Errorf("%v leaked a cancel affordance:\n%s", phase, out)
		}
		if strings.Contains(out, "tab to switch") {
			t.Errorf("%v fell back to the library default hint style:\n%s", phase, out)
		}
		if !strings.Contains(strings.ToLower(plainText(renderUpdatePhase(m))), "[") {
			t.Errorf("%v lost the key-chip styling", phase)
		}
	}
}

// --- notes section: bar, wheel ownership, in-place expansion -----------------

func notesFixtureModel(t *testing.T) *Model {
	t.Helper()
	m := &Model{width: 100, height: 42}
	td := target(version.ProductTd, "td", "1.0.0", "1.1.0", true)
	td.Notes = strings.Repeat("- changelog entry\n", 120)
	m.products = []version.Target{td}
	m.openUpdateModal()
	rendered := renderUpdatePhase(m)
	if rendered == "" {
		t.Fatal("preview did not render")
	}
	return m
}

func TestUpdateNotes_ScrollsAndExpandsInPlace(t *testing.T) {
	m := notesFixtureModel(t)
	u := m.updateUIState()

	if !u.notesPresent || u.notesVisible != notesCollapsedRows {
		t.Fatalf("collapsed window should show %d lines, state says present=%v visible=%d",
			notesCollapsedRows, u.notesPresent, u.notesVisible)
	}
	if u.notesTotal <= u.notesVisible {
		t.Fatalf("fixture should overflow: total=%d visible=%d", u.notesTotal, u.notesVisible)
	}

	// Wheel scrolls the notes, clamped at both ends.
	m.scrollUpdateNotes(3)
	if u.notesScroll != 3 {
		t.Fatalf("wheel down moved offset to %d, want 3", u.notesScroll)
	}
	m.scrollUpdateNotesTo(1 << 20)
	bottom := u.notesScroll
	if bottom != u.notesTotal-u.notesVisible {
		t.Fatalf("offset %d should clamp to %d", bottom, u.notesTotal-u.notesVisible)
	}
	if !m.updateNotesAtBoundary(3) {
		t.Error("at the bottom the boundary query must report bounded downward")
	}
	if m.updateNotesAtBoundary(-3) {
		t.Error("at the bottom there must still be room upward")
	}
	m.scrollUpdateNotes(-1 << 20)
	if u.notesScroll != 0 {
		t.Fatalf("offset should clamp back to 0, got %d", u.notesScroll)
	}

	// Expansion grows the window without changing the modal's width.
	before2 := lipgloss.Width(renderUpdatePhase(m))
	_, _ = m.applyUpdateAction("toggle-notes", nil)
	if !u.notesExpanded {
		t.Fatal("toggle-notes should expand the section")
	}
	if expanded := renderUpdatePhase(m); lipgloss.Width(expanded) != before2 {
		t.Errorf("expansion changed the box width from %d to %d", before2, lipgloss.Width(expanded))
	} else if u.notesVisible != modal.PreferredListRows(m.height) {
		t.Errorf("expanded window = %d rows, want the preferred list height %d",
			u.notesVisible, modal.PreferredListRows(m.height))
	}
	if out := renderUpdatePhase(m); !strings.Contains(out, "Collapse changelog") {
		t.Errorf("expanded section should offer collapse:\n%s", out)
	}
}

func TestUpdateNotes_BarRegionsRegisteredAndClaimed(t *testing.T) {
	m := notesFixtureModel(t)
	handler := m.updateMouseHandler

	foundThumb, foundTrack := false, false
	for _, r := range handler.HitMap.Regions() {
		switch r.ID {
		case modal.RegionScrollbarThumb:
			foundThumb = true
		case modal.RegionScrollbarTrack:
			foundTrack = true
		}
	}
	if !foundThumb || !foundTrack {
		t.Fatalf("overflowing notes must register bar regions: thumb=%v track=%v", foundThumb, foundTrack)
	}

	// A press on the track starts a gesture owned by this surface.
	var trackY int
	foundTrack = false
	for _, r := range handler.HitMap.Regions() {
		if r.ID == modal.RegionScrollbarTrack {
			trackY = r.Rect.Y + r.Rect.H/2
			foundTrack = true
			break
		}
	}
	if !foundTrack {
		t.Fatal("no track region to press")
	}
	x := 0
	for _, r := range handler.HitMap.Regions() {
		if r.ID == modal.RegionScrollbarThumb || r.ID == modal.RegionScrollbarTrack {
			x = r.Rect.X
			break
		}
	}
	handled, _ := m.updateNotesBarEvent(tea.MouseClickMsg{X: x, Y: trackY, Button: tea.MouseLeft})
	if !handled {
		t.Fatal("pressing the notes track must be claimed by the notes bar")
	}
	if !handler.IsDragging() {
		t.Error("a track press starts (or continues) a drag gesture")
	}
}

// --- diagnostics key-chip action line ----------------------------------------

func TestDiagnosticsChips_MouseReachable(t *testing.T) {
	m := &Model{}
	m.products = []version.Target{target(version.ProductTd, "td", "1.0.0", "1.1.0", true)}

	res := m.renderDiagnosticsChips(40, "", "")
	if !strings.Contains(res.Content, "[u]") || !strings.Contains(res.Content, "Update") {
		t.Fatalf("with a pending update diagnostics should offer a [u] Update chip, got %q", res.Content)
	}
	if !strings.Contains(res.Content, "[esc]") || !strings.Contains(res.Content, "Close") {
		t.Fatalf("the Close chip must sit on the same line, got %q", res.Content)
	}
	ids := make([]string, 0, len(res.Focusables))
	for _, f := range res.Focusables {
		ids = append(ids, f.ID)
	}
	if len(ids) != 2 || ids[0] != "update" || ids[1] != "close" {
		t.Fatalf("chips must register their hit targets in order, got %+v", ids)
	}

	// Without a pending update the line is Close only — no dead Update chip.
	empty := &Model{}
	if res := empty.renderDiagnosticsChips(40, "", ""); !strings.Contains(res.Content, "Close") ||
		strings.Contains(res.Content, "Update") || len(res.Focusables) != 1 || res.Focusables[0].ID != "close" {
		t.Errorf("no pending update must mean a lone Close chip, got %q %+v", res.Content, res.Focusables)
	}
}
