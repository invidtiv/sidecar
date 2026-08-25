package uirequest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeLayoutPayload_Modes(t *testing.T) {
	get, err := DecodeLayoutPayload(json.RawMessage(`{"mode":"get"}`))
	if err != nil || get.Mode != LayoutModeGet {
		t.Fatalf("get decode = %+v err=%v", get, err)
	}
	apply, err := DecodeLayoutPayload(json.RawMessage(`{"mode":"apply","panes":[{"kind":"file","targets":["a.go"],"at":"2.1"},{"kind":"shell","run":"make dev"}]}`))
	if err != nil || len(apply.Panes) != 2 {
		t.Fatalf("apply decode = %+v err=%v", apply, err)
	}
	if apply.Panes[0].At != "2.1" || apply.Panes[1].Run != "make dev" {
		t.Fatalf("pane fields lost: %+v", apply.Panes)
	}
}

func TestDecodeLayoutPayload_Refusals(t *testing.T) {
	for raw, want := range map[string]string{
		`{}`:                        "mode",
		``:                          "required",
		`{"mode":"sideways"}`:       "unknown layout mode",
		`{"mode":"apply"}`:          "no panes",
		`{"mode":"get","panes":[]}`: "",
		`not json`:                  "",
	} {
		if _, err := DecodeLayoutPayload(json.RawMessage(raw)); want != "" && !strings.Contains(err.Error(), want) {
			t.Errorf("DecodeLayoutPayload(%s) = %v, want %q", raw, err, want)
		}
	}
}

// The items array is additive and versioned: present only on acks that carry
// per-pane verdicts, always named by ItemsVersion, beside the untouched
// single status every existing consumer reads.
func TestAckItemsShape(t *testing.T) {
	plain, err := json.Marshal(Ack{Status: StatusOpened})
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"items", "itemsVersion", "layout"} {
		if strings.Contains(string(plain), banned) {
			t.Fatalf("plain ack leaked %q: %s", banned, plain)
		}
	}

	withItems, err := json.Marshal(Ack{
		Status:       StatusDeclined,
		Reason:       "first violation",
		ItemsVersion: 1,
		Items: []AckItem{
			{Index: 0, Verdict: ItemVerdictOpened, Cell: "2.1", Surface: "shell:s", Pane: 7},
			{Index: 1, Verdict: ItemVerdictDeclined, Reason: "two live terminals at a time; close one first"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Status       Status    `json:"status"`
		Reason       string    `json:"reason"`
		ItemsVersion int       `json:"itemsVersion"`
		Items        []AckItem `json:"items"`
	}
	if err := json.Unmarshal(withItems, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Status != StatusDeclined || decoded.Reason != "first violation" {
		t.Fatalf("single status damaged by items: %+v", decoded)
	}
	if decoded.ItemsVersion != 1 || len(decoded.Items) != 2 {
		t.Fatalf("items = %+v", decoded)
	}
	if decoded.Items[0].Verdict != ItemVerdictOpened || decoded.Items[0].Cell != "2.1" || decoded.Items[0].Pane != 7 {
		t.Fatalf("opened item = %+v", decoded.Items[0])
	}
	if decoded.Items[1].Verdict != ItemVerdictDeclined || decoded.Items[1].Reason != "two live terminals at a time; close one first" {
		t.Fatalf("declined item = %+v", decoded.Items[1])
	}
}
