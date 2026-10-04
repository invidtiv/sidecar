package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/uiapi"
)

func TestAPISpecCLIUsesGeneratedContractWithoutServer(t *testing.T) {
	apiStateTree(t, t.TempDir())
	want, err := uiapi.Spec()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"api", "spec"}, {"api", "spec", "--json"}} {
		code, out, stderr := runAPICLI(t, args...)
		if code != 0 || out != string(want) {
			t.Fatalf("%v: %d %s", args, code, stderr)
		}
	}
	if code, _, _ := runAPICLI(t, "api", "spec", "--unknown"); code != 2 {
		t.Fatal(code)
	}
}

func TestAPIServeFixturesCLIWithoutTmux(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "u1b-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	apiStateTree(t, root)
	// No tmux, git or external process can accidentally satisfy this proof.
	t.Setenv("PATH", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := make(chan []byte, 1)
	output := &fixtureEndpointWriter{ready: started}
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runAPIServe(Env{Ctx: ctx, StateDir: root, Stdout: output, Stderr: &stderr}, []string{"--port", "0", "--json", "--fixtures", filepath.Join("..", "..", "testdata", "ui-api", "v0")})
	}()
	select {
	case data := <-started:
		var endpoint uiapi.Endpoint
		if err := json.Unmarshal(data, &endpoint); err != nil {
			t.Fatal(err)
		}
		status, err := uiapi.NewLocalClientForSocket(endpoint).Status(ctx)
		if err != nil || status.ServerVersion != "fixture" {
			t.Fatalf("status %+v %v", status, err)
		}
	case code := <-done:
		t.Fatalf("serve exited %d: %s", code, stderr.String())
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("serve exited %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(uiapi.Dir(root), "endpoint.json")); !os.IsNotExist(err) {
		t.Fatalf("endpoint left: %v", err)
	}
}

type fixtureEndpointWriter struct{ ready chan []byte }

func (w *fixtureEndpointWriter) Write(data []byte) (int, error) {
	w.ready <- append([]byte(nil), data...)
	return len(data), nil
}
