package workspaceops

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// WorktreeDeleteState binds confirmation to this checkout incarnation and the
// bytes it would remove, including ignored and untracked work. It is computed
// only for delete planning/execution, never for catalog or startup refreshes.
// Symlinks are hashed as links; their destinations are never traversed.
func WorktreeDeleteState(ctx context.Context, path string) (string, error) {
	h := sha256.New()
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("cannot identify checkout incarnation")
	}
	if _, err := fmt.Fprintf(h, "%d:%d\n", stat.Dev, stat.Ino); err != nil {
		return "", err
	}
	err = filepath.WalkDir(path, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(path, name)
		if err != nil {
			return err
		}
		// Linked worktrees have a .git file. Never recurse into repository metadata.
		if rel == ".git" && entry.IsDir() {
			return filepath.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if rel == ".git" {
			_, _ = fmt.Fprintf(h, "%d\n", info.ModTime().UnixNano())
		}
		record, _ := json.Marshal(struct {
			Path string
			Mode uint32
			Size int64
		}{rel, uint32(info.Mode()), info.Size()})
		_, _ = h.Write(record)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(name)
			if err != nil {
				return err
			}
			_, _ = h.Write([]byte(target))
		case info.Mode().IsRegular():
			file, err := os.Open(name)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(h, &deleteStateReader{ctx: ctx, r: file})
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case !info.IsDir():
			return fmt.Errorf("cannot confirm removal of special file %q", rel)
		}
		_, _ = h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

type deleteStateReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *deleteStateReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func requireDeleteState(ctx context.Context, req WorktreeRemoval) error {
	if req.ExpectedDeleteState == "" {
		return nil
	}
	state, err := WorktreeDeleteState(ctx, req.Path)
	if err != nil {
		return &WorktreeIdentityError{Cause: fmt.Errorf("cannot verify confirmed checkout state: %w", err)}
	}
	if state != req.ExpectedDeleteState {
		return &WorktreeIdentityError{Cause: fmt.Errorf("worktree contents or incarnation changed since deletion was planned; inspect a new delete plan")}
	}
	return requireRemovalIdentity(ctx, req)
}
