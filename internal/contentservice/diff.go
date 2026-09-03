package contentservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/workspacediff"
)

// DiffDTO is the explicit wire form of one diff operation. Snapshot, commit,
// range, file, and full-file are mutually exclusive per operation.
type DiffDTO struct {
	Target     string           `json:"target"`
	Snapshot   *DiffSnapshotDTO `json:"snapshot,omitempty"`
	Commit     *DiffCommitDTO   `json:"commit,omitempty"`
	Range      *DiffRangeDTO    `json:"range,omitempty"`
	File       *DiffFileDTO     `json:"file,omitempty"`
	FullFile   *DiffFullFileDTO `json:"fullFile,omitempty"`
	Truncated  bool             `json:"truncated,omitempty"`
	PageOffset int              `json:"pageOffset,omitempty"`
	PageLimit  int              `json:"pageLimit,omitempty"`
	PageTotal  int              `json:"pageTotal,omitempty"`
}

// DiffSnapshotDTO is a working-tree summary/detail payload. File Raw patches
// may be omitted when the encoded form would blow the cap; the viewer then
// loads OpWorkingTreeFile.
type DiffSnapshotDTO struct {
	State                 string             `json:"state,omitempty"`
	WorkingTree           string             `json:"workingTree,omitempty"`
	Files                 []DiffFileRowDTO   `json:"files,omitempty"`
	Commits               []DiffCommitRowDTO `json:"commits,omitempty"`
	AggregateCommitted    string             `json:"aggregateCommitted,omitempty"`
	AggregateUncommitted  string             `json:"aggregateUncommitted,omitempty"`
	BaseRef               string             `json:"baseRef,omitempty"`
	MergeBase             string             `json:"mergeBase,omitempty"`
	UntrackedShown        int                `json:"untrackedShown,omitempty"`
	UntrackedOmitted      int                `json:"untrackedOmitted,omitempty"`
	UntrackedBytesOmitted int64              `json:"untrackedBytesOmitted,omitempty"`
	Truncated             bool               `json:"truncated,omitempty"`
}

// DiffFileRowDTO is one working-tree or range file.
type DiffFileRowDTO struct {
	Path      string `json:"path"`
	Raw       string `json:"raw,omitempty"`
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`
}

// DiffCommitRowDTO is one row in the working-tree commit list.
type DiffCommitRowDTO struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject,omitempty"`
	Pushed  bool   `json:"pushed,omitempty"`
}

// DiffCommitDTO is one commit's file list.
type DiffCommitDTO struct {
	Hash         string              `json:"hash"`
	ShortHash    string              `json:"shortHash,omitempty"`
	Subject      string              `json:"subject,omitempty"`
	IsMerge      bool                `json:"isMerge,omitempty"`
	ParentHashes []string            `json:"parentHashes,omitempty"`
	Files        []DiffCommitFileDTO `json:"files,omitempty"`
}

