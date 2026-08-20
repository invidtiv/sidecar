package app

import (
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/targetactivation"
	"github.com/marcus/sidecar/internal/terminallink"
)

// activateTarget executes a resolved activation. The decision — which plugin,
// which message, is this well-formed — belongs to targetactivation, which is
// state-free; the shell is here only because it is the one component that can
// focus plugins and (later) switch projects.
//
// Cross-project landing is not implemented yet: a target naming another project
// is refused out loud rather than activated against the wrong one.
func (m *Model) activateTarget(req ActivateTargetMsg) tea.Cmd {
	if !m.targetProjectIsCurrent(req.Project) {
		return msg.Blocked("Cannot jump to " + req.Project + " yet: cross-project activation is not wired up")
	}
	plan, err := targetactivation.Resolve(req.Target)
	if err != nil {
		return msg.Blocked(err.Error())
	}
	switch plan.Kind {
	case targetactivation.PlanOpenFile:
		// The canonical file message is project-relative, so the containment
		// rule is applied here rather than in Resolve: a terminal surface
		// resolving the same plan against its own root accepts paths this
		// route must refuse.
		path, err := targetactivation.RelativeProjectPath(plan.Path)
		if err != nil {
			return msg.Blocked(err.Error())
		}
		return tea.Batch(
			FocusPlugin(plan.PluginID),
			func() tea.Msg { return NavigateToFileMsg{Path: path, Line: plan.Line} },
		)
	case targetactivation.PlanOpenURL:
		return terminallink.OpenHTTP(plan.URL)
	case targetactivation.PlanOpenIssue:
		return tea.Batch(FocusPlugin(plan.PluginID), OpenIssuePane(plan.Issue))
	case targetactivation.PlanOpenDiff:
		return tea.Batch(FocusPlugin(plan.PluginID), OpenDiffPane(plan.Spec))
	case targetactivation.PlanOpenResource:
		return tea.Batch(FocusPlugin(plan.PluginID), OpenResourcePane(plan.Provider, plan.Matcher, plan.Locator))
	case targetactivation.PlanAttachSession:
		return tea.Batch(FocusPlugin(plan.PluginID), AttachSession(plan.Session))
	default:
		return nil
	}
}

// targetProjectIsCurrent reports whether a target's project qualifier names the
// project this instance is already showing. Empty means "wherever the user is",
// which is every same-project activation.
func (m *Model) targetProjectIsCurrent(project string) bool {
	project = strings.TrimSpace(project)
	if project == "" {
		return true
	}
	for _, candidate := range []string{m.ui.WorkDir, m.ui.ProjectRoot} {
		if candidate == "" {
			continue
		}
		if project == candidate || project == filepath.Base(candidate) {
			return true
		}
		if normalizedCandidate, err := normalizePath(candidate); err == nil {
			if normalizedProject, err := normalizePath(project); err == nil && normalizedProject == normalizedCandidate {
				return true
			}
		}
	}
	return false
}
