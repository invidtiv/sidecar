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

func TestDecodeLayoutPayload_SpecForms(t *testing.T) {
	apply, err := DecodeLayoutPayload(json.RawMessage(`{"mode":"apply","columns":[{"panes":[{"kind":"primary"}]}]}`))
	if err != nil || len(apply.Columns) == 0 {
		t.Fatalf("spec apply decode = %+v err=%v", apply, err)
	}
	columns, err := DecodeLayoutColumns(apply.Columns)
	if err != nil || len(columns) != 1 || len(columns[0].Panes) != 1 || columns[0].Panes[0].Kind != "primary" {
		t.Fatalf("columns decode = %+v err=%v", columns, err)
	}
	for raw, want := range map[string]string{
		`{"mode":"apply","panes":[{"kind":"diff"}],"columns":[]}`: "never both",
		`{"mode":"apply","columns":[]}`:                           "",
		`{"mode":"get","columns":[]}`:                             "carries no layout spec",
	} {
		_, err := DecodeLayoutPayload(json.RawMessage(raw))
		if want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("DecodeLayoutPayload(%s) = %v, want %q", raw, err, want)
		}
	}
}

func TestValidateLayoutSpec(t *testing.T) {
	valid := func(panes ...string) LayoutSpec {
		spec := LayoutSpec{}
		for _, column := range panes {
			var col LayoutSpecColumn
			if err := json.Unmarshal(json.RawMessage(column), &col); err != nil {
				t.Fatal(err)
			}
			spec.Columns = append(spec.Columns, col)
		}
		return spec
	}

	if err := ValidateLayoutSpec(valid(
		`{"panes":[{"kind":"primary"}]}`,
		`{"panes":[{"kind":"file","targets":["a.go"]},{"kind":"issue","targets":["td-1a2b3c"]}]}`,
	)); err != nil {
		t.Errorf("valid spec rejected: %v", err)
	}

	for name, tc := range map[string]struct {
		spec LayoutSpec
		want string
	}{
		"no columns":    {LayoutSpec{}, "at least one column"},
		"five columns":  {valid(`{}`, `{}`, `{}`, `{}`, `{}`), "cap is 4"},
		"empty column":  {valid(`{"panes":[]}`), "carries no panes"},
		"five rows":     {valid(`{"panes":[{"kind":"primary"},{"kind":"diff"},{},{},{}]}`), "cap is 4"},
		"unknown kind":  {valid(`{"panes":[{"kind":"browser"}]}`), "unknown pane kind"},
		"no primary":    {valid(`{"panes":[{"kind":"file","targets":["a.go"]}]}`), "exactly one"},
		"two primaries": {valid(`{"panes":[{"kind":"primary"}]}`, `{"panes":[{"kind":"primary"}]}`), "found 2"},
		"at in a spec": {valid(
			`{"panes":[{"kind":"primary"}]}`,
			`{"panes":[{"kind":"file","targets":["a.go"],"at":"2.1"}]}`,
		), "positions panes"},
		"primary fields": {valid(`{"panes":[{"kind":"primary","run":"x"}]}`), "takes no other fields"},
		"carry with run": {valid(`{"panes":[{"kind":"primary"}]}`, `{"panes":[{"kind":"shell","session":"s","run":"x"}]}`), "takes only"},
		"shell targets":  {valid(`{"panes":[{"kind":"primary"}]}`, `{"panes":[{"kind":"shell","targets":["a.go"]}]}`), "not targets"},
		"resource bare":  {valid(`{"panes":[{"kind":"primary"}]}`, `{"panes":[{"kind":"resource","targets":["CASH-1"]}]}`), "provider"},
		"file no target": {valid(`{"panes":[{"kind":"primary"}]}`, `{"panes":[{"kind":"file"}]}`), "needs at least one target"},
	} {
		err := ValidateLayoutSpec(tc.spec)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: ValidateLayoutSpec = %v, want %q", name, err, tc.want)
		}
	}
}
