package tabs

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// MaxPathLabel is the longest file-path label a tab will draw even when
// leftover width would let it grow. Paths past this are left-truncated so
// the filename stays visible.
const MaxPathLabel = 24

// FitPath left-truncates a file path so the filename stays visible. Cuts
// prefer a slash, producing labels like "…/dir/file.go". maxWidth is also
// clamped to MaxPathLabel so a lone tab cannot dominate the strip.
func FitPath(path string, maxWidth int) string {
	if maxWidth < 1 {
		return ""
	}
	if maxWidth > MaxPathLabel {
		maxWidth = MaxPathLabel
	}
	if ansi.StringWidth(path) <= maxWidth {
		return path
	}
	slashPath := filepath.ToSlash(path)
	segs := strings.Split(slashPath, "/")
	base := segs[len(segs)-1]
	for n := min(len(segs)-1, 3); n >= 1; n-- {
		candidate := "…/" + strings.Join(segs[len(segs)-n:], "/")
		if ansi.StringWidth(candidate) <= maxWidth {
			return candidate
		}
	}
	if ansi.StringWidth(base) <= maxWidth {
		return base
	}
	return ansi.Truncate(base, maxWidth, "…")
}

// FitEnd end-truncates text so a leading identifier stays visible.
func FitEnd(text string, maxWidth int) string {
	if maxWidth < 1 {
		return ""
	}
	if ansi.StringWidth(text) <= maxWidth {
		return text
	}
	return ansi.Truncate(text, maxWidth, "…")
}

// FitPathLabel is the FitLabel form of FitPath.
func FitPathLabel(text string, _, _, maxWidth int, _ bool) string {
	return FitPath(text, maxWidth)
}

// FitEndLabel is the FitLabel form of FitEnd.
func FitEndLabel(text string, _, _, maxWidth int, _ bool) string {
	return FitEnd(text, maxWidth)
}
