package issueview

import (
	"os"
	"path/filepath"
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
