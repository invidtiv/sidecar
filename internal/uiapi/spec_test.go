package uiapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
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
		if (&Server{}).routeTable()[path] == nil && path != terminalPath && path != eventsPath && path != "/{path}" {
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
		if strings.Contains(path, "{project}") {
			continue
		}
		if route.methods[http.MethodGet] == nil {
			continue
		}
		var response *http.Response
		if route.allows(ListenerLocal) {
			response, _ = h.localDo(req{method: http.MethodHead, path: strings.ReplaceAll(path, "{project}", "fixture-project")})
		} else {
			response, _ = h.browserDo(req{method: http.MethodHead, path: strings.ReplaceAll(path, "{project}", "fixture-project")})
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

func TestSpecRequiresAuthForRemoteOnlyTicketIssuance(t *testing.T) {
	h := newHarness(t)
	response, data := h.browserDo(req{method: http.MethodPost, path: "/api/v0/ws-tickets", body: "{}", header: mutationHeaders(h.ownOrigin(), nil)})
	expect(t, response, data, http.StatusUnauthorized, CodeUnauthenticated)
	data, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Security []map[string]any `json:"security"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	security := doc.Paths["/api/v0/ws-tickets"]["post"].Security
	if len(security) != 2 {
		t.Errorf("tickets need Browser/Tailnet credentials, got %v", security)
	}
	for _, alternative := range security {
		if len(alternative) == 0 {
			t.Fatal("spec permits anonymous tickets although neither served listener allows them")
		}
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

func TestSpecDocumentsEventsStreamAndSharedCatalogQuery(t *testing.T) {
	data, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	methods, ok := paths[eventsPath].(map[string]any)
	if !ok {
		t.Fatal("events upgrade absent from spec")
	}
	if len(methods) != 1 {
		t.Fatal("events is a GET-only upgrade")
	}
	get := methods["get"].(map[string]any)
	params := get["parameters"].([]any)
	if len(params) < 2 {
		t.Fatal("missing upgrade authentication parameters")
	}
	sessionsParams := paths["/api/v0/sessions"].(map[string]any)["get"].(map[string]any)["parameters"]
	var catalogParams []any
	for _, p := range params[2:] {
		name := p.(map[string]any)["name"]
		if name != "content" && name != "viewer" {
			catalogParams = append(catalogParams, p)
		}
	}
	if !reflect.DeepEqual(catalogParams, sessionsParams) {
		t.Fatal("event query differs from Sessions query")
	}
	if get["responses"].(map[string]any)["101"] == nil {
		t.Fatal("missing upgrade response")
	}
	stream, ok := doc["x-streams"].(map[string]any)[eventsPath].(map[string]any)
	if !ok || !reflect.DeepEqual(stream["response"], schemaRef("EventMessage")) {
		t.Fatal("missing event message stream schema")
	}
	if stream["request"] != nil {
		t.Fatal("events must remain server-to-client")
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	event, ok := schemas["EventMessage"].(map[string]any)
	if !ok {
		t.Fatal("missing EventMessage schema")
	}
	props := event["properties"].(map[string]any)
	if props["attention"] == nil || props["terminals"] == nil || props["catalog"] == nil {
		t.Fatal("event schema omits payloads")
	}
	walkSpecRefs(t, doc, schemas)
}

func TestSpecCatalogPathIsOptional(t *testing.T) {
	data, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	row := doc["components"].(map[string]any)["schemas"].(map[string]any)["CatalogRow"].(map[string]any)
	if row["properties"].(map[string]any)["path"] == nil {
		t.Fatal("catalog path missing from schema")
	}
	for _, name := range row["required"].([]any) {
		if name == "path" {
			t.Fatal("legacy rows without path must remain valid")
		}
	}
}

func TestTypedHelloAdvertisesEvents(t *testing.T) {
	h := newHarness(t)
	response, data := h.localDo(req{method: http.MethodGet, path: "/api/v0/hello"})
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.StatusCode)
	}
	var hello Hello
	if err := json.Unmarshal(data, &hello); err != nil {
		t.Fatal(err)
	}
	for _, capability := range hello.Capabilities {
		if capability == "events" {
			return
		}
	}
	t.Fatal("typed hello dropped events capability")
}

func TestViewerSpecGenerationIsDeterministic(t *testing.T) {
	want, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, err := Spec()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatal("request schema names collide nondeterministically")
		}
	}
}
