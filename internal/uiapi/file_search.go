package uiapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/filefind"
)

// FileSearchBackend keeps immutable fixtures behind the same transport seam:
// a fixture must never try to walk its synthetic root on this host.
type FileSearchBackend interface {
	SearchProject(context.Context, string, string, *filefind.Index, contentservice.FileSearchParams) (contentservice.FileSearchResult, error)
}

const fileSearchRoute = "/api/v0/projects/{project}/files/search"

func (s *Server) handleFileSearch(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireScope(w, c, ScopeContentRead) {
		return
	}
	if !s.fileSearchRequests.reserve(c.client) {
		writeError(w, 429, "too_many_outstanding", "This client already has four file searches in progress; wait for a search to finish.")
		return
	}
	defer s.fileSearchRequests.release(c.client)
	q := r.URL.Query()
	for key, values := range q {
		if key != "q" && key != "limit" && key != "workspace" && key != "recent" || key != "recent" && len(values) != 1 {
			writeError(w, 400, CodeInvalidRequest, "File search accepts one q, limit, workspace and repeated recent parameters.")
			return
		}
	}
	p := contentservice.FileSearchParams{Query: q.Get("q"), Recent: q["recent"]}
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > contentservice.MaxFileSearchResults {
			writeError(w, 400, CodeInvalidRequest, "limit must be between 1 and 100")
			return
		}
		p.Limit = n
	}
	if len(q.Get("workspace")) > contentservice.MaxLocatorBytes {
		writeError(w, 400, CodeInvalidRequest, "workspace is too long")
		return
	}
	if err := contentservice.ValidateFileSearch(p); err != nil {
		writeContentError(w, err)
		return
	}
	_, project := projectContentRoute(r.URL.Path)
	var result contentservice.FileSearchResult
	var err error
	if backend, ok := s.contentBackend().(FileSearchBackend); ok {
		result, err = backend.SearchProject(r.Context(), project, q.Get("workspace"), &s.fileIndex, p)
	} else {
		var ws contentservice.Workspace
		ws, err = s.contentBackend().LookupProject(r.Context(), project, q.Get("workspace"))
		if err == nil {
			result, err = contentservice.Default().SearchFiles(r.Context(), &s.fileIndex, ws.Root, p)
		}
	}
	if err != nil {
		writeContentError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}

func addFileSearchSpec(paths map[string]any) {
	op := paths[fileSearchRoute].(map[string]any)["get"].(map[string]any)
	op["x-required-scope"] = ScopeContentRead
	op["x-required-capability"] = "file_search"
	op["x-max-concurrent-requests-per-client"] = maxContentRequestsPerClient
	op["responses"].(map[string]any)["429"] = map[string]any{"description": "Four file searches are already in progress for this credential.", "content": jsonContent("ErrorBody")}
	op["parameters"] = []any{
		map[string]any{"name": "project", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
		map[string]any{"name": "q", "in": "query", "schema": map[string]any{"type": "string", "maxLength": contentservice.MaxFileSearchQueryBytes}},
		map[string]any{"name": "workspace", "in": "query", "description": contentWorkspaceDescription, "schema": map[string]any{"type": "string", "maxLength": contentservice.MaxLocatorBytes}},
		map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": contentservice.MaxFileSearchResults, "default": 50}},
		map[string]any{"name": "recent", "in": "query", "style": "form", "explode": true, "description": "Relative file paths, most recent first. Hints affect ranking only and never authorize paths.", "schema": map[string]any{"type": "array", "maxItems": contentservice.MaxFileSearchRecent, "items": map[string]any{"type": "string", "maxLength": contentservice.MaxLocatorBytes}}},
	}
}
