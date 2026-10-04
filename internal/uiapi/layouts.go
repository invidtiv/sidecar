package uiapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	"github.com/marcus/sidecar/internal/viewerlayout"
)

// LayoutDocument is the shared layout store document, with no cell geometry.
type LayoutDocument = viewerlayout.Document

func (s *Server) handleLayout(w http.ResponseWriter, r *http.Request, c caller) {
	if !s.hasScope(c, ScopeContentRead) && !s.requireScope(w, c, ScopeUIControl) {
		return
	}
	_, project := projectContentRoute(r.URL.Path)
	if len(r.URL.Query()) != 0 {
		writeError(w, 400, CodeInvalidRequest, "Layout takes no query parameters; the credential identifies its viewer.")
		return
	}
	ws, err := s.contentBackend().LookupProject(r.Context(), project, "")
	if err != nil {
		writeContentError(w, err)
		return
	}
	var store viewerlayout.Store = viewerlayout.FileStore{Dir: filepath.Join(s.dir, "layouts")}
	doc, etag, err := store.Get(c.client, ws.Root)
	if err != nil {
		writeContentError(w, err)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(304)
			return
		}
		writeJSON(w, 200, doc)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err == nil {
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) != nil || fields["layout"] == nil {
			writeError(w, 400, CodeInvalidRequest, "Send an object with an explicit layout field, or layout:null to clear it.")
			return
		}
		doc = LayoutDocument{}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&doc)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = fmt.Errorf("send exactly one JSON object")
			}
		}
	}
	if err != nil {
		writeError(w, 400, CodeInvalidRequest, err.Error())
		return
	}
	doc, etag, err = store.Put(c.client, ws.Root, r.Header.Get("If-Match"), doc)
	if errors.Is(err, viewerlayout.ErrRequired) {
		writeError(w, 428, "precondition_required", err.Error())
		return
	}
	if errors.Is(err, viewerlayout.ErrChanged) {
		w.Header().Set("ETag", etag)
		writeError(w, 412, "precondition_failed", err.Error())
		return
	}
	if errors.Is(err, viewerlayout.ErrInvalid) {
		writeError(w, 400, CodeInvalidRequest, err.Error())
		return
	}
	if err != nil {
		writeContentError(w, err)
		return
	}
	w.Header().Set("ETag", etag)
	writeJSON(w, 200, doc)
}
