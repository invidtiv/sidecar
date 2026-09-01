package contentservice

import (
	"context"
	"os/exec"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/noteview"
	"github.com/marcus/sidecar/internal/shellstate"
)

// Service is the host-side content application service.
//
// Nil function fields use the production defaults (config.Load, shells.json
// via projectdir, git worktree list, issueview/noteview lookup). Tests inject
// fakes so a fixture does not have to be a real Sidecar state tree.
type Service struct {
	LoadConfig  func() (*config.Config, error)
	ListShells  func(projectRoot string) ([]shellstate.Definition, error)
	Git         func(ctx context.Context, dir string, args ...string) ([]byte, error)
	LookupIssue func(ctx context.Context, workDir, issueID string, fallbacks []issueview.ProjectRef) (*issueview.Data, *issueview.Owner, error)
	LookupNote  func(ctx context.Context, workDir, noteID string) (*noteview.Data, error)
}

// Default returns a Service bound to this process's config and state.
func Default() *Service { return &Service{} }

func defaultGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	all := append([]string{"--no-optional-locks", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", all...)
	return cmd.Output()
}

func defaultLoadConfig() (*config.Config, error) { return config.Load() }

// Resolve maps a workspace + target onto identity and metadata. It does
// not ship a body. Issue and note resolve is identity only: the id is
// normalized and the workspace is re-validated, without consulting td.
func (s *Service) Resolve(ctx context.Context, workspaceID, kind, target string) (ResolveResult, error) {
	if err := ctx.Err(); err != nil {
		return ResolveResult{}, err
	}
	if err := requireKind(kind); err != nil {
		return ResolveResult{}, err
	}
	switch kind {
	case KindIssue:
		return s.resolveIssue(ctx, workspaceID, target)
	case KindNote:
		return s.resolveNote(ctx, workspaceID, target)
	}
	ws, err := s.lookupWorkspace(ctx, workspaceID)
	if err != nil {
		return ResolveResult{}, err
	}
	doc, err := ReadFile(ctx, ws.Root, target, "")
	if err != nil {
		return ResolveResult{}, err
	}
	return resolveResultFrom(ws.ID, doc), nil
}

// Read loads a typed payload, honoring ifRevision for a small notModified
// answer. operation is kind-specific: document, card, or note.
func (s *Service) Read(ctx context.Context, workspaceID, kind, operation, target, ifRevision string) (ReadResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadResult{}, err
	}
	if err := requireKind(kind); err != nil {
		return ReadResult{}, err
	}
	if err := requireOperation(kind, operation); err != nil {
		return ReadResult{}, err
	}
	ws, err := s.lookupWorkspace(ctx, workspaceID)
	if err != nil {
		return ReadResult{}, err
	}
	switch kind {
	case KindIssue:
		doc, err := s.readIssueAt(ctx, ws.Root, target, ifRevision, s.issueFallbacks())
		if err != nil {
			return ReadResult{}, err
		}
		return issueReadResultFrom(ws.ID, doc), nil
	case KindNote:
		doc, err := s.readNoteAt(ctx, ws.Root, target, ifRevision)
		if err != nil {
			return ReadResult{}, err
		}
		return noteReadResultFrom(ws.ID, doc), nil
	default:
		doc, err := ReadFile(ctx, ws.Root, target, ifRevision)
		if err != nil {
			return ReadResult{}, err
		}
		return readResultFrom(ws.ID, doc), nil
	}
}

func requireKind(kind string) error {
	switch kind {
	case "":
		return Usage("kind is required")
	case KindFile, KindIssue, KindNote:
		return nil
	default:
		return UnknownKind(kind)
	}
}

func requireOperation(kind, operation string) error {
	if operation == "" {
		return Usage("operation is required")
	}
	want := ""
	switch kind {
	case KindFile:
		want = OpDocument
	case KindIssue:
		want = OpCard
	case KindNote:
		want = OpNote
	}
	if want != "" && operation != want {
		return Usage("unknown content operation %q", operation)
	}
	return nil
}
