package app

import (
	"testing"

	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/uirequest"
)

func TestSessionsOwnsCreateSplit(t *testing.T) {
	m := Model{scope: ScopeGlobal, globalTab: GlobalSessions, overview: &overview.Model{}}
	req := uirequest.Request{Action: uirequest.ActionCreate, Options: uirequest.Options{Split: "right"}}
	if !m.sessionsOwnsCreateSplit(req) {
		t.Fatal("Sessions on screen did not claim create --split")
	}

	hidden := m
	hidden.scope = ScopeProject
	if hidden.sessionsOwnsCreateSplit(req) {
		t.Fatal("project scope claimed a Sessions split")
	}

	noSplit := req
	noSplit.Options.Split = ""
	if m.sessionsOwnsCreateSplit(noSplit) {
		t.Fatal("a workspace-row create was treated as a split")
	}
}
