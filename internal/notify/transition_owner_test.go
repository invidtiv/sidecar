package notify

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTransitionOwnedByProjectUsesDurableLexicalOwner(t *testing.T) {
	n := Notification{
		Origin: Origin{ProjectKey: "repo", WorkDir: "/removed/external/repo-topic"},
		Transition: &TransitionMetadata{
			LaneKey:     "shell:removed",
			ProjectRoot: "/projects/main/../main/repo",
		},
	}
	if !TransitionOwnedByProject(n, "/projects/main/repo") {
		t.Fatal("stored owner did not match its lexical project root")
	}
	if TransitionOwnedByProject(n, "/foreign/main/repo") {
		t.Fatal("same-basename foreign project matched stored owner")
	}
}

func TestTransitionOwnedByProjectLegacyFallbackIsConservative(t *testing.T) {
	projectOnly := Notification{
		Origin:     Origin{ProjectKey: "repo", TmuxSession: "legacy"},
		Transition: &TransitionMetadata{LaneKey: "shell:legacy"},
	}
	if TransitionOwnedByProject(projectOnly, "/projects/repo") {
		t.Fatal("ambiguous project-key-only legacy record gained dismissal authority")
	}
	inside := projectOnly
	inside.Origin.WorkDir = "/projects/repo/subdir"
	if !TransitionOwnedByProject(inside, "/projects/repo") {
		t.Fatal("lexically contained legacy record was not recognized")
	}
	outside := inside
	outside.Origin.WorkDir = "/foreign/repo"
	if TransitionOwnedByProject(outside, "/projects/repo") {
		t.Fatal("foreign legacy workdir gained dismissal authority")
	}
}

func TestTransitionProjectRootJSONIsAdditiveAndOptional(t *testing.T) {
	legacy, err := json.Marshal(TransitionMetadata{Class: TransitionWaiting, LaneKey: "shell:one", DedupeKey: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), "projectRoot") {
		t.Fatalf("empty project root was not omitted: %s", legacy)
	}
	var decoded TransitionMetadata
	if err := json.Unmarshal([]byte(`{"class":"waiting","laneKey":"shell:one","dedupeKey":"one"}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ProjectRoot != "" || decoded.LaneKey != "shell:one" {
		t.Fatalf("legacy transition decoded as %#v", decoded)
	}
}
