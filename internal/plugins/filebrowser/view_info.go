package filebrowser

import (
	"path/filepath"

	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/modal"
)

// renderInfoModalContent renders the file info modal.
func (p *Plugin) renderInfoModalContent() string {
	p.ensureInfoModal()
	if p.infoModal == nil {
		return ""
	}
	return p.infoModal.Render(p.width, p.height, p.mouseHandler)
}

// ensureInfoModal builds/rebuilds the info modal.
func (p *Plugin) ensureInfoModal() {
	modalW := 60
	if modalW > p.width-4 {
		modalW = p.width - 4
	}
	if modalW < 30 {
		modalW = 30
	}

	if p.infoModal != nil && p.infoModalWidth == modalW {
		return
	}
	p.infoModalWidth = modalW

	title := "File Info"
	if path := p.infoTargetPath(); path != "" {
		title = filepath.Base(path)
	}

	p.infoModal = modal.New(title,
		modal.WithWidth(modalW),
		modal.WithHints(false),
	).
		AddSection(p.infoModalDetailsSection())
}

func (p *Plugin) clearInfoModal() {
	p.infoModal = nil
	p.infoModalWidth = 0
}

func (p *Plugin) infoTargetPath() string {
	if p.activePane == PanePreview && p.previewFile != "" {
		return p.previewFile
	}
	node := p.tree.GetNode(p.treeCursor)
	if node != nil {
		return node.Path
	}
	return ""
}

func (p *Plugin) infoModalDetailsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		path := p.infoTargetPath()
		root := ""
		if p.ctx != nil {
			root = p.ctx.WorkDir
		}
		details := docview.Inspect(root, path)
		if p.activePane != PanePreview {
			if node := p.tree.GetNode(p.treeCursor); node != nil && node.IsDir {
				details.Kind = "Directory"
				details.Size = "--"
				details.IsDir = true
			}
		}
		return modal.RenderedSection{Content: docview.RenderInfo(details, p.gitStatus, p.gitLastCommit)}
	}, nil)
}
