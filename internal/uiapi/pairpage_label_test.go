package uiapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The pairing page names the device it pairs, and the server cleans that name
// exactly as it cleans an access request's label before storing it.
func TestPairingExchangeStoresACleanedDeviceLabel(t *testing.T) {
	h := newHarness(t)
	_, pub := browserTestKey(t)
	exchange := func(label string) {
		t.Helper()
		body, _ := json.Marshal(PairingExchangeRequest{Code: h.pairingCode("/").Code, PublicKey: pub, Label: label})
		r, b := h.browserDo(req{method: http.MethodPost, path: "/api/v0/pairing/exchange", body: string(body), header: mutationHeaders(h.ownOrigin(), nil)})
		expect(t, r, b, http.StatusOK, "")
	}
	device := func() Device {
		t.Helper()
		r, b := h.localDo(req{method: http.MethodGet, path: "/api/v0/pairing/sessions"})
		expect(t, r, b, http.StatusOK, "")
		var list DeviceList
		if err := json.Unmarshal(b, &list); err != nil || len(list.Devices) != 1 {
			t.Fatalf("devices = %s (%v)", b, err)
		}
		return list.Devices[0]
	}

	exchange("  Safari\u202e on\tmacOS\x00 " + strings.Repeat("x", 100))
	got := device()
	if !strings.HasPrefix(got.Label, "Safari on macOS x") || len([]rune(got.Label)) != maxDeviceLabelRunes || !storedLabelValid(got.Label) {
		t.Fatalf("label = %q", got.Label)
	}
	if got.ApprovedVia != approvedViaLink {
		t.Fatalf("approved_via = %q", got.ApprovedVia)
	}

	// Pairing the same key again without a name keeps the name it had.
	exchange("")
	if label := device().Label; !strings.HasPrefix(label, "Safari on macOS") {
		t.Fatalf("label after an unnamed re-pair = %q", label)
	}
	exchange("Firefox on Linux")
	if label := device().Label; label != "Firefox on Linux" {
		t.Fatalf("label after a renamed re-pair = %q", label)
	}
}

// The built-in page sends the name it derives from the user agent.
func TestPairPageSendsADeviceLabel(t *testing.T) {
	for _, want := range []string{"const deviceLabel = (ua) =>", "label: deviceLabel(navigator.userAgent || \"\") || undefined"} {
		if !strings.Contains(pairScript, want) {
			t.Fatalf("pair script lacks %q", want)
		}
	}
}
