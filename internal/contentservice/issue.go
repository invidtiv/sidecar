package contentservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/marcus/sidecar/internal/issueview"
)

// IssueDTO is the explicit wire form of one issue card. Parent, children,
// siblings, logs, and owner are named fields so a json:"-" graph cannot
// silently drop related rows.
type IssueDTO struct {
	ID          string         `json:"id"`
	Title       string         `json:"title,omitempty"`
	Status      string         `json:"status,omitempty"`
	Type        string         `json:"type,omitempty"`
	Priority    string         `json:"priority,omitempty"`
	Points      int            `json:"points,omitempty"`
	Description string         `json:"description,omitempty"`
	Acceptance  string         `json:"acceptance,omitempty"`
	ParentID    string         `json:"parentId,omitempty"`
	Labels      []string       `json:"labels,omitempty"`
	CreatedAt   string         `json:"createdAt,omitempty"`
	UpdatedAt   string         `json:"updatedAt,omitempty"`
	Logs        []IssueLogDTO  `json:"logs,omitempty"`
	Parent      *IssueRefDTO   `json:"parent,omitempty"`
	Children    []IssueRefDTO  `json:"children,omitempty"`
	Siblings    []IssueRefDTO  `json:"siblings,omitempty"`
	Owner       *IssueOwnerDTO `json:"owner,omitempty"`
}

// IssueRefDTO is a parent, child, or sibling pointer on the wire.
type IssueRefDTO struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	Status   string `json:"status,omitempty"`
	Type     string `json:"type,omitempty"`
	Priority string `json:"priority,omitempty"`
}

// IssueLogDTO is one progress line on the wire.
type IssueLogDTO struct {
	Timestamp string `json:"timestamp,omitempty"`
	Session   string `json:"session,omitempty"`
	Message   string `json:"message,omitempty"`
	Type      string `json:"type,omitempty"`
}

// IssueOwnerDTO names the project a cross-project hit came from.
type IssueOwnerDTO struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

// IssueDocument is the host-side issue payload plus the revision a later
// conditional read will send back.
type IssueDocument struct {
	Data        *issueview.Data
	Owner       *issueview.Owner
	Revision    string
	NotModified bool
}

// ReadIssue loads one issue from workDir, searching fallbacks only on a
// genuine local miss. If ifRevision matches the current payload revision,
// the body is omitted and NotModified is set.
func ReadIssue(ctx context.Context, root, issueID, ifRevision string, fallbacks []issueview.ProjectRef) (IssueDocument, error) {
	return Default().readIssueAt(ctx, root, issueID, ifRevision, fallbacks)
}

func (s *Service) readIssueAt(ctx context.Context, root, issueID, ifRevision string, fallbacks []issueview.ProjectRef) (IssueDocument, error) {
	if err := ctx.Err(); err != nil {
		return IssueDocument{}, err
	}
	id := issueview.NormalizeID(issueID)
	if id == "" {
		return IssueDocument{}, Rejected("invalid issue id %q", issueID)
	}
	lookup := s.LookupIssue
	if lookup == nil {
		lookup = defaultLookupIssue
	}
	data, owner, err := lookup(ctx, root, id, fallbacks)
	if err != nil {
		if issueview.IsNotFound(err) {
			return IssueDocument{}, Rejected("%s", err.Error())
		}
		return IssueDocument{}, Internal("load issue", err)
	}
	if data == nil || data.ID == "" {
		return IssueDocument{}, Rejected("issue %q not found", id)
	}
	rev := revisionForValue(issueDTOFrom(data, owner))
	if ifRevision != "" && ifRevision == rev {
		return IssueDocument{Revision: rev, NotModified: true}, nil
	}
	return IssueDocument{Data: data, Owner: owner, Revision: rev}, nil
}

func (s *Service) resolveIssue(ctx context.Context, workspaceID, target string) (ResolveResult, error) {
	ws, err := s.lookupWorkspace(ctx, workspaceID)
	if err != nil {
		return ResolveResult{}, err
	}
	id := issueview.NormalizeID(target)
	if id == "" {
		return ResolveResult{}, Rejected("invalid issue id %q", target)
	}
	return ResolveResult{Kind: KindIssue, Workspace: ws.ID, Target: id, Display: id}, nil
}

func (s *Service) issueFallbacks() []issueview.ProjectRef {
	load := s.LoadConfig
	if load == nil {
		load = defaultLoadConfig
	}
	cfg, err := load()
	if err != nil || cfg == nil {
		return nil
	}
	return issueview.ProjectRefsFromConfig(cfg)
}

func defaultLookupIssue(_ context.Context, workDir, issueID string, fallbacks []issueview.ProjectRef) (*issueview.Data, *issueview.Owner, error) {
	return issueview.Lookup(workDir, issueID, fallbacks)
}

