// uiviewerproof is a bounded fixture UI: the real CLI, bus and HTTP/events
// server execute everything, while this client adopts and acknowledges trees.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/uiapi"
	"github.com/marcus/sidecar/internal/uirequest"
)

type proof struct {
	ctx                                                             context.Context
	base, socket, binary, config, project, session, root, token, id string
	workspace, projectRoot                                          string
	conn                                                            *websocket.Conn
	http                                                            *http.Client
}

func main() {
	p := &proof{}
	flag.StringVar(&p.base, "url", "", "Browser base URL")
	flag.StringVar(&p.socket, "socket", "", "Private Local socket")
	flag.StringVar(&p.binary, "sidecar", "", "Temporary sidecar binary")
	flag.StringVar(&p.config, "config", "", "Isolated configuration")
	flag.StringVar(&p.project, "project", "proof", "Explicit configured project")
	flag.StringVar(&p.session, "session", "", "Private managed session")
	flag.StringVar(&p.root, "root", "", "Private project root")
	flag.StringVar(&p.projectRoot, "project-root", "", "Owning configured project root (defaults to root)")
	flag.StringVar(&p.workspace, "workspace", "", "Explicit durable worktree workspace ID")
	flag.Parse()
	if p.projectRoot == "" {
		p.projectRoot = p.root
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	p.ctx = ctx
	p.http = &http.Client{Timeout: 5 * time.Second}
	if err := p.run(); err != nil {
		fmt.Fprintln(os.Stderr, "viewer proof:", err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"viewer_relay": true, "cli_open": true, "layout_get_apply_move": true, "focused_refusal": true, "late_ack_refusal": true, "path_boundary": true, "ui_control_scope": true, "workspace": p.workspace})
}
func (p *proof) call(path string, body any, local bool) ([]byte, int, error) {
	data, _ := json.Marshal(body)
	request, err := http.NewRequestWithContext(p.ctx, "POST", p.base+path, bytes.NewReader(data))
	if err != nil {
		return nil, 0, err
	}
	client := p.http
	if local {
		request.URL.Scheme = "http"
		request.URL.Host = "sidecar"
		client = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", p.socket)
		}}}
	} else {
		request.Header.Set("Authorization", "Bearer "+p.token)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Sidecar-Request", "1")
	if path == p.layoutPath() {
		request.Method = "PUT"
		_, etag, err := p.layout()
		if err != nil {
			return nil, 0, err
		}
		request.Header.Set("If-Match", etag)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	out, err := io.ReadAll(response.Body)
	return out, response.StatusCode, err
}
func (p *proof) layout() (uiapi.LayoutDocument, string, error) {
	return p.readLayout(p.layoutPath())
}
func (p *proof) layoutPath() string {
	path := "/api/v0/projects/" + url.PathEscape(p.project) + "/layout"
	if p.workspace != "" {
		path += "?workspace=" + url.QueryEscape(p.workspace)
	}
	return path
}
func (p *proof) readLayout(path string) (uiapi.LayoutDocument, string, error) {
	request, _ := http.NewRequestWithContext(p.ctx, "GET", p.base+path, nil)
	request.Header.Set("Authorization", "Bearer "+p.token)
	response, err := p.http.Do(request)
	if err != nil {
		return uiapi.LayoutDocument{}, "", err
	}
	defer func() { _ = response.Body.Close() }()
	var doc uiapi.LayoutDocument
	err = json.NewDecoder(response.Body).Decode(&doc)
	if response.StatusCode != 200 {
		return doc, "", fmt.Errorf("layout GET %d", response.StatusCode)
	}
	return doc, response.Header.Get("ETag"), err
}
func (p *proof) presence(focused bool) error {
	_, code, err := p.call("/api/v0/viewers/presence", uiapi.ViewerPresenceRequest{ViewerID: p.id, Focused: focused, Visible: true, Project: p.project, Workspace: p.workspace, Session: p.session, Viewport: uiapi.Viewport{Width: 1200, Height: 800}, FocusedPane: 1}, false)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("presence %d", code)
	}
	return nil
}
func (p *proof) next(kind string) (uiapi.EventMessage, error) {
	for {
		_, data, err := p.conn.Read(p.ctx)
		if err != nil {
			return uiapi.EventMessage{}, err
		}
		var event uiapi.EventMessage
		if err = json.Unmarshal(data, &event); err != nil {
			return event, err
		}
		if event.Type == kind {
			return event, nil
		}
	}
}

type commandResult struct {
	data []byte
	code int
	err  error
}

