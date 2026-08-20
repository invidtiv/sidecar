// Package tdsetup owns Sidecar's narrow boundary for initializing a td
// project. The td CLI remains the source of truth; Sidecar only performs the
// preflight both of its setup surfaces need and invokes td's public command.
package tdsetup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/tdroot"
)

var ErrNotInitialized = errors.New("td is not initialized for this project")

const (
	OriginNotes     = "notes"
	OriginTDMonitor = "td-monitor"
)

// ResultMsg is broadcast after a user-confirmed initialization attempt. Both
// td-backed plugins consume successful results so neither stays on a stale
// setup screen after the other initialized the project. Origin lets only the
// surface that started a failed attempt present its error.
type ResultMsg struct {
	ProjectRoot string
	Origin      string
	Epoch       uint64
	Err         error
}

func (m ResultMsg) GetEpoch() uint64 { return m.Epoch }

func todosConflictError() error {
	return fmt.Errorf("a .todos file exists where a directory is required; remove or rename it (for example, mv .todos .todos.bak) and try again: %w", tdroot.ErrTodosIsFile)
}

// Status reports whether td has an environment to open. It deliberately
// checks only for the .todos directory: td init itself treats that directory
// as the initialization marker. The caller must run this off the startup and
// render paths because td-root resolution may inspect git worktree state.
func Status(projectRoot string) error {
	if err := tdroot.CheckTodosConflict(projectRoot); err != nil {
		if errors.Is(err, tdroot.ErrTodosIsFile) {
			return todosConflictError()
		}
		return err
	}
	root := tdroot.ResolveTDRoot(projectRoot)
	info, err := os.Stat(filepath.Join(root, tdroot.TodosDir))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotInitialized
	}
	if err != nil {
		return fmt.Errorf("check td setup: %w", err)
	}
	if !info.IsDir() {
		return todosConflictError()
	}
	return nil
}

// Initialize runs td's public initialization command. A blank response is
// supplied to td's optional agent-instructions prompt, so this boundary never
// accepts a change to AGENTS.md or CLAUDE.md. td still owns its documented
// .todos and .gitignore behavior.
func Initialize(projectRoot string) error {
	if err := tdroot.CheckTodosConflict(projectRoot); err != nil {
		if errors.Is(err, tdroot.ErrTodosIsFile) {
			return todosConflictError()
		}
		return err
	}
	cmd := exec.Command("td", "-w", projectRoot, "init")
	cmd.Dir = projectRoot
	cmd.Stdin = strings.NewReader("\n")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("td init failed: %w", err)
	}
	return fmt.Errorf("td init failed: %s: %w", detail, err)
}
