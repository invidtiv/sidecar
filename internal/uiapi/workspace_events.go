package uiapi

import (
	"context"

	"github.com/marcus/sidecar/internal/workspacewire"
)

// WorkspaceEventSource adds owning-host project references to resource invalidations.
type WorkspaceEventSource interface {
	WorkspaceInvalidation(context.Context) (workspacewire.WorkspaceEvent, error)
}

func (s *Server) refreshWorkspaceEvents(ctx context.Context, pending *eventPending) {
	source, ok := s.opts.Backend.(WorkspaceEventSource)
	if !ok {
		return
	}
	event, err := source.WorkspaceInvalidation(ctx)
	if err != nil {
		pending.put(EventMessage{Type: "error", Error: &ErrorDetail{Code: CodeBackend, Message: err.Error()}})
		return
	}
	pending.put(EventMessage{Type: "workspace", Workspace: &event})
}