func (p *proof) cli(args ...string) <-chan commandResult {
	done := make(chan commandResult, 1)
	go func() {
		args = append([]string{"-config", p.config}, args...)
		cmd := exec.CommandContext(p.ctx, p.binary, args...)
		cmd.Dir = p.root
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if e, ok := err.(*exec.ExitError); ok {
				exit = e
			}
			if exit != nil {
				code = exit.ExitCode()
			} else {
				code = -1
			}
		}
		done <- commandResult{out, code, err}
	}()
	return done
}
func (p *proof) command(args ...string) ([]byte, error) {
	done := p.cli(args...)
	event, err := p.next("ui_request")
	if err != nil {
		return nil, err
	}
	if event.UIRequest == nil {
		return nil, fmt.Errorf("missing typed request")
	}
	if event.UIRequest.Project != p.project || event.UIRequest.Workspace != p.workspace || event.UIRequest.Request.Origin.WorkDir != p.root {
		return nil, fmt.Errorf("proposal did not address the selected project/workspace: %+v", event.UIRequest)
	}
	_, code, err := p.call("/api/v0/viewers/ack", uiapi.ViewerAckRequest{ViewerID: p.id, ID: event.UIRequest.ID, Status: uirequest.StatusOpened}, false)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("ack %d", code)
	}
	result := <-done
	if result.code != 0 {
		return nil, fmt.Errorf("CLI exit %d: %s", result.code, result.data)
	}
	return result.data, nil
}
func (p *proof) flags() []string {
	return []string{"--project", p.projectRoot, "--shell", p.session, "--wait", "4s", "--json"}
}
func (p *proof) run() error {
	data, code, err := p.call("/api/v0/origins", uiapi.OriginRequest{Origin: "https://viewer-proof.example", Scopes: []string{uiapi.ScopeUIControl, uiapi.ScopeContentRead}}, true)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("pair %d", code)
	}
	var registration uiapi.OriginRegistration
	if err = json.Unmarshal(data, &registration); err != nil {
		return err
	}
	p.token = registration.Token
	var mainETag string
	if p.workspace != "" {
		_, mainETag, err = p.readLayout("/api/v0/projects/" + url.PathEscape(p.project) + "/layout")
		if err != nil {
			return err
		}
	}
	conn, _, err := websocket.Dial(p.ctx, strings.Replace(p.base, "http:", "ws:", 1)+"/api/v0/events?viewer=uiRequestRelayV1", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + p.token}}})
	if err != nil {
		return err
	}
	p.conn = conn
	defer func() { _ = conn.CloseNow() }()
	if _, err = p.next("hello"); err != nil {
		return err
	}
	event, err := p.next("viewer")
	if err != nil {
		return err
	}
	p.id = event.Viewer.ID
	doc := uiapi.LayoutDocument{Layout: &state.PaneLayoutJSON{Kind: "terminal", Session: p.session}}
	_, code, err = p.call(p.layoutPath(), doc, false)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("layout PUT %d", code)
	}
	if err = p.presence(true); err != nil {
		return err
	}
	if _, err = p.command(append([]string{"open", "README.md"}, p.flags()...)...); err != nil {
		return err
	}
	saved, _, err := p.layout()
	if err != nil {
		return err
	}
	if saved.Layout.Split == nil || saved.Layout.Split.B.Tabs[0].Path != "README.md" {
		return fmt.Errorf("CLI open did not become a browser document pane")
	}
	report, err := p.command(append([]string{"layout", "get"}, p.flags()...)...)
	if err != nil {
		return err
	}
	if !bytes.Contains(report, []byte("viewport_css_pixels")) || bytes.Contains(report, []byte(`"viewport":`)) {
		return fmt.Errorf("layout get not browser/CSS pixels: %s", report)
	}
	if _, err = p.command(append([]string{"layout", "move", "2.1", "--to", "left"}, p.flags()...)...); err != nil {
		return err
	}
	if _, err = p.command(append([]string{"layout", "apply", "--spec", `{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"file","targets":["README.md"]}]}]}`}, p.flags()...)...); err != nil {
		return err
	}
	// The read itself uses ui:control only for the relay and content:read for content.
	if _, err = p.command(append([]string{"open", "--diff"}, p.flags()...)...); err != nil {
		return err
	}
	if err = p.presence(false); err != nil {
		return err
	}
	result := <-p.cli(append([]string{"open", "README.md"}, p.flags()...)...)
	if result.code != 4 {
		return fmt.Errorf("unfocused request exit %d: %s", result.code, result.data)
	}
	if err = p.presence(true); err != nil {
		return err
	}
	_, before, err := p.layout()
	if err != nil {
		return err
	}
	done := p.cli(append([]string{"open", "README.md"}, p.flags()...)...)
	event, err = p.next("ui_request")
	if err != nil {
		return err
	}
	if err = p.presence(false); err != nil {
		return err
	}
	_, code, err = p.call("/api/v0/viewers/ack", uiapi.ViewerAckRequest{ViewerID: p.id, ID: event.UIRequest.ID, Status: uirequest.StatusOpened}, false)
	if err != nil {
		return err
	}
	if code != 409 {
		return fmt.Errorf("late ack %d", code)
	}
	result = <-done
	if result.code != 4 {
		return fmt.Errorf("blurred in-flight CLI exit %d", result.code)
	}
	_, after, err := p.layout()
	if err != nil {
		return err
	}
	if before != after {
		return fmt.Errorf("blurred acknowledgement changed layout")
	}
	if err = p.presence(true); err != nil {
		return err
	}
	outside := p.root + "/../outside.md"
	if err = os.WriteFile(outside, []byte("outside the content root"), 0600); err != nil {
		return err
	}
	result = <-p.cli(append([]string{"open", outside}, p.flags()...)...)
	if result.code != 2 {
		return fmt.Errorf("outside-root CLI exit %d: %s", result.code, result.data)
	}
	if p.workspace != "" {
		_, after, err := p.readLayout("/api/v0/projects/" + url.PathEscape(p.project) + "/layout")
		if err != nil {
			return err
		}
		if after != mainETag {
			return fmt.Errorf("worktree relay changed the main checkout's layout")
		}
	}
	return nil
}
