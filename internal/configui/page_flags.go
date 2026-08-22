package configui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
)

const regionFlag = "config-flag-"

// Feature Flags is the whole flag registry, one switch per flag, in the shape
// Panels & Integrations uses for surfaces. It is a page of its own rather than
// a section on Advanced because the Configuration detail pane does not scroll —
// clampLines truncates — so a list that grows with the registry cannot share a
// page with anything a user still needs to reach.
//
// The list is derived from internal/features rather than curated here. A
// hand-picked list is how tmux_interactive_input, tmux_inline_edit,
// files_auto_refresh, plugin_content_panes and terminal_resource_providers all
// ended up settable only by editing config.json.

func (m *Model) buildFlags(b *paneBuilder) {
	b.text(PaneTitle(PageTitle(PageFlags)), "")
	b.lead("Every feature flag Sidecar knows about. A flag that is off is off for this build, not missing.")
	// Above the list, not below it. Appended after twelve rows this was the
	// first thing the pane's height clamp cut, so a user toggling a
	// startup-scoped flag on a normal-sized terminal saw the success toast and
	// never the sentence telling them it needs a restart.
	if m.restartNote != "" {
		b.blank()
		b.note(m.restartNote)
	}
	b.blank()

	// Flags another page owns sort last, so the switches that work here are not
	// interleaved with rows that only point elsewhere.
	items := previews()
	owned := make([]preview, 0, len(items))
	switches := make([]preview, 0, len(items))
	for _, item := range items {
		if item.owner != "" {
			owned = append(owned, item)
			continue
		}
		switches = append(switches, item)
	}

	// No blank line between rows: the list has to stay shorter than the pane at
	// ordinary terminal heights, because the pane truncates and the row cursor
	// walks onto rows that were cut. Each row is one line and the focused one
	// explains itself.
	for _, item := range switches {
		m.previewRow(b, item)
	}

	if len(owned) > 0 {
		b.blank()
		b.text(SectionHeader("Set on other pages"))
		for _, item := range owned {
			m.previewRow(b, item)
		}
	}
}

// preview is one feature flag offered on Feature Flags.
type preview struct {
	// flag is the feature name in features.flags.
	flag string
	// label is what the user reads.
	label string
	// help is the input-aligned explanation under the control.
	help string
	// restart marks a flag whose consumer reads it once at startup, so the
	// change is real but not visible until Sidecar is restarted. It is set per
	// flag from what actually consumes it, never as blanket caution.
	restart bool
	// note is an honest scope line for a flag that applies live but not
	// retroactively.
	note string
	// owner names the page that already offers this flag as a first-class
	// setting. A flag with an owner is listed here read-only, with a jump to
	// the control that owns it, because a second toggle is how two surfaces
	// start disagreeing: Panels pairs conversations_plugin with the plugin's
	// own enabled key, and a raw flag switch here would set one and not the
	// other. This is the rule FocusNotesPreference already follows in the
	// other direction.
	owner PageID
	// ownerControl is the control id on that page to put the cursor on.
	ownerControl string
	// reads answers what this row should show, when the raw flag is not it.
	// Panels' Conversations switch is the flag AND the plugin's own enabled
	// key, and toggleConversations clears only the plugin key on the way off —
	// so the flag stays true and a row reading it alone renders ON next to a
	// Panels page rendering OFF. The row is labelled with the panel's name, so
	// it owes the panel's answer. Nil means the flag itself.
	reads func(*Model) bool
}

// state is what the row reports: the owning surface's answer where one exists,
// the flag otherwise.
func (p preview) state(m *Model) bool {
	if p.reads != nil {
		return p.reads(m)
	}
	return m.flagEnabled(p.flag)
}

