package contentservice

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidRemoteResultRefusesALogLine(t *testing.T) {
	logLine := []byte(`{"level":"info","msg":"loading nvm","name":"nvm","path":"/usr/local/nvm"}`)

	var resolve ResolveResult
	if err := json.Unmarshal(logLine, &resolve); err != nil {
		t.Fatal(err)
	}
	if resolve.ValidRemoteResult() {
		t.Fatalf("a log line passed for resolve: %+v", resolve)
	}

	var read ReadResult
	if err := json.Unmarshal(logLine, &read); err != nil {
		t.Fatal(err)
	}
	if read.ValidRemoteResult() {
		t.Fatalf("a log line passed for read: %+v", read)
	}

	for _, body := range []string{`{}`, `{"kind":"file"}`, `{"revision":"v1"}`, `{"path":"/x"}`} {
		var empty ResolveResult
		if err := json.Unmarshal([]byte(body), &empty); err != nil {
			t.Fatal(err)
		}
		if empty.ValidRemoteResult() {
			t.Errorf("%s passed for resolve", body)
		}
		var emptyRead ReadResult
		if err := json.Unmarshal([]byte(body), &emptyRead); err != nil {
			t.Fatal(err)
		}
		if emptyRead.ValidRemoteResult() {
			t.Errorf("%s passed for read", body)
		}
	}
}

func TestValidRemoteResultAcceptsRealAnswers(t *testing.T) {
	var resolve ResolveResult
	if err := json.Unmarshal([]byte(`{"kind":"file","workspace":"p:shell:s1","display":"a.md","path":"/p/a.md","revision":"v1:abc","futureField":1}`), &resolve); err != nil {
		t.Fatal(err)
	}
	if !resolve.ValidRemoteResult() {
		t.Fatalf("real resolve refused: %+v", resolve)
	}

	var read ReadResult
	if err := json.Unmarshal([]byte(`{"kind":"file","operation":"document","workspace":"p:shell:s1","display":"a.md","path":"/p/a.md","revision":"v1:abc","content":"hi"}`), &read); err != nil {
		t.Fatal(err)
	}
	if !read.ValidRemoteResult() {
		t.Fatalf("real read refused: %+v", read)
	}

	var notModified ReadResult
	if err := json.Unmarshal([]byte(`{"kind":"file","notModified":true,"revision":"v1:abc"}`), &notModified); err != nil {
		t.Fatal(err)
	}
	if !notModified.ValidRemoteResult() {
		t.Fatalf("notModified refused: %+v", notModified)
	}

	var oversize ReadResult
	if err := json.Unmarshal([]byte(`{"kind":"file","oversize":true,"truncated":true,"revision":"v1:abc","totalSize":900000}`), &oversize); err != nil {
		t.Fatal(err)
	}
	if !oversize.ValidRemoteResult() {
		t.Fatalf("oversize refused: %+v", oversize)
	}
}

func TestEncodeReadResultTruncatesBeforeTransportCap(t *testing.T) {
	// Quotes double under JSON encoding. A 500 KiB file of quotes would blow
	// MaxRunOutputBytes if shipped raw; the encoder must return valid JSON
	// under MaxEncodedBytes instead.
	content := strings.Repeat("\"", filepreviewMaxForTest(t))
	result := ReadResult{
		Kind:      KindFile,
		Operation: OpDocument,
		Workspace: "p:worktree:/p",
		Display:   "quotes.txt",
		Path:      "/p/quotes.txt",
		Revision:  "v1:deadbeef",
		Content:   content,
		TotalSize: int64(len(content)),
	}
	raw, err := EncodeReadResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > MaxEncodedBytes {
		t.Fatalf("encoded %d bytes, cap %d", len(raw), MaxEncodedBytes)
	}
	if len(raw) >= transportOutputCap {
		t.Fatalf("encoded result reached the transport cap")
	}
	var decoded ReadResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, raw[:min(len(raw), 200)])
	}
	if !decoded.ValidRemoteResult() {
		t.Fatalf("encoded oversize/truncated failed ValidRemoteResult: %+v", decoded)
	}
	if !decoded.Truncated && !decoded.Oversize {
		t.Fatalf("quote-heavy payload was not marked truncated: content=%d", len(decoded.Content))
	}
	if decoded.Revision != result.Revision {
		t.Fatalf("revision changed by encode: %q", decoded.Revision)
	}
}

func filepreviewMaxForTest(t *testing.T) int {
	t.Helper()
	// 400 KiB of quotes encodes to ~800 KiB of JSON plus envelope, which is
	// over MaxEncodedBytes (768 KiB) and would also threaten the 1 MiB cap.
	return 400 << 10
}

func TestDirectAndJSONContractsMatch(t *testing.T) {
	result := ReadResult{
		Kind:      KindFile,
		Operation: OpDocument,
		Workspace: "p:shell:s1",
		Display:   "a.md",
		Path:      "/p/a.md",
		Revision:  "v1:abc",
		Content:   "hello from disk\n",
		TotalSize: 16,
		ModTime:   "2026-08-31T00:00:00Z",
		Mode:      "0644",
	}
	raw, err := EncodeReadResult(result)
	if err != nil {
		t.Fatal(err)
	}
	var viaJSON ReadResult
	if err := json.Unmarshal(raw, &viaJSON); err != nil {
		t.Fatal(err)
	}
	if viaJSON.Display != result.Display || viaJSON.Content != result.Content || viaJSON.Revision != result.Revision {
		t.Fatalf("JSON drifted: %+v", viaJSON)
	}

	notModified := ReadResult{Kind: KindFile, NotModified: true, Revision: "v1:abc"}
	raw, err = EncodeReadResult(notModified)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ReadResult
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.NotModified || decoded.Revision != "v1:abc" || !decoded.ValidRemoteResult() {
		t.Fatalf("notModified JSON = %+v", decoded)
	}
}
