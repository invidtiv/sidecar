package workspacediff

import "context"

// Loader is the git operations a View issues. A nil Loader uses the
// package-level local git functions. Injected loaders must not teach View
// about SSH, JSON, or HostID.
type Loader interface {
	LoadSnapshot(ctx context.Context, workdir, baseRef, ifRevision string) (SnapshotResult, error)
	LoadCommitDetail(ctx context.Context, workdir, hash, ifRevision string) (CommitResult, error)
	LoadRange(ctx context.Context, workdir string, t Target, ifRevision string) (RangeResult, error)
	LoadCommitFile(ctx context.Context, workdir, hash, path, parentHash, ifRevision string) (FileResult, error)
	LoadWorkingTreeFile(ctx context.Context, workdir, path, ifRevision string) (FileResult, error)
}

// SnapshotResult is one working-tree snapshot load.
type SnapshotResult struct {
	Snapshot    *Snapshot
	Revision    string
	NotModified bool
}

// CommitResult is one commit-detail load.
type CommitResult struct {
	Commit      *CommitDetail
	Revision    string
	NotModified bool
}

// RangeResult is one A..B / A...B patch load.
type RangeResult struct {
	Raw         string
	Files       []File
	Revision    string
	NotModified bool
}

// FileResult is one selected-file patch load.
type FileResult struct {
	Path        string
	Raw         string
	Revision    string
	NotModified bool
}

type localLoader struct{}

func (localLoader) LoadSnapshot(ctx context.Context, workdir, baseRef, _ string) (SnapshotResult, error) {
	snap, err := LoadSnapshot(ctx, workdir, baseRef)
	if err != nil {
		return SnapshotResult{}, err
	}
	return SnapshotResult{Snapshot: snap}, nil
}

func (localLoader) LoadCommitDetail(ctx context.Context, workdir, hash, _ string) (CommitResult, error) {
	detail, err := LoadCommitDetail(ctx, workdir, hash)
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{Commit: detail}, nil
}

func (localLoader) LoadRange(ctx context.Context, workdir string, t Target, _ string) (RangeResult, error) {
	raw, err := LoadRangeDiff(ctx, workdir, t)
	if err != nil {
		return RangeResult{}, err
	}
	return RangeResult{Raw: raw, Files: ParseFiles(raw)}, nil
}

func (localLoader) LoadCommitFile(ctx context.Context, workdir, hash, path, parentHash, _ string) (FileResult, error) {
	raw, err := LoadCommitFileDiff(ctx, workdir, hash, path, parentHash)
	if err != nil {
		return FileResult{}, err
	}
	return FileResult{Path: path, Raw: raw}, nil
}

func (localLoader) LoadWorkingTreeFile(ctx context.Context, workdir, path, _ string) (FileResult, error) {
	raw, err := LoadWorkingTreeFileDiff(ctx, workdir, path)
	if err != nil {
		return FileResult{}, err
	}
	return FileResult{Path: path, Raw: raw}, nil
}

func (v *View) git() Loader {
	if v != nil && v.Loader != nil {
		return v.Loader
	}
	return localLoader{}
}
