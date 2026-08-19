package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/notify"
)

// notifyEnv is a CLI environment pinned to a private state dir, with tmux
// deliberately out of the picture so the tests never touch a live server.
func notifyEnv(t *testing.T) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	var out, errOut bytes.Buffer
	return Env{Stdout: &out, Stderr: &errOut, StateDir: t.TempDir()}, &out, &errOut
}

func TestNotifyPostFallsBackToTheLog(t *testing.T) {
	env, out, errOut := notifyEnv(t)

	if code := runNotifyPost(env, []string{"--body", "detail", "--source", "session", "Tests are green"}); code != 0 {
		t.Fatalf("post = %d, stderr %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "no running Sidecar instance") {
		t.Fatalf("expected the fallback to be reported, got %q", out.String())
	}

	all, err := notify.ReadAll(notify.Path(env.StateDir))
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 notification in the log, got %d", len(all))
	}
	if all[0].Title != "Tests are green" || all[0].Body != "detail" || all[0].Source != notify.SourceSession {
		t.Fatalf("post did not record its flags: %+v", all[0])
	}
	if all[0].Origin.Zero() {
		t.Fatalf("a posted notification must carry its caller's origin")
	}
}

func TestNotifyPostValidates(t *testing.T) {
	env, _, errOut := notifyEnv(t)
	if code := runNotifyPost(env, []string{}); code != 2 {
		t.Fatalf("missing title should be a usage error, got %d", code)
	}
	if code := runNotifyPost(env, []string{"--source", "nope", "title"}); code != 2 {
		t.Fatalf("unknown source should be a usage error, got %d", code)
	}
	if !strings.Contains(errOut.String(), "unknown source") {
		t.Fatalf("expected the valid sources to be named, got %q", errOut.String())
	}
	if code := runNotifyPost(env, []string{"--expiry", "soon", "title"}); code != 2 {
		t.Fatalf("unparseable expiry should be a usage error, got %d", code)
	}
}

func TestNotifyPostExpiryFlag(t *testing.T) {
	env, _, errOut := notifyEnv(t)
	if code := runNotifyPost(env, []string{"--expiry", "never", "waiting on you"}); code != 0 {
		t.Fatalf("post = %d, stderr %q", code, errOut.String())
	}
	all, _ := notify.ReadAll(notify.Path(env.StateDir))
	if len(all) != 1 || !all[0].Sticky || all[0].ExpiresAt != nil {
		t.Fatalf("--expiry never should be sticky: %+v", all)
	}
}

func TestNotifyListReadsTheLogWithNoInstance(t *testing.T) {
	env, out, errOut := notifyEnv(t)
	if code := runNotifyList(env, nil); code != 0 || !strings.Contains(out.String(), "No notifications") {
		t.Fatalf("empty list = %d, %q", code, out.String())
	}

	if code := runNotifyPost(env, []string{"one"}); code != 0 {
		t.Fatalf("post: %d %q", code, errOut.String())
	}
	out.Reset()
	if code := runNotifyList(env, []string{"--json"}); code != 0 {
		t.Fatalf("list --json = %d, stderr %q", code, errOut.String())
	}
	var res struct {
		Unread int                   `json:"unread"`
		Items  []notify.Notification `json:"items"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("parse list json: %v (%q)", err, out.String())
	}
	if res.Unread != 1 || len(res.Items) != 1 || res.Items[0].Title != "one" {
		t.Fatalf("unexpected list result: %+v", res)
	}
	if code := runNotifyList(env, []string{"--nope"}); code != 2 {
		t.Fatalf("unknown option should be a usage error, got %d", code)
	}
}

func TestNotifyDismissIsOriginChecked(t *testing.T) {
	env, out, errOut := notifyEnv(t)

	// Something posted by somebody else, written straight into the log.
	store, err := notify.Open(env.StateDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	theirs, err := store.Post(notify.Notification{
		ID:     "ntf-theirs",
		Source: notify.SourceAgent,
		Title:  "another agent's",
		Origin: notify.Origin{TmuxSession: "sidecar-sh-someone-else"},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	_ = store.Close()

	if code := runNotifyDismiss(env, []string{theirs.ID}); code != 4 {
		t.Fatalf("dismissing another caller's notification = %d, want 4", code)
	}
	if !strings.Contains(errOut.String(), "only dismiss its own") {
		t.Fatalf("expected a refusal that says why, got %q", errOut.String())
	}
	if code := runNotifyDismiss(env, []string{"ntf-missing"}); code != 3 {
		t.Fatalf("unknown id = %d, want 3", code)
	}

	// And one this caller posted.
	errOut.Reset()
	if code := runNotifyPost(env, []string{"mine"}); code != 0 {
		t.Fatalf("post: %d %q", code, errOut.String())
	}
	all, _ := notify.ReadAll(notify.Path(env.StateDir))
	mineID := ""
	for _, n := range all {
		if n.Title == "mine" {
			mineID = n.ID
		}
	}
	if mineID == "" {
		t.Fatalf("posted notification not found in %+v", all)
	}
	out.Reset()
	if code := runNotifyDismiss(env, []string{mineID}); code != 0 {
		t.Fatalf("dismissing my own = %d, stderr %q", code, errOut.String())
	}

	after, _ := notify.ReadAll(notify.Path(env.StateDir))
	for _, n := range after {
		if n.ID == mineID && !n.Dismissed() {
			t.Fatalf("dismiss did not stick: %+v", n)
		}
		if n.ID == theirs.ID && n.Dismissed() {
			t.Fatalf("refused dismissal must not have been applied")
		}
	}
	if notify.UnreadCount(after) != 1 {
		t.Fatalf("only the other caller's notification stays unread, got %d", notify.UnreadCount(after))
	}
}

func TestNotifyRootDispatches(t *testing.T) {
	env, out, errOut := notifyEnv(t)
	if code := runNotifyRoot(env, nil); code != 0 || !strings.Contains(out.String(), "sidecar notify") {
		t.Fatalf("bare notify should print help, got %d %q", code, out.String())
	}
	if code := runNotifyRoot(env, []string{"nonsense"}); code != 2 {
		t.Fatalf("unknown subcommand = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "unknown notify command") {
		t.Fatalf("stderr = %q", errOut.String())
	}
	out.Reset()
	if code := runNotifyRoot(env, []string{"list"}); code != 0 {
		t.Fatalf("notify list via root = %d", code)
	}
}
