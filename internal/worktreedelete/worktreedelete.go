// Package worktreedelete owns the "Delete Worktree?" confirmation shared by
// every surface that can delete a worktree.
//
// The project workspace (internal/plugins/workspace) and the global Workspaces
// browser (internal/overview) are two projections of one model, so the
// confirmation they raise must be one construction, not two that resemble each
// other. This package holds the whole of it: the modal's sections, the branch
// cleanup options, and the key/mouse routing that turns a press into a
// decision. Hosts own only what is genuinely theirs — how the target is
// selected, and how the deletion is executed afterwards.
package worktreedelete

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// Title is the confirmation's heading. Exported so a host's tests can assert
// the shared modal is the one on screen.
const Title = "Delete Worktree?"

// Action IDs. They are exported because hosts route mouse and keyboard actions
// through the same identifiers.
const (
	LocalBranchID  = "delete-confirm-local-branch"
	RemoteBranchID = "delete-confirm-remote-branch"
	DeleteID       = "delete-confirm-delete"
	CancelID       = "delete-confirm-cancel"
)

// Width is the confirmation's preferred width before clamping.
const Width = 58

// Target is the presentation-neutral identity of the worktree being deleted.
type Target struct {
	Name      string
	Branch    string
	Path      string
	IsMissing bool
}

// Outcome is what a key or click asked for.
type Outcome int

const (
	// OutcomeNone means the input was absorbed without a decision.
	OutcomeNone Outcome = iota
	// OutcomeConfirm means the user asked for the deletion to proceed.
	OutcomeConfirm
	// OutcomeCancel means the user dismissed the confirmation.
	OutcomeCancel
)

// Dirtiness is what the host knows about uncommitted work in the target.
//
// It is three values rather than a bool because a confirmation opens before
// git can answer: "we have not asked yet" and "we asked and it is clean" are
// different sentences, and rendering the second while the first is true would
// be the same lie as the old unconditional warning, only pointed the other way.
type Dirtiness int

const (
	// DirtinessUnknown means no answer has arrived — or git could not give one.
	DirtinessUnknown Dirtiness = iota
	// DirtinessClean means git reported nothing to lose.
	DirtinessClean
	// DirtinessDirty means git reported uncommitted or untracked work.
	DirtinessDirty
)

// ProbeDirtiness asks git whether a worktree holds work a removal would
// destroy, in the form the confirmation renders.
//
// Both hosts call this, off the keypress path, when the confirmation opens —
// one `git status` for the one worktree the user named, rather than a status
// per worktree per inventory refresh. An unreadable worktree stays Unknown:
// telling a user their worktree is clean because git failed is exactly the
// reassurance this ticket exists to remove. A missing directory is Unknown too;
// the confirmation already says the directory is gone.
func ProbeDirtiness(ctx context.Context, path string, missing bool) Dirtiness {
	if missing || strings.TrimSpace(path) == "" {
		return DirtinessUnknown
	}
	dirty, err := workspaceops.WorktreeIsDirty(ctx, path)
	if err != nil {
		return DirtinessUnknown
	}
	if dirty {
		return DirtinessDirty
	}
	return DirtinessClean
}

// State is one open confirmation. The zero value is closed.
type State struct {
	target Target
	open   bool

	// IsMainBranch protects the repository's primary branch: while true the
	// branch cleanup options are not offered at all.
	IsMainBranch bool
	// HasRemote reports that the branch exists on origin. Hosts may resolve it
	// asynchronously; the modal reads it at render time.
	HasRemote bool
	// DeleteLocal and DeleteRemote are the optional branch cleanup choices.
	DeleteLocal  bool
	DeleteRemote bool

	// Dirty is what git says about uncommitted work in the target. Hosts
	// resolve it asynchronously, as they do HasRemote, and call Invalidate.
	Dirty Dirtiness

	built      *modal.Modal
	builtWidth int
}

