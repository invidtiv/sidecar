package app

import (
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

// --- disabled buttons during install ----------------------------------------

func TestUpdateButtons_DisabledDuringInstall(t *testing.T) {
	u := &updateUIState{phase: UpdateModalProgress}
	btns := updateButtons(u)
	if len(btns) != 1 || btns[0].ID != "update" || !btns[0].Disabled {
		t.Fatalf("an update batch shows a disabled Update Now, got %+v", btns)
	}

	u.retryBatch = true
	btns = updateButtons(u)
	if len(btns) != 1 || btns[0].ID != "retry" || !btns[0].Disabled {
		t.Fatalf("a retry batch shows a disabled Retry, got %+v", btns)
	}

	u.phase = UpdateModalError
	for _, b := range updateButtons(u) {
		if b.Disabled {
			t.Errorf("settled phases must not disable their actions: %+v", b)
		}
	}

	// Disabled means inert: no hit region to click, and Enter cannot reach it.
	m := modelWithBatch([]version.Target{target(version.ProductTasks, "Tasks", "1.5.0", "1.6.0", true)})
	out := renderUpdatePhase(m)
	if !strings.Contains(out, "Update Now") {
		t.Errorf("installing surface should show its launching action, disabled:\n%s", out)
	}
	handler := m.updateMouseHandler
	for _, r := range handler.HitMap.Regions() {
		if r.ID == "update" || r.ID == "retry" {
			t.Errorf("disabled button registered a hit region: %s", r.ID)
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

// --- one hint style -----------------------------------------------------------

func TestUpdateHint_OneStyleNoFalseCancel(t *testing.T) {
	plan := []version.Target{target(version.ProductTd, "td", "1.0.0", "1.1.0", true)}
	phases := map[UpdateModalState]string{
		UpdateModalPreview:  "enter update",
		UpdateModalProgress: "esc hides · update continues",
		UpdateModalError:    "enter retry · esc close",
	}
	for phase := range phases {
		m := &Model{width: 100, height: 40, products: plan}
		m.updateModalState = phase
		out := strings.ToLower(renderUpdatePhase(m))
		if !strings.Contains(out, phases[phase]) {
			t.Errorf("%v hint missing %q:\n%s", phase, phases[phase], out)
		}
		if strings.Contains(out, "cancel") {
			t.Errorf("%v leaked a cancel hint:\n%s", phase, out)
		}
		if strings.Contains(out, "tab to switch") {
			t.Errorf("%v fell back to the library default hint style:\n%s", phase, out)
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

// --- diagnostics Update button (decision 5) ----------------------------------

func TestDiagnosticsUpdateButton_MouseReachable(t *testing.T) {
	m := &Model{}
	m.products = []version.Target{target(version.ProductTd, "td", "1.0.0", "1.1.0", true)}

	section := m.diagnosticsUpdateButton()
	res := section.Render(40, "", "")
	if !strings.Contains(res.Content, "Update") {
		t.Fatalf("with a pending update diagnostics should offer an Update button, got %q", res.Content)
	}
	if len(res.Focusables) != 1 || res.Focusables[0].ID != "update" {
		t.Fatalf("the button must register its hit target as %q, got %+v", "update", res.Focusables)
	}

	// Without a pending update there is no button to press.
	empty := &Model{}
	if res := empty.diagnosticsUpdateButton().Render(40, "", ""); res.Content != "" || len(res.Focusables) != 0 {
		t.Errorf("no pending update must mean no Update button, got %q %+v", res.Content, res.Focusables)
	}
}
