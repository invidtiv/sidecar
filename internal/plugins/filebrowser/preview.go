package filebrowser

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/alecthomas/chroma/v2/quick"

	"github.com/marcus/sidecar/internal/image"
	"github.com/marcus/sidecar/internal/styles"
)

const (
	maxPreviewSize  = 500 * 1024 // 500KB
	maxPreviewLines = 10000
)

// PreviewResult contains the loaded file content.
type PreviewResult struct {
	Content          string
	Lines            []string
	HighlightedLines []string // Syntax highlighted lines
	IsBinary         bool
	IsImage          bool // True if file is a recognized image format
	IsTruncated      bool
	TotalSize        int64
	ModTime          time.Time   // File modification time
	Mode             os.FileMode // File permissions
	Error            error
}

// PreviewLoadedMsg signals that file preview content is ready.
type PreviewLoadedMsg struct {
	Epoch              uint64 // Epoch when request was issued (for stale detection)
	NavigateGeneration uint64 // Non-zero only for NavigateToFileMsg-owned loads
	Result             PreviewResult
	Path               string
}

// GetEpoch implements plugin.EpochMessage.
func (m PreviewLoadedMsg) GetEpoch() uint64 { return m.Epoch }

// LoadPreview creates a command to load file content.
func LoadPreview(rootDir, path string, epoch uint64) tea.Cmd {
	return func() tea.Msg {
		fullPath := filepath.Join(rootDir, path)

		info, err := os.Stat(fullPath)
		if err != nil {
			return PreviewLoadedMsg{
				Epoch:  epoch,
				Path:   path,
				Result: PreviewResult{Error: err},
			}
		}

		// Check for image files BEFORE binary detection
		// Image files are handled by the image renderer, not text preview
		if image.IsImageFile(path) {
			result := PreviewResult{TotalSize: info.Size(), ModTime: info.ModTime(), Mode: info.Mode()}
			result.IsImage = true
			return PreviewLoadedMsg{Epoch: epoch, Path: path, Result: result}
		}

		f, err := os.Open(fullPath)
		if err != nil {
			return PreviewLoadedMsg{Epoch: epoch, Path: path, Result: PreviewResult{Error: err}}
		}
		defer func() { _ = f.Close() }()
		return PreviewLoadedMsg{
			Epoch:  epoch,
			Path:   path,
			Result: loadPreviewFromOpenFile(f, path, info),
		}
	}
}

// LoadPreviewFile reads an already-open regular file and takes ownership of it.
// This lets containment-sensitive callers pin the inode before scheduling the
// asynchronous read rather than re-following a pathname later.
func LoadPreviewFile(file *os.File, path string, epoch uint64) tea.Cmd {
	return func() tea.Msg {
		defer func() { _ = file.Close() }()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			if err == nil {
				err = fmt.Errorf("not a regular file")
			}
			return PreviewLoadedMsg{Epoch: epoch, Path: path, Result: PreviewResult{Error: err}}
		}
		return PreviewLoadedMsg{Epoch: epoch, Path: path, Result: loadPreviewFromOpenFile(file, path, info)}
	}
}

func loadPreviewFromOpenFile(file *os.File, path string, info os.FileInfo) PreviewResult {
	result := PreviewResult{TotalSize: info.Size(), ModTime: info.ModTime(), Mode: info.Mode()}
	readSize := info.Size()
	if readSize > maxPreviewSize {
		readSize = maxPreviewSize
		result.IsTruncated = true
	}
	data := make([]byte, readSize)
	n, err := file.Read(data)
	if err != nil && !errors.Is(err, io.EOF) {
		result.Error = err
		return result
	}
	data = data[:n]
	if isBinary(data) {
		result.IsBinary = true
		return result
	}
	result.Content = string(data)
	result.Lines = strings.Split(result.Content, "\n")
	highlighted, err := Highlight(result.Content, filepath.Ext(path), styles.GetSyntaxTheme())
	if err == nil {
		result.HighlightedLines = strings.Split(highlighted, "\n")
	} else {
		result.HighlightedLines = result.Lines
	}
	if len(result.Lines) > maxPreviewLines {
		result.Lines = result.Lines[:maxPreviewLines]
		result.HighlightedLines = result.HighlightedLines[:maxPreviewLines]
		result.IsTruncated = true
	}
	return result
}

// Highlight returns a syntax highlighted string.
// Pattern from knipferrc/fm code/code.go
func Highlight(content, extension, syntaxTheme string) (string, error) {
	buf := new(bytes.Buffer)
	if err := quick.Highlight(buf, content, extension, "terminal256", syntaxTheme); err != nil {
		return "", fmt.Errorf("highlight: %w", err)
	}
	return buf.String(), nil
}

// isBinary checks if data contains null bytes in first 512 bytes.
// Pattern from knipferrc/fm filesystem/filesystem.go
func isBinary(data []byte) bool {
	checkLen := 512
	if len(data) < checkLen {
		checkLen = len(data)
	}
	return bytes.Contains(data[:checkLen], []byte{0})
}