// DiffCommitFileDTO is one path inside a commit.
type DiffCommitFileDTO struct {
	Path      string `json:"path"`
	Status    string `json:"status,omitempty"`
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`
}

// DiffRangeDTO is git diff A..B / A...B.
type DiffRangeDTO struct {
	Spec  string           `json:"spec"`
	Raw   string           `json:"raw,omitempty"`
	Files []DiffFileRowDTO `json:"files,omitempty"`
}

// DiffFileDTO is one selected-file patch (working-tree or commit).
type DiffFileDTO struct {
	Path string `json:"path"`
	Raw  string `json:"raw,omitempty"`
}

// DiffFullFileDTO is a paged old/new file pair for full-file view.
type DiffFullFileDTO struct {
	Path       string `json:"path"`
	OldContent string `json:"oldContent,omitempty"`
	NewContent string `json:"newContent,omitempty"`
	RawDiff    string `json:"rawDiff,omitempty"`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Total      int    `json:"total,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// DiffDocument is the host-side diff payload plus the revision a later
// conditional read will send back.
type DiffDocument struct {
	DTO         *DiffDTO
	Snapshot    *workspacediff.Snapshot
	Commit      *workspacediff.CommitDetail
	RangeRaw    string
	FileRaw     string
	FilePath    string
	Revision    string
	NotModified bool
}

// ReadParams is one content read, including optional diff locators.
type ReadParams struct {
	WorkspaceID string
	Kind        string
	Operation   string
	Target      string
	IfRevision  string
	Path        string
	Parent      string
	Offset      int
	Limit       int
	Provider    string
	Matcher     string
	Refresh     bool
	// Collection and the four beside it are the plugin collection operations'
	// parameters. They are meaningless for every other kind.
	Collection string
	Query      string
	View       string
	Sort       string
	Cursor     string
}

func validDiffOperation(op string) bool {
	switch op {
	case OpWorkingTree, OpWorkingTreeFile, OpCommit, OpRange, OpCommitFile, OpFullFile:
		return true
	default:
		return false
	}
}

func (s *Service) resolveDiff(ctx context.Context, workspaceID, target string) (ResolveResult, error) {
	ws, err := s.lookupWorkspace(ctx, workspaceID)
	if err != nil {
		return ResolveResult{}, err
	}
	resolved, err := ResolveDiff(ctx, ws.Root, target)
	if err != nil {
		return ResolveResult{}, err
	}
	ident := resolved.Identity()
	return ResolveResult{
		Kind:      KindDiff,
		Workspace: ws.ID,
		Target:    ident,
		Display:   resolved.TabLabel(),
		Revision:  ident,
	}, nil
}

// ResolveDiff existence-gates a git spec against root. Working-tree targets
// are returned unchanged; commit and range specs are rev-parsed.
func ResolveDiff(ctx context.Context, root, spec string) (workspacediff.Target, error) {
	if err := ctx.Err(); err != nil {
		return workspacediff.Target{}, err
	}
	if err := validateLocator(spec, "target"); err != nil {
		return workspacediff.Target{}, err
	}
	target, ok := workspacediff.ParseSpec(spec)
	if !ok {
		return workspacediff.Target{}, Rejected("not a git spec %q", spec)
	}
	resolved, err := workspacediff.ResolveSpec(ctx, root, target)
	if err != nil {
		return workspacediff.Target{}, Rejected("unknown git object %q", spec)
	}
	if resolved.Identity() == "" {
		return workspacediff.Target{}, Rejected("unknown git object %q", spec)
	}
	return resolved, nil
}

// ReadDiff loads one diff operation from an already-validated root.
func ReadDiff(ctx context.Context, root string, params ReadParams) (DiffDocument, error) {
	return Default().readDiffAt(ctx, root, params)
}

func (s *Service) readDiffAt(ctx context.Context, root string, params ReadParams) (DiffDocument, error) {
	if err := ctx.Err(); err != nil {
		return DiffDocument{}, err
	}
	if err := validateLocator(params.Target, "target"); err != nil {
		return DiffDocument{}, err
	}
	switch params.Operation {
	case OpWorkingTree:
		return s.readWorkingTree(ctx, root, params.IfRevision)
	case OpWorkingTreeFile:
		return s.readWorkingTreeFile(ctx, root, params.Path, params.IfRevision)
	case OpCommit:
		return s.readCommit(ctx, root, params.Target, params.IfRevision)
	case OpRange:
		return s.readRange(ctx, root, params.Target, params.IfRevision)
	case OpCommitFile:
		return s.readCommitFile(ctx, root, params.Target, params.Path, params.Parent, params.IfRevision)
	case OpFullFile:
		return s.readFullFile(ctx, root, params)
	default:
		return DiffDocument{}, Usage("unknown content operation %q", params.Operation)
	}
}

func (s *Service) readWorkingTree(ctx context.Context, root, ifRevision string) (DiffDocument, error) {
	rev, err := s.workingTreeRevision(ctx, root)
	if err != nil {
		return DiffDocument{}, err
	}
	if ifRevision != "" && ifRevision == rev {
		return DiffDocument{Revision: rev, NotModified: true}, nil
	}
	snap, err := workspacediff.LoadSnapshot(ctx, root, "")
	if err != nil {
		return DiffDocument{}, Internal("load working-tree diff", err)
	}
	dto := diffSnapshotDTO(snap)
	return DiffDocument{DTO: dto, Snapshot: snap, Revision: rev}, nil
}

func (s *Service) readWorkingTreeFile(ctx context.Context, root, path, ifRevision string) (DiffDocument, error) {
	rel, err := containDiffPath(path)
	if err != nil {
		return DiffDocument{}, err
	}
	rev, err := s.workingTreeRevision(ctx, root)
	if err != nil {
		return DiffDocument{}, err
	}
	fileRev := rev + ":" + rel
	if ifRevision != "" && ifRevision == fileRev {
		return DiffDocument{Revision: fileRev, NotModified: true}, nil
	}
	raw, err := loadWorkingTreeFileDiff(ctx, root, rel)
	if err != nil {
		return DiffDocument{}, Internal("load working-tree file", err)
	}
	dto := &DiffDTO{
		Target: workspacediff.IdentityWorkingTree,
		File:   &DiffFileDTO{Path: rel, Raw: raw},
	}
	return DiffDocument{DTO: dto, FileRaw: raw, FilePath: rel, Revision: fileRev}, nil
}

func (s *Service) readCommit(ctx context.Context, root, target, ifRevision string) (DiffDocument, error) {
	spec, err := ResolveDiff(ctx, root, target)
	if err != nil {
		return DiffDocument{}, err
	}
	if spec.Kind != workspacediff.TargetCommit {
		return DiffDocument{}, Rejected("target %q is not a commit", target)
	}
	rev := spec.Identity()
	if ifRevision != "" && ifRevision == rev {
		return DiffDocument{Revision: rev, NotModified: true}, nil
	}
	detail, err := workspacediff.LoadCommitDetail(ctx, root, spec.A)
	if err != nil {
		return DiffDocument{}, Internal("load commit", err)
	}
	if detail == nil {
		return DiffDocument{}, Rejected("commit %q not found", spec.A)
	}
	dto := &DiffDTO{Target: rev, Commit: diffCommitDTO(detail)}
	return DiffDocument{DTO: dto, Commit: detail, Revision: rev}, nil
}

func (s *Service) readRange(ctx context.Context, root, target, ifRevision string) (DiffDocument, error) {
	spec, err := ResolveDiff(ctx, root, target)
	if err != nil {
		return DiffDocument{}, err
	}
	if spec.Kind != workspacediff.TargetRange {
		return DiffDocument{}, Rejected("target %q is not a range", target)
	}
	rev := spec.Identity()
	if ifRevision != "" && ifRevision == rev {
		return DiffDocument{Revision: rev, NotModified: true}, nil
	}
	raw, err := workspacediff.LoadRangeDiff(ctx, root, spec)
	if err != nil {
		return DiffDocument{}, Internal("load range", err)
	}
	files := workspacediff.ParseFiles(raw)
	dto := &DiffDTO{
		Target: rev,
		Range:  &DiffRangeDTO{Spec: strings.TrimPrefix(rev, "r:"), Raw: raw, Files: diffFileRows(files)},
	}
	return DiffDocument{DTO: dto, RangeRaw: raw, Revision: rev}, nil
}

func (s *Service) readCommitFile(ctx context.Context, root, target, path, parent, ifRevision string) (DiffDocument, error) {
	rel, err := containDiffPath(path)
	if err != nil {
		return DiffDocument{}, err
	}
	spec, err := ResolveDiff(ctx, root, target)
	if err != nil {
		return DiffDocument{}, err
	}
	hash := spec.A
	if spec.Kind == workspacediff.TargetWorkingTree {
		return DiffDocument{}, Rejected("commit-file requires a commit target")
	}
	if spec.Kind == workspacediff.TargetRange {
		hash = spec.B
	}
	rev := "c:" + hash + ":" + rel
	if parent != "" {
		rev += ":" + parent
	}
	if ifRevision != "" && ifRevision == rev {
		return DiffDocument{Revision: rev, NotModified: true}, nil
	}
	raw, err := workspacediff.LoadCommitFileDiff(ctx, root, hash, rel, parent)
	if err != nil {
		return DiffDocument{}, Internal("load commit file", err)
	}
	dto := &DiffDTO{Target: spec.Identity(), File: &DiffFileDTO{Path: rel, Raw: raw}}
	return DiffDocument{DTO: dto, FileRaw: raw, FilePath: rel, Revision: rev}, nil
}

func (s *Service) readFullFile(ctx context.Context, root string, params ReadParams) (DiffDocument, error) {
	rel, err := containDiffPath(params.Path)
	if err != nil {
		return DiffDocument{}, err
	}
	spec, err := ResolveDiff(ctx, root, params.Target)
	if err != nil {
		return DiffDocument{}, err
	}
	oldContent, newContent, rawDiff, rev, err := s.fullFileContents(ctx, root, spec, rel, params.Parent)
	if err != nil {
		return DiffDocument{}, err
	}
	if params.IfRevision != "" && params.IfRevision == rev {
		return DiffDocument{Revision: rev, NotModified: true}, nil
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}
	oldPage, oldTotal := pageText(oldContent, offset, params.Limit)
	newPage, newTotal := pageText(newContent, offset, params.Limit)
	total := oldTotal
	if newTotal > total {
		total = newTotal
	}
	truncated := params.Limit > 0 && (offset+params.Limit) < total
	if len(oldContent) > workspacediff.MaxUntrackedFileSize || len(newContent) > workspacediff.MaxUntrackedFileSize {
		truncated = true
	}
	dto := &DiffDTO{
		Target: spec.Identity(),
		FullFile: &DiffFullFileDTO{
			Path: rel, OldContent: oldPage, NewContent: newPage, RawDiff: rawDiff,
			Offset: offset, Limit: params.Limit, Total: total, Truncated: truncated,
		},
		Truncated:  truncated,
		PageOffset: offset,
		PageLimit:  params.Limit,
		PageTotal:  total,
	}
	return DiffDocument{DTO: dto, FilePath: rel, Revision: rev}, nil
}

func (s *Service) fullFileContents(ctx context.Context, root string, spec workspacediff.Target, path, parent string) (oldContent, newContent, rawDiff, rev string, err error) {
	switch spec.Kind {
	case workspacediff.TargetCommit:
		parentRef := spec.A + "~1"
		if parent != "" {
			parentRef = parent
		}
		oldContent, _ = s.gitShowFile(ctx, root, parentRef, path)
		newContent, _ = s.gitShowFile(ctx, root, spec.A, path)
		rawDiff, err = workspacediff.LoadCommitFileDiff(ctx, root, spec.A, path, parent)
		rev = spec.Identity() + ":" + path
		return oldContent, newContent, rawDiff, rev, err
	default:
		oldContent, _ = s.gitShowFile(ctx, root, "HEAD", path)
		newContent, _ = readWorktreeFileBounded(root, path)
		rawDiff, err = loadWorkingTreeFileDiff(ctx, root, path)
		wtRev, revErr := s.workingTreeRevision(ctx, root)
		if revErr != nil {
			return "", "", "", "", revErr
		}
		return oldContent, newContent, rawDiff, wtRev + ":" + path, err
	}
}

func (s *Service) gitShowFile(ctx context.Context, root, rev, path string) (string, error) {
	out, err := s.gitOutput(ctx, root, "show", rev+":"+filepath.ToSlash(path))
	if err != nil {
		return "", err
	}
	if len(out) > workspacediff.MaxUntrackedFileSize {
		return string(out[:workspacediff.MaxUntrackedFileSize]), nil
	}
	return string(out), nil
}

func (s *Service) gitOutput(ctx context.Context, root string, args ...string) ([]byte, error) {
	git := s.Git
	if git == nil {
		git = defaultGit
	}
	return git(ctx, root, args...)
}

func (s *Service) workingTreeRevision(ctx context.Context, root string) (string, error) {
	head, err := s.gitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return "", Internal("resolve HEAD", err)
	}
	status, err := s.gitOutput(ctx, root, "status", "--porcelain=v1", "-z")
	if err != nil {
		return "", Internal("git status", err)
	}
	sum := sha256.Sum256(append(append(bytes.TrimSpace(head), '\n'), status...))
	return "v1:" + hex.EncodeToString(sum[:]), nil
}

