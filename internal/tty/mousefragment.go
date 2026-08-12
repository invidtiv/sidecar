package tty

import "time"

// MouseFragment reassembles an SGR mouse report that the input reader split
// across reads and delivered as ordinary key text.
//
// GateKey decides one key at a time and can only recognize a fragment that
// looks like one on its own. This holds the partial report between reads and
// matches it against the real grammar, which is what tells a genuine "<35;10;20M"
// leak apart from a user typing "<".
//
// It keeps no clock of its own: every caller passes the same now it gates the
// rest of the key press with, so a headless driver can replay a split report
// without waiting for one.
type MouseFragment struct {
	pending string
	at      time.Time
}

// MouseFragmentTimeout bounds how long a partial report is held. The remainder
// of a real report arrives in the same burst of terminal output; anything later
// is someone typing.
const MouseFragmentTimeout = 50 * time.Millisecond

// Consume reports whether text is part of a split SGR mouse report, retaining
// it when more of the report is still to come.
//
// escapePressed and escapeAt are the double-escape window: an ESC delivered as
// its own key press may be the report's first byte rather than the keyboard's.
func (f *MouseFragment) Consume(text string, escapePressed bool, escapeAt, now time.Time) bool {
	if f.pending != "" && now.Sub(f.at) >= MouseFragmentTimeout {
		f.pending = ""
	}

	if f.pending != "" {
		combined := f.pending + text
		if possible, complete := SGRMouseFragmentState(combined); possible {
			if complete {
				f.pending = ""
			} else {
				f.remember(combined, now)
			}
			return true
		}
		f.pending = ""
	}

	if escapePressed && now.Sub(escapeAt) < MouseFragmentTimeout {
		combined := "\x1b" + text
		if possible, complete := SGRMouseFragmentState(combined); possible {
			if !complete {
				f.remember(combined, now)
			}
			return true
		}
	}

	if possible, complete := SGRMouseFragmentState(text); possible {
		// A lone "[" is valid user input. Only the escape and mouse-proximity
		// gates in GateKey may claim it.
		if text == "[" {
			return false
		}
		if !complete {
			f.remember(text, now)
		}
		return true
	}
	return false
}

// Hold retains text as the start of a report whose remainder is still to come.
// It is how a caller hands over a fragment GateKey claimed on its own.
func (f *MouseFragment) Hold(text string, now time.Time) { f.remember(text, now) }

// Reset drops any partial report. A surface that stops taking input cannot
// carry half a report into the next thing the user does.
func (f *MouseFragment) Reset() { f.pending = "" }

// Pending reports the partial report currently held.
func (f *MouseFragment) Pending() string { return f.pending }

func (f *MouseFragment) remember(fragment string, now time.Time) {
	f.pending = fragment
	f.at = now
}

// SGRMouseFragmentState recognizes a complete SGR mouse report or any prefix
// that can become one. The grammar is ESC? "[" "<" digits ";" digits ";"
// digits ("M"|"m").
func SGRMouseFragmentState(s string) (possible, complete bool) {
	if s == "" {
		return false, false
	}
	i := 0
	if s[i] == '\x1b' {
		i++
		if i == len(s) {
			return true, false
		}
	}
	for _, want := range []byte{'[', '<'} {
		if i == len(s) {
			return true, false
		}
		if s[i] != want {
			return false, false
		}
		i++
	}
	for field := 0; field < 3; field++ {
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if start == i {
			return i == len(s), false
		}
		if field < 2 {
			if i == len(s) {
				return true, false
			}
			if s[i] != ';' {
				return false, false
			}
			i++
		}
	}
	if i == len(s) {
		return true, false
	}
	if (s[i] == 'M' || s[i] == 'm') && i+1 == len(s) {
		return true, true
	}
	return false, false
}