// previewCopy is the hand-written presentation for a flag, keyed by flag name.
// A flag absent from this map still appears on the page — see previews — using
// the registry's own name and description, so registering a feature is all it
// takes to make it reachable. The entries here are the ones worth saying more
// about than the registry does.
//
// Restart accuracy, checked against each flag's consumers rather than applied
// as blanket caution:
//   - cross_project_overview is read once in app.New to decide whether the
//     cross-project surface is constructed at all → restart.
//   - terminal_resource_providers gates a describe pass that runs once after
//     the first ready frame (app.describeResourceProvidersCmd) → restart.
//   - workspace_doc_panes is checked live wherever a pane or a diff is opened
//     (workspace.Plugin, internal/overview) → immediate.
//   - tmux_full_attach is checked live, but a terminal resolves its chords when
//     it is created (app.TerminalConfig) → immediate, next terminal.
//   - workspace_terminal_panel is checked live every time the split panel is
//     shown or toggled → immediate.
//   - tmux_interactive_input, tmux_inline_edit, files_auto_refresh and
//     plugin_content_panes are all read at the point of use → immediate.
var previewCopy = map[string]preview{
	features.CrossProjectOverview.Name: {
		label:   "Cross-project Activity",
		help:    "Show workspaces from every configured project in Activity.",
		restart: true,
	},
	features.WorkspaceDocPanes.Name: {
		label: "Document panes",
		help:  "Open files, issues, and diffs beside your active workspace.",
	},
	features.TmuxFullAttach.Name: {
		label: "Full tmux attach",
		help:  "Hand the terminal over to tmux's native client and shortcuts.",
		note:  "Applies to terminals opened after the change, and unlocks the attach chord on Terminal.",
	},
	features.WorkspaceTerminalPanel.Name: {
		label: "Split workspace terminal",
		help:  "Show a dedicated terminal next to the workspace list.",
	},
	features.TmuxInteractiveInput.Name: {
		label: "Type into terminals",
		help:  "Send keystrokes to tmux panes instead of showing them read-only.",
	},
	features.TmuxInlineEdit.Name: {
		label: "Inline file editing",
		help:  "Edit a previewed file in place instead of opening an external editor.",
	},
	features.FilesAutoRefresh.Name: {
		label: "Auto-refresh files",
		help:  "Watch expanded directories and refresh the tree when they change on disk.",
	},
	features.PluginContentPanes.Name: {
		label: "Plugin content panes",
		help:  "Open passive content panes beside Files, Git, Notes, and the embedded issue hosts.",
	},
	features.TerminalResourceProviders.Name: {
		label:   "Terminal resource providers",
		help:    "Recognize and open resources from configured external providers.",
		restart: true,
		note:    "Providers are described once at startup; turning this off stops every provider process.",
	},
	features.NotesPlugin.Name: {
		label:        "Notes panel",
		help:         "Capture quick notes in a Sidecar panel.",
		owner:        PagePanels,
		ownerControl: regionPanel + panelIDNotes,
	},
	features.TasksPlugin.Name: {
		label:        "Tasks panel",
		help:         "Show the embedded Tasks tab.",
		owner:        PagePanels,
		ownerControl: regionPanel + panelIDTasks,
	},
	features.ConversationsPlugin.Name: {
		label:        conversationsFlagLabel,
		help:         "Browse multi-agent session history.",
		owner:        PagePanels,
		ownerControl: regionPanel + panelIDConversations,
		reads:        (*Model).conversationsOn,
	},
}

// previews is every registered feature flag, in registry order, each carrying
// whatever hand-written copy it has. The list is derived rather than curated so
// a flag cannot be added to internal/features and stay invisible — which is how
// five of them (interactive input, inline edit, auto-refresh, content panes,
// resource providers) were only reachable by hand-editing config.json.
func previews() []preview {
	all := features.ListAll()
	items := make([]preview, 0, len(all))
	for _, feature := range all {
		items = append(items, previewFor(feature))
	}
	return items
}

// previewFor is the derivation for one feature: hand-written copy where it
// exists, the registry's own name and description where it does not. It is a
// separate function so the fallback can be tested with a feature that has no
// entry — every registered flag currently has one, which made a test that
// skipped curated flags iterate zero times and prove nothing.
func previewFor(feature features.Feature) preview {
	item := previewCopy[feature.Name]
	item.flag = feature.Name
	if item.label == "" {
		item.label = feature.Name
	}
	if item.help == "" {
		item.help = feature.Description
	}
	return item
}

// previewRow paints one flag the way Panels paints a surface: title and the
// same ON/OFF pill on the first line, explanation underneath. Only the pill
// toggles on click.
//
// A flag owned by another page is shown with its real state but does not toggle
// here; activating the row jumps to the control that owns it.
func (m *Model) previewRow(b *paneBuilder, item preview) {
	enabled := item.state(m)
	// Every clause that has something to say gets said. The old switch stopped
	// at the first match, so terminal_resource_providers — the only flag that
	// both needs a restart and has a scope note — silently dropped the half
	// that warns turning it off kills running provider processes.
	detail := item.help
	if item.owner != "" {
		detail += " Set this on " + PageTitle(item.owner) + ", which pairs it with the panel's own settings."
	}
	if item.restart {
		detail += " Read once when Sidecar starts, so a change takes effect after a restart."
	}
	if item.note != "" {
		detail += " " + item.note
	}
	// Only the focused row spends lines on its explanation; see
	// panelToggleFocusDetail.
	detailFor := func(s State) string {
		if !s.Focused {
			return ""
		}
		return detail
	}
	if item.owner != "" {
		b.panelStatus(regionFlag+item.flag, item.label, "", detailFor, enabled, func(m *Model) tea.Cmd {
			m.Navigate(item.owner)
			m.detailFocus = true
			m.focusControlByID(item.ownerControl)
			return nil
		})
		return
	}
	b.panelToggleFocusDetail(regionFlag+item.flag, item.label, "", detailFor, enabled, func(m *Model) tea.Cmd {
		next := !m.flagEnabled(item.flag)
		// The restart requirement is stated at save time, next to the control
		// that needs it, and only for the flags that genuinely need it.
		if item.restart {
			m.noteRestart()
		}
		return saveFlagCmd(toggleNotice(item.label, next), item.flag, next)
	})
}
