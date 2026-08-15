// Package workspaceops holds the parts of workspace creation that belong to no
// particular view.
//
// It exists so the project Workspaces plugin and, later, the global browser can
// invoke one implementation of the same operation rather than each owning a
// copy. The rule that keeps it honest is the same one workspacelist follows:
// nothing here imports a plugin, the app, or a view. It receives explicit
// arguments and returns explicit results.
//
// This first file is the path-containment and durable-write layer. It moved
// first because it is the code most worth having in one reviewable place: every
// function here exists to stop a symlink, a racing rename, or a partially
// written file from turning workspace setup into a write outside the worktree
// it was aimed at.
package workspaceops

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// SafeRelativePath reports whether a configured path may be joined to a root.
// It is the first gate: absolute paths, "..", and anything climbing out are
// refused before any filesystem call is made.
func SafeRelativePath(path string) bool {
	clean := filepath.Clean(path)
	return path != "" && clean != "." && clean != ".." && !filepath.IsAbs(path) &&
		!strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// ContainedRegularFile resolves rel under root and returns its real path,
// refusing anything that is not a regular file reached without traversing a
// symlink.
func ContainedRegularFile(root, rel string) (string, error) {
	file, err := OpenContainedRegularFile(root, rel)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	return file.Name(), nil
}

// OpenContainedRegularFile opens rel under root with no symlink traversal.
func OpenContainedRegularFile(root, rel string) (*os.File, error) {
	return OpenContainedRegularFileWithHook(root, rel, nil)
}

// OpenContainedRegularFileWithHook is OpenContainedRegularFile with a seam for
// tests to interleave a racing rename between pinning the root and walking it.
func OpenContainedRegularFileWithHook(root, rel string, beforeWalk func()) (*os.File, error) {
	if !SafeRelativePath(rel) {
		return nil, fmt.Errorf("path must remain relative")
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	rootDir, err := OpenPinnedDirectory(rootReal, ".", false)
	if err != nil {
		return nil, err
	}
	if beforeWalk != nil {
		beforeWalk()
	}
	dir, err := WalkPinnedDirectory(rootDir, filepath.Dir(filepath.Clean(rel)), false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	leaf := filepath.Base(filepath.Clean(rel))
	fd, err := unix.Openat(int(dir.Fd()), leaf, unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, NormalizeOpenError(filepath.Join(rootReal, filepath.Clean(rel)), err)
	}
	target := filepath.Join(rootReal, filepath.Clean(rel))
	file := os.NewFile(uintptr(fd), target)
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("artifact is not a regular file: %s", target)
	}
	return file, nil
}

// OpenPinnedDirectory opens root, then walks rel beneath it a component at a
// time. The caller owns the returned directory handle.
func OpenPinnedDirectory(root, rel string, create bool) (*os.File, error) {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(rootReal, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	current := os.NewFile(uintptr(fd), rootReal)
	return WalkPinnedDirectory(current, rel, create)
}

// WalkPinnedDirectory descends rel from an already-open directory using openat
// with O_NOFOLLOW, so no component can be swapped for a symlink mid-walk. It
// closes current on every path out, including failure.
func WalkPinnedDirectory(current *os.File, rel string, create bool) (*os.File, error) {
	clean := filepath.Clean(rel)
	if clean == "." || clean == "" {
		return current, nil
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		_ = current.Close()
		return nil, fmt.Errorf("directory path escapes pinned root")
	}
	for _, component := range strings.Split(clean, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		if create {
			if err := unix.Mkdirat(int(current.Fd()), component, 0755); err != nil && !errors.Is(err, unix.EEXIST) {
				_ = current.Close()
				return nil, err
			}
		}
		nextFD, err := unix.Openat(int(current.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil {
			path := filepath.Join(current.Name(), component)
			_ = current.Close()
			return nil, NormalizeOpenError(path, err)
		}
		next := os.NewFile(uintptr(nextFD), filepath.Join(current.Name(), component))
		_ = current.Close()
		current = next
	}
	return current, nil
}

// PathError gives callers a stable, actionable refusal while retaining the
// platform errno for errors.Is and diagnostics. With O_NOFOLLOW, Darwin reports
// a final symlink as ELOOP and a symlink used as a directory as ENOTDIR; both
// mean the path cannot be safely traversed.
type PathError struct {
	Path string
	Err  error
}

func (e *PathError) Error() string {
	return fmt.Sprintf("path containment refused for %q: symlink or non-directory component", e.Path)
}

func (e *PathError) Unwrap() error { return e.Err }

// NormalizeOpenError converts the platform's symlink errnos into a PathError.
func NormalizeOpenError(path string, err error) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return &PathError{Path: path, Err: err}
	}
	return err
}

// CopyOpenFile copies an already-opened source to dst, preserving its mode and
// flushing before it reports success.
func CopyOpenFile(source *os.File, dst string) error {
	info, err := source.Stat()
	if err != nil {
		return err
	}
	dest, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dest, source)
	if syncErr := dest.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := dest.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

// EnsureRealDirectoryPath rejects symlink traversal for every existing path
// component below root. Missing components are allowed only when requested;
// callers re-run this after creation to narrow the remaining TOCTOU window.
func EnsureRealDirectoryPath(root, target string, requireExisting bool) error {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootReal, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes allowed root")
	}
	current := rootReal
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			if requireExisting {
				return statErr
			}
			continue
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink path component is not allowed: %s", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("path component is not a directory: %s", current)
		}
	}
	return nil
}

// MkdirPinnedTemp creates a uniquely named staging directory inside an already
// open directory handle, so the parent cannot be swapped between naming and
// creating.
func MkdirPinnedTemp(root *os.File) (string, error) {
	for attempt := 0; attempt < 100; attempt++ {
		name := fmt.Sprintf(".sidecar-worktree-%d-%d", os.Getpid(), time.Now().UnixNano()+int64(attempt))
		if err := unix.Mkdirat(int(root.Fd()), name, 0700); err == nil {
			return name, nil
		} else if !errors.Is(err, unix.EEXIST) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate a staging directory")
}

// WriteDurableFile writes data to path atomically: a temp file in the same
// directory, flushed and renamed, then the directory itself flushed. A reader
// sees either the old contents or the new ones, never a partial write, and the
// result survives a crash immediately after the call returns.
func WriteDurableFile(path string, data []byte, mode os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sidecar-write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmpName, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
