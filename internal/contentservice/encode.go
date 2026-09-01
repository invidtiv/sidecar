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

// EncodeCatalogResult writes the compact JSON object for a picker catalog,
// shrinking the file list until the encoded form fits under MaxEncodedBytes.
func EncodeCatalogResult(result CatalogResult) ([]byte, error) {
	if result.Kind == "" {
		result.Kind = KindCatalog
	}
	for {
		raw, err := marshalJSON(result)
		if err != nil {
			return nil, Internal("encode catalog result", err)
		}
		if len(raw) <= MaxEncodedBytes {
			return raw, nil
		}
		if len(result.Files) > 1 {
			result.Files = result.Files[:len(result.Files)/2]
			result.Truncated = true
			continue
		}
		return nil, Rejected("encoded catalog result exceeds %d bytes", MaxEncodedBytes)
	}
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
		overflow := len(raw) - MaxEncodedBytes
		if result.Content != "" {
			cut := len(result.Content) - overflow - 64
			if cut >= len(result.Content) {
				cut = len(result.Content) / 2
			}
			if cut < 0 {
				cut = 0
			}
			result.Content = shrinkUTF8(result.Content, cut)
			result.Truncated = true
			continue
		}
		if shrinkDiffDTO(result.Diff, overflow) {
			result.Truncated = true
			if result.Diff != nil {
				result.Diff.Truncated = true
			}
			continue
		}
		if result.Resource != nil && result.Resource.Body != nil && result.Resource.Body.Text != "" {
			cut := len(result.Resource.Body.Text) - overflow - 64
			if cut >= len(result.Resource.Body.Text) {
				cut = len(result.Resource.Body.Text) / 2
			}
			if cut < 0 {
				cut = 0
			}
			result.Resource.Body.Text = shrinkUTF8(result.Resource.Body.Text, cut)
			result.Truncated = true
			continue
		}
		oversize := ReadResult{
			Kind:      result.Kind,
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
}

func shrinkDiffDTO(d *DiffDTO, overflow int) bool {
	if d == nil {
		return false
	}
	cut := func(s *string) bool {
		if s == nil || *s == "" {
			return false
		}
		n := len(*s) - overflow - 64
		if n >= len(*s) {
			n = len(*s) / 2
		}
		if n < 0 {
			n = 0
		}
		*s = shrinkUTF8(*s, n)
		return true
	}
	if d.Snapshot != nil {
		if cut(&d.Snapshot.WorkingTree) {
			d.Snapshot.Truncated = true
			return true
		}
		if cut(&d.Snapshot.AggregateCommitted) {
			d.Snapshot.Truncated = true
			return true
		}
		if cut(&d.Snapshot.AggregateUncommitted) {
			d.Snapshot.Truncated = true
			return true
		}
		for i := range d.Snapshot.Files {
			if cut(&d.Snapshot.Files[i].Raw) {
				d.Snapshot.Truncated = true
				return true
			}
		}
	}
	if d.Range != nil {
		if cut(&d.Range.Raw) {
			return true
		}
		for i := range d.Range.Files {
			if cut(&d.Range.Files[i].Raw) {
				return true
			}
		}
	}
	if d.File != nil && cut(&d.File.Raw) {
		return true
	}
	if d.FullFile != nil {
		if cut(&d.FullFile.RawDiff) {
			d.FullFile.Truncated = true
			return true
		}
		if cut(&d.FullFile.OldContent) {
			d.FullFile.Truncated = true
			return true
		}
		if cut(&d.FullFile.NewContent) {
			d.FullFile.Truncated = true
			return true
		}
	}
	return false
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
