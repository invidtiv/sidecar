package contentservice

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

// MaxEncodedBytes is the cap on a content verb's final JSON. It sits
// comfortably under hosts.MaxRunOutputBytes (1 MiB) so the host returns a
// small structured truncated/oversize object rather than a cut that is not
// valid JSON. Do not raise MaxRunOutputBytes to fit a document.
const MaxEncodedBytes = 768 << 10

// transportOutputCap is hosts.MaxRunOutputBytes. Duplicated so this package
// does not import hosts (hosts tests import this package).
const transportOutputCap = 1 << 20

func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncodeResolveResult writes the compact JSON object (plus newline) for
// resolve. Resolve payloads are small; this still refuses to emit a value
// larger than MaxEncodedBytes.
func EncodeResolveResult(result ResolveResult) ([]byte, error) {
	raw, err := marshalJSON(result)
	if err != nil {
		return nil, Internal("encode resolve result", err)
	}
	if len(raw) > MaxEncodedBytes {
		return nil, Rejected("encoded resolve result exceeds %d bytes", MaxEncodedBytes)
	}
	return raw, nil
}

// EncodeReadResult writes the compact JSON object for a document read,
// shrinking Content until the encoded form fits under MaxEncodedBytes.
// An unchanged file's revision is preserved. If even metadata will not fit,
// a small oversize object is returned instead of invalid JSON.
func EncodeReadResult(result ReadResult) ([]byte, error) {
	if result.NotModified {
		return marshalJSON(result)
	}
	for {
		raw, err := marshalJSON(result)
		if err != nil {
			return nil, Internal("encode read result", err)
		}
		if len(raw) <= MaxEncodedBytes {
			return raw, nil
		}
		if result.Content == "" {
			oversize := ReadResult{
				Kind:      KindFile,
				Oversize:  true,
				Truncated: true,
				Revision:  result.Revision,
				TotalSize: result.TotalSize,
				Workspace: result.Workspace,
				Display:   result.Display,
			}
			raw, err = marshalJSON(oversize)
			if err != nil {
				return nil, Internal("encode oversize result", err)
			}
			if len(raw) > MaxEncodedBytes {
				return nil, Rejected("encoded read result exceeds %d bytes", MaxEncodedBytes)
			}
			return raw, nil
		}
		overflow := len(raw) - MaxEncodedBytes
		cut := len(result.Content) - overflow - 64
		if cut >= len(result.Content) {
			cut = len(result.Content) / 2
		}
		if cut < 0 {
			cut = 0
		}
		result.Content = shrinkUTF8(result.Content, cut)
		result.Truncated = true
	}
}

func shrinkUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// encodedFitsUnderCap is a compile-time-adjacent assertion tests pin: the
// content cap must stay below the transport cap.
func encodedFitsUnderCap() bool {
	return MaxEncodedBytes < transportOutputCap
}
