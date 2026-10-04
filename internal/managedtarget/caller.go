package managedtarget

import (
	"fmt"
	"path/filepath"
	"strings"
)

const CallerConflict ErrorKind = "caller_conflict"

// CallerEvidence is independently collected context for an implicit selector.
// Paths are canonicalized by the host adapter. A shell's owning project does
// not change when its live pane changes directory.
type CallerEvidence struct {
	ClaimedSession, PaneSession, Cwd, PaneWorkDir, OriginWorkDir, RegisteredCwdRoot string
	OriginVerified                                                                  bool
}

// ValidateCaller refuses contradictions, never chooses a replacement target.
// A live pane directory verifies a deliberate cd, even outside the owning
// project. Without live directory evidence, only a known registered cwd can
// contradict the stored origin; arbitrary directories claim no other shell.
func ValidateCaller(e CallerEvidence) error {
	conflict := func(reason string) error {
		return &Error{Kind: CallerConflict, Message: "caller identity conflict: " + reason + "; name the destination with an explicit target (TARGET or --target), --shell, or --project"}
	}
	if e.ClaimedSession != "" && e.PaneSession != "" && e.ClaimedSession != e.PaneSession {
		return conflict(fmt.Sprintf("SIDECAR_SHELL claims %q but TMUX_PANE belongs to %q", e.ClaimedSession, e.PaneSession))
	}
	if e.ClaimedSession != "" && !e.OriginVerified {
		return conflict(fmt.Sprintf("cannot verify the managed shell %q claimed by SIDECAR_SHELL", e.ClaimedSession))
	}
	if e.PaneWorkDir != "" && e.Cwd != "" {
		if !callerPathWithin(e.PaneWorkDir, e.Cwd) {
			return conflict(fmt.Sprintf("caller directory %q disagrees with pane %q directory %q", e.Cwd, e.PaneSession, e.PaneWorkDir))
		}
		return nil
	}
	if e.RegisteredCwdRoot != "" && e.OriginWorkDir != "" && !callerPathWithin(e.OriginWorkDir, e.Cwd) {
		return conflict(fmt.Sprintf("caller directory %q is in registered workspace %q, but managed shell %q belongs to directory %q", e.Cwd, e.RegisteredCwdRoot, e.ClaimedSession, e.OriginWorkDir))
	}
	return nil
}

func callerPathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
