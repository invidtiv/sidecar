package resource

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSanitizeDocumentHappyPath(t *testing.T) {
	raw := `{
	  "identity": "CASH-1245",
	  "title": "Refund totals differ after partial capture",
	  "subtitle": "Bug",
	  "status": {"label": "IN PROGRESS", "tone": "info"},
	  "fields": [{"label": "Assignee", "value": "Marcus"}],
	  "body": {"format": "markdown", "text": "Ticket description..."},
	  "sourceUrl": "https://jira.example.test/browse/CASH-1245",
	  "updatedAt": "2026-08-17T17:31:00Z",
	  "freshForSeconds": 60,
	  "somethingTheHostHasNeverHeardOf": {"nested": true}
	}`
	var w WireDocument
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc, rerr := SanitizeDocument(&w)
	if rerr != nil {
		t.Fatalf("SanitizeDocument: %v", rerr)
	}
	if doc.Identity != "CASH-1245" || doc.Title != "Refund totals differ after partial capture" {
		t.Fatalf("unexpected head: %+v", doc)
	}
	if doc.Status == nil || doc.Status.Tone != ToneInfo || doc.Status.Label != "IN PROGRESS" {
		t.Fatalf("status = %+v", doc.Status)
	}
	if len(doc.Fields) != 1 || doc.Fields[0].Label != "Assignee" {
		t.Fatalf("fields = %+v", doc.Fields)
	}
	if doc.Body == nil || doc.Body.Format != FormatMarkdown {
		t.Fatalf("body = %+v", doc.Body)
	}
	if doc.SourceURL != "https://jira.example.test/browse/CASH-1245" {
		t.Fatalf("sourceUrl = %q", doc.SourceURL)
	}
	if !doc.UpdatedAt.Equal(time.Date(2026, 8, 17, 17, 31, 0, 0, time.UTC)) {
		t.Fatalf("updatedAt = %s", doc.UpdatedAt)
	}
	if doc.FreshFor != time.Minute {
		t.Fatalf("freshFor = %s", doc.FreshFor)
	}
}

func TestSanitizeDocumentRequiresIdentityAndTitle(t *testing.T) {
	if _, err := SanitizeDocument(&WireDocument{Title: "t"}); err == nil || err.Code != CodeInternal {
		t.Fatalf("missing identity should be internal, got %v", err)
	}
	if _, err := SanitizeDocument(&WireDocument{Identity: "i"}); err == nil || err.Code != CodeInternal {
		t.Fatalf("missing title should be internal, got %v", err)
	}
	if _, err := SanitizeDocument(nil); err == nil {
		t.Fatal("nil document should fail")
	}
	// A title made entirely of control characters sanitizes to empty and is
	// therefore a missing title, not a blank card.
	if _, err := SanitizeDocument(&WireDocument{Identity: "i", Title: "\x01\x02"}); err == nil {
		t.Fatal("control-only title should fail")
	}
}

func TestSanitizeDocumentBounds(t *testing.T) {
	w := &WireDocument{
		Identity:        strings.Repeat("i", MaxIdentityChars+50),
		Title:           strings.Repeat("t", MaxTitleChars+50),
		Subtitle:        strings.Repeat("s", MaxSubtitleChars+50),
		Body:            &WireBody{Format: "markdown", Text: strings.Repeat("b", MaxBodyBytes+1000)},
		SourceURL:       "https://example.test/x",
		FreshForSeconds: 1e9,
	}
	for i := 0; i < MaxFields+20; i++ {
		w.Fields = append(w.Fields, WireField{
			Label: strings.Repeat("l", MaxFieldLabelChars+10),
			Value: strings.Repeat("v", MaxFieldValueChars+10),
		})
	}
	doc, err := SanitizeDocument(w)
	if err != nil {
		t.Fatalf("SanitizeDocument: %v", err)
	}
	if runeLen(doc.Identity) != MaxIdentityChars {
		t.Fatalf("identity length %d", runeLen(doc.Identity))
	}
	if runeLen(doc.Title) != MaxTitleChars {
		t.Fatalf("title length %d", runeLen(doc.Title))
	}
	if runeLen(doc.Subtitle) != MaxSubtitleChars {
		t.Fatalf("subtitle length %d", runeLen(doc.Subtitle))
	}
	if len(doc.Fields) != MaxFields {
		t.Fatalf("field count %d", len(doc.Fields))
	}
	if runeLen(doc.Fields[0].Label) != MaxFieldLabelChars || runeLen(doc.Fields[0].Value) != MaxFieldValueChars {
		t.Fatalf("field cell not bounded")
	}
	if len(doc.Body.Text) > MaxBodyBytes {
		t.Fatalf("body length %d", len(doc.Body.Text))
	}
	if doc.FreshFor != MaxFreshFor {
		t.Fatalf("freshFor %s", doc.FreshFor)
	}
}

