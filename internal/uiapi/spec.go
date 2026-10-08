package uiapi

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/layoutreport"
	"github.com/marcus/sidecar/internal/mobileproto"
)

// Spec generates the public OpenAPI 3.1 contract from the same wire types used
// by handlers, CLI clients and the terminal service. It performs no I/O.
func Spec() ([]byte, error) {
	schemas := map[string]any{}
	values := map[string]any{
		"NotificationSnapshot": NotificationSnapshot{}, "NotificationMutation": NotificationMutation{}, "NotificationReceiptRequest": NotificationReceiptRequest{}, "NotificationReceiptResponse": NotificationReceiptResponse{}, "NotificationClaimResponse": NotificationClaimResponse{}, "NotificationsConfig": config.NotificationsConfig{}, "NotificationSettingsOptions": NotificationSettingsOptions{},
		"FileSearchResult":  contentservice.FileSearchResult{},
		"ContentReadResult": contentservice.ReadResult{}, "ContentTreeResult": contentservice.TreeResult{}, "ContentRef": ContentRef{}, "LayoutDocument": LayoutDocument{},
		"ViewerLayoutReport": layoutreport.Report{}, "ViewerPresenceRequest": ViewerPresenceRequest{}, "ViewerPresenceResponse": ViewerPresenceResponse{}, "ViewerAckRequest": ViewerAckRequest{}, "ViewerAckResponse": ViewerAckResponse{},
		"Hello": Hello{}, "Status": Status{}, "Endpoint": Endpoint{}, "ErrorBody": ErrorBody{},
		"TicketRequest": TicketRequest{}, "TicketResponse": TicketResponse{},
		"PairingCodeRequest": PairingCodeRequest{}, "PairingCode": PairingCode{},
		"PairingExchangeRequest": PairingExchangeRequest{}, "PairingExchange": PairingExchange{},
		"SessionProofChallengeRequest": SessionProofChallengeRequest{}, "SessionProofChallenge": SessionProofChallenge{},
		"SessionProofRequest": SessionProofRequest{}, "SessionToken": SessionToken{},
		"OriginRequest": OriginRequest{}, "OriginRegistration": OriginRegistration{},
		"OriginList": OriginList{}, "OriginRevocation": OriginRevocation{}, "SessionRevocation": SessionRevocation{},
		"AccessRequestCreate": AccessRequestCreate{}, "AccessRequestCreated": AccessRequestCreated{}, "AccessRequestStatus": AccessRequestStatus{},
		"AccessRequestList": AccessRequestList{}, "AccessApproveRequest": AccessApproveRequest{}, "AccessApproval": AccessApproval{},
		"AccessDenyRequest": AccessDenyRequest{}, "AccessDenial": AccessDenial{}, "DeviceList": DeviceList{}, "DeviceRevocation": DeviceRevocation{},
		"CatalogSnapshot": mobileproto.CatalogSnapshot{}, "TerminalRequest": mobileproto.Request{},
		"TerminalResponse": mobileproto.Response{}, "EventMessage": EventMessage{},
	}
	workspaceSpecTypes(values)
	for name, value := range values {
		reflected := (&jsonschema.Reflector{Anonymous: true, Namer: workspaceSchemaName}).Reflect(value)
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
			reflected := (&jsonschema.Reflector{Anonymous: true, ExpandedStruct: true, Namer: workspaceSchemaName}).Reflect(values[name])
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
			"x-listeners": listeners}
		security := []any{}
		if !public {
			for _, listener := range listeners {
				switch Listener(listener) {
				case ListenerLocal:
					security = append(security, map[string]any{})
					operation["x-local-auth"] = "No credential on the mode-0600 Unix socket; the empty security alternative applies only there."
				case ListenerBrowser:
					security = append(security, map[string]any{"bearerAuth": []string{}})
				case ListenerTailnet:
					security = append(security, map[string]any{"tailnetLogin": []string{}})
				}
			}
		}
		operation["security"] = security
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
	paths["/api/v0/status"].(map[string]any)["get"].(map[string]any)["description"] = "Server status. ui_configured is available on every listener; ui_dir is included only on the trusted Local socket and omitted on Browser and Tailnet."
	add(contentRoute, "get", "", "ContentReadResult", all, false)
	add(treeRoute, "get", "", "ContentTreeResult", all, false)
	add(layoutRoute, "get", "", "LayoutDocument", all, false)
	add(layoutRoute, "put", "LayoutDocument", "LayoutDocument", all, false)
	add(fileSearchRoute, "get", "", "FileSearchResult", all, false)
	addFileSearchSpec(paths)
	addContentSpec(paths)
	for _, method := range []string{"get", "put"} {
		op := paths[layoutRoute].(map[string]any)[method].(map[string]any)
		delete(op, "x-required-scope")
		op["x-required-scopes-any-of"] = []string{ScopeContentRead, ScopeUIControl}
	}
	add(notificationsPath, "get", "", "NotificationSnapshot", all, false)
	add(notificationSettingsPath, "get", "", "NotificationsConfig", all, false)
	add(notificationSettingsPath, "put", "NotificationsConfig", "NotificationsConfig", all, false)
	paths[notificationSettingsPath].(map[string]any)["put"].(map[string]any)["description"] = "Validates and saves the whole notifications section. A validation refusal is 400 invalid_request whose error.field names the dotted path of the refused field (quietHours.start, sources.agent.expiry); its message begins with notifications.<field>."
	add(notificationOptionsPath, "get", "", "NotificationSettingsOptions", all, false)
	paths[notificationOptionsPath].(map[string]any)["get"].(map[string]any)["description"] = "What a settings form needs beside the stored config: registered sources (id, title, description, built-in rule) loudest first, and whether this credential may save."
	add(notificationReadPath, "post", "NotificationMutation", "NotificationSnapshot", all, false)
	add(notificationDismissPath, "post", "NotificationMutation", "NotificationSnapshot", all, false)
	add(notificationClaimPath, "post", "NotificationReceiptRequest", "NotificationClaimResponse", all, false)
	add(notificationReceiptPath, "post", "NotificationReceiptRequest", "NotificationReceiptResponse", all, false)
	for _, path := range []string{notificationsPath, notificationSettingsPath, notificationOptionsPath, notificationReadPath, notificationDismissPath, notificationReceiptPath, notificationClaimPath} {
		for method, op := range paths[path].(map[string]any) {
			scope := ScopeUIControl
			if method == "get" {
				scope = ScopeContentRead
			}
			op.(map[string]any)["x-required-scope"] = scope
			if method == "get" {
				delete(op.(map[string]any), "x-required-scope")
				op.(map[string]any)["x-required-scopes-any-of"] = []string{ScopeContentRead, ScopeUIControl}
			}
		}
	}

	add(viewerPresencePath, "post", "ViewerPresenceRequest", "ViewerPresenceResponse", all, false)
	add(viewerAckPath, "post", "ViewerAckRequest", "ViewerAckResponse", all, false)
	for _, path := range []string{viewerPresencePath, viewerAckPath} {
		paths[path].(map[string]any)["post"].(map[string]any)["x-required-scope"] = ScopeUIControl
	}

	add("/api/v0/ws-tickets", "post", "TicketRequest", "TicketResponse", remote, false)
	add("/api/v0/pairing/codes", "post", "PairingCodeRequest", "PairingCode", local, false)
	add("/api/v0/pairing/sessions", "delete", "", "SessionRevocation", local, false)
	sessionRevoke := paths["/api/v0/pairing/sessions"].(map[string]any)["delete"].(map[string]any)
	sessionRevoke["description"] = "Durably purges browser sessions before success; invalidates unused tickets and closes their streams. Revoked credentials remain invalid after restart."
	sessionRevoke["parameters"] = append(sessionRevoke["parameters"].([]any), map[string]any{"name": "origin", "in": "query", "schema": map[string]any{"type": "string"}, "description": "Omit to revoke every browser session; supply an origin to revoke only its sessions."})
	add("/api/v0/pairing/exchange", "post", "PairingExchangeRequest", "PairingExchange", []string{"browser"}, true)
	paths["/api/v0/pairing/exchange"].(map[string]any)["post"].(map[string]any)["description"] = "Registers an exact-origin ECDSA P-256 public key and returns a 15-minute memory-only bearer. Public registrations persist with 30-day sliding expiry and a 180-day absolute cap. The client stores a non-extractable private CryptoKey in IndexedDB; never persist a bearer."
	add("/api/v0/pairing/session-proof", "post", "SessionProofChallengeRequest", "SessionProofChallenge", []string{"browser"}, true)
	paths["/api/v0/pairing/session-proof"].(map[string]any)["post"].(map[string]any)["description"] = "Issues an exact-origin, registration-bound single-use nonce and server timestamp, valid for 60 seconds. Requires the Browser listener's own Origin and mutation guards."
	add("/api/v0/pairing/session-proof/verify", "post", "SessionProofRequest", "SessionToken", []string{"browser"}, true)
	paths["/api/v0/pairing/session-proof/verify"].(map[string]any)["post"].(map[string]any)["description"] = "Consumes the nonce attempt and verifies ECDSA P-256/SHA-256 over the domain-separated origin/registration/nonce/timestamp message. Signature is base64url raw 64-byte r||s. Returns a 15-minute bearer kept only in client memory. See ui-api.md for exact bytes."
	addAccessSpec(paths, add, all)
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
	revocation["description"] = "Revokes the paired origin and browser sessions bound to that exact origin, including unused tickets and open streams. Revocations survive restart."
	revocation["parameters"] = append(revocation["parameters"].([]any), map[string]any{"name": "origin", "in": "query", "required": true, "schema": map[string]any{"type": "string"}})
	paths[terminalPath] = streamOperation("terminal", all)
	paths[eventsPath] = streamOperation("events", all)
	events := paths[eventsPath].(map[string]any)["get"].(map[string]any)
	events["parameters"] = append(events["parameters"].([]any), params...)
	events["parameters"] = append(events["parameters"].([]any), map[string]any{"name": "notifications", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"1"}}, "description": "Subscribe to notification centre snapshots; requires content:read or ui:control."})
	events["parameters"] = append(events["parameters"].([]any), map[string]any{"name": "viewer", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"uiRequestRelayV1"}}, "description": "Register this events connection as a screen viewer; requires full or ui:control. Receive viewer identity, report focused/visible presence, acknowledge each ui_request via HTTP. No reconnect replay."})
	events["parameters"] = append(events["parameters"].([]any), map[string]any{"name": "content", "in": "query", "schema": map[string]any{"type": "array", "items": map[string]any{"type": "string", "contentMediaType": "application/json", "contentSchema": schemaRef("ContentRef")}, "maxItems": 32}, "style": "form", "explode": true, "description": "JSON reference for each open content pane; reconnect to change the set. Requires content:read."})
	paths["/pair"] = map[string]any{"get": map[string]any{"operationId": "pair_page", "security": []any{}, "x-listeners": []string{"browser"}, "responses": map[string]any{"200": map[string]any{"description": "Pairing page, consumes no code", "content": map[string]any{"text/html": map[string]any{"schema": map[string]any{"type": "string"}}}}}}}
	paths["/{path}"] = map[string]any{"get": map[string]any{"operationId": "ui_files", "x-listeners": remote, "description": "Static UI files with SPA fallback. Browser listener public; Tailnet requires allowed login. API paths never fall back. Each request resolves a fresh confined root, so rebuilt directories are served without restart and symlink escapes remain refused.", "parameters": []any{map[string]any{"name": "path", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}}, "responses": map[string]any{"200": map[string]any{"description": "UI file or index.html"}, "503": map[string]any{"description": "UI directory temporarily unavailable during rebuild; retry shortly."}}}}
	workspaceSpec(schemas, paths, add)
	// dispatch maps HEAD to GET, and static files also support HEAD. A
	// WebSocket handshake remains GET-only. HEAD responses have no body.
	for path, value := range paths {
		if path == terminalPath || path == eventsPath {
			continue
		}
		methods := value.(map[string]any)
		get, ok := methods["get"].(map[string]any)
		if !ok {
			continue
		}
		head := map[string]any{}
		for key, item := range get {
			head[key] = item
		}
		head["operationId"] = get["operationId"].(string) + "_head"
		responses := map[string]any{}
		for code, value := range get["responses"].(map[string]any) {
			response := map[string]any{}
			for key, item := range value.(map[string]any) {
				if key != "content" {
					response[key] = item
				}
			}
			responses[code] = response
		}
		head["responses"] = responses
		methods["head"] = head
	}

	doc := map[string]any{"openapi": "3.1.0", "jsonSchemaDialect": "https://json-schema.org/draft/2020-12/schema",
		"info":  map[string]any{"title": "Sidecar UI API", "version": "0", "description": "Generated by sidecar api spec. HTTP and WebSocket auth, guards and ordering: docs/reference/ui-api.md and mobile-protocol.md."},
		"paths": paths, "components": map[string]any{"schemas": schemas, "securitySchemes": map[string]any{
			"bearerAuth":        map[string]any{"type": "http", "scheme": "bearer"},
			"accessRequestPoll": map[string]any{"type": "apiKey", "in": "header", "name": "Authorization", "description": "The value `Request <poll_secret>`, with the poll_secret POST /api/v0/pairing/requests returned. It reads only that request's status."},
			"tailnetLogin":      map[string]any{"type": "apiKey", "in": "header", "name": tailscaleLoginHead, "description": "Trusted only on the dedicated Tailnet listener."}}},
		"x-streams": map[string]any{eventsPath: map[string]any{"transport": "websocket", "messageType": "text", "response": schemaRef("EventMessage"), "protocol": "ui_api", "version": APIVersion, "description": "Server-to-client JSON envelopes only; hello, catalog and terminals baseline first. Contiguous seq starts at 1 on each connection. Catalog changes precede attention. Reconnect replaces the baseline without replay. Client data closes with 4400. See ui-api.md."}, terminalPath: map[string]any{"transport": "websocket", "messageType": "text", "request": schemaRef("TerminalRequest"), "response": schemaRef("TerminalResponse"), "protocol": "mobile", "version": mobileproto.Version, "maxMessageBytes": mobileproto.MaxLineBytes, "description": "One JSON envelope per text message. hello first; open before frame; operation_sequence and applied reset/output checkpoints govern mutations. See mobile-protocol.md."}}}
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

// Agent control and mobile both define Target and Error. Package identity is
// part of their schema name so map iteration cannot overwrite either contract.
func workspaceSchemaName(t reflect.Type) string {
	if strings.HasSuffix(t.PkgPath(), "/notify") {
		return "Notification" + t.Name()
	}
	if strings.HasSuffix(t.PkgPath(), "/uirequest") {
		return "UIRequest" + t.Name()
	}
	if strings.HasSuffix(t.PkgPath(), "/agentcontrol") {
		return "AgentControl" + t.Name()
	}
	return t.Name()
}