// Open arms the confirmation for a target. isMainBranch is the host's answer
// about the repository's primary branch; HasRemote may be filled in later.
func (s *State) Open(target Target, isMainBranch bool) {
	s.target = target
	s.open = true
	s.IsMainBranch = isMainBranch
	s.HasRemote = false
	// A worktree whose directory is already gone is almost always being cleaned
	// up together with its branch, so that box starts ticked.
	s.DeleteLocal = target.IsMissing
	s.DeleteRemote = false
	s.Dirty = DirtinessUnknown
	s.built = nil
	s.builtWidth = 0
}

// Active reports that a confirmation is open.
func (s *State) Active() bool { return s.open }

// Target is the worktree the open confirmation refers to.
func (s *State) Target() Target { return s.target }

// Clear closes the confirmation and drops its state.
func (s *State) Clear() {
	*s = State{}
}

// Invalidate discards the cached modal so the next render rebuilds it. Hosts
// call it after an asynchronous answer (remote branch existence) lands.
func (s *State) Invalidate() {
	s.built = nil
	s.builtWidth = 0
}

// DeleteRemoteBranch reports whether the remote branch should be deleted:
// the box is only meaningful when a remote branch was actually found.
func (s *State) DeleteRemoteBranch() bool { return s.DeleteRemote && s.HasRemote }

// Modal builds (and caches) the confirmation for a surface `avail` cells wide.
// It returns nil while no confirmation is open.
func (s *State) Modal(avail int) *modal.Modal {
	if !s.open {
		return nil
	}
	width := ClampWidth(avail)
	if s.built != nil && s.builtWidth == width {
		return s.built
	}
	s.builtWidth = width
	s.built = s.build(width)
	return s.built
}

// ClampWidth is the confirmation's width on a surface `avail` cells wide.
func ClampWidth(avail int) int {
	width := Width
	if width > avail-4 {
		width = avail - 4
	}
	if width < 20 {
		width = 20
	}
	return width
}

func (s *State) build(width int) *modal.Modal {
	branchOptions := func() bool { return !s.IsMainBranch }
	remoteOptions := func() bool { return !s.IsMainBranch && s.HasRemote }

	return modal.New(Title,
		modal.WithWidth(width),
		modal.WithVariant(modal.VariantDanger),
		modal.WithHints(false),
	).
		AddSection(s.infoSection()).
		AddSection(modal.Spacer()).
		AddSection(s.warningSection()).
		AddSection(modal.Spacer()).
		AddSection(modal.When(branchOptions, branchHeaderSection())).
		AddSection(modal.When(branchOptions, modal.Checkbox(LocalBranchID, "Delete local branch", &s.DeleteLocal))).
		AddSection(modal.When(branchOptions, s.localHintSection())).
		AddSection(modal.When(remoteOptions, modal.Checkbox(RemoteBranchID, "Delete remote branch", &s.DeleteRemote))).
		AddSection(modal.When(remoteOptions, s.remoteHintSection())).
		AddSection(modal.When(branchOptions, modal.Spacer())).
		AddSection(modal.Buttons(
			modal.Btn(" Delete ", DeleteID, modal.BtnDanger()),
			modal.Btn(" Cancel ", CancelID),
		))
}

func (s *State) infoSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		var sb strings.Builder
		fmt.Fprintf(&sb, "Name:   %s\n", lipgloss.NewStyle().Bold(true).Render(s.target.Name))
		fmt.Fprintf(&sb, "Branch: %s\n", s.target.Branch)
		fmt.Fprintf(&sb, "Path:   %s", dim(s.target.Path))
		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

