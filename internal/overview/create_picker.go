package overview

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/filefind"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacecreate"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// notesWanted mirrors assembly.NotesWanted, which this package cannot import:
// the Note row exists exactly when the Notes plugin does.
func (m *Model) notesWanted() bool {
	if m.config == nil {
		return false
	}
	return m.config.Plugins.TDMonitor.Enabled && features.IsEnabled(features.NotesPlugin.Name)
}

// configuredProviders is one kind row per enabled terminal-resource provider.
func (m *Model) configuredProviders() []workspacecreate.ProviderItem {
	if m.config == nil {
		return nil
	}
	var items []workspacecreate.ProviderItem
	for _, provider := range m.config.TerminalResources.EnabledProviders() {
		items = append(items, workspacecreate.ProviderItem{ID: provider.ID})
	}
	return items
}

type createPickerDataMsg struct {
	Refs   []workspaceops.DiffRef
	Issues []workspaceops.IssueRef
	Notes  []workspaceops.NoteRef
}

func (m *Model) loadCreatePickerData() tea.Cmd {
	root := ""
	if selected, ok := m.SelectedWorkspace(); ok {
		root = selected.Path
	}
	if root == "" && len(m.projects) > 0 {
		root = m.projects[0].Path
	}
	if root == "" {
		return nil
	}
	wantNotes := m.notesWanted()
	dir := root
	return func() tea.Msg {
		ctx := context.Background()
		msg := createPickerDataMsg{}
		if refs, err := workspaceops.RecentDiffRefs(ctx, dir, 15); err == nil {
			msg.Refs = refs
		}
		if issues, err := workspaceops.RecentIssues(ctx, dir, 30); err == nil {
			msg.Issues = issues
		}
		if wantNotes {
			if notes, err := workspaceops.RecentNotes(ctx, dir, 20); err == nil {
				msg.Notes = notes
			}
		}
		return msg
	}
}

func (m *Model) loadCreateFileCandidates() tea.Cmd {
	root := ""
	if selected, ok := m.SelectedWorkspace(); ok {
		root = selected.Path
	}
	if root == "" {
		return nil
	}
	dir := root
	return func() tea.Msg {
		paths, _ := filefind.ScanPaths(dir, false)
		return workspacecreate.FilesScannedMsg{Root: dir, Paths: paths}
	}
}

func toSuggestions(values []string) []workspacecreate.Suggestion {
	out := make([]workspacecreate.Suggestion, 0, len(values))
	for _, v := range values {
		out = append(out, workspacecreate.Suggestion{Value: v, Label: v})
	}
	return out
}

func applyPickerData(form *workspacecreate.Form, msg createPickerDataMsg) {
	if form == nil {
		return
	}
	form.SetDiffRefs(toSuggestions(func() []string {
		values := make([]string, 0, len(msg.Refs))
		for _, ref := range msg.Refs {
			values = append(values, ref.Identity+"  "+ref.Label)
		}
		return values
	}()))
	issues := make([]workspacecreate.Suggestion, 0, len(msg.Issues))
	for _, issue := range msg.Issues {
		badge := ""
		if issue.Status == "in_progress" {
			badge = "in progress"
		}
		label := strings.TrimSpace(issue.ID + "  " + issue.Title)
		issues = append(issues, workspacecreate.Suggestion{Value: issue.ID, Label: label, Badge: badge})
	}
	form.SetIssues(issues)
	notes := make([]workspacecreate.Suggestion, 0, len(msg.Notes))
	for _, note := range msg.Notes {
		label := note.ID
		if note.Title != "" {
			label = note.ID + "  " + note.Title
		}
		notes = append(notes, workspacecreate.Suggestion{Value: note.ID, Label: label})
	}
	form.SetNotes(notes)
}

func (m *Model) applyCreateFileCandidates(msg workspacecreate.FilesScannedMsg) {
	if m.createForm == nil {
		return
	}
	recent := make([]string, 0, 8)
	seen := make(map[string]bool)
	if doc := m.preview.doc; doc != nil {
		for _, item := range doc.tabs.Items {
			if item.View == nil {
				continue
			}
			rel := docview.NormalizeTabPath(item.View.Title())
			if rel == "" || seen[rel] {
				continue
			}
			seen[rel] = true
			recent = append(recent, rel)
		}
	}
	candidates := make([]string, 0, len(recent)+len(msg.Paths))
	candidates = append(candidates, recent...)
	for _, path := range msg.Paths {
		if !seen[path] {
			seen[path] = true
			candidates = append(candidates, path)
		}
	}
	m.createForm.SetFileCandidates(candidates)
}

// openPreviewTarget opens one resolved target on this surface's preview —
// the whole per-kind body of the ui-request open path, minus acknowledgements,
// so the pane switcher cannot drift from what `sidecar open` does here.
// Caller owns m.openSplit. ok reports whether anything is on screen afterwards:
// an open that only focuses an existing tab legitimately returns no command.
func (m *Model) openPreviewTarget(target uirequest.Target) (tea.Cmd, bool) {
	onScreen := func(kind panelayout.Kind) bool {
		return m.preview.deck != nil && m.preview.deck.Leaf(kind) != 0
	}
	var cmd tea.Cmd
	switch target.Kind {
	case uirequest.TargetKindFile:
		cmd = m.openPreviewDocTarget(target)
		shown := false
		if doc := m.preview.doc; doc != nil {
			if view := doc.view(); view != nil {
				shown = docview.NormalizeTabPath(view.Title()) == docview.NormalizeTabPath(target.Value)
			}
		}
		return cmd, shown || onScreen(panelayout.Document)
	case uirequest.TargetKindIssue:
		cmd = m.openPreviewIssue(target.Value)
		return cmd, onScreen(panelayout.Issue)
	case uirequest.TargetKindNote:
		cmd = m.openPreviewNote(target.Value)
		return cmd, onScreen(panelayout.Note)
	case uirequest.TargetKindDiff:
		root := ""
		if selected, ok := m.SelectedWorkspace(); ok {
			root = selected.Path
		}
		cmd = m.openPreviewDiff(uirequest.DiffTarget(root, target.Value))
		return cmd, onScreen(panelayout.Diff)
	case uirequest.TargetKindResource:
		ref, refusal := resourceview.ReferenceForLocator(m.resourceMatchers, target.Provider, target.Value)
		if refusal != "" {
			return nil, false
		}
		cmd = m.OpenPreviewResource(ref)
		return cmd, onScreen(panelayout.Resource)
	default:
		return nil, false
	}
}

// submitPaneTargetForm resolves the picker step's answer and opens it through
// this surface's own open paths with the recorded placement as axis override.
func (m *Model) submitPaneTargetForm() tea.Cmd {
	if m.createForm == nil {
		return nil
	}
	selected, ok := m.SelectedWorkspace()
	if !ok {
		m.setCreateError("Select a workspace to open beside")
		return nil
	}
	target, err := m.createForm.TargetFor(selected.Path)
	if err != nil {
		m.setCreateError(err.Error())
		return nil
	}
	placement := m.createForm.PlacementSplit()
	m.closeCreateShell()
	prevSplit := m.openSplit
	m.openSplit = placement
	defer func() { m.openSplit = prevSplit }()
	cmd, opened := m.openPreviewTarget(target)
	if !opened {
		return appmsg.ShowToast("the window is too small to split", 3*time.Second)
	}
	return cmd
}
