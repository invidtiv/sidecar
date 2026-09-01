package workspacecreate

import (
	"testing"

	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacediff"
)

func TestResolvePickerTargetRemoteDoesNotNeedALocalRoot(t *testing.T) {
	file, err := ResolvePickerTargetRemote(KindFile, "", "HOST-ONLY.md:12")
	if err != nil {
		t.Fatal(err)
	}
	if file.Kind != uirequest.TargetKindFile || file.Value != "HOST-ONLY.md" || file.Line != 12 {
		t.Fatalf("file = %+v", file)
	}

	diff, err := ResolvePickerTargetRemote(KindDiff, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if diff.Kind != uirequest.TargetKindDiff || diff.Value != workspacediff.IdentityWorkingTree {
		t.Fatalf("diff = %+v", diff)
	}

	issue, err := ResolvePickerTargetRemote(KindIssue, "", "td-a4dd72")
	if err != nil {
		t.Fatal(err)
	}
	if issue.Kind != uirequest.TargetKindIssue || issue.Value != "td-a4dd72" {
		t.Fatalf("issue = %+v", issue)
	}
}
