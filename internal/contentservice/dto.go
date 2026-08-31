package contentservice

import "strings"

// ResolveResult is the machine contract for `sidecar content resolve --json`.
//
// Every field that makes this this verb's answer is required so a structured
// log line cannot decode as success. Mode is an octal permission string, not
// os.FileMode.
type ResolveResult struct {
	Kind      string `json:"kind"`
	Workspace string `json:"workspace,omitempty"`
	Display   string `json:"display,omitempty"`
	Path      string `json:"path,omitempty"`
	Revision  string `json:"revision,omitempty"`
	TotalSize int64  `json:"totalSize,omitempty"`
	ModTime   string `json:"modTime,omitempty"`
	Mode      string `json:"mode,omitempty"`
}

// ValidRemoteResult reports whether a decoded object is this verb's answer.
func (r ResolveResult) ValidRemoteResult() bool {
	return r.Kind == KindFile &&
		strings.TrimSpace(r.Workspace) != "" &&
		strings.TrimSpace(r.Display) != "" &&
		strings.TrimSpace(r.Path) != "" &&
		strings.TrimSpace(r.Revision) != ""
}

// ReadResult is the machine contract for `sidecar content read --json`.
//
// notModified, oversize, and a full document are all answers. A log line is
// not. Content is omitted on notModified and on binary/image documents.
type ReadResult struct {
	Kind        string `json:"kind"`
	Operation   string `json:"operation,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	Display     string `json:"display,omitempty"`
	Path        string `json:"path,omitempty"`
	Revision    string `json:"revision,omitempty"`
	NotModified bool   `json:"notModified,omitempty"`
	Oversize    bool   `json:"oversize,omitempty"`
	Content     string `json:"content,omitempty"`
	Binary      bool   `json:"binary,omitempty"`
	Image       bool   `json:"image,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
	TotalSize   int64  `json:"totalSize,omitempty"`
	ModTime     string `json:"modTime,omitempty"`
	Mode        string `json:"mode,omitempty"`
}

// ValidRemoteResult reports whether a decoded object is this verb's answer.
//
// Three shapes are answers: a notModified refresh, a structured oversize
// refusal, and a document carrying workspace + display + revision. A login
// profile logging `{"level":"info","msg":"loading nvm","path":"..."}` matches
// none of them.
func (r ReadResult) ValidRemoteResult() bool {
	if r.Kind != KindFile || strings.TrimSpace(r.Revision) == "" {
		return false
	}
	if r.NotModified {
		return true
	}
	if r.Oversize {
		return true
	}
	return strings.TrimSpace(r.Workspace) != "" &&
		strings.TrimSpace(r.Display) != "" &&
		r.Operation == OpDocument
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
