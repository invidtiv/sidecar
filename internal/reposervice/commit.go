package reposervice

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/contentservice"
)

// MaxCommitFiles caps one commit's file list for the same reason MaxStatusFiles
// caps the status one: a labelled truncation beats an answer that will not fit.
const MaxCommitFiles = 5000

// commitFormat is NUL-separated so the body, which contains newlines, can be
// the last field and survive intact.
const commitFormat = "%H%x00%h%x00%an%x00%ae%x00%at%x00%P%x00%s%x00%b"

// CommitResult is the machine contract for `sidecar repo commit --json`.
type CommitResult struct {
	Kind         string        `json:"kind"`
	Workspace    string        `json:"workspace"`
	NoRepository bool          `json:"noRepository,omitempty"`
	Commit       *CommitDetail `json:"commit,omitempty"`
}

// CommitDetail is one commit with its file list.
type CommitDetail struct {
	Hash        string       `json:"hash"`
	ShortHash   string       `json:"shortHash,omitempty"`
	Subject     string       `json:"subject,omitempty"`
	Body        string       `json:"body,omitempty"`
	Author      string       `json:"author,omitempty"`
	AuthorEmail string       `json:"authorEmail,omitempty"`
	Date        time.Time    `json:"date,omitempty"`
	Parents     []string     `json:"parents,omitempty"`
	Merge       bool         `json:"merge,omitempty"`
	Files       []CommitFile `json:"files,omitempty"`
	Truncated   bool         `json:"truncated,omitempty"`
}

// CommitFile is one path inside a commit.
type CommitFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status"`
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`
}

// ValidRemoteResult reports whether a decoded object is this verb's answer.
func (r CommitResult) ValidRemoteResult() bool {
	return r.Kind == KindCommit && strings.TrimSpace(r.Workspace) != ""
}

// Commit returns one commit's metadata and file list.
func (s *Service) Commit(ctx context.Context, workspaceID, hash string) (CommitResult, error) {
	r, ok, err := s.open(ctx, workspaceID)
	if err != nil {
		return CommitResult{}, err
	}
	result := CommitResult{Kind: KindCommit, Workspace: r.ID}
	if !ok {
		result.NoRepository = true
		return result, nil
	}
	commit, err := requireHash(hash)
	if err != nil {
		return CommitResult{}, err
	}

	out, err := s.git(ctx, r.Root, "show", "--format="+commitFormat, "-s", commit)
	if err != nil {
		return CommitResult{}, contentservice.Rejected("commit %q is not in this repository", commit)
	}
	detail := parseCommitMeta(out)
	if detail == nil {
		return CommitResult{}, contentservice.Rejected("commit %q is not in this repository", commit)
	}

	// A merge is diffed against its first parent. git's combined diff for a
	// clean merge lists no files at all, which a viewer would render as a
	// commit that changed nothing.
	nameStatus := []string{"show", "--name-status", "--format=", "-z", detail.Hash}
	numstat := []string{"show", "--numstat", "--format=", "-z", detail.Hash}
	if detail.Merge {
		nameStatus = []string{"diff", "--name-status", "-z", detail.Parents[0], detail.Hash}
		numstat = []string{"diff", "--numstat", "-z", detail.Parents[0], detail.Hash}
	}
	detail.Files = s.commitFiles(ctx, r, nameStatus, numstat)
	if len(detail.Files) > MaxCommitFiles {
		detail.Files = detail.Files[:MaxCommitFiles]
		detail.Truncated = true
	}
	result.Commit = detail
	return result, nil
}

func parseCommitMeta(out []byte) *CommitDetail {
	fields := strings.SplitN(strings.TrimRight(string(out), "\n"), "\x00", 8)
	if len(fields) < 7 || fields[0] == "" {
		return nil
	}
	detail := &CommitDetail{
		Hash:        fields[0],
		ShortHash:   fields[1],
		Author:      fields[2],
		AuthorEmail: fields[3],
		Parents:     strings.Fields(fields[5]),
		Subject:     fields[6],
	}
	detail.Merge = len(detail.Parents) > 1
	if seconds, err := strconv.ParseInt(fields[4], 10, 64); err == nil {
		detail.Date = time.Unix(seconds, 0).UTC()
	}
	if len(fields) > 7 {
		detail.Body = strings.TrimSpace(fields[7])
	}
	return detail
}

func (s *Service) commitFiles(ctx context.Context, r repo, nameStatus, numstat []string) []CommitFile {
	files := map[string]*CommitFile{}
	var order []string
	get := func(path string) *CommitFile {
		if file, ok := files[path]; ok {
			return file
		}
		file := &CommitFile{Path: path, Status: "M"}
		files[path] = file
		order = append(order, path)
		return file
	}

	if out, err := s.git(ctx, r.Root, nameStatus...); err == nil {
		for _, row := range parseNameStatus(out) {
			file := get(row.Path)
			file.Status = row.Status
			file.OldPath = row.OldPath
		}
	}
	if out, err := s.git(ctx, r.Root, numstat...); err == nil {
		for _, row := range parseNumstat(out) {
			file := get(row.Path)
			file.Additions, file.Deletions = row.Additions, row.Deletions
		}
	}

	sort.Strings(order)
	list := make([]CommitFile, 0, len(order))
	for _, path := range order {
		list = append(list, *files[path])
	}
	return list
}

type nameStatusRow struct {
	Status  string
	Path    string
	OldPath string
}

// parseNameStatus reads `--name-status -z`: a status record followed by one
// path, or by an old and a new path for a rename or copy.
func parseNameStatus(out []byte) []nameStatusRow {
	records := splitNUL(out)
	rows := make([]nameStatusRow, 0, len(records))
	for i := 0; i < len(records); i++ {
		code := records[i]
		if code == "" {
			continue
		}
		letter := string(code[0])
		if letter == "R" || letter == "C" {
			if i+2 >= len(records) {
				return rows
			}
			rows = append(rows, nameStatusRow{Status: letter, OldPath: records[i+1], Path: records[i+2]})
			i += 2
			continue
		}
		if i+1 >= len(records) {
			return rows
		}
		rows = append(rows, nameStatusRow{Status: letter, Path: records[i+1]})
		i++
	}
	return rows
}
