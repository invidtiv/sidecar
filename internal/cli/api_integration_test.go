package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/uiapi"
)

// apiStateTree isolates state and config the way setupIsolatedCLI does, in a
// directory directly under the temp root so the API's Unix socket paths stay
// inside the sun_path bound (t.TempDir nests too deep on macOS). Removal is
// best-effort: a shell started in the private tmux inherits XDG_STATE_HOME,
// and its login hooks can still write there after the session is killed.
func apiStateTree(t *testing.T, projectRoot string) string {
	t.Helper()
	stateHome, err := os.MkdirTemp("", "scapi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateHome) })
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	configPath := filepath.Join(stateHome, "config", "config.json")
	config.SetConfigPath(configPath)
	t.Cleanup(func() { config.SetConfigPath(defaultTestConfigPath) })
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"projects":{"list":[{"name":"demo","path":` + quoteJSON(t, projectRoot) + `}]}}`
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(stateHome, "sidecar")
	writeProjectMeta(t, stateDir, "demo", projectRoot)
	return stateDir
}

type apiTerminal struct {
	t      *testing.T
	conn   *websocket.Conn
	number int
}

func (a *apiTerminal) send(request mobileproto.Request) {
	a.t.Helper()
	a.number++
	request.Version = mobileproto.Version
	request.RequestID = "it-" + strconv.Itoa(a.number)
	data, err := json.Marshal(request)
	if err != nil {
		a.t.Fatal(err)
	}
	if err := a.conn.Write(context.Background(), websocket.MessageText, data); err != nil {
		a.t.Fatalf("write %s: %v", request.Type, err)
	}
}

func (a *apiTerminal) next(timeout time.Duration) mobileproto.Response {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	kind, data, err := a.conn.Read(ctx)
	if err != nil {
		a.t.Fatalf("read: %v", err)
	}
	if kind != websocket.MessageText || bytes.HasSuffix(data, []byte("\n")) {
		a.t.Fatalf("message kind %v with trailing newline %v", kind, bytes.HasSuffix(data, []byte("\n")))
	}
	var response mobileproto.Response
	if err := json.Unmarshal(data, &response); err != nil {
		a.t.Fatalf("decode %q: %v", data, err)
	}
	return response
}

// call sends request and returns its correlated response, collecting any
// asynchronous frames that arrive first.
func (a *apiTerminal) call(request mobileproto.Request, frames *[]mobileproto.Response) mobileproto.Response {
	a.t.Helper()
	a.send(request)
	for {
		response := a.next(15 * time.Second)
		if response.RequestID == "" {
			if frames != nil {
				*frames = append(*frames, response)
			}
			continue
		}
		return response
	}
}

func (a *apiTerminal) frame(match func(mobileproto.Response) bool) mobileproto.Response {
	a.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		response := a.next(time.Until(deadline))
		if response.Type == mobileproto.ResponseFrame && (match == nil || match(response)) {
			return response
		}
	}
	a.t.Fatal("no matching frame")
	return mobileproto.Response{}
}

func tmuxOwnerOption(t *testing.T, session string) string {
	t.Helper()
	out, _ := exec.Command("tmux", "show-options", "-v", "-t", session, "@sidecar-owner").Output()
	return strings.TrimSpace(string(out))
}

// TestAPITerminalRoundTripAgainstLocalOwner drives the whole steel thread in
// process: a managed shell on this package's private tmux server, the real
// mobile owner service behind the API backend, and the WebSocket terminal
// route over the Local socket.
func TestAPITerminalRoundTripAgainstLocalOwner(t *testing.T) {
	testenv.RequireTmux(t)
	workDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		workDir = resolved
	}
	stateDir := apiStateTree(t, workDir)
	t.Chdir(workDir)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"create", "shell", "--name", "api proof", "--json", "--wait", "0"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("create shell: %d %s %s", code, errOut.String(), out.String())
	}
	var created createShellResult
	if err := json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	session := created.Shell.Session
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })

	env := defaultEnv(io.Discard, io.Discard)
	if env.StateDir != stateDir {
		t.Fatalf("state dir %s, want %s", env.StateDir, stateDir)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, err := newMobileBackend(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	server, err := uiapi.Start(uiapi.Options{StateDir: env.StateDir, Port: 0, Backend: backend, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = server.Shutdown(shutdownCtx)
	})
	client := uiapi.NewLocalClientForSocket(server.Endpoint())

	// GET /api/v0/sessions is `sidecar mobile sessions --json` for the same query.
	var viaHTTP json.RawMessage
	if err := client.Do(ctx, http.MethodGet, "/api/v0/sessions?sort=name", nil, &viaHTTP); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if handled, code := Run([]string{"mobile", "sessions", "--json", "--sort", "name"}, &out, &errOut); !handled || code != 0 {
		t.Fatalf("mobile sessions: %d %s", code, errOut.String())
	}
	assertSameCatalogShape(t, viaHTTP, out.Bytes(), session)

	conn, _, err := websocket.Dial(ctx, "ws://sidecar.local/api/v0/terminal", &websocket.DialOptions{HTTPClient: client.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(mobileproto.MaxLineBytes)
	defer func() { _ = conn.CloseNow() }()
	term := &apiTerminal{t: t, conn: conn}

	hello := term.call(mobileproto.Request{Type: mobileproto.RequestHello}, nil)
	if hello.Type != mobileproto.ResponseHello || hello.Capabilities == nil {
		t.Fatalf("hello = %+v", hello)
	}
	resolved := term.call(mobileproto.Request{Type: mobileproto.RequestResolve, Target: session}, nil)
	if resolved.Type != mobileproto.ResponseResolved || resolved.Target == nil {
		t.Fatalf("resolve = %+v", resolved)
	}
	opened := term.call(mobileproto.Request{Type: mobileproto.RequestOpen, TargetHandle: resolved.Target.Handle, AttachmentID: "api-it"}, nil)
	if opened.Type != mobileproto.ResponseOpened {
		t.Fatalf("open = %+v", opened)
	}
	first := term.frame(nil)
	attachment := opened.AttachmentHandle
	reset, output := first.ResetGeneration, first.OutputSequence

	var frames []mobileproto.Response
	control := term.call(mobileproto.Request{Type: mobileproto.RequestControl, AttachmentHandle: attachment, OperationSequence: 1,
		LastResetGeneration: reset, LastOutputSequence: output, Columns: first.Geometry.Columns, Rows: first.Geometry.Rows}, &frames)
	if control.Type == mobileproto.ResponseError && control.Error != nil && control.Error.Code == mobileproto.ErrorUnsupportedMode {
		t.Skipf("this tmux cannot report the input modes control needs: %s", control.Error.Message)
	}
	if control.Type != mobileproto.ResponseControl || !control.Control {
		t.Fatalf("control = %+v", control)
	}
	var status uiapi.Status
	if err := client.Do(ctx, http.MethodGet, "/api/v0/status", nil, &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Terminals) != 1 || !status.Terminals[0].Control || status.Terminals[0].Session != session {
		t.Fatalf("status while in control = %+v", status.Terminals)
	}
	for _, f := range frames {
		if f.Type == mobileproto.ResponseFrame && f.ResetGeneration == control.ResetGeneration {
			reset, output = f.ResetGeneration, f.OutputSequence
		}
	}
	if control.ResetGeneration != reset {
		latest := term.frame(func(r mobileproto.Response) bool { return r.ResetGeneration == control.ResetGeneration })
		reset, output = latest.ResetGeneration, latest.OutputSequence
	}

	input := term.call(mobileproto.Request{Type: mobileproto.RequestInput, AttachmentHandle: attachment, OperationSequence: 2,
		LastResetGeneration: reset, LastOutputSequence: output, DataBase64: base64.StdEncoding.EncodeToString([]byte("echo UIAPI_ECHO_$((7000+3))F3A\r"))}, &frames)
	if input.Type != mobileproto.ResponseAccepted {
		t.Fatalf("input = %+v", input)
	}
	echoed := term.frame(func(r mobileproto.Response) bool {
		vt, _ := base64.StdEncoding.DecodeString(r.RenderVTBase64)
		return bytes.Contains(vt, []byte("UIAPI_ECHO_7003F3A"))
	})
	released := term.call(mobileproto.Request{Type: mobileproto.RequestRelease, AttachmentHandle: attachment, OperationSequence: 3,
		LastResetGeneration: echoed.ResetGeneration, LastOutputSequence: echoed.OutputSequence}, nil)
	if released.Type != mobileproto.ResponseReleased {
		t.Fatalf("release = %+v", released)
	}
	if owner := tmuxOwnerOption(t, session); owner != "" {
		t.Fatalf("@sidecar-owner after release = %q", owner)
	}
	closed := term.call(mobileproto.Request{Type: mobileproto.RequestClose, AttachmentHandle: attachment}, nil)
	if closed.Type != mobileproto.ResponseClosed {
		t.Fatalf("close = %+v", closed)
	}
	if err := conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatal(err)
	}

	// A second stream takes control and then just disconnects: socket close is
	// EOF, and EOF releases the lease exactly as stdin EOF does.
	conn2, _, err := websocket.Dial(ctx, "ws://sidecar.local/api/v0/terminal", &websocket.DialOptions{HTTPClient: client.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	conn2.SetReadLimit(mobileproto.MaxLineBytes)
	term2 := &apiTerminal{t: t, conn: conn2}
	term2.call(mobileproto.Request{Type: mobileproto.RequestHello}, nil)
	resolved2 := term2.call(mobileproto.Request{Type: mobileproto.RequestResolve, Target: session}, nil)
	opened2 := term2.call(mobileproto.Request{Type: mobileproto.RequestOpen, TargetHandle: resolved2.Target.Handle, AttachmentID: "api-it-2"}, nil)
	frame2 := term2.frame(nil)
	control2 := term2.call(mobileproto.Request{Type: mobileproto.RequestControl, AttachmentHandle: opened2.AttachmentHandle, OperationSequence: 1,
		LastResetGeneration: frame2.ResetGeneration, LastOutputSequence: frame2.OutputSequence, Columns: frame2.Geometry.Columns, Rows: frame2.Geometry.Rows}, nil)
	if control2.Type != mobileproto.ResponseControl {
		t.Fatalf("second control = %+v", control2)
	}
	if tmuxOwnerOption(t, session) == "" {
		t.Fatal("control did not set @sidecar-owner")
	}
	_ = conn2.CloseNow()
	deadline := time.Now().Add(10 * time.Second)
	for tmuxOwnerOption(t, session) != "" {
		if time.Now().After(deadline) {
			t.Fatalf("socket close left @sidecar-owner = %q", tmuxOwnerOption(t, session))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// stripObservedAt removes every observation timestamp, at any depth.
func stripObservedAt(value any) {
	switch v := value.(type) {
	case map[string]any:
		delete(v, "observed_at")
		for _, child := range v {
			stripObservedAt(child)
		}
	case []any:
		for _, child := range v {
			stripObservedAt(child)
		}
	}
}

// assertSameCatalogShape compares the HTTP and CLI catalog documents with the
// observation-time fields removed.
func assertSameCatalogShape(t *testing.T, viaHTTP, viaCLI []byte, session string) {
	t.Helper()
	normalize := func(data []byte) map[string]any {
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("decode catalog %q: %v", data, err)
		}
		delete(doc, "generation")
		stripObservedAt(doc)
		return doc
	}
	left, right := normalize(viaHTTP), normalize(viaCLI)
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	if !bytes.Equal(a, b) {
		t.Fatalf("HTTP catalog differs from CLI catalog:\nhttp %s\ncli  %s", a, b)
	}
	if !bytes.Contains(viaHTTP, []byte(session)) {
		t.Fatalf("catalog does not list %s: %s", session, viaHTTP)
	}
}
