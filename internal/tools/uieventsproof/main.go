// Command uieventsproof verifies the isolated live event journey: hello,
// initial catalog, a CLI shell rename pushed by the manifest watcher, terminal
// attachments and holders while another proof runs, and graceful shutdown.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/coder/websocket"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/uiapi"
)

func run() error {
	endpoint := flag.String("url", "", "events WebSocket URL")
	origin := flag.String("origin", "", "Origin header")
	binary := flag.String("sidecar", "", "isolated sidecar binary")
	configPath := flag.String("config", "", "isolated config")
	session := flag.String("session", "", "isolated shell session to rename")
	flag.Parse()
	if *endpoint == "" || *binary == "" || *configPath == "" || *session == "" {
		return errors.New("-url, -sidecar, -config and -session are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, *endpoint, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {*origin}}})
	if err != nil {
		return err
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(mobileproto.MaxLineBytes)
	seq := uint64(0)
	read := func() (uiapi.EventMessage, error) {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return uiapi.EventMessage{}, err
		}
		if kind != websocket.MessageText {
			return uiapi.EventMessage{}, errors.New("binary event")
		}
		var m uiapi.EventMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return m, err
		}
		if m.Seq != seq+1 {
			return m, fmt.Errorf("sequence %d after %d", m.Seq, seq)
		}
		seq = m.Seq
		return m, nil
	}
	hello, err := read()
	if err != nil {
		return err
	}
	if hello.Type != "hello" || hello.APIInstance == "" {
		return fmt.Errorf("hello: %+v", hello)
	}
	catalog, err := read()
	if err != nil {
		return err
	}
	if catalog.Type != "catalog" || catalog.Catalog == nil {
		return fmt.Errorf("catalog: %+v", catalog)
	}
	old := catalog.Catalog.Generation
	start := time.Now()
	renamed := "Events watcher proof"
	data, err := exec.CommandContext(ctx, *binary, "-config", *configPath, "shell", "rename", "--target", *session, renamed).CombinedOutput()
	if err != nil {
		return fmt.Errorf("rename: %w: %s", err, data)
	}
	found, opened, closed, holder := false, false, false, false
	for {
		m, err := read()
		if err != nil {
			return err
		}
		switch m.Type {
		case "catalog":
			if m.Catalog.Generation == old {
				return errors.New("unchanged generation delivered")
			}
			for _, section := range m.Catalog.Sections {
				for _, row := range section.Rows {
					if row.Session == *session && row.DisplayName == renamed {
						found = true
					}
				}
			}
			if found {
				if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"rename_found": true, "manifest_push_ms": time.Since(start).Milliseconds()}); err != nil {
					return err
				}
			}
			old = m.Catalog.Generation
		case "terminals":
			if m.Terminals == nil {
				return errors.New("terminals payload missing")
			}
			if len(*m.Terminals) > 0 {
				opened = true
			}
			if opened && len(*m.Terminals) == 0 {
				closed = true
			}
			for _, term := range *m.Terminals {
				if term.Holder != nil && term.Holder.Kind != "" && term.Holder.Label != "" {
					holder = true
				}
			}
		case "error":
			return fmt.Errorf("events error: %+v", m.Error)
		case "shutdown":
			if !found || !opened || !closed || !holder {
				return fmt.Errorf("missing evidence rename=%v open=%v closed=%v holder=%v", found, opened, closed, holder)
			}
			_, _, err := conn.Read(ctx)
			if websocket.CloseStatus(err) != uiapi.CloseShuttingDown {
				return fmt.Errorf("shutdown close: %v", err)
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"shutdown_found": true, "attachments_open_close": true, "holder_found": true, "last_seq": seq})
		}
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "uieventsproof:", err)
		os.Exit(1)
	}
}
