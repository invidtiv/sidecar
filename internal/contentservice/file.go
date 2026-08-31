package contentservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/marcus/sidecar/internal/filepreview"
	"github.com/marcus/sidecar/internal/image"
	"github.com/marcus/sidecar/internal/terminallink"
)

// KindFile is the only kind this slice serves.
const KindFile = "file"

// OpDocument is the file read operation: bounded document bytes + metadata.
const OpDocument = "document"

// MaxLocatorBytes refuses a workspace id or file target larger than this.
// PATH_MAX is typically 1024; this is a hostile-input bound, not a budget.
const MaxLocatorBytes = 4096

// ResolvedFile is identity only: the display value a viewer stores as the
// document reference, and the host's absolute path. No body.
type ResolvedFile struct {
	Display  string
	Absolute string
}

// Document is a bounded file payload plus the revision a later conditional
// read will send back. Mode stays os.FileMode here; the wire DTO converts it.
type Document struct {
	Display     string
	Absolute    string
	Content     string
	Binary      bool
	Image       bool
	Truncated   bool
	TotalSize   int64
	ModTime     time.Time
	Mode        os.FileMode
	Revision    string
	NotModified bool
}

func validateLocator(raw, what string) error {
	if strings.TrimSpace(raw) == "" {
		return Rejected("%s is required", what)
	}
	if len(raw) > MaxLocatorBytes {
		return Rejected("%s exceeds %d bytes", what, MaxLocatorBytes)
	}
	if containsControl(raw) {
		return Rejected("%s contains control characters", what)
	}
	return nil
}

func containsControl(raw string) bool {
	for _, r := range raw {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func isHomeToken(raw string) bool {
	return raw == "~" || strings.HasPrefix(raw, "~/")
}

func isRelativeTarget(raw string) bool {
	if isHomeToken(raw) {
		return false
	}
	return !filepath.IsAbs(raw)
}

func canonical(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

func contained(root, path string) bool {
	root, path = canonical(root), canonical(path)
	if root == "" || path == "" {
		return false
	}
	sep := string(filepath.Separator)
	return path == root || strings.HasPrefix(path, root+sep)
}

// ResolveFile maps a file target onto a regular readable file using root as
// the validated workspace directory.
//
// Relative targets must stay inside root after symlink evaluation. Explicit
// absolute and ~/ targets keep today's local rule: a regular readable file
// outside the project is allowed.
func ResolveFile(root, raw string) (ResolvedFile, error) {
	if err := validateLocator(raw, "target"); err != nil {
		return ResolvedFile{}, err
	}
	display, abs, ok := terminallink.ResolveFile(root, raw)
	if !ok {
		return ResolvedFile{}, Rejected("file %q is not readable from %s", raw, root)
	}
	if isRelativeTarget(raw) && !contained(root, abs) {
		return ResolvedFile{}, Rejected("file %q escapes workspace root", raw)
	}
	return ResolvedFile{Display: display, Absolute: abs}, nil
}

// ReadFile loads bounded document bytes for a previously-resolved target.
// If ifRevision matches the current file revision, the body is omitted and
// NotModified is set — one read, not a metadata preflight plus a second body
// read.
func ReadFile(ctx context.Context, root, target, ifRevision string) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	resolved, err := ResolveFile(root, target)
	if err != nil {
		return Document{}, err
	}
	return readResolved(ctx, resolved, ifRevision)
}

func readResolved(ctx context.Context, resolved ResolvedFile, ifRevision string) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	file, err := terminallink.OpenRegular(resolved.Absolute)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Document{}, Rejected("file %q is not readable", resolved.Display)
		}
		return Document{}, Rejected("file %q is not readable: %v", resolved.Display, err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return Document{}, Internal("stat file", err)
	}
	if !info.Mode().IsRegular() {
		return Document{}, Rejected("file %q is not a regular file", resolved.Display)
	}

	doc := Document{
		Display:   resolved.Display,
		Absolute:  resolved.Absolute,
		TotalSize: info.Size(),
		ModTime:   info.ModTime(),
		Mode:      info.Mode(),
	}

	if image.IsImageFile(resolved.Display) || image.IsImageFile(resolved.Absolute) {
		doc.Image = true
		doc.Revision = revisionFor(info, nil, true)
		if ifRevision != "" && ifRevision == doc.Revision {
			doc.NotModified = true
			return doc, nil
		}
		return doc, nil
	}

	readSize := info.Size()
	if readSize > filepreview.MaxPreviewSize {
		readSize = filepreview.MaxPreviewSize
		doc.Truncated = true
	}
	data := make([]byte, readSize)
	n, err := file.Read(data)
	if err != nil && !errors.Is(err, io.EOF) {
		return Document{}, Internal("read file", err)
	}
	data = data[:n]
	doc.Revision = revisionFor(info, data, false)
	if ifRevision != "" && ifRevision == doc.Revision {
		doc.NotModified = true
		return doc, nil
	}
	if isBinary(data) {
		doc.Binary = true
		return doc, nil
	}
	doc.Content = string(data)
	return doc, nil
}

func revisionFor(info os.FileInfo, data []byte, image bool) string {
	sum := sha256.New()
	_, _ = fmt.Fprintf(sum, "%d:%d:%d:", info.Size(), info.ModTime().UnixNano(), uint32(info.Mode()))
	if image {
		sum.Write([]byte("image"))
	} else {
		sum.Write(data)
	}
	return "v1:" + hex.EncodeToString(sum.Sum(nil))
}

func isBinary(data []byte) bool {
	checkLen := 512
	if len(data) < checkLen {
		checkLen = len(data)
	}
	return containsNull(data[:checkLen])
}

func containsNull(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

func modeOctal(mode os.FileMode) string {
	return fmt.Sprintf("%#o", uint32(mode.Perm()))
}

func formatModTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
