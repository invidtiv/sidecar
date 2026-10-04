package uiapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpecMatchesCommittedDocumentAndRoutes(t *testing.T) {
	data, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "docs", "reference", "ui-api.openapi.json")
	if os.Getenv("UPDATE_UI_API_SPEC") == "1" {
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, got) {
		t.Fatal("OpenAPI is stale: run ./scripts/update-ui-api-contract.sh")
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	for path, route := range (&Server{}).routeTable() {

		methods, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatalf("route %s absent from spec", path)
		}
		for method := range route.methods {
			if methods[strings.ToLower(method)] == nil {
				t.Fatalf("%s %s absent from spec", method, path)
			}
		}
		count := len(route.methods)
		if route.methods[http.MethodGet] != nil {
			count++
		}
		if len(methods) != count {
			t.Fatalf("spec lists unsupported methods on %s", path)
		}
	}
	for path := range paths {
		if (&Server{}).routeTable()[path] == nil && path != terminalPath && path != "/{path}" {
			t.Fatalf("spec lists unsupported route %s", path)
		}
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	walkSpecRefs(t, doc, schemas)
}

func TestSpecDocumentsHeadMethodsServedByHandlers(t *testing.T) {
	h := newHarness(t)
	data, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for path, route := range h.s.routeTable() {
		if route.methods[http.MethodGet] == nil {
			continue
		}
		var response *http.Response
		if route.allows(ListenerLocal) {
			response, _ = h.localDo(req{method: http.MethodHead, path: path})
		} else {
			response, _ = h.browserDo(req{method: http.MethodHead, path: path})
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("HEAD %s: %d", path, response.StatusCode)
		}
		if doc.Paths[path]["head"] == nil {
			t.Errorf("handler serves HEAD %s but spec omits it", path)
		}
	}
	response, _ := h.browserDo(req{method: http.MethodHead, path: "/"})
	if response.StatusCode != http.StatusOK || doc.Paths["/{path}"]["head"] == nil {
		t.Error("static HEAD route absent from spec")
	}
}
func walkSpecRefs(t *testing.T, value any, schemas map[string]any) {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/components/schemas/")
			if schemas[name] == nil {
				t.Fatalf("unresolved ref %s", ref)
			}
			if target, ok := schemas[name].(map[string]any); ok && target["$ref"] == ref {
				t.Fatalf("self reference %s", ref)
			}
		}
		for _, child := range v {
			walkSpecRefs(t, child, schemas)
		}
	case []any:
		for _, child := range v {
			walkSpecRefs(t, child, schemas)
		}
	}
}
