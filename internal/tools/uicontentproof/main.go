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
	data, _, code, err := request("GET", prefix+"tree", "", "")
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
	fmt.Println("content proof: DTOs, root containment, tree, ETags, stale-write refusal, livewatch event and refetch PASS")
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
