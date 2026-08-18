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

// Status is the optional single-line state of a resource.
type Status struct {
	Label string
	Tone  Tone
}

// Field is one ordered label/value pair in the bounded grid.
type Field struct {
	Label string
	Value string
}

// Body is the optional long-form text of a resource.
type Body struct {
	Format Format
	Text   string
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
