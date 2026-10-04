package uiapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/contentservice"
)

const ScopeContentRead = "content:read"

// ContentBackend preserves the shared content DTOs at the transport seam.
type ContentBackend interface {
	LookupProject(context.Context, string, string) (contentservice.Workspace, error)
	ReadProject(context.Context, string, string, contentservice.ReadParams) (contentservice.ReadResult, error)
	TreeProject(context.Context, string, string, []string) (contentservice.TreeResult, error)
}

func (s *Server) contentBackend() ContentBackend {
	if s.opts.Content != nil {
		return s.opts.Content
	}
	if backend, ok := s.opts.Backend.(ContentBackend); ok {
		return backend
	}
	return contentservice.Default()
}

const contentRoute = "/api/v0/projects/{project}/content"
const treeRoute = "/api/v0/projects/{project}/tree"
const layoutRoute = "/api/v0/projects/{project}/layout"

// projectContentRoute matches only this lane's three resource paths.
func projectContentRoute(path string) (template, project string) {
	rest, ok := strings.CutPrefix(path, "/api/v0/projects/")
	if !ok {
		return "", ""
	}
	project, resource, ok := strings.Cut(rest, "/")
	if !ok || project == "" || strings.Contains(resource, "/") {
		return "", ""
	}
	switch resource {
	case "content":
		return contentRoute, project
	case "tree":
		return treeRoute, project
	case "layout":
		return layoutRoute, project
	}
	return "", ""
}

func (s *Server) hasScope(c caller, scope string) bool {
	if !strings.HasPrefix(c.client, "origin:") {
		return true
	}
	record, ok := s.origins.lookup(strings.TrimPrefix(c.client, "origin:"))
	if !ok || record.TokenSHA256 != c.credential {
		return false
	}
	for _, grant := range record.Scopes {
		if grant == ScopeFull || grant == scope {
			return true
		}
	}
	return false
}

func (s *Server) requireScope(w http.ResponseWriter, c caller, scope string) bool {
	if s.hasScope(c, scope) {
		return true
	}
	writeError(w, http.StatusForbidden, "scope_refused", fmt.Sprintf("This credential needs %s; pair the origin with that scope.", scope))
	return false
}

func contentParams(q url.Values) (contentservice.ReadParams, error) {
	var p contentservice.ReadParams
	for key, values := range q {
		if len(values) != 1 {
			return p, fmt.Errorf("%s takes one value", key)
		}
		switch key {
		case "workspace", "kind", "operation", "target", "path", "parent", "if_revision", "offset", "limit":
		default:
			return p, fmt.Errorf("unknown content parameter %q", key)
		}
		if len(values[0]) > contentservice.MaxLocatorBytes {
			return p, fmt.Errorf("%s is too long", key)
		}
	}
	p.Kind, p.Operation, p.Target = q.Get("kind"), q.Get("operation"), q.Get("target")
	p.Path, p.Parent, p.IfRevision = q.Get("path"), q.Get("parent"), q.Get("if_revision")
	if p.Operation == "" {
		switch p.Kind {
		case contentservice.KindFile:
			p.Operation = contentservice.OpDocument
		case contentservice.KindIssue:
			p.Operation = contentservice.OpCard
		case contentservice.KindNote:
			p.Operation = contentservice.OpNote
		case contentservice.KindDiff:
			p.Operation = contentservice.OpWorkingTree
		}
	}
	if p.Kind == contentservice.KindDiff && p.Target == "" {
		p.Target = "working-tree"
	}
	for key, into := range map[string]*int{"offset": &p.Offset, "limit": &p.Limit} {
		if q.Has(key) {
			n, err := strconv.Atoi(q.Get(key))
			if err != nil || n < 0 {
				return p, fmt.Errorf("%s must be a nonnegative integer", key)
			}
			*into = n
		}
	}
	return p, nil
}

func (s *Server) handleContent(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireScope(w, c, ScopeContentRead) {
		return
	}
	_, project := projectContentRoute(r.URL.Path)
	p, err := contentParams(r.URL.Query())
	if err != nil {
		writeError(w, 400, CodeInvalidRequest, err.Error())
		return
	}
	result, err := s.contentBackend().ReadProject(r.Context(), project, r.URL.Query().Get("workspace"), p)
	if err != nil {
		writeContentError(w, err)
		return
	}
	data, err := contentservice.EncodeReadResult(result)
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeContentJSON(w, data)
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.requireScope(w, c, ScopeContentRead) {
		return
	}
	_, project := projectContentRoute(r.URL.Path)
	q := r.URL.Query()
	for key, values := range q {
		if key != "path" && key != "workspace" || key == "workspace" && len(values) != 1 {
			writeError(w, 400, CodeInvalidRequest, "Tree accepts repeated path and one workspace parameter.")
			return
		}
	}
	result, err := s.contentBackend().TreeProject(r.Context(), project, q.Get("workspace"), q["path"])
	if err != nil {
		writeContentError(w, err)
		return
	}
	data, err := contentservice.EncodeTreeResult(result)
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeContentJSON(w, data)
}

func writeContentError(w http.ResponseWriter, err error) {
	var refusal *contentservice.Error
	if errors.As(err, &refusal) {
		status := http.StatusBadRequest
		if refusal.Code == contentservice.CodeRejected {
			status = http.StatusForbidden
		}
		if refusal.Code == contentservice.CodeInternal {
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, string(refusal.Code), refusal.Error())
		return
	}
	writeError(w, 503, CodeBackend, err.Error())
}

func addContentSpec(paths map[string]any) {
	for _, path := range []string{contentRoute, treeRoute, layoutRoute} {
		for _, value := range paths[path].(map[string]any) {
			op := value.(map[string]any)
			params, _ := op["parameters"].([]any)
			params = append(params, map[string]any{"name": "project", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "description": "Exact configured project name."})
			op["x-required-scope"] = ScopeContentRead
			if path == contentRoute {
				for _, name := range []string{"workspace", "kind", "operation", "target", "path", "parent", "if_revision", "offset", "limit"} {
					schema := map[string]any{"type": "string"}
					if name == "offset" || name == "limit" {
						schema = map[string]any{"type": "integer", "minimum": 0}
					}
					params = append(params, map[string]any{"name": name, "in": "query", "schema": schema, "required": name == "kind"})
				}
			}
			if path == treeRoute {
				params = append(params, map[string]any{"name": "workspace", "in": "query", "schema": map[string]any{"type": "string"}}, map[string]any{"name": "path", "in": "query", "schema": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": contentservice.MaxTreePaths}, "style": "form", "explode": true})
			}
			if path == layoutRoute {
				header := "If-None-Match"
				required := false
				if op["requestBody"] != nil {
					header = "If-Match"
					required = true
					op["requestBody"].(map[string]any)["required"] = true
				}
				params = append(params, map[string]any{"name": header, "in": "header", "required": required, "schema": map[string]any{"type": "string"}})
				responses := op["responses"].(map[string]any)
				responses["200"].(map[string]any)["headers"] = map[string]any{"ETag": map[string]any{"schema": map[string]any{"type": "string"}, "description": "Quoted content digest; send unchanged as If-Match."}}
				if required {
					for _, code := range []string{"412", "428"} {
						responses[code] = map[string]any{"description": "Read the layout and retry with its current ETag.", "content": jsonContent("ErrorBody")}
					}
				} else {
					responses["304"] = map[string]any{"description": "Layout unchanged"}
				}
			}
			op["parameters"] = params
		}
	}
}

func writeContentJSON(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