func (s *State) warningSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		warningStyle := lipgloss.NewStyle().Foreground(styles.Warning)

		var sb strings.Builder
		sb.WriteString(warningStyle.Render("This will:"))
		sb.WriteString("\n")
		if s.target.IsMissing {
			sb.WriteString(dim("  • Directory already removed"))
			sb.WriteString("\n")
			sb.WriteString(dim("  • Clean up git worktree metadata"))
		} else {
			sb.WriteString(dim("  • Remove the working directory"))
			sb.WriteString("\n")
			// The truthful line. This used to read "Uncommitted changes will be
			// lost" for every worktree, dirty or not, which is how a warning
			// becomes wallpaper — and since the confirmation now force-removes,
			// it is the only thing between a keypress and lost work
			// (td-d37612). It says what git actually reported, and says nothing
			// reassuring until git has reported.
			switch s.Dirty {
			case DirtinessDirty:
				sb.WriteString(warningStyle.Render("  • " + DirtyLine))
			case DirtinessClean:
				sb.WriteString(dim("  • " + CleanLine))
			default:
				sb.WriteString(dim("  • " + UnknownDirtinessLine))
			}
		}
		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

// The three things the confirmation can say about uncommitted work. They are
// exported so each host's tests assert against the shared wording rather than
// its own copy of it.
const (
	// DirtyLine is shown only when git reported work that removal would destroy.
	DirtyLine = "Uncommitted changes will be lost"
	// CleanLine is shown when git reported a clean worktree.
	CleanLine = "No uncommitted changes to lose"
	// UnknownDirtinessLine is shown until git answers, and if it cannot.
	UnknownDirtinessLine = "Checking for uncommitted changes…"
)

func branchHeaderSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		return modal.RenderedSection{Content: lipgloss.NewStyle().Bold(true).Render("Branch Cleanup (Optional)")}
	}, nil)
}

func (s *State) localHintSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		return modal.RenderedSection{Content: dim("  Removes '" + s.target.Branch + "' locally")}
	}, nil)
}

func (s *State) remoteHintSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		return modal.RenderedSection{Content: dim("  Removes 'origin/" + s.target.Branch + "'")}
	}, nil)
}

func dim(s string) string { return styles.Muted.Render(s) }

// HandleKey routes a key press to the open confirmation. Hosts act on the
// returned outcome; the tea.Cmd belongs to the modal's own widgets.
func (s *State) HandleKey(avail int, msg tea.KeyPressMsg) (Outcome, tea.Cmd) {
	built := s.Modal(avail)
	if built == nil {
		return OutcomeNone, nil
	}

	switch msg.String() {
	case "D":
		// Power-user shortcut: the key that opened the confirmation confirms it.
		return OutcomeConfirm, nil
	case "esc", "q":
		return OutcomeCancel, nil
	case "j", "down", "l", "right":
		built.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab})
		return OutcomeNone, nil
	case "k", "up", "h", "left":
		built.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		return OutcomeNone, nil
	}

	action, cmd := built.HandleKey(msg)
	return s.apply(action), cmd
}

// HandleMouse routes a mouse event to the open confirmation.
func (s *State) HandleMouse(avail int, msg tea.MouseMsg, handler *mouse.Handler) Outcome {
	built := s.Modal(avail)
	if built == nil {
		return OutcomeNone
	}
	return s.apply(built.HandleMouse(msg, handler))
}

// WheelAtBoundary reports that the confirmation cannot scroll further, so the
// host may pass the wheel on.
func (s *State) WheelAtBoundary(avail int, msg tea.MouseWheelMsg, handler *mouse.Handler) bool {
	built := s.Modal(avail)
	if built == nil {
		return false
	}
	return built.WheelAtBoundary(msg, handler)
}

// apply turns a modal action ID into an outcome, toggling the branch options
// the confirmation owns itself.
func (s *State) apply(action string) Outcome {
	switch action {
	case "cancel", CancelID:
		return OutcomeCancel
	case DeleteID:
		return OutcomeConfirm
	case LocalBranchID:
		if !s.IsMainBranch {
			s.DeleteLocal = !s.DeleteLocal
		}
	case RemoteBranchID:
		if !s.IsMainBranch && s.HasRemote {
			s.DeleteRemote = !s.DeleteRemote
		}
	}
	return OutcomeNone
}
