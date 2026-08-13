package workspacediff

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// RenderOpts controls optional chrome the project plugin needs and the
// global preview does not: hit regions, list-pane width.
type RenderOpts struct {
	Hit          func(id string, x, y, w, h int, data any)
	ContentBaseX int
	BaseY        int
	PanelHeight  int
	Truncate     func(string, int, string) string
}

const (
	RegionDivider      = "diff-tab-divider"
	RegionFile         = "diff-tab-file"
	RegionCommit       = "diff-tab-commit"
	RegionDiffPane     = "diff-tab-diff-pane"
	RegionPreviewFile  = "diff-tab-preview-file"
	RegionFileListPane = "diff-tab-filelist-pane"
	RegionCommitBack   = "commit-file-back"
	RegionCommitFile   = "commit-file-item"
	RegionCommitDiff   = "commit-file-diff-pane"
	dividerHitWidth    = 3
)

func dimText(s string) string { return styles.Muted.Render(s) }

func fileListWidth(totalWidth int) int {
	w := totalWidth * 25 / 100
	if w < 20 {
		w = 20
	}
	maxW := totalWidth - 30
	if maxW < 20 {
		maxW = 20
	}
	if w > maxW {
		w = maxW
	}
	return w
}

func padToHeight(content string, height, width int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

func (v *View) baseRef() string {
	if v.Snapshot != nil && v.Snapshot.BaseRef != "" {
		return v.Snapshot.BaseRef
	}
	return "resolved base"
}

// Render draws the working-tree + commits Diff view.
func (v *View) Render(width, height int, opts RenderOpts) string {
	switch v.State {
	case LoadStateLoading:
		return dimText("Loading diff…")
	case LoadStateError:
		err := v.Error
		if opts.Truncate != nil {
			err = opts.Truncate(err, width, "…")
		}
		return styles.StatusDeleted.Render("Error loading diff") + "\n" + err
	}
	if v.Scope == ScopeAggregate {
		return v.renderAggregate(width, height)
	}

	hasFiles := len(v.Files) > 0
	hasCommits := len(v.Commits) > 0
	if !hasFiles && !hasCommits {
		if v.Raw == "" {
			if v.Scope == ScopeCommits {
				return dimText(fmt.Sprintf("Commits unique to %s: none", v.baseRef()))
			}
			return dimText("Working Tree vs HEAD: clean")
		}
		return v.renderRaw(v.Content, width, height, opts)
	}

	v.ClampCursor()

	if width < CollapseThreshold {
		return v.renderCollapsed(width, height, opts)
	}

	listWidth := v.ListWidth
	if listWidth <= 0 {
		listWidth = fileListWidth(width)
	}
	if listWidth < 20 {
		listWidth = 20
	}
	maxW := width - 30
	if maxW < 20 {
		maxW = 20
	}
	if listWidth > maxW {
		listWidth = maxW
	}
	diffPaneWidth := width - listWidth - 1
	if diffPaneWidth < 10 {
		diffPaneWidth = 10
	}

	if opts.Hit != nil && opts.ContentBaseX > 0 {
		opts.Hit(RegionDivider, opts.ContentBaseX+listWidth, 0, dividerHitWidth, opts.PanelHeight, nil)
	}
	rightX := opts.ContentBaseX + listWidth + 1

	leftPane := v.renderFileList(listWidth, height, opts.ContentBaseX, opts.BaseY, opts)
	rightPane := v.renderDiffPane(diffPaneWidth, height, rightX, opts.BaseY, opts)
	leftPane = padToHeight(leftPane, height, listWidth)
	rightPane = padToHeight(rightPane, height, diffPaneWidth)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, renderDivider(height), rightPane)
}

func (v *View) renderCollapsed(width, height int, opts RenderOpts) string {
	if v.Focus == FocusDiff {
		return v.renderDiffPane(width, height, 0, 0, opts)
	}
	return v.renderFileList(width, height, 0, 0, opts)
}

