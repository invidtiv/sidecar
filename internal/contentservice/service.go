package contentservice

import (
	"context"
	"os/exec"
	"strconv"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/noteview"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/resourceprovider"
	"github.com/marcus/sidecar/internal/shellstate"
)

// Service is the host-side content application service.
//
// Nil function fields use the production defaults (config.Load, shells.json
// via projectdir, git worktree list, issueview/noteview lookup). Tests inject
// fakes so a fixture does not have to be a real Sidecar state tree.
type Service struct {
	LoadConfig         func() (*config.Config, error)
	ListShells         func(projectRoot string) ([]shellstate.Definition, error)
	Git                func(ctx context.Context, dir string, args ...string) ([]byte, error)
	LookupIssue        func(ctx context.Context, workDir, issueID string, fallbacks []issueview.ProjectRef) (*issueview.Data, *issueview.Owner, error)
	LookupNote         func(ctx context.Context, workDir, noteID string) (*noteview.Data, error)
	NewResourceManager func() (*resourceprovider.Manager, error)
	ListIssues         func(ctx context.Context, root string, limit int) ([]CatalogIssue, error)
	ListNotes          func(ctx context.Context, root string, limit int) ([]CatalogNote, error)
}

// Default returns a Service bound to this process's config and state.
func Default() *Service { return &Service{} }

func defaultGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	all := append([]string{"--no-optional-locks", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", all...)
	return cmd.Output()
}

func defaultLoadConfig() (*config.Config, error) { return config.Load() }

func defaultListIssues(ctx context.Context, root string, limit int) ([]CatalogIssue, error) {
	if limit <= 0 {
		limit = catalogIssueLimit
	}
	var out []CatalogIssue
	for _, status := range []string{"in_progress", "open"} {
		cmd := exec.CommandContext(ctx, "td", "list", "--json", "--status", status, "--limit", strconv.Itoa(limit))
		cmd.Dir = root
		output, err := cmd.Output()
		if err != nil {
			continue
		}
		out = append(out, parseTDListIssues(output, limit)...)
	}
	if len(out) > limit*2 {
		out = out[:limit*2]
	}
	return out, nil
}

func defaultListNotes(ctx context.Context, root string, limit int) ([]CatalogNote, error) {
	if limit <= 0 {
		limit = catalogNoteLimit
	}
	cmd := exec.CommandContext(ctx, "td", "-w", root, "--json", "note", "list")
	cmd.Env = append(cmd.Environ(), "TD_SYNC_AUTO_START=0", "TD_ANALYTICS=false")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseTDListNotes(output, limit), nil
}

// ResolveParams is one content resolve, including resource identity.
type ResolveParams struct {
	WorkspaceID string
	Kind        string
	Target      string
	Provider    string
	Matcher     string
}

// Resolve maps a workspace + target onto identity and metadata. It does
// not ship a body. Issue and note resolve is identity only: the id is
// normalized and the workspace is re-validated, without consulting td.
func (s *Service) Resolve(ctx context.Context, workspaceID, kind, target string) (ResolveResult, error) {
	return s.ResolveParams(ctx, ResolveParams{WorkspaceID: workspaceID, Kind: kind, Target: target})
}

// ResolveParams is Resolve with resource provider/matcher identity.
func (s *Service) ResolveParams(ctx context.Context, params ResolveParams) (ResolveResult, error) {
	if err := ctx.Err(); err != nil {
		return ResolveResult{}, err
	}
	if err := requireKind(params.Kind); err != nil {
		return ResolveResult{}, err
	}
	switch params.Kind {
	case KindIssue:
		return s.resolveIssue(ctx, params.WorkspaceID, params.Target)
	case KindNote:
		return s.resolveNote(ctx, params.WorkspaceID, params.Target)
	case KindDiff:
		return s.resolveDiff(ctx, params.WorkspaceID, params.Target)
	case KindResource:
		return s.ResolveResourceRef(ctx, params.WorkspaceID, resource.Reference{
			Instance: params.Provider, Matcher: params.Matcher, Locator: params.Target,
		})
	}
	ws, err := s.lookupWorkspace(ctx, params.WorkspaceID)
	if err != nil {
		return ResolveResult{}, err
	}
	doc, err := ReadFile(ctx, ws.Root, params.Target, "")
	if err != nil {
		return ResolveResult{}, err
	}
	return resolveResultFrom(ws.ID, doc), nil
}

// Read loads a typed payload, honoring ifRevision for a small notModified
// answer. operation is kind-specific: document, card, note, or a diff op.
func (s *Service) Read(ctx context.Context, workspaceID, kind, operation, target, ifRevision string) (ReadResult, error) {
	return s.ReadParams(ctx, ReadParams{
		WorkspaceID: workspaceID, Kind: kind, Operation: operation, Target: target, IfRevision: ifRevision,
	})
}

// ReadParams is Read with optional diff locators (path, parent, paging).
func (s *Service) ReadParams(ctx context.Context, params ReadParams) (ReadResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadResult{}, err
	}
	if err := requireKind(params.Kind); err != nil {
		return ReadResult{}, err
	}
	if err := requireOperation(params.Kind, params.Operation); err != nil {
		return ReadResult{}, err
	}
	ws, err := s.lookupWorkspace(ctx, params.WorkspaceID)
	if err != nil {
		return ReadResult{}, err
	}
	switch params.Kind {
	case KindIssue:
		doc, err := s.readIssueAt(ctx, ws.Root, params.Target, params.IfRevision, s.issueFallbacks())
		if err != nil {
			return ReadResult{}, err
		}
		return issueReadResultFrom(ws.ID, doc), nil
	case KindNote:
		doc, err := s.readNoteAt(ctx, ws.Root, params.Target, params.IfRevision)
		if err != nil {
			return ReadResult{}, err
		}
		return noteReadResultFrom(ws.ID, doc), nil
	case KindDiff:
		doc, err := s.readDiffAt(ctx, ws.Root, params)
		if err != nil {
			return ReadResult{}, err
		}
		return diffReadResultFrom(ws.ID, doc, params.Operation), nil
	case KindResource:
		return s.ReadResource(ctx, params.WorkspaceID, resource.Reference{
			Instance: params.Provider, Matcher: params.Matcher, Locator: params.Target,
		}, params.Refresh)
	default:
		doc, err := ReadFile(ctx, ws.Root, params.Target, params.IfRevision)
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
	case KindFile, KindIssue, KindNote, KindDiff, KindResource:
		return nil
	default:
		return UnknownKind(kind)
	}
}

func requireOperation(kind, operation string) error {
	if operation == "" {
		return Usage("operation is required")
	}
	switch kind {
	case KindFile:
		if operation != OpDocument {
			return Usage("unknown content operation %q", operation)
		}
	case KindIssue:
		if operation != OpCard {
			return Usage("unknown content operation %q", operation)
		}
	case KindNote:
		if operation != OpNote {
			return Usage("unknown content operation %q", operation)
		}
	case KindDiff:
		if !validDiffOperation(operation) {
			return Usage("unknown content operation %q", operation)
		}
	case KindResource:
		if operation != OpResource {
			return Usage("unknown content operation %q", operation)
		}
	}
	return nil
}
