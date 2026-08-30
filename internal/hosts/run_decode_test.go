package hosts

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// shellLike and planLike are the shapes the Sessions browser actually decodes
// into, reduced to the fields that matter. Nothing here implements
// ResultValidator, so these cases exercise the floor the decoder enforces for
// every result type.
type shellLike struct {
	Shell struct {
		Session string `json:"session"`
	} `json:"shell"`
}

type planLike struct {
	Branch string `json:"branch"`
	Path   string `json:"path"`
}

// TestRunSidecarRejectsAJSONBannerLine is the reviewer's reproduction.
//
// jsonStarts found candidates, the first one decoded, and json.Unmarshal
// returned nil because Go ignores unknown fields and tolerates missing ones. So
// `{"level":"info","msg":"loading nvm"}` — a login profile or a wrapper logging
// structured lines, which is the exact row state the plan names as required —
// WAS the result as far as this seam was concerned, and the caller got a
// zero-valued struct with no error. Downstream that rendered a blank worktree
// confirmation for an operation that then really ran on the user's machine.
func TestRunSidecarRejectsAJSONBannerLine(t *testing.T) {
	t.Run("the result behind a JSON log line still wins", func(t *testing.T) {
		stdout := `{"level":"info","msg":"loading nvm"}` + "\n" + `{"shell":{"session":"proj-demo"}}` + "\n"
		client := testRunClient(t, Host{ID: "h", Target: "h"}, stubInvoker(stdout, "", 0))
		var result shellLike
		if err := client.RunSidecar(context.Background(), []string{"create", "shell", "--json"}, &result); err != nil {
			t.Fatalf("RunSidecar: %v", err)
		}
		if result.Shell.Session != "proj-demo" {
			t.Fatalf("session = %q, want proj-demo (the log line won)", result.Shell.Session)
		}
	})

	t.Run("an empty object does not answer for a plan", func(t *testing.T) {
		stdout := "{}\n" + `{"branch":"feature","path":"/home/me/api-feature"}` + "\n"
		client := testRunClient(t, Host{ID: "h", Target: "h"}, stubInvoker(stdout, "", 0))
		var plan planLike
		if err := client.RunSidecar(context.Background(), []string{"create", "worktree", "--plan", "--json"}, &plan); err != nil {
			t.Fatalf("RunSidecar: %v", err)
		}
		if plan.Branch != "feature" || plan.Path != "/home/me/api-feature" {
			t.Fatalf("plan = %+v, want the real plan", plan)
		}
	})

	t.Run("a log line alone is a named failure, not a zero result", func(t *testing.T) {
		client := testRunClient(t, Host{ID: "mac-mini", Target: "mac-mini"},
			stubInvoker(`{"level":"info","msg":"loading nvm"}`+"\n", "", 0))
		var plan planLike
		err := client.RunSidecar(context.Background(), []string{"create", "worktree", "--plan", "--json"}, &plan)
		if got := RunFailure(err); got != FailNotResult {
			t.Fatalf("failure = %q, want %q (err %v, plan %+v)", got, FailNotResult, err, plan)
		}
		if plan.Branch != "" || plan.Path != "" {
			t.Fatalf("a rejected candidate left %+v behind in the caller's value", plan)
		}
		if !strings.Contains(err.Error(), "loading nvm") {
			t.Errorf("message %q does not show what the host actually wrote", err)
		}
		var runErr *RunError
		if !errors.As(err, &runErr) || runErr.Fix() == "" {
			t.Error("a not-result failure has no suggested fix")
		}
	})
}

// TestRunSidecarPrefersTheLastValue: the CLI writes its result last, and
// everything a login shell prints comes before it. Preferring the last decodable
// value is the cheap half of the fix; the zero-value rule above is the half that
// catches a banner sharing a field name.
func TestRunSidecarPrefersTheLastValue(t *testing.T) {
	stdout := `{"shell":{"session":"stale-banner-session"}}` + "\n" + `{"shell":{"session":"the-real-one"}}` + "\n"
	client := testRunClient(t, Host{ID: "h", Target: "h"}, stubInvoker(stdout, "", 0))
	var result shellLike
	if err := client.RunSidecar(context.Background(), []string{"create", "shell", "--json"}, &result); err != nil {
		t.Fatalf("RunSidecar: %v", err)
	}
	if result.Shell.Session != "the-real-one" {
		t.Fatalf("session = %q, want the last value on stdout", result.Shell.Session)
	}
}

// validatedResult states which fields make a decoded object its verb's answer.
type validatedResult struct {
	Session string `json:"session"`
	Path    string `json:"path"`
}

func (r validatedResult) ValidRemoteResult() bool { return r.Session != "" && r.Path != "" }

// TestRunSidecarHonoursAResultValidator: the zero-value floor cannot tell a
// half-filled object from a whole one. A type that says what its required
// fields are gets that refusal too — and unknown fields stay tolerated, because
// a host one version ahead adding a field is not an error.
func TestRunSidecarHonoursAResultValidator(t *testing.T) {
	client := testRunClient(t, Host{ID: "h", Target: "h"},
		stubInvoker(`{"path":"/var/log/nvm","level":"info"}`+"\n", "", 0))
	var result validatedResult
	err := client.RunSidecar(context.Background(), []string{"create", "shell", "--json"}, &result)
	if got := RunFailure(err); got != FailNotResult {
		t.Fatalf("failure = %q, want %q (err %v, result %+v)", got, FailNotResult, err, result)
	}
	if result.Path != "" {
		t.Fatalf("a rejected candidate leaked into the caller's value: %+v", result)
	}

	// Same type, a real result carrying a field this build does not know.
	client = testRunClient(t, Host{ID: "h", Target: "h"},
		stubInvoker(`{"session":"proj-demo","path":"/srv/api","futureField":42}`+"\n", "", 0))
	var ok validatedResult
	if err := client.RunSidecar(context.Background(), []string{"create", "shell", "--json"}, &ok); err != nil {
		t.Fatalf("an unknown field made a real result fail: %v", err)
	}
	if ok.Session != "proj-demo" || ok.Path != "/srv/api" {
		t.Fatalf("decoded %+v", ok)
	}
}

// A caller that passes &struct{}{} is asking only "did it exit 0 and write
// JSON". The zero value is the only value that type can hold, so the emptiness
// rule must not turn every such call into a failure.
func TestRunSidecarAcceptsAnEmptyResultType(t *testing.T) {
	client := testRunClient(t, Host{ID: "h", Target: "h"}, stubInvoker("{\"sent\":true}\n", "", 0))
	var result struct{}
	if err := client.RunSidecar(context.Background(), []string{"shell", "send", "--json"}, &result); err != nil {
		t.Fatalf("RunSidecar: %v", err)
	}
}
