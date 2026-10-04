package uiapi

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/marcus/sidecar/internal/mobileproto"
)

// Spec generates the public OpenAPI 3.1 contract from the same wire types used
// by handlers, CLI clients and the terminal service. It performs no I/O.
func Spec() ([]byte, error) {
	schemas := map[string]any{}
	values := map[string]any{
		"Hello": Hello{}, "Status": Status{}, "Endpoint": Endpoint{}, "ErrorBody": ErrorBody{},
		"TicketRequest": TicketRequest{}, "TicketResponse": TicketResponse{},
		"PairingCodeRequest": PairingCodeRequest{}, "PairingCode": PairingCode{},
		"PairingExchangeRequest": PairingExchangeRequest{}, "PairingExchange": PairingExchange{},
		"OriginRequest": OriginRequest{}, "OriginRegistration": OriginRegistration{},
		"OriginList": OriginList{}, "OriginRevocation": OriginRevocation{},
		"CatalogSnapshot": mobileproto.CatalogSnapshot{}, "TerminalRequest": mobileproto.Request{},
		"TerminalResponse": mobileproto.Response{},
	}
	for name, value := range values {
		reflected := (&jsonschema.Reflector{Anonymous: true}).Reflect(value)
		data, err := json.Marshal(reflected)
		if err != nil {
			return nil, err
		}
		var root map[string]any
		if err := json.Unmarshal(data, &root); err != nil {
			return nil, err
		}
		defs, _ := root["$defs"].(map[string]any)
		for key, def := range defs {
			schemas[key] = rewriteSchema(def)
		}
		ref, _ := root["$ref"].(string)
		if ref != "" {
			schemas[name] = map[string]any{"$ref": "#/components/schemas/" + strings.TrimPrefix(ref, "#/$defs/")}
		} else {
			delete(root, "$schema")
			delete(root, "$defs")
			schemas[name] = rewriteSchema(root)
		}
	}
	// Named aliases must not point at themselves.
	for name, value := range schemas {
		if m, ok := value.(map[string]any); ok && m["$ref"] == "#/components/schemas/"+name {
			reflected := (&jsonschema.Reflector{Anonymous: true, ExpandedStruct: true}).Reflect(values[name])
			data, err := json.Marshal(reflected)
			if err != nil {
				return nil, err
			}
			var root map[string]any
			if err := json.Unmarshal(data, &root); err != nil {
				return nil, err
			}
			delete(root, "$schema")
			delete(root, "$defs")
			schemas[name] = rewriteSchema(root)
		}
	}
	paths := map[string]any{}
	add := func(path, method, request, response string, listeners []string, public bool) {
		operation := map[string]any{"operationId": strings.ReplaceAll(strings.TrimPrefix(path, "/api/v0/"), "/", "_") + "_" + method,
			"responses": map[string]any{"200": map[string]any{"description": "Success", "content": jsonContent(response)},
				"default": map[string]any{"description": "Named refusal; see ui-api.md for status codes and remedies", "content": jsonContent("ErrorBody")}},
			"x-listeners": listeners, "x-local-auth": "No credential on the mode-0600 Unix socket; the empty security alternative applies only there.", "security": []any{map[string]any{"bearerAuth": []string{}}, map[string]any{"tailnetLogin": []string{}}, map[string]any{}}}
		if public {
			operation["security"] = []any{}
		}
		if request != "" {
			operation["requestBody"] = map[string]any{"required": false, "content": jsonContent(request)}
		}
		if method != "get" {
			operation["parameters"] = []any{map[string]any{"name": mutationHeader, "in": "header", "required": false, "schema": map[string]any{"type": "string", "const": "1"}, "description": "Required on Browser and Tailnet; not on Local."}}
		}
		if paths[path] == nil {
			paths[path] = map[string]any{}
		}
		paths[path].(map[string]any)[method] = operation
	}
	all := []string{"local", "browser", "tailnet"}
	remote := []string{"browser", "tailnet"}
	local := []string{"local"}
	add("/api/v0/hello", "get", "", "Hello", all, false)
	add("/api/v0/sessions", "get", "", "CatalogSnapshot", all, false)
	add("/api/v0/status", "get", "", "Status", all, false)
	add("/api/v0/ws-tickets", "post", "TicketRequest", "TicketResponse", remote, false)
	add("/api/v0/pairing/codes", "post", "PairingCodeRequest", "PairingCode", local, false)
	add("/api/v0/pairing/exchange", "post", "PairingExchangeRequest", "PairingExchange", []string{"browser"}, true)
	add("/api/v0/origins", "get", "", "OriginList", local, false)
	add("/api/v0/origins", "post", "OriginRequest", "OriginRegistration", local, false)
	add("/api/v0/origins", "delete", "", "OriginRevocation", local, false)
	params := []any{}
	for _, key := range []string{"sort", "search", "host", "provider", "state", "show_idle_sessions"} {
		schema := map[string]any{"type": "string"}
		if key == "host" || key == "provider" || key == "state" {
			schema = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": mobileproto.MaxCatalogFilters}
		}
		if key == "sort" {
			schema["enum"] = []string{"activity", "project", "recent", "name"}
		}
		if key == "show_idle_sessions" {
			schema = map[string]any{"type": "boolean"}
		}
		params = append(params, map[string]any{"name": key, "in": "query", "schema": schema, "style": "form", "explode": true})
	}
	paths["/api/v0/sessions"].(map[string]any)["get"].(map[string]any)["parameters"] = params
	revocation := paths["/api/v0/origins"].(map[string]any)["delete"].(map[string]any)
	revocation["parameters"] = append(revocation["parameters"].([]any), map[string]any{"name": "origin", "in": "query", "required": true, "schema": map[string]any{"type": "string"}})
	paths[terminalPath] = streamOperation("terminal", all)
	paths["/pair"] = map[string]any{"get": map[string]any{"operationId": "pair_page", "security": []any{}, "x-listeners": []string{"browser"}, "responses": map[string]any{"200": map[string]any{"description": "Pairing page, consumes no code", "content": map[string]any{"text/html": map[string]any{"schema": map[string]any{"type": "string"}}}}}}}
	paths["/{path}"] = map[string]any{"get": map[string]any{"operationId": "ui_files", "x-listeners": remote, "description": "Static UI files with SPA fallback. Browser listener public; Tailnet requires allowed login. API paths never fall back.", "parameters": []any{map[string]any{"name": "path", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}}, "responses": map[string]any{"200": map[string]any{"description": "UI file or index.html"}}}}

	doc := map[string]any{"openapi": "3.1.0", "jsonSchemaDialect": "https://json-schema.org/draft/2020-12/schema",
		"info":  map[string]any{"title": "Sidecar UI API", "version": "0", "description": "Generated by sidecar api spec. HTTP and WebSocket auth, guards and ordering: docs/reference/ui-api.md and mobile-protocol.md."},
		"paths": paths, "components": map[string]any{"schemas": schemas, "securitySchemes": map[string]any{
			"bearerAuth":   map[string]any{"type": "http", "scheme": "bearer"},
			"tailnetLogin": map[string]any{"type": "apiKey", "in": "header", "name": tailscaleLoginHead, "description": "Trusted only on the dedicated Tailnet listener."}}},
		"x-streams": map[string]any{terminalPath: map[string]any{"transport": "websocket", "messageType": "text", "request": schemaRef("TerminalRequest"), "response": schemaRef("TerminalResponse"), "protocol": "mobile", "version": mobileproto.Version, "maxMessageBytes": mobileproto.MaxLineBytes, "description": "One JSON envelope per text message. hello first; open before frame; operation_sequence and applied reset/output checkpoints govern mutations. See mobile-protocol.md."}}}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("generate UI API spec: %w", err)
	}
	return append(data, '\n'), nil
}

