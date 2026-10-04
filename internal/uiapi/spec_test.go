package uiapi

import (
	"bytes"
	"encoding/json"
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
		t.Fatal("OpenAPI is stale: UPDATE_UI_API_SPEC=1 go test ./internal/uiapi -run TestSpecMatchesCommittedDocumentAndRoutes")
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
		if len(methods) != len(route.methods) {
			t.Fatalf("spec lists unsupported methods on %s", path)
		}
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	walkSpecRefs(t, doc, schemas)
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
