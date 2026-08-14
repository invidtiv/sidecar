package issueview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadIssueAttachesChildrenAndSiblings(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = tree ]; then\n" +
		"  if [ \"$2\" = td-epic ]; then\n" +
		`    printf '{"id":"td-epic","title":"Epic","status":"open","type":"epic","priority":"P1","children":[{"id":"td-a","title":"One","status":"closed","type":"task","priority":"P2","children":[]},{"id":"td-b","title":"Two","status":"open","type":"task","priority":"P2","children":[]}]}\n'` + "\n" +
		"  else\n" +
		`    printf '{"id":"%s","title":"Child","status":"open","type":"task","priority":"P2","children":[{"id":"td-sub","title":"Sub","status":"open","type":"task","priority":"P3","children":[]}]}\n' "$2"` + "\n" +
		"  fi\n" +
		"  exit 0\n" +
		"fi\n" +
		`printf '{"id":"td-b","title":"Two","status":"open","type":"task","priority":"P2","parent_id":"td-epic","description":"Body","labels":["tui"]}\n'` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	data, err := loadIssue(dir, "td-b")
	if err != nil {
		t.Fatal(err)
	}
	if data.ID != "td-b" || data.ParentID != "td-epic" {
		t.Fatalf("issue = %#v", data)
	}
	if data.Parent == nil || data.Parent.ID != "td-epic" || data.Parent.Type != "epic" {
		t.Fatalf("parent = %#v", data.Parent)
	}
	if len(data.Children) != 1 || data.Children[0].ID != "td-sub" {
		t.Fatalf("children = %#v", data.Children)
	}
	if len(data.Siblings) != 2 || data.Siblings[0].ID != "td-a" || data.Siblings[1].ID != "td-b" {
		t.Fatalf("siblings = %#v", data.Siblings)
	}
}

func TestLoadIssueDisablesSyncAndAnalyticsForEveryTdRead(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "td-env.log")
	script := "#!/bin/sh\n" +
		`printf '%s|%s|%s|%s|%s\n' "$1" "$TD_SYNC_AUTO_START" "$TD_ANALYTICS" "$ISSUEVIEW_SENTINEL" "$PATH" >> "$ISSUEVIEW_ENV_LOG"` + "\n" +
		"if [ \"$1\" = show ]; then\n" +
		`  printf '{"id":"td-child","title":"Child","status":"open","type":"task","priority":"P2","parent_id":"td-parent"}\n'` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`printf '{"id":"%s","title":"Tree","status":"open","type":"epic","priority":"P1","children":[]}\n' "$2"` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	path := dir + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", path)
	t.Setenv("TD_SYNC_AUTO_START", "1")
	t.Setenv("TD_ANALYTICS", "true")
	t.Setenv("ISSUEVIEW_SENTINEL", "preserved")
	t.Setenv("ISSUEVIEW_ENV_LOG", logPath)

	if _, err := loadIssue(dir, "td-child"); err != nil {
		t.Fatal(err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logged)), "\n")
	if len(lines) != 3 {
		t.Fatalf("td invocations = %d, want show, child tree, parent tree:\n%s", len(lines), logged)
	}
	for _, line := range lines {
		fields := strings.SplitN(line, "|", 5)
		if len(fields) != 5 {
			t.Fatalf("malformed td environment record %q", line)
		}
		if fields[1] != "0" || fields[2] != "false" {
			t.Errorf("%s environment = sync %q analytics %q, want 0/false", fields[0], fields[1], fields[2])
		}
		if fields[3] != "preserved" || fields[4] != path {
			t.Errorf("%s lost inherited environment: sentinel=%q path=%q", fields[0], fields[3], fields[4])
		}
	}
}

func TestShowIssueStillReturnsTdErrorWithReadOnlyEnvironment(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"echo 'usage: td show' >&2\n" +
		"echo 'Error: bad issue id' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := showIssue(dir, "td-missing")
	if err == nil || err.Error() != "bad issue id" {
		t.Fatalf("showIssue error = %v, want bad issue id", err)
	}
}
