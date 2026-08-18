package resource

import "time"

// The wire types are exactly the JSON shapes in the protocol document. They
// exist so decoding is total — every field is optional at the JSON layer and
// every rule is enforced in one place, Sanitize* — rather than scattered across
// json tags a future field could quietly bypass.
//
// Unknown JSON fields are ignored: encoding/json's default behavior is the
// forward-compatibility rule the protocol asks for, so no decoder in Sidecar
// may set DisallowUnknownFields on a provider response.

// WireStatus is `{label, tone}`.
type WireStatus struct {
	Label string `json:"label"`
	Tone  string `json:"tone,omitempty"`
}

// WireField is one `{label, value}` pair.
type WireField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// WireBody is `{format, text}`.
type WireBody struct {
	Format string `json:"format,omitempty"`
	Text   string `json:"text"`
}

// WireDocument is the `resource` object of a success response.
type WireDocument struct {
	Identity        string      `json:"identity"`
	Title           string      `json:"title"`
	Subtitle        string      `json:"subtitle,omitempty"`
	Status          *WireStatus `json:"status,omitempty"`
	Fields          []WireField `json:"fields,omitempty"`
	Body            *WireBody   `json:"body,omitempty"`
	SourceURL       string      `json:"sourceUrl,omitempty"`
	UpdatedAt       string      `json:"updatedAt,omitempty"`
	FreshForSeconds float64     `json:"freshForSeconds,omitempty"`
}

// WireError is the `error` object of a typed failure response. Retryable is a
// pointer so an omitted value takes the code's documented default rather than
// silently reading as false.
type WireError struct {
	Code      string `json:"code"`
	Message   string `json:"message,omitempty"`
	Retryable *bool  `json:"retryable,omitempty"`
	SetupHint string `json:"setupHint,omitempty"`
}

// SanitizeError turns a provider's typed failure into a host one. It never
// fails: an empty or unknown code becomes internal.
func SanitizeError(w *WireError) *Error {
	if w == nil {
		return Errorf(CodeInternal, "provider returned neither a resource nor an error")
	}
	code := CoerceCode(w.Code)
	retryable := DefaultRetryable(code)
	if w.Retryable != nil {
		retryable = *w.Retryable
	}
	return &Error{
		Code:      code,
		Message:   SanitizeLine(w.Message, MaxMessageChars),
		Retryable: retryable,
		SetupHint: SanitizeLine(w.SetupHint, MaxSetupHintChars),
	}
}

// SanitizeDocument enforces every bound in the Limits table and returns a
// document safe to hand to view state. The only failures are the two required
// fields: a resource with no identity or no title cannot be keyed or labelled,
// so it is a typed internal error rather than a blank card.
func SanitizeDocument(w *WireDocument) (Document, *Error) {
	if w == nil {
		return Document{}, Errorf(CodeInternal, "provider returned an empty resource")
	}

	doc := Document{
		Identity:  SanitizeLine(w.Identity, MaxIdentityChars),
		Title:     SanitizeLine(w.Title, MaxTitleChars),
		Subtitle:  SanitizeLine(w.Subtitle, MaxSubtitleChars),
		SourceURL: SanitizeURL(w.SourceURL),
		FreshFor:  ClampFreshFor(w.FreshForSeconds),
	}
	if doc.Identity == "" {
		return Document{}, Errorf(CodeInternal, "provider returned a resource without an identity")
	}
	if doc.Title == "" {
		return Document{}, Errorf(CodeInternal, "provider returned a resource without a title")
	}

	if w.Status != nil {
		label := SanitizeLine(w.Status.Label, MaxStatusLabelChars)
		if label != "" {
			doc.Status = &Status{Label: label, Tone: CoerceTone(w.Status.Tone)}
		}
	}

	if len(w.Fields) > 0 {
		fields := make([]Field, 0, min(len(w.Fields), MaxFields))
		for _, f := range w.Fields {
			if len(fields) == MaxFields {
				break
			}
			label := SanitizeLine(f.Label, MaxFieldLabelChars)
			value := SanitizeLine(f.Value, MaxFieldValueChars)
			if label == "" && value == "" {
				continue
			}
			fields = append(fields, Field{Label: label, Value: value})
		}
		if len(fields) > 0 {
			doc.Fields = fields
		}
	}

	if w.Body != nil {
		text := SanitizeBody(w.Body.Text, MaxBodyBytes)
		if text != "" {
			doc.Body = &Body{Format: CoerceFormat(w.Body.Format), Text: text}
		}
	}

	// An unparseable timestamp is dropped rather than failing the document.
	doc.UpdatedAt = parseTimestamp(w.UpdatedAt)

	return doc, nil
}

// parseTimestamp accepts RFC 3339, with or without fractional seconds, and
// returns the zero time for anything else.
func parseTimestamp(v string) time.Time {
	v = SanitizeLine(v, 64)
	if v == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