func containDiffPath(path string) (string, error) {
	if err := validateLocator(path, "path"); err != nil {
		return "", err
	}
	cleaned := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", Rejected("path %q escapes the workspace", path)
	}
	return filepath.ToSlash(cleaned), nil
}

func loadWorkingTreeFileDiff(ctx context.Context, root, path string) (string, error) {
	raw, err := workspacediff.LoadWorkingTreeFileDiff(ctx, root, path)
	if err != nil {
		return "", err
	}
	return raw, nil
}

func readWorktreeFileBounded(root, path string) (string, error) {
	full := filepath.Join(root, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	if info.Size() > workspacediff.MaxUntrackedFileSize {
		return "", nil
	}
	f, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, workspacediff.MaxUntrackedFileSize+1))
	if err != nil {
		return "", err
	}
	if len(data) > workspacediff.MaxUntrackedFileSize {
		return "", nil
	}
	return string(data), nil
}

func pageText(s string, offset, limit int) (string, int) {
	if s == "" {
		return "", 0
	}
	lines := strings.Split(s, "\n")
	total := len(lines)
	if limit <= 0 {
		if offset <= 0 {
			return s, total
		}
		if offset >= total {
			return "", total
		}
		return strings.Join(lines[offset:], "\n"), total
	}
	if offset >= total {
		return "", total
	}
	if offset < 0 {
		offset = 0
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return strings.Join(lines[offset:end], "\n"), total
}

func diffSnapshotDTO(snap *workspacediff.Snapshot) *DiffDTO {
	if snap == nil {
		return &DiffDTO{Target: workspacediff.IdentityWorkingTree}
	}
	files := workspacediff.ParseFiles(snap.WorkingTree)
	return &DiffDTO{
		Target: workspacediff.IdentityWorkingTree,
		Snapshot: &DiffSnapshotDTO{
			State:                 persistLoadState(snap.State),
			WorkingTree:           snap.WorkingTree,
			Files:                 diffFileRows(files),
			Commits:               diffCommitRows(snap.Commits),
			AggregateCommitted:    snap.AggregateCommitted,
			AggregateUncommitted:  snap.AggregateUncommitted,
			BaseRef:               snap.BaseRef,
			MergeBase:             snap.MergeBase,
			UntrackedShown:        snap.UntrackedShown,
			UntrackedOmitted:      snap.UntrackedOmitted,
			UntrackedBytesOmitted: snap.UntrackedBytesOmitted,
			Truncated:             snap.Truncated,
		},
		Truncated: snap.Truncated,
	}
}

func persistLoadState(state workspacediff.LoadState) string {
	switch state {
	case workspacediff.LoadStateClean:
		return "clean"
	case workspacediff.LoadStateTruncated:
		return "truncated"
	case workspacediff.LoadStateError:
		return "error"
	case workspacediff.LoadStateLoading:
		return "loading"
	case workspacediff.LoadStateReady:
		return "ready"
	default:
		return ""
	}
}

func parseLoadState(s string) workspacediff.LoadState {
	switch s {
	case "clean":
		return workspacediff.LoadStateClean
	case "truncated":
		return workspacediff.LoadStateTruncated
	case "error":
		return workspacediff.LoadStateError
	case "loading":
		return workspacediff.LoadStateLoading
	default:
		return workspacediff.LoadStateReady
	}
}

func diffFileRows(files []workspacediff.File) []DiffFileRowDTO {
	if len(files) == 0 {
		return nil
	}
	out := make([]DiffFileRowDTO, len(files))
	for i, f := range files {
		out[i] = DiffFileRowDTO{Path: f.Path, Raw: f.Raw, Additions: f.Additions, Deletions: f.Deletions}
	}
	return out
}

func diffCommitRows(commits []workspacediff.CommitInfo) []DiffCommitRowDTO {
	if len(commits) == 0 {
		return nil
	}
	out := make([]DiffCommitRowDTO, len(commits))
	for i, c := range commits {
		out[i] = DiffCommitRowDTO{Hash: c.Hash, Subject: c.Subject, Pushed: c.Pushed}
	}
	return out
}

func diffCommitDTO(detail *workspacediff.CommitDetail) *DiffCommitDTO {
	if detail == nil {
		return nil
	}
	files := make([]DiffCommitFileDTO, len(detail.Files))
	for i, f := range detail.Files {
		files[i] = DiffCommitFileDTO{Path: f.Path, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions}
	}
	return &DiffCommitDTO{
		Hash:         detail.Hash,
		ShortHash:    detail.ShortHash,
		Subject:      detail.Subject,
		IsMerge:      detail.IsMerge,
		ParentHashes: append([]string(nil), detail.ParentHashes...),
		Files:        files,
	}
}

func diffReadResultFrom(workspace string, doc DiffDocument, operation string) ReadResult {
	result := ReadResult{
		Kind:        KindDiff,
		Operation:   operation,
		Workspace:   workspace,
		Revision:    doc.Revision,
		NotModified: doc.NotModified,
	}
	if doc.NotModified {
		result.Operation = ""
		result.Workspace = ""
		return result
	}
	result.Diff = doc.DTO
	if result.Diff != nil {
		result.Target = result.Diff.Target
		result.Display = result.Diff.Target
		result.Truncated = result.Diff.Truncated
	}
	return result
}

// SnapshotFromDTO converts a wire working-tree snapshot back into the shared viewer payload.
func SnapshotFromDTO(dto *DiffSnapshotDTO) *workspacediff.Snapshot {
	if dto == nil {
		return nil
	}
	commits := make([]workspacediff.CommitInfo, len(dto.Commits))
	for i, c := range dto.Commits {
		commits[i] = workspacediff.CommitInfo{Hash: c.Hash, Subject: c.Subject, Pushed: c.Pushed}
	}
	working := dto.WorkingTree
	files := make([]workspacediff.File, len(dto.Files))
	for i, f := range dto.Files {
		files[i] = workspacediff.File{Path: f.Path, Raw: f.Raw, Additions: f.Additions, Deletions: f.Deletions}
	}
	if working == "" && len(files) > 0 {
		parts := make([]string, 0, len(files))
		for _, f := range files {
			if f.Raw != "" {
				parts = append(parts, strings.TrimRight(f.Raw, "\n"))
			}
		}
		working = strings.Join(parts, "\n")
	}
	return &workspacediff.Snapshot{
		State:                 parseLoadState(dto.State),
		WorkingTree:           working,
		Files:                 files,
		Commits:               commits,
		AggregateCommitted:    dto.AggregateCommitted,
		AggregateUncommitted:  dto.AggregateUncommitted,
		BaseRef:               dto.BaseRef,
		MergeBase:             dto.MergeBase,
		UntrackedShown:        dto.UntrackedShown,
		UntrackedOmitted:      dto.UntrackedOmitted,
		UntrackedBytesOmitted: dto.UntrackedBytesOmitted,
		Truncated:             dto.Truncated,
	}
}

// CommitFromDTO converts a wire commit detail back into the shared viewer payload.
func CommitFromDTO(dto *DiffCommitDTO) *workspacediff.CommitDetail {
	if dto == nil {
		return nil
	}
	files := make([]workspacediff.CommitFile, len(dto.Files))
	for i, f := range dto.Files {
		files[i] = workspacediff.CommitFile{Path: f.Path, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions}
	}
	return &workspacediff.CommitDetail{
		Hash:         dto.Hash,
		ShortHash:    dto.ShortHash,
		Subject:      dto.Subject,
		IsMerge:      dto.IsMerge,
		ParentHashes: append([]string(nil), dto.ParentHashes...),
		Files:        files,
	}
}
