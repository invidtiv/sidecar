package cli

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/uiapi"
)

// browserAccessRequest asks a running server for access as a new browser
// does, with a fresh P-256 key.
func browserAccessRequest(t *testing.T, server *uiapi.Server, label string) uiapi.AccessRequestCreated {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := key.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	pub := uiapi.BrowserPublicKey{Kty: "EC", Crv: "P-256", X: base64.RawURLEncoding.EncodeToString(encoded[1:33]), Y: base64.RawURLEncoding.EncodeToString(encoded[33:])}
	body, _ := json.Marshal(uiapi.AccessRequestCreate{PublicKey: pub, Label: label})
	request, _ := http.NewRequest(http.MethodPost, server.BrowserURL()+"/api/v0/pairing/requests", bytes.NewReader(body))
	request.Header.Set("Origin", server.BrowserURL())
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Sidecar-Request", "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("request access: %d %s", response.StatusCode, data)
	}
	var created uiapi.AccessRequestCreated
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatal(err)
	}
	return created
}

func TestAPIAccessVerbsWithoutAServer(t *testing.T) {
	apiStateTree(t, t.TempDir())
	for _, args := range [][]string{{"api", "requests"}, {"api", "approve", "K7Q-4MX"}, {"api", "deny", "x"}, {"api", "devices"}, {"api", "devices", "revoke", "abcdef"}} {
		code, _, stderr := runAPICLI(t, args...)
		if code != 1 || !strings.Contains(stderr, "sidecar api serve") {
			t.Fatalf("%v: code %d stderr %q", args, code, stderr)
		}
	}
}

func TestAPIAccessVerbUsage(t *testing.T) {
	apiStateTree(t, t.TempDir())
	for _, args := range [][]string{{"api", "approve"}, {"api", "approve", "a", "b"}, {"api", "deny"}, {"api", "requests", "extra"}, {"api", "requests", "--bogus"}, {"api", "devices", "revoke"}} {
		if code, _, _ := runAPICLI(t, args...); code != 2 {
			t.Fatalf("%v: code %d, want 2", args, code)
		}
	}
	for _, args := range [][]string{{"api", "approve", "--help"}, {"api", "devices", "revoke", "--help"}, {"api", "requests", "-h"}} {
		code, stdout, _ := runAPICLI(t, args...)
		if code != 0 || !strings.Contains(stdout, "Usage:") {
			t.Fatalf("%v: %d %q", args, code, stdout)
		}
	}
}

func TestAPIAccessVerbsAgainstARunningServer(t *testing.T) {
	stateDir := apiStateTree(t, t.TempDir())
	server, err := uiapi.Start(uiapi.Options{StateDir: stateDir, Port: 0, Backend: staticAPIBackend{}, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	if code, stdout, _ := runAPICLI(t, "api", "requests"); code != 0 || !strings.Contains(stdout, "No browsers are waiting") {
		t.Fatalf("empty requests: %d %q", code, stdout)
	}
	first := browserAccessRequest(t, server, "Safari on macOS")
	second := browserAccessRequest(t, server, "")
	code, stdout, stderr := runAPICLI(t, "api", "requests", "--json")
	var list uiapi.AccessRequestList
	if code != 0 || json.Unmarshal([]byte(stdout), &list) != nil || len(list.Requests) != 2 {
		t.Fatalf("requests --json: %d %q %q", code, stdout, stderr)
	}
	if strings.Contains(stdout, first.Code) || strings.Contains(stdout, strings.ReplaceAll(first.Code, "-", "")) {
		t.Fatalf("requests leaks a code: %s", stdout)
	}
	code, stdout, _ = runAPICLI(t, "api", "requests")
	if code != 0 || !strings.Contains(stdout, `"Safari on macOS"`) || !strings.Contains(stdout, "(unnamed browser)") || !strings.Contains(stdout, first.RequestID) || !strings.Contains(stdout, "sidecar api approve CODE") {
		t.Fatalf("requests: %d %q", code, stdout)
	}

	// Named refusals exit 4 with the code on stderr, and on stdout with --json.
	wrong := "000000"
	if strings.ReplaceAll(first.Code, "-", "") == wrong || strings.ReplaceAll(second.Code, "-", "") == wrong {
		wrong = "111111"
	}
	if code, _, stderr = runAPICLI(t, "api", "approve", wrong); code != apiRefusedExit || !strings.HasPrefix(stderr, "access_code_invalid: ") {
		t.Fatalf("wrong code: %d %q", code, stderr)
	}
	code, stdout, _ = runAPICLI(t, "api", "approve", "nope", "--json")
	var refusal uiapi.ErrorBody
	if code != apiRefusedExit || json.Unmarshal([]byte(stdout), &refusal) != nil || refusal.Error.Code != uiapi.CodeInvalidRequest {
		t.Fatalf("malformed code --json: %d %q", code, stdout)
	}

	code, stdout, stderr = runAPICLI(t, "api", "approve", strings.ToLower(first.Code), "--json")
	var approval uiapi.AccessApproval
	if code != 0 || json.Unmarshal([]byte(stdout), &approval) != nil || approval.RequestID != first.RequestID || approval.ApprovedVia != "cli" || approval.Label != "Safari on macOS" {
		t.Fatalf("approve --json: %d %q %q", code, stdout, stderr)
	}
	if code, stdout, _ = runAPICLI(t, "api", "deny", second.RequestID); code != 0 || !strings.Contains(stdout, "Denied request "+second.RequestID) {
		t.Fatalf("deny: %d %q", code, stdout)
	}
	if code, _, stderr = runAPICLI(t, "api", "deny", second.RequestID); code != apiRefusedExit || !strings.HasPrefix(stderr, "access_request_not_found: ") {
		t.Fatalf("second deny: %d %q", code, stderr)
	}

	code, stdout, _ = runAPICLI(t, "api", "devices", "--json")
	var devices uiapi.DeviceList
	if code != 0 || json.Unmarshal([]byte(stdout), &devices) != nil || len(devices.Devices) != 1 || devices.Devices[0].ID != approval.RegistrationID || devices.Devices[0].ApprovedVia != "cli" {
		t.Fatalf("devices --json: %d %q", code, stdout)
	}
	if code, stdout, _ = runAPICLI(t, "api", "devices"); code != 0 || !strings.Contains(stdout, approval.RegistrationID[:12]) || !strings.Contains(stdout, "approved via cli") {
		t.Fatalf("devices: %d %q", code, stdout)
	}
	if code, _, stderr = runAPICLI(t, "api", "devices", "revoke", "abc"); code != apiRefusedExit || !strings.HasPrefix(stderr, "invalid_request: ") {
		t.Fatalf("short prefix: %d %q", code, stderr)
	}
	if code, _, stderr = runAPICLI(t, "api", "devices", "revoke", "zzzzzzzz"); code != apiRefusedExit || !strings.HasPrefix(stderr, "device_not_found: ") {
		t.Fatalf("unknown device: %d %q", code, stderr)
	}
	code, stdout, stderr = runAPICLI(t, "api", "devices", "revoke", approval.RegistrationID[:8])
	if code != 0 || !strings.Contains(stdout, "Signed out "+approval.RegistrationID[:12]) {
		t.Fatalf("revoke: %d %q %q", code, stdout, stderr)
	}
	if code, stdout, _ = runAPICLI(t, "api", "devices"); code != 0 || !strings.Contains(stdout, "No browsers are registered") {
		t.Fatalf("devices after revoke: %d %q", code, stdout)
	}
}
