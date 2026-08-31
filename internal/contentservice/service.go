package contentservice

import (
	"context"
	"os/exec"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
)

// Service is the host-side file content application service.
//
// Nil function fields use the production defaults (config.Load, shells.json
// via projectdir, git worktree list). Tests inject fakes so a fixture does
// not have to be a real Sidecar state tree.
type Service struct {
	LoadConfig func() (*config.Config, error)
	ListShells func(projectRoot string) ([]shellstate.Definition, error)
	Git        func(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// Default returns a Service bound to this process's config and state.
func Default() *Service { return &Service{} }

func defaultGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	all := append([]string{"--no-optional-locks", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", all...)
	return cmd.Output()
}

// Resolve maps a workspace + file target onto identity and metadata. It does
// not ship a body.
func (s *Service) Resolve(ctx context.Context, workspaceID, kind, target string) (ResolveResult, error) {
	if err := ctx.Err(); err != nil {
		return ResolveResult{}, err
	}
	if err := requireKind(kind); err != nil {
		return ResolveResult{}, err
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

// Read loads a file document, honoring ifRevision for a small notModified
// answer. operation must be document.
func (s *Service) Read(ctx context.Context, workspaceID, kind, operation, target, ifRevision string) (ReadResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadResult{}, err
	}
	if err := requireKind(kind); err != nil {
		return ReadResult{}, err
	}
	if operation != OpDocument {
		if operation == "" {
			return ReadResult{}, Usage("operation is required")
		}
		return ReadResult{}, Usage("unknown content operation %q", operation)
	}
	ws, err := s.lookupWorkspace(ctx, workspaceID)
	if err != nil {
		return ReadResult{}, err
	}
	doc, err := ReadFile(ctx, ws.Root, target, ifRevision)
	if err != nil {
		return ReadResult{}, err
	}
	return readResultFrom(ws.ID, doc), nil
}

func requireKind(kind string) error {
	switch kind {
	case "":
		return Usage("kind is required")
	case KindFile:
		return nil
	default:
		return UnknownKind(kind)
	}
}
