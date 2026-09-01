package docview

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/clip"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/textselect"
	"github.com/marcus/sidecar/internal/tty"
)

// RevealErrorMsg is sent when reveal in the OS file manager fails.
type RevealErrorMsg struct {
	Err error
}

// EditExternal opens a document through Sidecar's shared external-editor
// launcher. Hosts pass the viewer's root and title so the action remains tied
// to the focused document rather than to a primary file-browser selection.
func EditExternal(root, path string, line int) tea.Cmd {
	if path == "" {
		return nil
	}
	return func() tea.Msg {
		return plugin.OpenFileMsg{
			Editor: tty.ResolveEditor(),
			Path:   resolvePath(root, path),
			LineNo: line,
		}
	}
}

// GitInfoMsg is git status and last commit for a path.
type GitInfoMsg struct {
	Path       string
	Status     string
	LastCommit string
}

// FileDetails is stat + classification for a root-relative path.
type FileDetails struct {
	Path        string
	Name        string
	Kind        string
	Size        string
	Where       string
	Modified    string
	Permissions string
	IsDir       bool
	Err         error
}

// Reveal opens path in the OS file manager. path is root-relative.
func Reveal(root, path string) tea.Cmd {
	return func() tea.Msg {
		err := revealPath(root, path, runtime.GOOS, func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		})
		if err != nil {
			return RevealErrorMsg{Err: err}
		}
		return nil
	}
}

func revealPath(root, path, goos string, run func(string, ...string) ([]byte, error)) error {
	return reveal(resolvePath(root, path), goos, run)
}

func reveal(fullPath, goos string, run func(string, ...string) ([]byte, error)) error {
	var name string
	var args []string
	switch goos {
	case "darwin":
		name, args = "open", []string{"-R", fullPath}
	case "windows":
		name, args = "explorer", []string{"/select,", fullPath}
	case "linux":
		name, args = "xdg-open", []string{filepath.Dir(fullPath)}
	default:
		return fmt.Errorf("reveal not supported on %s", goos)
	}
	output, err := run(name, args...)
	if err == nil {
		return nil
	}
	if detail := strings.TrimSpace(string(output)); detail != "" {
		return fmt.Errorf("reveal %s: %w: %s", fullPath, err, detail)
	}
	return fmt.Errorf("reveal %s: %w", fullPath, err)
}

// YankPath copies the relative path to the clipboard.
func YankPath(path string) tea.Cmd {
	if path == "" {
		return nil
	}
	return clip.Copy(path, func(r clip.Result) tea.Msg {
		return msg.FlashMsg{Text: r.Message("Copied: " + path)}
	})
}

// YankContents copies the file at root/path to the clipboard.
func YankContents(root, path string) tea.Cmd {
	if path == "" {
		return nil
	}
	empty := msg.FlashMsg{Text: "No content to copy"}
	return clip.CopyFrom(
		func() (string, tea.Msg) {
			data, err := os.ReadFile(resolvePath(root, path))
			if err != nil {
				return "", empty
			}
			return string(data), empty
		},
		func(r clip.Result, text string) tea.Msg {
			lines := strings.Count(text, "\n")
			if !strings.HasSuffix(text, "\n") {
				lines++
			}
			return msg.FlashMsg{Text: r.Message(fmt.Sprintf("Copied %d lines", lines))}
		},
	)
}

// YankSelectionOrContents matches the Files preview's y behavior for every
// document host: copy the visible selection when one exists, otherwise copy
// the file itself. Keeping the choice here prevents each pane projection from
// quietly choosing a different source.
func (m *Model) YankSelectionOrContents() tea.Cmd {
	if m == nil {
		return nil
	}
	if selected := m.SelectionText(); len(selected) > 0 {
		return m.yankSelection(selected)
	}
	return YankContents(m.Root(), m.Title())
}

// YankSelectionOrLoaded copies the visible selection, or the already loaded
// body. It never opens a local path, so a remote document cannot yank this
// machine's twin file.
func (m *Model) YankSelectionOrLoaded() tea.Cmd {
	if m == nil {
		return nil
	}
	if selected := m.SelectionText(); len(selected) > 0 {
		return m.yankSelection(selected)
	}
	return YankText(m.result.Content)
}

