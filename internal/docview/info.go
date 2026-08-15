package docview

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
)

// Info is a file-info modal for a (root, path).
type Info struct {
	Root, Path            string
	GitStatus, LastCommit string

	modal *modal.Modal
	width int
}

// OpenInfo starts the info modal and fetches git details.
func OpenInfo(root, path string) (*Info, tea.Cmd) {
	info := &Info{
		Root:       root,
		Path:       path,
		GitStatus:  "Loading...",
		LastCommit: "Loading...",
	}
	return info, FetchGitInfo(root, path)
}

// ApplyGit records a fetch that belongs to this path.
func (i *Info) ApplyGit(msg GitInfoMsg) {
	if i == nil {
		return
	}
	if msg.Path != "" && msg.Path != i.Path {
		return
	}
	i.GitStatus = msg.Status
	i.LastCommit = msg.LastCommit
}

// HandleKey routes modal keys. closed is true when the user dismissed it.
func (i *Info) HandleKey(msg tea.KeyPressMsg) (closed bool, cmd tea.Cmd) {
	if i == nil {
		return true, nil
	}
	i.ensure(i.width)
	if i.modal == nil {
		return true, nil
	}
	switch msg.String() {
	case "q", "i", "I":
		return true, nil
	}
	action, cmd := i.modal.HandleKey(msg)
	if action == "cancel" {
		return true, cmd
	}
	return false, cmd
}

// HandleMouse routes modal mouse events. closed is true on dismiss.
func (i *Info) HandleMouse(msg tea.MouseMsg, handler *mouse.Handler) (closed bool) {
	if i == nil || i.modal == nil {
		return true
	}
	return i.modal.HandleMouse(msg, handler) == "cancel"
}

// Render paints the modal and registers its hit regions.
func (i *Info) Render(width, height int, handler *mouse.Handler) string {
	if i == nil {
		return ""
	}
	modalW := 60
	if modalW > width-4 {
		modalW = width - 4
	}
	if modalW < 30 {
		modalW = 30
	}
	i.ensure(modalW)
	if i.modal == nil {
		return ""
	}
	return i.modal.Render(width, height, handler)
}

func (i *Info) ensure(width int) {
	if width < 30 {
		width = 30
	}
	if i.modal != nil && i.width == width {
		return
	}
	i.width = width
	title := "File Info"
	if i.Path != "" {
		title = filepath.Base(i.Path)
	}
	i.modal = modal.New(title,
		modal.WithWidth(width),
		modal.WithHints(false),
	).AddSection(modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		return modal.RenderedSection{Content: RenderInfo(Inspect(i.Root, i.Path), i.GitStatus, i.LastCommit)}
	}, nil))
}

// WheelAtBoundary reports whether this wheel event is certainly a no-op for the
// info modal, using the geometry of its most recent render. It never rebuilds
// or mutates content, and reports unknown (false) before the first render.
func (i *Info) WheelAtBoundary(msg tea.MouseWheelMsg, handler *mouse.Handler) bool {
	if i == nil || i.modal == nil {
		return false
	}
	return i.modal.WheelAtBoundary(msg, handler)
}
