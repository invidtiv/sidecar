package contentservice

import "strings"

// ResolveResult is the machine contract for `sidecar content resolve --json`.
//
// Every field that makes this this verb's answer is required so a structured
// log line cannot decode as success. Mode is an octal permission string, not
// os.FileMode. Issue and note resolve is identity only: Target is the
// normalized id and Revision is omitted because resolve does not consult td.
type ResolveResult struct {
	Kind      string `json:"kind"`
	Workspace string `json:"workspace,omitempty"`
	Display   string `json:"display,omitempty"`
	Path      string `json:"path,omitempty"`
	Target    string `json:"target,omitempty"`
	Revision  string `json:"revision,omitempty"`
	TotalSize int64  `json:"totalSize,omitempty"`
	ModTime   string `json:"modTime,omitempty"`
	Mode      string `json:"mode,omitempty"`
}

// ValidRemoteResult reports whether a decoded object is this verb's answer.
func (r ResolveResult) ValidRemoteResult() bool {
	if strings.TrimSpace(r.Workspace) == "" {
		return false
	}
	switch r.Kind {
	case KindFile:
		return strings.TrimSpace(r.Display) != "" &&
			strings.TrimSpace(r.Path) != "" &&
			strings.TrimSpace(r.Revision) != ""
	case KindIssue, KindNote, KindDiff:
		return strings.TrimSpace(r.Target) != ""
	default:
		return false
	}
}

// ReadResult is the machine contract for `sidecar content read --json`.
//
// notModified, oversize, and a full payload are all answers. A log line is
// not. Content is omitted on notModified and on binary/image documents.
// Issue and Note are explicit wire DTOs — never a json:"-" graph dump.
type ReadResult struct {
	Kind        string    `json:"kind"`
	Operation   string    `json:"operation,omitempty"`
	Workspace   string    `json:"workspace,omitempty"`
	Display     string    `json:"display,omitempty"`
	Path        string    `json:"path,omitempty"`
	Target      string    `json:"target,omitempty"`
	Revision    string    `json:"revision,omitempty"`
	NotModified bool      `json:"notModified,omitempty"`
	Oversize    bool      `json:"oversize,omitempty"`
	Content     string    `json:"content,omitempty"`
	Binary      bool      `json:"binary,omitempty"`
	Image       bool      `json:"image,omitempty"`
	Truncated   bool      `json:"truncated,omitempty"`
	TotalSize   int64     `json:"totalSize,omitempty"`
	ModTime     string    `json:"modTime,omitempty"`
	Mode        string    `json:"mode,omitempty"`
	Issue       *IssueDTO `json:"issue,omitempty"`
	Note        *NoteDTO  `json:"note,omitempty"`
	Diff        *DiffDTO  `json:"diff,omitempty"`
}

// ValidRemoteResult reports whether a decoded object is this verb's answer.
//
// Three shapes are answers: a notModified refresh, a structured oversize
// refusal, and a typed payload carrying workspace + revision. A login
// profile logging `{"level":"info","msg":"loading nvm","path":"..."}` matches
// none of them.
func (r ReadResult) ValidRemoteResult() bool {
	switch r.Kind {
	case KindFile, KindIssue, KindNote, KindDiff:
	default:
		return false
	}
	if strings.TrimSpace(r.Revision) == "" {
		return false
	}
	if r.NotModified {
		return true
	}
	if r.Oversize {
		return true
	}
	if strings.TrimSpace(r.Workspace) == "" {
		return false
	}
	switch r.Kind {
	case KindFile:
		return strings.TrimSpace(r.Display) != "" && r.Operation == OpDocument
	case KindIssue:
		return r.Operation == OpCard && r.Issue != nil && strings.TrimSpace(r.Issue.ID) != ""
	case KindNote:
		return r.Operation == OpNote && r.Note != nil && strings.TrimSpace(r.Note.ID) != ""
	case KindDiff:
		return validDiffOperation(r.Operation) && r.Diff != nil && strings.TrimSpace(r.Diff.Target) != ""
	default:
		return false
	}
}

func resolveResultFrom(workspace string, doc Document) ResolveResult {
	return ResolveResult{
		Kind:      KindFile,
		Workspace: workspace,
		Display:   doc.Display,
		Path:      doc.Absolute,
		Revision:  doc.Revision,
		TotalSize: doc.TotalSize,
		ModTime:   formatModTime(doc.ModTime),
		Mode:      modeOctal(doc.Mode),
	}
}

func readResultFrom(workspace string, doc Document) ReadResult {
	result := ReadResult{
		Kind:        KindFile,
		Operation:   OpDocument,
		Workspace:   workspace,
		Display:     doc.Display,
		Path:        doc.Absolute,
		Revision:    doc.Revision,
		NotModified: doc.NotModified,
		Content:     doc.Content,
		Binary:      doc.Binary,
		Image:       doc.Image,
		Truncated:   doc.Truncated,
		TotalSize:   doc.TotalSize,
		ModTime:     formatModTime(doc.ModTime),
		Mode:        modeOctal(doc.Mode),
	}
	if doc.NotModified {
		result.Operation = ""
		result.Display = ""
		result.Path = ""
		result.Content = ""
		result.Workspace = ""
		result.ModTime = ""
		result.Mode = ""
		result.TotalSize = 0
		result.Binary = false
		result.Image = false
		result.Truncated = false
	}
	return result
}