func (m *Model) yankSelection(selected []string) tea.Cmd {
	return m.SelectionCopyCmd(textselect.Result{Copy: selected, CopyAsked: true}, func(notice textselect.CopyNotice) tea.Msg {
		if notice.IsError {
			return msg.ToastMsg{Message: notice.Message, Duration: notice.Duration, IsError: true}
		}
		return msg.FlashMsg{Text: notice.Message}
	})
}

// YankText copies text that is already in memory.
func YankText(text string) tea.Cmd {
	if text == "" {
		return func() tea.Msg { return msg.FlashMsg{Text: "No content to copy"} }
	}
	lines := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		lines++
	}
	return clip.Copy(text, func(r clip.Result) tea.Msg {
		return msg.FlashMsg{Text: r.Message(fmt.Sprintf("Copied %d lines", lines))}
	})
}

// FetchGitInfo retrieves git status and last commit for a root-relative path.
func FetchGitInfo(root, path string) tea.Cmd {
	return func() tea.Msg {
		if path == "" {
			return GitInfoMsg{Path: path}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain", "--", path)
		statusCmd.Dir = root
		statusOut, err := statusCmd.Output()
		var status string
		if err != nil {
			status = "Error"
		} else {
			status = strings.TrimSpace(string(statusOut))
			if status == "" {
				status = "Clean"
			} else if len(status) >= 2 {
				status = status[:2]
			}
		}

		logCmd := exec.CommandContext(ctx, "git", "log", "-1", "--format=%h - %s (%cr)", "--", path)
		logCmd.Dir = root
		logOut, err := logCmd.Output()
		var lastCommit string
		if err != nil {
			lastCommit = "Error"
		} else {
			lastCommit = strings.TrimSpace(string(logOut))
			if lastCommit == "" {
				lastCommit = "Not committed"
			}
		}

		return GitInfoMsg{Path: path, Status: status, LastCommit: lastCommit}
	}
}

// Inspect stats a root-relative path.
func Inspect(root, path string) FileDetails {
	if path == "" {
		return FileDetails{}
	}
	d := FileDetails{Path: path, Where: filepath.Dir(path)}
	info, err := os.Stat(resolvePath(root, path))
	if err != nil {
		d.Err = err
		return d
	}
	d.Name = info.Name()
	d.IsDir = info.IsDir()
	d.Kind = fileKind(d.Name, d.IsDir)
	if d.IsDir {
		d.Size = "--"
	} else {
		d.Size = FormatSize(info.Size())
	}
	d.Modified = info.ModTime().Format("Jan 2, 2006 at 15:04")
	d.Permissions = info.Mode().String()
	return d
}

func resolvePath(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, path)
}

func fileKind(name string, isDir bool) string {
	if isDir {
		return "Directory"
	}
	ext := filepath.Ext(name)
	if ext != "" && len(ext) > 1 {
		return strings.ToUpper(ext[1:]) + " File"
	}
	return "File"
}

// FormatSize formats a byte count in human-readable form.
func FormatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%dB", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// RenderInfo paints the file-info field list.
func RenderInfo(d FileDetails, gitStatus, lastCommit string) string {
	if d.Path == "" {
		return styles.Muted.Render("No file selected")
	}
	if d.Err != nil {
		return styles.StatusDeleted.Render("Error reading file: " + d.Err.Error())
	}

	labelStyle := styles.Muted.Width(12).Align(lipgloss.Right).MarginRight(2)
	valueStyle := lipgloss.NewStyle().Foreground(styles.TextPrimary)

	fields := []struct{ label, value string }{
		{"Kind:", d.Kind},
		{"Size:", d.Size},
		{"Where:", d.Where},
		{"Modified:", d.Modified},
		{"Permissions:", d.Permissions},
		{"Git Status:", gitStatus},
		{"Commit:", lastCommit},
	}

	var sb strings.Builder
	for i, f := range fields {
		sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
			labelStyle.Render(f.label),
			valueStyle.Render(f.value),
		))
		if i < len(fields)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