func issueReadResultFrom(workspace string, doc IssueDocument) ReadResult {
	result := ReadResult{
		Kind:        KindIssue,
		Operation:   OpCard,
		Workspace:   workspace,
		Revision:    doc.Revision,
		NotModified: doc.NotModified,
	}
	if doc.NotModified {
		result.Operation = ""
		result.Workspace = ""
		return result
	}
	result.Issue = issueDTOFrom(doc.Data, doc.Owner)
	if result.Issue != nil {
		result.Target = result.Issue.ID
		result.Display = result.Issue.ID
	}
	return result
}

func issueDTOFrom(data *issueview.Data, owner *issueview.Owner) *IssueDTO {
	if data == nil {
		return nil
	}
	dto := &IssueDTO{
		ID:          data.ID,
		Title:       data.Title,
		Status:      data.Status,
		Type:        data.Type,
		Priority:    data.Priority,
		Points:      data.Points,
		Description: data.Description,
		Acceptance:  data.Acceptance,
		ParentID:    data.ParentID,
		Labels:      append([]string(nil), data.Labels...),
		CreatedAt:   data.CreatedAt,
		UpdatedAt:   data.UpdatedAt,
		Logs:        issueLogsFrom(data.Logs),
		Parent:      issueRefFrom(data.Parent),
		Children:    issueRefsFrom(data.Children),
		Siblings:    issueRefsFrom(data.Siblings),
	}
	if owner != nil && owner.Root != "" {
		dto.Owner = &IssueOwnerDTO{Name: owner.Name, Root: owner.Root}
	}
	return dto
}

func issueLogsFrom(logs []issueview.Log) []IssueLogDTO {
	if len(logs) == 0 {
		return nil
	}
	out := make([]IssueLogDTO, len(logs))
	for i, log := range logs {
		out[i] = IssueLogDTO{Timestamp: log.Timestamp, Session: log.Session, Message: log.Message, Type: log.Type}
	}
	return out
}

func issueRefFrom(ref *issueview.Ref) *IssueRefDTO {
	if ref == nil {
		return nil
	}
	out := IssueRefDTO{ID: ref.ID, Title: ref.Title, Status: ref.Status, Type: ref.Type, Priority: ref.Priority}
	return &out
}

func issueRefsFrom(refs []issueview.Ref) []IssueRefDTO {
	if len(refs) == 0 {
		return nil
	}
	out := make([]IssueRefDTO, len(refs))
	for i, ref := range refs {
		out[i] = IssueRefDTO{ID: ref.ID, Title: ref.Title, Status: ref.Status, Type: ref.Type, Priority: ref.Priority}
	}
	return out
}

// IssueFromDTO converts a wire issue back into the shared viewer payload.
func IssueFromDTO(dto *IssueDTO) (*issueview.Data, *issueview.Owner) {
	if dto == nil {
		return nil, nil
	}
	data := &issueview.Data{
		ID:          dto.ID,
		Title:       dto.Title,
		Status:      dto.Status,
		Type:        dto.Type,
		Priority:    dto.Priority,
		Points:      dto.Points,
		Description: dto.Description,
		Acceptance:  dto.Acceptance,
		ParentID:    dto.ParentID,
		Labels:      append([]string(nil), dto.Labels...),
		CreatedAt:   dto.CreatedAt,
		UpdatedAt:   dto.UpdatedAt,
		Logs:        issueLogsTo(dto.Logs),
		Parent:      issueRefTo(dto.Parent),
		Children:    issueRefsTo(dto.Children),
		Siblings:    issueRefsTo(dto.Siblings),
	}
	var owner *issueview.Owner
	if dto.Owner != nil && dto.Owner.Root != "" {
		owner = &issueview.Owner{Name: dto.Owner.Name, Root: dto.Owner.Root}
	}
	return data, owner
}

func issueLogsTo(logs []IssueLogDTO) []issueview.Log {
	if len(logs) == 0 {
		return nil
	}
	out := make([]issueview.Log, len(logs))
	for i, log := range logs {
		out[i] = issueview.Log{Timestamp: log.Timestamp, Session: log.Session, Message: log.Message, Type: log.Type}
	}
	return out
}

func issueRefTo(ref *IssueRefDTO) *issueview.Ref {
	if ref == nil {
		return nil
	}
	out := issueview.Ref{ID: ref.ID, Title: ref.Title, Status: ref.Status, Type: ref.Type, Priority: ref.Priority}
	return &out
}

func issueRefsTo(refs []IssueRefDTO) []issueview.Ref {
	if len(refs) == 0 {
		return nil
	}
	out := make([]issueview.Ref, len(refs))
	for i, ref := range refs {
		out[i] = issueview.Ref{ID: ref.ID, Title: ref.Title, Status: ref.Status, Type: ref.Type, Priority: ref.Priority}
	}
	return out
}

func revisionForValue(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("v1:err:%s", err.Error())
	}
	sum := sha256.Sum256(raw)
	return "v1:" + hex.EncodeToString(sum[:])
}