func renderDivider(height int) string {
	style := lipgloss.NewStyle().Foreground(styles.BorderNormal)
	var sb strings.Builder
	for i := 0; i < height; i++ {
		sb.WriteString(style.Render("│"))
		if i < height-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func (v *View) renderFileList(width, height, baseX, baseY int, opts RenderOpts) string {
	var sb strings.Builder
	files := v.Files
	commits := v.Commits
	fileListActive := v.Focus == FocusFileList
	maxWidth := width - 2

	if opts.Hit != nil && baseX > 0 {
		opts.Hit(RegionFileListPane, baseX, baseY, width, height, nil)
	}

	headerText := fmt.Sprintf("Working Tree vs HEAD (%d)", len(files))
	if v.Scope == ScopeCommits {
		headerText = fmt.Sprintf("Commits vs %s (%d)", v.baseRef(), len(commits))
	} else if v.Snapshot != nil && v.Snapshot.Truncated {
		headerText = fmt.Sprintf("Working Tree vs HEAD (%d) [untracked caps: %d files, %d B/file, %d B total; %d omitted]",
			len(files), MaxUntrackedFiles, MaxUntrackedFileSize, MaxUntrackedTotalBytes, v.Snapshot.UntrackedOmitted)
	}
	if fileListActive {
		sb.WriteString(styles.Title.Render(headerText))
	} else {
		sb.WriteString(styles.Muted.Render(headerText))
	}
	sb.WriteString("\n")
	linesUsed := 1

	commitLines := 0
	if len(commits) > 0 {
		commitLines = 2 + len(commits)
		if commitLines > height/3 {
			commitLines = height / 3
			if commitLines < 3 {
				commitLines = 3
			}
		}
	}
	filesHeight := height - linesUsed - commitLines
	if filesHeight < 3 {
		filesHeight = 3
	}

	if v.Cursor < len(files) {
		if v.Cursor < v.Scroll {
			v.Scroll = v.Cursor
		}
		if v.Cursor >= v.Scroll+filesHeight {
			v.Scroll = v.Cursor - filesHeight + 1
		}
	}

	startIdx := v.Scroll
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := startIdx + filesHeight
	if endIdx > len(files) {
		endIdx = len(files)
	}

	for i := startIdx; i < endIdx; i++ {
		file := files[i]
		selected := i == v.Cursor
		if opts.Hit != nil && baseX > 0 {
			opts.Hit(RegionFile, baseX, baseY+linesUsed, width, 1, i)
		}
		statusIcon := "M"
		if file.Additions > 0 && file.Deletions == 0 {
			statusIcon = "A"
		} else if file.Additions == 0 && file.Deletions > 0 {
			statusIcon = "D"
		}
		fileName := file.Path
		statsStr := fmt.Sprintf("+%d/-%d", file.Additions, file.Deletions)
		availableWidth := maxWidth - 4
		if lipgloss.Width(fileName)+lipgloss.Width(statsStr)+1 > availableWidth {
			keepWidth := availableWidth - len(statsStr) - 2
			if keepWidth > 3 {
				fileName = "…" + ansi.TruncateLeft(fileName, lipgloss.Width(fileName)-keepWidth, "")
			}
		}
		if selected && fileListActive {
			plainLine := fmt.Sprintf("%s %s %s", statusIcon, fileName, statsStr)
			if gap := maxWidth - lipgloss.Width(plainLine); gap > 0 {
				plainLine += strings.Repeat(" ", gap)
			}
			sb.WriteString(styles.ListItemSelected.Render(plainLine))
		} else {
			var statusStyle lipgloss.Style
			switch statusIcon {
			case "A":
				statusStyle = styles.StatusStaged
			case "D":
				statusStyle = styles.StatusDeleted
			default:
				statusStyle = styles.StatusModified
			}
			styledLine := statusStyle.Render(statusIcon) + " " + fileName + " " + styles.Muted.Render(statsStr)
			if gap := maxWidth - lipgloss.Width(styledLine); gap > 0 {
				styledLine += strings.Repeat(" ", gap)
			}
			sb.WriteString(styledLine)
		}
		sb.WriteString("\n")
		linesUsed++
	}

	if len(commits) > 0 {
		sb.WriteString(styles.Muted.Render(strings.Repeat("─", maxWidth)))
		sb.WriteString("\n")
		linesUsed++
		sb.WriteString(styles.Title.Render(fmt.Sprintf("Commits (%d)", len(commits))))
		sb.WriteString("\n")
		linesUsed++

		maxCommitLines := height - linesUsed
		if maxCommitLines < 0 {
			maxCommitLines = 0
		}
		for i, commit := range commits {
			if i >= maxCommitLines {
				break
			}
			selected := (len(files) + i) == v.Cursor
			if opts.Hit != nil && baseX > 0 {
				opts.Hit(RegionCommit, baseX, baseY+linesUsed, width, 1, len(files)+i)
			}
			hash := commit.Hash
			if len(hash) > 7 {
				hash = hash[:7]
			}
			subject := commit.Subject
			subjectWidth := maxWidth - 12
			if subjectWidth < 10 {
				subjectWidth = 10
			}
			if lipgloss.Width(subject) > subjectWidth {
				subject = ansi.Truncate(subject, subjectWidth, "…")
			}
			if selected && fileListActive {
				plainIndicator := "○ "
				if commit.Pushed {
					plainIndicator = "↑ "
				}
				plainLine := fmt.Sprintf("%s%s %s", plainIndicator, hash, subject)
				if gap := maxWidth - lipgloss.Width(plainLine); gap > 0 {
					plainLine += strings.Repeat(" ", gap)
				}
				sb.WriteString(styles.ListItemSelected.Render(plainLine))
			} else {
				var indicator string
				if commit.Pushed {
					indicator = styles.DiffAdd.Render("↑") + " "
				} else {
					indicator = styles.Muted.Render("○") + " "
				}
				styledLine := fmt.Sprintf("%s%s %s", indicator, styles.Code.Render(hash), subject)
				if gap := maxWidth - lipgloss.Width(styledLine); gap > 0 {
					styledLine += strings.Repeat(" ", gap)
				}
				sb.WriteString(styledLine)
			}
			sb.WriteString("\n")
			linesUsed++
		}
	}
	return sb.String()
}

func (v *View) renderDiffPane(width, height, baseX, baseY int, opts RenderOpts) string {
	if v.Cursor >= len(v.Files) {
		commit, ok := v.SelectedCommit()
		if !ok {
			return dimText("Select a file to view diff")
		}
		return v.renderCommitPreview(commit, width, height, baseX, baseY, opts)
	}
	file := v.Files[v.Cursor]
	var sb strings.Builder
	headerStr := file.Path + " [unified]"
	if v.Focus == FocusDiff {
		sb.WriteString(styles.Title.Render(headerStr))
	} else {
		sb.WriteString(styles.Muted.Render(headerStr))
	}
	sb.WriteString("\n\n")
	contentHeight := height - 2
	if contentHeight < 1 {
		contentHeight = 1
	}
	sb.WriteString(v.renderRaw(file.Raw, width, contentHeight, opts))
	if opts.Hit != nil && baseX > 0 {
		opts.Hit(RegionDiffPane, baseX, baseY, width, height, nil)
	}
	return sb.String()
}

func (v *View) renderCommitPreview(commit CommitInfo, width, height, baseX, baseY int, opts RenderOpts) string {
	var sb strings.Builder
	hashStyle := lipgloss.NewStyle().Foreground(styles.Warning)
	pushedLabel := dimText("local")
	if commit.Pushed {
		pushedLabel = styles.DiffAdd.Render("pushed")
	}
	sb.WriteString(styles.Title.Render("Commit"))
	sb.WriteString(" ")
	sb.WriteString(hashStyle.Render(commit.Hash))
	sb.WriteString(" ")
	sb.WriteString(pushedLabel)
	sb.WriteString("\n")
	sb.WriteString(commit.Subject)
	sb.WriteString("\n\n")
	linesUsed := 3

	if v.CommitDetail != nil {
		files := v.CommitDetail.Files
		sb.WriteString(styles.Muted.Render(fmt.Sprintf("Files (%d)", len(files))))
		sb.WriteString("\n")
		linesUsed++
		maxLines := height - linesUsed
		if maxLines < 0 {
			maxLines = 0
		}
		maxWidth := width - 2
		for i, file := range files {
			if i >= maxLines {
				break
			}
			if opts.Hit != nil && baseX > 0 {
				opts.Hit(RegionPreviewFile, baseX, baseY+linesUsed+i, width, 1, i)
			}
			statusIcon := file.Status
			if statusIcon == "" {
				statusIcon = "M"
			}
			var statusStyle lipgloss.Style
			switch statusIcon {
			case "A":
				statusStyle = styles.StatusStaged
			case "D":
				statusStyle = styles.StatusDeleted
			default:
				statusStyle = styles.StatusModified
			}
			fileName := file.Path
			fileRunes := []rune(fileName)
			if len(fileRunes) > maxWidth-4 {
				keep := maxWidth - 5
				if keep > 0 {
					fileName = "…" + string(fileRunes[len(fileRunes)-keep:])
				}
			}
			plainLine := fmt.Sprintf("%s %s", statusIcon, fileName)
			if gap := maxWidth - lipgloss.Width(plainLine); gap > 0 {
				plainLine += strings.Repeat(" ", gap)
			}
			sb.WriteString(statusStyle.Render(plainLine))
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString(dimText("Loading files..."))
	}
	return sb.String()
}

func (v *View) renderAggregate(width, height int) string {
	if v.Snapshot == nil {
		return dimText("Loading aggregate diff…")
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Aggregate: %s..HEAD", v.Snapshot.MergeBase)
	sb.WriteString("\nCommitted branch changes\n")
	if strings.TrimSpace(v.Snapshot.AggregateCommitted) == "" {
		sb.WriteString("(none)\n")
	} else {
		sb.WriteString(v.Snapshot.AggregateCommitted)
		sb.WriteByte('\n')
	}
	sb.WriteString("\nUncommitted working tree changes vs HEAD\n")
	if strings.TrimSpace(v.Snapshot.AggregateUncommitted) == "" {
		sb.WriteString("(none)\n")
	} else {
		sb.WriteString(v.Snapshot.AggregateUncommitted)
	}
	return v.renderRaw(sb.String(), width, height, RenderOpts{})
}

func (v *View) renderRaw(content string, width, height int, opts RenderOpts) string {
	lines := splitLines(content)
	start := v.DiffScroll
	if start >= len(lines) {
		start = len(lines) - 1
	}
	if start < 0 {
		start = 0
	}
	end := start + height
	if end > len(lines) {
		end = len(lines)
	}
	var rendered []string
	for _, line := range lines[start:end] {
		line = ui.ExpandTabs(line, 4)
		var styledLine string
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			styledLine = styles.DiffHeader.Render(line)
		case strings.HasPrefix(line, "@@"):
			styledLine = lipgloss.NewStyle().Foreground(styles.Info).Render(line)
		case strings.HasPrefix(line, "+"):
			styledLine = styles.DiffAdd.Render(line)
		case strings.HasPrefix(line, "-"):
			styledLine = styles.DiffRemove.Render(line)
		default:
			styledLine = line
		}
		if opts.Truncate != nil && lipgloss.Width(styledLine) > width {
			styledLine = opts.Truncate(styledLine, width, "")
		}
		rendered = append(rendered, styledLine)
	}
	return strings.Join(rendered, "\n")
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
