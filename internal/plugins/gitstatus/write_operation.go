package gitstatus

import (
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
)

type operationKind string

const (
	operationStage      operationKind = "stage"
	operationUnstage    operationKind = "unstage"
	operationStageAll   operationKind = "stage all"
	operationUnstageAll operationKind = "unstage all"
)

// titleCase capitalizes the first letter of each space-separated word in an
// operationKind (e.g. "stage all" -> "Stage All"). All values are ASCII, so
// this avoids pulling in golang.org/x/text/cases for ordinary word casing.
func titleCase(s string) string {
	words := strings.Split(s, " ")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

type gitWriteExecutor func(workDir string, args []string) error

type operationRequest struct {
	ID    uint64
	Epoch uint64
	Kind  operationKind
	Args  []string
}

type operationResultMsg struct {
	ID    uint64
	Epoch uint64
	Kind  operationKind
	Err   error
}

func (m operationResultMsg) GetEpoch() uint64 { return m.Epoch }

type selectionIdentity struct {
	path       string
	wantStaged bool
}

func operationProgressLabel(kind operationKind) string {
	switch kind {
	case operationStage:
		return "Staging…"
	case operationUnstage:
		return "Unstaging…"
	case operationStageAll:
		return "Staging all…"
	case operationUnstageAll:
		return "Unstaging all…"
	default:
		return "Git write…"
	}
}

func executeGitWrite(workDir string, args []string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, detail)
}

func (p *Plugin) beginWrite(kind operationKind, args []string, selection selectionIdentity) tea.Cmd {
	p.nextOperationID++
	var epoch uint64
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}
	req := operationRequest{
		ID:    p.nextOperationID,
		Epoch: epoch,
		Kind:  kind,
		Args:  append([]string(nil), args...),
	}
	p.activeOperation = &req
	p.operationError = ""
	p.operationSelection = selection
	executor := p.writeExecutor
	if executor == nil {
		executor = executeGitWrite
	}
	workDir := p.repoRoot
	return func() tea.Msg {
		return operationResultMsg{
			ID:    req.ID,
			Epoch: req.Epoch,
			Kind:  req.Kind,
			Err:   executor(workDir, req.Args),
		}
	}
}

func (p *Plugin) writeBusyToast() tea.Cmd {
	return func() tea.Msg {
		// A refused keypress, from the source that means "you have to act"
		// rather than generic app chrome (audit row 72).
		return notify.Alert(notify.SourceWaiting, notify.SeverityWarning, "Git write already in progress")
	}
}

func (p *Plugin) writeInProgress() bool {
	return p.activeOperation != nil || p.auxWriteInProgress || p.commitInProgress ||
		p.pushInProgress || p.fetchInProgress || p.pullInProgress
}

func isStatusMutationKey(key string) bool {
	switch key {
	case "s", "u", "S", "U", "c", "A", "D", "z", "Z", "ctrl+z", "b", "L", "P", "f":
		return true
	default:
		return false
	}
}

func writeBlockedCommand(id string) bool {
	switch id {
	case "stage-file", "unstage-file", "stage-all", "unstage-all",
		"commit", "amend", "execute-commit", "discard-changes",
		"stash", "stash-pop", "stash-apply", "confirm-pop",
		"branch-picker", "pull", "pull-merge", "pull-rebase",
		"pull-ff-only", "pull-autostash", "abort-pull", "push",
		"force-push", "push-upstream", "fetch":
		return true
	default:
		return false
	}
}

func (p *Plugin) restoreOperationSelection() {
	identity := p.operationSelection
	if identity.path == "" {
		return
	}
	entries := p.tree.AllEntries()
	for i, entry := range entries {
		if entry.Path == identity.path && entry.Staged == identity.wantStaged {
			p.cursor = i
			p.ensureCursorVisible()
			p.operationSelection = selectionIdentity{}
			return
		}
	}
	for i, entry := range entries {
		if entry.Path == identity.path {
			p.cursor = i
			p.ensureCursorVisible()
			break
		}
	}
	p.operationSelection = selectionIdentity{}
}

var _ plugin.EpochMessage = operationResultMsg{}
