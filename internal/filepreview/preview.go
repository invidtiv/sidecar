// Package filepreview loads a regular file into a text preview.
// It is the shared loader for the Files plugin and docview, so neither
// surface needs to import the other.
package filepreview

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
	// MaxPreviewSize is the raw-byte cap for a document preview. JSON-encoded
	// remote payloads may have to cut earlier; see contentservice.MaxEncodedBytes.
	MaxPreviewSize = 500 * 1024
	// MaxPreviewLines caps the line split a viewer keeps from a preview.
	MaxPreviewLines = 10000
)

// PreviewResult contains the loaded file content.
type PreviewResult struct {
	Content          string
	Lines            []string
	HighlightedLines []string
	IsBinary         bool
	IsImage          bool
	IsTruncated      bool
	TotalSize        int64
	ModTime          time.Time
	Mode             os.FileMode
	Error            error
}

// PreviewLoadedMsg signals that file preview content is ready.
type PreviewLoadedMsg struct {
	Epoch              uint64
	NavigateGeneration uint64
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
	if readSize > MaxPreviewSize {
		readSize = MaxPreviewSize
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
	if len(result.Lines) > MaxPreviewLines {
		result.Lines = result.Lines[:MaxPreviewLines]
		result.HighlightedLines = result.HighlightedLines[:MaxPreviewLines]
		result.IsTruncated = true
	}
	return result
}

// Highlight returns a syntax highlighted string.
func Highlight(content, extension, syntaxTheme string) (string, error) {
	buf := new(bytes.Buffer)
	if err := quick.Highlight(buf, content, extension, "terminal256", syntaxTheme); err != nil {
		return "", fmt.Errorf("highlight: %w", err)
	}
	return buf.String(), nil
}

func isBinary(data []byte) bool {
	checkLen := 512
	if len(data) < checkLen {
		checkLen = len(data)
	}
	return bytes.Contains(data[:checkLen], []byte{0})
}
