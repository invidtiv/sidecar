package uiapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// An allowed login approving from a device on the direct listener is
// recorded with that device, as Tailscale names it, on the approval, the
// device list and the store.
func TestDirectTailnetApprovalRecordsTheApprovingDevice(t *testing.T) {
	h := newDirectHarness(t, func(f *fakeTailnet) {
		peer := ownerPeer()
		peer.Device = "marcusbook-pro\u202e"
		f.peers[loopbackPeer] = peer
	})
	_, created := h.requestAccess("Safari on iOS")
	body, _ := json.Marshal(AccessApproveRequest{Code: created.Code})
	r, b := h.directDo(req{method: http.MethodPost, path: accessApprovePath, body: string(body), header: mutationHeaders(directOrigin(), nil)})
	expect(t, r, b, http.StatusOK, "")
	var approval AccessApproval
	if err := json.Unmarshal(b, &approval); err != nil {
		t.Fatal(err)
	}
	if approval.ApprovedVia != "tailnet:"+testTailnetLogin || approval.ApprovedDevice != "marcusbook-pro" {
		t.Fatalf("approval = %+v", approval)
	}

	r, b = h.localDo(req{path: devicesPath})
	expect(t, r, b, http.StatusOK, "")
	var list DeviceList
	if err := json.Unmarshal(b, &list); err != nil || len(list.Devices) != 1 || list.Devices[0].ApprovedDevice != "marcusbook-pro" {
		t.Fatalf("devices = %s (%v)", b, err)
	}
	records, err := readSessions(filepath.Join(Dir(h.state), sessionsFileName))
	if err != nil || records[approval.RegistrationID].ApprovedDevice != "marcusbook-pro" {
		t.Fatalf("store = %+v (%v)", records, err)
	}

	// Approving the same key again from the CLI records the CLI, not the old device.
	again := h.requestAccessWithKey(records[approval.RegistrationID].PublicKey, "")
	body, _ = json.Marshal(AccessApproveRequest{Code: again.Code})
	r, b = h.localDo(req{method: http.MethodPost, path: accessApprovePath, body: string(body)})
	expect(t, r, b, http.StatusOK, "")
	var reapproval AccessApproval
	if err := json.Unmarshal(b, &reapproval); err != nil || reapproval.ApprovedVia != approvedViaCLI || reapproval.ApprovedDevice != "" {
		t.Fatalf("re-approval = %s (%v)", b, err)
	}
}

// The stored device name holds the label invariant, and only a tailnet
// approval may carry one.
func TestStoredApprovingDeviceIsValidated(t *testing.T) {
	h := newHarness(t)
	_, paired := h.pairBrowserKey()
	path := filepath.Join(Dir(h.state), sessionsFileName)
	records, err := readSessions(path)
	if err != nil {
		t.Fatal(err)
	}
	record := records[paired.RegistrationID]
	write := func(s session) string {
		t.Helper()
		out := filepath.Join(t.TempDir(), sessionsFileName)
		data, _ := json.Marshal(sessionsFile{Version: sessionsVersion, Registrations: map[string]session{paired.RegistrationID: s}})
		if err := os.WriteFile(out, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return out
	}

	ok := record
	ok.ApprovedVia, ok.ApprovedDevice = "tailnet:"+testTailnetLogin, "  marcusbook   pro "
	got, err := readSessions(write(ok))
	if err != nil || got[paired.RegistrationID].ApprovedDevice != "marcusbook pro" {
		t.Fatalf("valid device = %+v (%v)", got, err)
	}
	for name, bad := range map[string]session{
		"device on a link approval": func() session { s := record; s.ApprovedDevice = "laptop"; return s }(),
		"control character":         func() session { s := ok; s.ApprovedDevice = "lap\x07top"; return s }(),
		"too long":                  func() session { s := ok; s.ApprovedDevice = string(make([]rune, maxDeviceLabelRunes+1)); return s }(),
	} {
		if _, err := readSessions(write(bad)); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	// A version-2 store written before the field existed has no key at all.
	raw, _ := os.ReadFile(path)
	if json.Valid(raw) && containsKey(raw, "approved_device") {
		t.Fatalf("a link registration wrote approved_device: %s", raw)
	}
}

func containsKey(raw []byte, key string) bool {
	var file struct {
		Registrations map[string]map[string]json.RawMessage `json:"registrations"`
	}
	if json.Unmarshal(raw, &file) != nil {
		return false
	}
	for _, r := range file.Registrations {
		if _, ok := r[key]; ok {
			return true
		}
	}
	return false
}
