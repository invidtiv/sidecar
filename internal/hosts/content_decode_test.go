package hosts

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/marcus/sidecar/internal/contentservice"
)

func TestRunSidecarDecodesContentResults(t *testing.T) {
	if MaxRunOutputBytes != 1<<20 {
		t.Fatalf("MaxRunOutputBytes changed to %d; content must not enlarge it", MaxRunOutputBytes)
	}
	logLine := `{"level":"info","msg":"loading nvm","name":"nvm","path":"/usr/local/nvm"}`

	t.Run("log line alone is not a content result", func(t *testing.T) {
		client := testRunClient(t, Host{ID: "h", Target: "h"}, stubInvoker(logLine+"\n", "", 0))
		var result contentservice.ReadResult
		err := client.RunSidecar(context.Background(), []string{"content", "read", "--json"}, &result)
		if got := RunFailure(err); got != FailNotResult {
			t.Fatalf("failure = %q, want %q (err %v, result %+v)", got, FailNotResult, err, result)
		}
	})

	t.Run("real read behind a log line still wins", func(t *testing.T) {
		answer := contentservice.ReadResult{
			Kind: "file", Operation: "document", Workspace: "p:shell:s1",
			Display: "a.md", Path: "/p/a.md", Revision: "v1:abc", Content: "hi\n",
		}
		raw, err := json.Marshal(answer)
		if err != nil {
			t.Fatal(err)
		}
		client := testRunClient(t, Host{ID: "h", Target: "h"}, stubInvoker(logLine+"\n"+string(raw)+"\n", "", 0))
		var result contentservice.ReadResult
		if err := client.RunSidecar(context.Background(), []string{"content", "read", "--json"}, &result); err != nil {
			t.Fatalf("RunSidecar: %v", err)
		}
		if !result.ValidRemoteResult() || result.Content != "hi\n" {
			t.Fatalf("decoded %+v", result)
		}
	})

	t.Run("notModified is an answer", func(t *testing.T) {
		raw, err := contentservice.EncodeReadResult(contentservice.ReadResult{Kind: "file", NotModified: true, Revision: "v1:abc"})
		if err != nil {
			t.Fatal(err)
		}
		client := testRunClient(t, Host{ID: "h", Target: "h"}, stubInvoker(string(raw), "", 0))
		var result contentservice.ReadResult
		if err := client.RunSidecar(context.Background(), []string{"content", "read", "--json"}, &result); err != nil {
			t.Fatalf("RunSidecar: %v", err)
		}
		if !result.NotModified || result.Revision != "v1:abc" {
			t.Fatalf("decoded %+v", result)
		}
	})

	t.Run("empty object is not a resolve result", func(t *testing.T) {
		client := testRunClient(t, Host{ID: "h", Target: "h"}, stubInvoker("{}\n", "", 0))
		var result contentservice.ResolveResult
		err := client.RunSidecar(context.Background(), []string{"content", "resolve", "--json"}, &result)
		if got := RunFailure(err); got != FailNotResult {
			t.Fatalf("failure = %q, want %q (err %v)", got, FailNotResult, err)
		}
	})
}
