package reposervice

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"

	"github.com/marcus/sidecar/internal/contentservice"
)

// MaxEncodedBytes is the cap on a repo verb's final JSON. It is deliberately
// the same number the content verbs are held to: both travel the same host run
// transport, and a second cap would be a second thing to keep under it.
const MaxEncodedBytes = contentservice.MaxEncodedBytes

func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncodeStatusResult writes the JSON object for a status read, halving the
// changed-file list until the encoded form fits.
func EncodeStatusResult(result StatusResult) ([]byte, error) {
	for {
		raw, err := marshalJSON(result)
		if err != nil {
			return nil, contentservice.Internal("encode repo status result", err)
		}
		if len(raw) <= MaxEncodedBytes {
			return raw, nil
		}
		if len(result.Files) <= 1 {
			return nil, contentservice.Rejected("encoded repo status result exceeds %d bytes", MaxEncodedBytes)
		}
		result.Files = result.Files[:len(result.Files)/2]
		result.Truncated = true
	}
}

// EncodeDiffResult writes the JSON object for one patch, shrinking the patch
// until the encoded form fits and saying so.
func EncodeDiffResult(result DiffResult) ([]byte, error) {
	for {
		raw, err := marshalJSON(result)
		if err != nil {
			return nil, contentservice.Internal("encode repo diff result", err)
		}
		if len(raw) <= MaxEncodedBytes {
			return raw, nil
		}
		if result.Patch == "" {
			return nil, contentservice.Rejected("encoded repo diff result exceeds %d bytes", MaxEncodedBytes)
		}
		cut := len(result.Patch) - (len(raw) - MaxEncodedBytes) - 64
		if cut >= len(result.Patch) || cut < 0 {
			cut = len(result.Patch) / 2
		}
		result.Patch = shrinkUTF8(result.Patch, cut)
		result.Truncated = true
	}
}

// EncodeHistoryResult writes the JSON object for one history page, halving the
// commit list until the encoded form fits.
//
// The cursor is dropped with the rows it no longer describes: a cursor naming a
// commit the viewer never received would silently skip everything between.
func EncodeHistoryResult(result HistoryResult) ([]byte, error) {
	for {
		raw, err := marshalJSON(result)
		if err != nil {
			return nil, contentservice.Internal("encode repo history result", err)
		}
		if len(raw) <= MaxEncodedBytes {
			return raw, nil
		}
		if len(result.Commits) <= 1 {
			return nil, contentservice.Rejected("encoded repo history result exceeds %d bytes", MaxEncodedBytes)
		}
		result.Commits = result.Commits[:len(result.Commits)/2]
		result.NextCursor = result.Commits[len(result.Commits)-1].Hash
		result.Truncated = true
	}
}

// EncodeCommitResult writes the JSON object for one commit, dropping the body
// before the file list: a file list the viewer can page is worth more than a
// commit message it can read in `git show`.
func EncodeCommitResult(result CommitResult) ([]byte, error) {
	for {
		raw, err := marshalJSON(result)
		if err != nil {
			return nil, contentservice.Internal("encode repo commit result", err)
		}
		if len(raw) <= MaxEncodedBytes {
			return raw, nil
		}
		if result.Commit == nil {
			return nil, contentservice.Rejected("encoded repo commit result exceeds %d bytes", MaxEncodedBytes)
		}
		switch {
		case len(result.Commit.Body) > 0:
			result.Commit.Body = shrinkUTF8(result.Commit.Body, len(result.Commit.Body)/2)
			result.Commit.Truncated = true
		case len(result.Commit.Files) > 1:
			result.Commit.Files = result.Commit.Files[:len(result.Commit.Files)/2]
			result.Commit.Truncated = true
		default:
			return nil, contentservice.Rejected("encoded repo commit result exceeds %d bytes", MaxEncodedBytes)
		}
	}
}

// EncodeRefsResult writes the JSON object for a refs listing, halving the
// longest list until the encoded form fits.
func EncodeRefsResult(result RefsResult) ([]byte, error) {
	for {
		raw, err := marshalJSON(result)
		if err != nil {
			return nil, contentservice.Internal("encode repo refs result", err)
		}
		if len(raw) <= MaxEncodedBytes {
			return raw, nil
		}
		switch {
		case len(result.Branches) > 1 && len(result.Branches) >= len(result.RemoteBranches):
			result.Branches = result.Branches[:len(result.Branches)/2]
		case len(result.RemoteBranches) > 1:
			result.RemoteBranches = result.RemoteBranches[:len(result.RemoteBranches)/2]
		case len(result.Stashes) > 1:
			result.Stashes = result.Stashes[:len(result.Stashes)/2]
		default:
			return nil, contentservice.Rejected("encoded repo refs result exceeds %d bytes", MaxEncodedBytes)
		}
		result.Truncated = true
	}
}

// shrinkUTF8 truncates to n bytes without leaving a partial rune behind.
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