func TestSanitizeDocumentCoercions(t *testing.T) {
	doc, err := SanitizeDocument(&WireDocument{
		Identity:  "i",
		Title:     "t",
		Status:    &WireStatus{Label: "Odd", Tone: "chartreuse"},
		Body:      &WireBody{Format: "asciidoc", Text: "x"},
		SourceURL: "ssh://host/x",
		UpdatedAt: "not a timestamp",
	})
	if err != nil {
		t.Fatalf("SanitizeDocument: %v", err)
	}
	if doc.Status.Tone != ToneNeutral {
		t.Fatalf("unknown tone = %q", doc.Status.Tone)
	}
	if doc.Body.Format != FormatText {
		t.Fatalf("unknown format = %q", doc.Body.Format)
	}
	if doc.SourceURL != "" {
		t.Fatalf("ssh sourceUrl survived: %q", doc.SourceURL)
	}
	if !doc.UpdatedAt.IsZero() {
		t.Fatalf("unparseable timestamp survived: %s", doc.UpdatedAt)
	}
}

func TestSanitizeDocumentStripsOSCFromEveryString(t *testing.T) {
	osc := "\x1b]8;;https://evil.test\x1b\\x\x1b]8;;\x1b\\"
	doc, err := SanitizeDocument(&WireDocument{
		Identity: "id" + osc,
		Title:    "title" + osc,
		Subtitle: "sub" + osc,
		Status:   &WireStatus{Label: "st" + osc},
		Fields:   []WireField{{Label: "l" + osc, Value: "v" + osc}},
		Body:     &WireBody{Text: "body" + osc},
	})
	if err != nil {
		t.Fatalf("SanitizeDocument: %v", err)
	}
	for name, s := range map[string]string{
		"identity": doc.Identity,
		"title":    doc.Title,
		"subtitle": doc.Subtitle,
		"status":   doc.Status.Label,
		"label":    doc.Fields[0].Label,
		"value":    doc.Fields[0].Value,
		"body":     doc.Body.Text,
	} {
		if strings.Contains(s, "\x1b") || strings.Contains(s, "evil.test") {
			t.Fatalf("%s kept OSC payload: %q", name, s)
		}
	}
}

func TestSanitizeError(t *testing.T) {
	retryable := false
	e := SanitizeError(&WireError{Code: "unauthorized", Message: "creds expired", Retryable: &retryable, SetupHint: "run configure"})
	if e.Code != CodeUnauthorized || e.Retryable || e.SetupHint != "run configure" {
		t.Fatalf("error = %+v", e)
	}

	e = SanitizeError(&WireError{Code: "teapot"})
	if e.Code != CodeInternal || !e.Retryable {
		t.Fatalf("unknown code = %+v", e)
	}

	e = SanitizeError(&WireError{Code: "rate_limited"})
	if !e.Retryable {
		t.Fatal("rate_limited should default retryable")
	}
	e = SanitizeError(&WireError{Code: "not_found"})
	if e.Retryable {
		t.Fatal("not_found should default non-retryable")
	}
	if SanitizeError(nil) == nil {
		t.Fatal("nil wire error must still produce a typed error")
	}
}

func TestReferenceValid(t *testing.T) {
	if !(resourceRef("a", "b", "c")).Valid() {
		t.Fatal("simple reference should be valid")
	}
	if (resourceRef("", "b", "c")).Valid() {
		t.Fatal("empty instance should be invalid")
	}
	if (resourceRef("a", "b", strings.Repeat("x", MaxLocatorChars+1))).Valid() {
		t.Fatal("oversize locator should be invalid")
	}
}

func resourceRef(instance, matcher, locator string) Reference {
	return Reference{Instance: instance, Matcher: matcher, Locator: locator}
}
