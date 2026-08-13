package filebrowser

import (
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/filepreview"
)

// Preview types and loaders live in internal/filepreview so docview can load
// files without importing this plugin (and the app cycle that would create).

type PreviewResult = filepreview.PreviewResult
type PreviewLoadedMsg = filepreview.PreviewLoadedMsg

func LoadPreview(rootDir, path string, epoch uint64) tea.Cmd {
	return filepreview.LoadPreview(rootDir, path, epoch)
}

func LoadPreviewFile(file *os.File, path string, epoch uint64) tea.Cmd {
	return filepreview.LoadPreviewFile(file, path, epoch)
}

func Highlight(content, extension, syntaxTheme string) (string, error) {
	return filepreview.Highlight(content, extension, syntaxTheme)
}
