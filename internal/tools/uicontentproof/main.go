// Command uicontentproof proves project content, conditional layouts and live
// invalidation against the isolated server started by ui-api-proof.sh.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/uiapi"
)

func run() error {
	socket := flag.String("socket", "", "isolated API socket")
	root := flag.String("root", "", "isolated project")
	flag.Parse()
	if *socket == "" || *root == "" {
		return errors.New("socket and root are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", *socket)
	}}}
	request := func(method, path, body, match string) ([]byte, string, int, error) {
		req, err := http.NewRequestWithContext(ctx, method, "http://sidecar"+path, bytes.NewBufferString(body))
		if err != nil {
			return nil, "", 0, err
		}
		if match != "" {
			req.Header.Set("If-Match", match)
		}
		response, err := client.Do(req)
		if err != nil {
			return nil, "", 0, err
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(response.Body)
		return data, response.Header.Get("ETag"), response.StatusCode, err
	}
	prefix := "/api/v0/projects/proof/"
	// Ordinary project secrets remain user-viewable; Git administrative data
	// is refused even for trusted Local callers. Never read a real credential.
	if err := os.WriteFile(filepath.Join(*root, ".env"), []byte("PROOF=ordinary-content\n"), 0600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(filepath.Join(*root, ".env")) }()
	for _, target := range []string{".git/config", ".git/HEAD"} {
		data, _, code, err := request("GET", prefix+"content?kind=file&target="+url.QueryEscape(target), "", "")
		if err != nil {
			return err
		}
		if code != 403 {
			return fmt.Errorf("git internals readable: %d %s", code, data)
		}
	}
	data, _, code, err := request("GET", prefix+"content?kind=file&target=.env", "", "")
	if err != nil {
		return err
	}
	if code != 200 || !bytes.Contains(data, []byte("ordinary-content")) {
		return fmt.Errorf("ordinary .env refused: %d %s", code, data)
	}
	for _, query := range []string{"kind=file&target=README.md", "kind=diff"} {
		data, _, code, err := request("GET", prefix+"content?"+query, "", "")
		if err != nil {
			return err
		}
		if code != 200 {
			return fmt.Errorf("content: %d %s", code, data)
		}
		var doc contentservice.ReadResult
		if err = json.Unmarshal(data, &doc); err != nil {
			return err
		}
		if !doc.ValidRemoteResult() {
			return fmt.Errorf("invalid DTO %s", data)
		}
	}

	// Oversized patches must be truncated before response encoding for every
	// operation, while retained rows continue to form a valid content DTO.
	largePath := filepath.Join(*root, "large-proof.txt")
	defer func() { _ = os.Remove(largePath) }()
	git := func(args ...string) error {
		all := append([]string{"-C", *root, "-c", "user.name=Proof", "-c", "user.email=proof@example.invalid"}, args...)
		out, err := exec.CommandContext(ctx, "git", all...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("git proof: %s: %w", out, err)
		}
		return nil
	}
	// The filename must cross Git quoting, HTTP JSON and a selected source
	// read unchanged. Clients never parse or reconstruct Git header names.
	quotedName := `quoted "><img src=x>.md`
	quotedPath := filepath.Join(*root, quotedName)
	defer func() { _ = os.Remove(quotedPath) }()
	if err = os.WriteFile(quotedPath, []byte("quoted base\n"), 0600); err != nil {
		return err
	}
	if err = git("add", "--", quotedName); err != nil {
		return err
	}
	if err = git("commit", "-qm", "Quoted path proof baseline"); err != nil {
		return err
	}
	if err = os.WriteFile(quotedPath, []byte("quoted changed\n"), 0600); err != nil {
		return err
	}
	data, _, code, err = request("GET", prefix+"content?kind=diff&operation=working-tree&target=wt", "", "")
	if err != nil {
		return err
	}
	var quotedDoc contentservice.ReadResult
	if code != 200 {
		return fmt.Errorf("quoted diff: %d %s", code, data)
	}
	if err = json.Unmarshal(data, &quotedDoc); err != nil {
		return err
	}
	if quotedDoc.Diff == nil || quotedDoc.Diff.Snapshot == nil {
		return errors.New("quoted diff has no snapshot")
	}
	var foundQuoted bool
	for _, row := range quotedDoc.Diff.Snapshot.Files {
		if row.Path != quotedName {
			continue
		}
		foundQuoted = true
		query := url.Values{"kind": {"file"}, "target": {row.Path}}
		data, _, code, err = request("GET", prefix+"content?"+query.Encode(), "", "")
		if err != nil {
			return err
		}
		var source contentservice.ReadResult
		if code != 200 {
			return fmt.Errorf("quoted source: %d %s", code, data)
		}
		if err = json.Unmarshal(data, &source); err != nil {
			return err
		}
		if source.Content != "quoted changed\n" {
			return errors.New("quoted source did not round-trip")
		}
	}
	if !foundQuoted {
		return fmt.Errorf("quoted repository path absent from HTTP rows: %+v", quotedDoc.Diff.Snapshot.Files)
	}
	if err = os.WriteFile(largePath, []byte("base\n"), 0600); err != nil {
		return err
	}
	if err = git("add", "large-proof.txt"); err != nil {
		return err
	}
	if err = git("commit", "-qm", "Large diff proof baseline"); err != nil {
		return err
	}
	if err = os.WriteFile(largePath, []byte(strings.Repeat("changed line\n", 180000)), 0600); err != nil {
		return err
	}
	checkLarge := func(operation, target string) error {
		query := url.Values{"kind": {"diff"}, "operation": {operation}, "target": {target}, "path": {"large-proof.txt"}}
		data, _, code, err := request("GET", prefix+"content?"+query.Encode(), "", "")
		if err != nil {
			return err
		}
		if code != 200 {
			return fmt.Errorf("large %s: %d %s", operation, code, data)
		}
		var doc contentservice.ReadResult
		if err = json.Unmarshal(data, &doc); err != nil {
			return err
		}
		if !doc.ValidRemoteResult() || !doc.Truncated || doc.Diff == nil || !doc.Diff.Truncated {
			return fmt.Errorf("large %s silently truncated or invalid", operation)
		}
		if len(data) > contentservice.MaxEncodedBytes {
			return fmt.Errorf("large %s exceeded transport cap: %d", operation, len(data))
		}
		return nil
	}
	for _, operation := range []string{contentservice.OpWorkingTreeFile, contentservice.OpFullFile} {
		if err = checkLarge(operation, "wt"); err != nil {
			return err
		}
	}
	if err = git("add", "large-proof.txt"); err != nil {
		return err
	}
	if err = git("commit", "-qm", "Oversized diff proof"); err != nil {
		return err
	}
	for _, item := range [][2]string{{contentservice.OpRange, "HEAD~1..HEAD"}, {contentservice.OpCommitFile, "HEAD"}, {contentservice.OpFullFile, "HEAD"}} {
		if err = checkLarge(item[0], item[1]); err != nil {
			return err
		}
	}
	data, _, code, err = request("GET", prefix+"tree", "", "")
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("tree %d %s", code, data)
	}
	outside := filepath.Join(filepath.Dir(*root), "outside.md")
	if err = os.WriteFile(outside, []byte("OUTSIDE"), 0600); err != nil {
		return err
	}
	if err = os.Symlink(outside, filepath.Join(*root, "escape.md")); err != nil {
		return err
	}
	defer func() { _ = os.Remove(filepath.Join(*root, "escape.md")) }()
	for _, target := range []string{"../outside.md", "escape.md", outside} {
		data, _, code, err = request("GET", prefix+"content?kind=file&target="+url.QueryEscape(target), "", "")
		if err != nil {
			return err
		}
		if code != 403 || bytes.Contains(data, []byte("OUTSIDE")) {
			return fmt.Errorf("escape was not refused: %d %s", code, data)
		}
	}
	_, etag, code, err := request("GET", prefix+"layout", "", "")
	if err != nil {
		return err
	}
	if code != 200 || etag == "" {
		return fmt.Errorf("layout GET %d %q", code, etag)
	}
	layout := `{"layout":{"split":{"axis":"cols","ratio":50,"a":{"kind":"terminal"},"b":{"kind":"doc","tabs":[{"path":"README.md","mode":"rendered"}]}}}}`
	_, _, code, err = request("PUT", prefix+"layout", layout, etag)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("layout PUT %d", code)
	}
	_, _, code, err = request("PUT", prefix+"layout", layout, etag)
	if err != nil {
		return err
	}
	if code != 412 {
		return fmt.Errorf("stale layout accepted: %d", code)
	}
	ref := uiapi.ContentRef{Project: "proof", Kind: "file", Target: "README.md"}
	raw, _ := json.Marshal(ref)
	conn, _, err := websocket.Dial(ctx, "ws://sidecar/api/v0/events?"+url.Values{"content": {string(raw)}}.Encode(), &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return err
	}
	defer func() { _ = conn.CloseNow() }()
	seq := uint64(0)
	read := func() (uiapi.EventMessage, error) {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return uiapi.EventMessage{}, err
		}
		var event uiapi.EventMessage
		err = json.Unmarshal(data, &event)
		if event.Seq != seq+1 {
			return event, fmt.Errorf("event sequence %d after %d", event.Seq, seq)
		}
		seq = event.Seq
		return event, err
	}
	for _, kind := range []string{"hello", "catalog", "terminals"} {
		event, err := read()
		if err != nil {
			return err
		}
		if event.Type != kind {
			return fmt.Errorf("baseline %s: %+v", kind, event)
		}
	}
	if err = os.WriteFile(filepath.Join(*root, "README.md"), []byte("# Live content\n"), 0600); err != nil {
		return err
	}
	for {
		event, err := read()
		if err != nil {
			return err
		}
		if event.Type == "content" {
			if len(event.Content.Resources) != 1 || event.Content.Resources[0] != ref {
				return fmt.Errorf("wrong invalidation: %+v", event)
			}
			break
		}
	}
	data, _, code, err = request("GET", prefix+"content?kind=file&target=README.md", "", "")
	if err != nil {
		return err
	}
	if code != 200 || !strings.Contains(string(data), "Live content") {
		return fmt.Errorf("refetch %d %s", code, data)
	}
	fmt.Println("content proof: DTOs, bounded oversized diff prefixes, root containment, Git internals refusal, .env read, tree, ETags, stale-write refusal, livewatch event and refetch PASS")
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