func schemaRef(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}
func jsonContent(name string) map[string]any {
	return map[string]any{"application/json": map[string]any{"schema": schemaRef(name)}}
}
func rewriteSchema(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if key == "$ref" {
				if ref, ok := item.(string); ok {
					v[key] = strings.ReplaceAll(ref, "#/$defs/", "#/components/schemas/")
				}
			} else {
				v[key] = rewriteSchema(item)
			}
		}
		// Go nil slices encode as null, including non-omitempty response slices.
		if v["type"] == "array" {
			v["type"] = []string{"array", "null"}
		}
	case []any:
		for i, item := range v {
			v[i] = rewriteSchema(item)
		}
	}
	return value
}

func streamOperation(name string, listeners []string) map[string]any {
	return map[string]any{"get": map[string]any{"operationId": name + "_stream", "x-listeners": listeners,
		"description": "Authenticated WebSocket upgrade. Exact Host guard before upgrade; Origin/auth failures use close codes after upgrade. A single-use origin-bound ticket or explicit bearer authenticates Browser; Local is trusted; Tailnet uses its allowed login. See ui-api.md.",
		"parameters":  []any{map[string]any{"name": "ticket", "in": "query", "schema": map[string]any{"type": "string"}}, map[string]any{"name": "Origin", "in": "header", "schema": map[string]any{"type": "string"}, "description": "Required for tickets and Tailnet. Explicit Browser bearer clients may omit Origin."}},
		"responses":   map[string]any{"101": map[string]any{"description": "WebSocket upgraded; see x-streams for messages and close codes"}, "426": map[string]any{"description": "Upgrade required", "content": jsonContent("ErrorBody")}, "421": map[string]any{"description": "Host refused", "content": jsonContent("ErrorBody")}}}}
}
