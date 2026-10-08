package issueview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// slowTd installs a td that never answers within the test, and records each
// invocation so a test can tell whether later steps still ran.
func slowTd(t *testing.T) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	log = filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\n" +
		`printf '%s\n' "$1" >> "$ISSUEVIEW_CALLS"` + "\n" +
		"exec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ISSUEVIEW_CALLS", log)
	return dir, log
}

// td-ca0533: an API issue read that its client abandons must stop at once;
// the per-client content-read slot is held until the lookup returns.
func TestLookupContextStopsTdWhenCancelled(t *testing.T) {
	dir, log := slowTd(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	started := time.Now()
	data, owner, err := LookupContext(ctx, dir, "td-slow", []ProjectRef{{Name: "Other", Root: t.TempDir()}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if data != nil || owner != nil {
		t.Fatalf("cancelled lookup returned data %#v owner %#v", data, owner)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancelled lookup took %v", elapsed)
	}
	calls, _ := os.ReadFile(log)
	// The first show may be killed before it logs; nothing may run after it.
	if got := string(calls); got != "" && got != "show\n" {
		t.Fatalf("td calls after cancellation = %q, want at most the first show", got)
	}
}

func TestLookupContextStopsTreeReadsWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = show ]; then\n" +
		`  printf '{"id":"td-b","title":"Two","status":"open","type":"task","priority":"P2","parent_id":"td-epic"}\n'` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = dep ]; then printf '{}\\n'; exit 0; fi\n" +
		"exec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	started := time.Now()
	if _, _, err := LookupContext(ctx, dir, "td-b", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancelled tree read took %v", elapsed)
	}
}
