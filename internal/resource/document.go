package resource

import "time"

// Tone is the coarse severity of a status pill. It is presentation-neutral on
// purpose: the host maps it to a palette, the provider does not choose colors.
type Tone string

// Stable v1 tones. Anything else coerces to ToneNeutral.
const (
	ToneNeutral Tone = "neutral"
	ToneInfo    Tone = "info"
	ToneSuccess Tone = "success"
	ToneWarning Tone = "warning"
	ToneDanger  Tone = "danger"
)

// CoerceTone maps an arbitrary provider string onto a known tone.
func CoerceTone(v string) Tone {
	switch Tone(v) {
	case ToneInfo:
		return ToneInfo
	case ToneSuccess:
		return ToneSuccess
	case ToneWarning:
		return ToneWarning
	case ToneDanger:
		return ToneDanger
	default:
		return ToneNeutral
	}
}

// Format is how a body's text should be interpreted.
type Format string

// Stable v1 formats. Anything else coerces to FormatText, which is the safer
// of the two: unknown markup is shown, never interpreted.
const (
	FormatText     Format = "text"
	FormatMarkdown Format = "markdown"
)

// CoerceFormat maps an arbitrary provider string onto a known format.
func CoerceFormat(v string) Format {
	if Format(v) == FormatMarkdown {
		return FormatMarkdown
	}
	return FormatText
}

// FieldKind tells the host how to present a field value. It never changes
// validation: a timestamp that does not parse is still shown as the text the
// provider sent, because the alternative is silently hiding a value the user
// can see in the service itself.
type FieldKind string

// Stable v1 field kinds. Anything else coerces to FieldKindText.
const (
	FieldKindText      FieldKind = "text"
	FieldKindTimestamp FieldKind = "timestamp"
	FieldKindUser      FieldKind = "user"
)

// CoerceFieldKind maps an arbitrary provider string onto a known kind.
func CoerceFieldKind(v string) FieldKind {
	switch FieldKind(v) {
	case FieldKindTimestamp:
		return FieldKindTimestamp
	case FieldKindUser:
		return FieldKindUser
	default:
		return FieldKindText
	}
}

// Status is the optional single-line state of a resource.
type Status struct {
	Label string
	Tone  Tone
}

// Field is one ordered label/value pair in the bounded grid.
type Field struct {
	Label string
	Value string
	// Kind is a presentation hint. M1 owns what it does with it; M0's job is
	// only to carry and bound it, never to lose it.
	Kind FieldKind
}

// Body is the optional long-form text of a resource.
type Body struct {
	Format Format
	Text   string
	// Truncated reports that the provider's body exceeded MaxBodyBytes and was
	// cut at a rune boundary. The host shows the user what it kept and says so;
	// it does not refuse a document for being long.
	Truncated bool
}

// Document is the whole of what a provider may put on screen. Every string in
// it has already been through Sanitize*: valid UTF-8, bounded, control-free,
// and OSC-free. SourceURL, when non-empty, is a validated http/https URL.
type Document struct {
	// Identity is the provider-stable canonical ID. It may differ from the
	// locator that produced the lookup; the host re-keys the tab when it does.
	Identity string
	Title    string
	Subtitle string
	// Status is nil when the provider supplied none.
	Status *Status
	Fields []Field
	// Body is nil when the provider supplied none.
	Body      *Body
	SourceURL string
	// UpdatedAt is the zero time when absent or unparseable. An unparseable
	// timestamp is dropped, never an error.
	UpdatedAt time.Time
	// FreshFor is the clamped freshness hint the cache honors.
	FreshFor time.Duration
	// Sections are the titled blocks under the card. Empty for every document
	// a frozen-protocol provider returns.
	Sections []Section
}

// Reference is {provider instance, matcher, locator}: what a match produces
// and what a resolve consumes. It is the only provider-shaped value that
// reaches persisted state, and it carries no secret.
type Reference struct {
	Instance string
	Matcher  string
	Locator  string
}

// Valid reports whether a reference is well-formed enough to send to a
// provider. It is a bounds check, not an existence check.
func (r Reference) Valid() bool {
	return r.Instance != "" && r.Matcher != "" && r.Locator != "" &&
		runeLen(r.Instance) <= MaxInstanceIDChars &&
		runeLen(r.Matcher) <= MaxMatcherIDChars &&
		runeLen(r.Locator) <= MaxLocatorChars
}
