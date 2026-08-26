package panecodec

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
)

func TestEncodeByteCompatibleFixture(t *testing.T) {
	st := fixtureState()
	live := []Live{
		{Kind: KindTerminal},
		{Kind: KindShell, Session: "sidecar-tp-abc", Name: "dev"},
	}
	got := Encode(st, Options{Live: live})
	want := fixtureJSON()
	if diff := jsonDiff(t, got, want); diff != "" {
		t.Fatalf("encode fixture:\n%s", diff)
	}
}

func TestDecodeEncodeRoundTripStable(t *testing.T) {
	want := fixtureJSON()
	st, live := Decode(want, Options{})
	got := Encode(st, Options{Live: live})
	if diff := jsonDiff(t, got, want); diff != "" {
		t.Fatalf("decode→encode:\n%s", diff)
	}
	if len(live) != 2 {
		t.Fatalf("live = %#v, want terminal and shell", live)
	}
	if live[0].Kind != KindTerminal || live[1].Kind != KindShell || live[1].Session != "sidecar-tp-abc" {
		t.Fatalf("live records = %#v", live)
	}
}

func TestEncodeNeverWritesContentpanesKindNames(t *testing.T) {
	st := fixtureState()
	raw, err := json.Marshal(Encode(st, Options{Live: []Live{{Kind: KindShell, Session: "sidecar-tp-abc"}}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"primary"`, `"document"`, `"columns"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("encoded JSON contains contentpanes vocabulary %s: %s", forbidden, raw)
		}
	}
}

func TestIssueOwnerFieldsRoundTrip(t *testing.T) {
	st := contentpanes.State{Version: 1, Root: &contentpanes.NodeState{
		Kind: "issue",
		Pane: &contentpanes.PaneState{Kind: "issue", Tabs: []contentpanes.TabState{{
			Ref:       contentlink.Ref{Kind: contentlink.KindIssue, Value: "td-abc1"},
			Scroll:    3,
			OwnerName: "Proj-B",
			OwnerRoot: "/tmp/proj-b",
		}}},
	}}
	encoded := Encode(st, Options{})
	if encoded == nil || len(encoded.IssueTabs) != 1 {
		t.Fatalf("encoded = %#v", encoded)
	}
	tab := encoded.IssueTabs[0]
	if tab.OwnerName != "Proj-B" || tab.OwnerRoot != "/tmp/proj-b" || tab.Issue != "td-abc1" || tab.Scroll != 3 {
		t.Fatalf("owner fields dropped on encode: %#v", tab)
	}

	back, _ := Decode(encoded, Options{})
	if back.Root == nil || back.Root.Pane == nil || len(back.Root.Pane.Tabs) != 1 {
		t.Fatalf("decoded = %#v", back)
	}
	got := back.Root.Pane.Tabs[0]
	if got.OwnerName != "Proj-B" || got.OwnerRoot != "/tmp/proj-b" {
		t.Fatalf("owner fields dropped on decode: %#v", got)
	}
}

func TestLegacyIssueScrollBecomesIssueTabs(t *testing.T) {
	st, _ := Decode(&state.PaneLayoutJSON{Kind: KindIssue, Issue: "td-1a2b3c", Scroll: 4}, Options{})
	encoded := Encode(st, Options{})
	if encoded == nil || encoded.Issue != "" || encoded.Scroll != 0 {
		t.Fatalf("legacy fields still written: %#v", encoded)
	}
	if len(encoded.IssueTabs) != 1 || encoded.IssueTabs[0].Issue != "td-1a2b3c" || encoded.IssueTabs[0].Scroll != 4 {
		t.Fatalf("legacy issue = %#v", encoded)
	}
}

func TestUnknownKindPreservedForContentpanesCollapse(t *testing.T) {
	layout := &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
		Axis: axisCols, Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: KindTerminal},
		B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
			Axis: axisRows, Ratio: 50,
			A: &state.PaneLayoutJSON{Kind: "hologram"},
			B: &state.PaneLayoutJSON{Kind: KindDoc, Tabs: []state.PaneDocTabJSON{{Path: "README.md", Mode: docModeRaw}}},
		}},
	}}
	st, live := Decode(layout, Options{})
	if st.Root == nil || st.Root.B == nil || st.Root.B.A == nil || st.Root.B.A.Kind != "hologram" {
		t.Fatalf("projection dropped unknown kind before contentpanes could: %#v", st.Root)
	}
	if len(live) != 1 || live[0].Kind != KindTerminal {
		t.Fatalf("live = %#v", live)
	}

	deck := contentpanes.Decode(contentpanes.SurfaceContext{Root: t.TempDir(), Surface: "s", Epoch: 1}, contentpanes.Config{}, st)
	if panelayout.FirstOfKind(deck.Tree(), panelayout.Document) == nil || panelayout.FirstOfKind(deck.Tree(), panelayout.Primary) == nil {
		t.Fatalf("collapsed tree lost a known leaf: %#v", deck.Tree())
	}
	if deck.Tree() == nil || deck.Tree().Split == nil {
		t.Fatalf("hologram collapse should leave primary beside document: %#v", deck.Tree())
	}
}

func TestShellSessionIsNeverAPaneID(t *testing.T) {
	st := contentpanes.State{Version: 1, Root: &contentpanes.NodeState{Kind: stateKindShell}}
	encoded := Encode(st, Options{Live: []Live{{Kind: KindShell, Session: "sidecar-tp-main"}}})
	if encoded == nil || encoded.Kind != KindShell || encoded.Session != "sidecar-tp-main" {
		t.Fatalf("encoded shell = %#v", encoded)
	}
	_, live := Decode(&state.PaneLayoutJSON{Kind: KindShell, Session: "%17"}, Options{})
	if len(live) != 1 || live[0].Session != "%17" {
		t.Fatalf("decode must surface the raw selector so the host can reject pane ids: %#v", live)
	}
}

func TestDecodeDropsExtraShellLeaves(t *testing.T) {
	layout := &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
		Axis: axisCols, Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: KindTerminal},
		B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
			Axis: axisRows, Ratio: 50,
			A: &state.PaneLayoutJSON{Kind: KindShell, Session: "sidecar-tp-first"},
			B: &state.PaneLayoutJSON{Kind: KindShell, Session: "sidecar-tp-second"},
		}},
	}}
	st, live := Decode(layout, Options{})
	var shells []Live
	for _, l := range live {
		if l.Kind == KindShell {
			shells = append(shells, l)
		}
	}
	if len(shells) != 1 || shells[0].Session != "sidecar-tp-first" {
		t.Fatalf("live shells = %#v, want the first only", live)
	}
	if n := countStateKind(st.Root, stateKindShell); n != 1 {
		t.Fatalf("state shell nodes = %d, want 1 after extras collapse", n)
	}
}

func countStateKind(n *contentpanes.NodeState, kind string) int {
	if n == nil {
		return 0
	}
	if n.A != nil || n.B != nil {
		return countStateKind(n.A, kind) + countStateKind(n.B, kind)
	}
	if n.Kind == kind {
		return 1
	}
	return 0
}

func fixtureState() contentpanes.State {
	return contentpanes.State{Version: 1, Root: &contentpanes.NodeState{
		Axis: stateAxisColumns, Ratio: 50,
		A: &contentpanes.NodeState{Kind: stateKindPrimary},
		B: &contentpanes.NodeState{
			Axis: stateAxisRows, Ratio: 40,
			A: &contentpanes.NodeState{
				Kind: stateKindDocument,
				Pane: &contentpanes.PaneState{Kind: stateKindDocument, Active: 0, Tabs: []contentpanes.TabState{{
					Ref: contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, Scroll: 3, Wrap: true, Rendered: true,
				}}},
			},
			B: &contentpanes.NodeState{
				Axis: stateAxisColumns, Ratio: 60,
				A: &contentpanes.NodeState{
					Kind: KindIssue,
					Pane: &contentpanes.PaneState{Kind: KindIssue, Tabs: []contentpanes.TabState{{
						Ref: contentlink.Ref{Kind: contentlink.KindIssue, Value: "td-abc1"}, Scroll: 2,
						OwnerName: "Proj-B", OwnerRoot: "/tmp/proj-b",
					}}},
				},
				B: &contentpanes.NodeState{
					Axis: stateAxisRows, Ratio: 50,
					A: &contentpanes.NodeState{
						Kind: KindNote,
						Pane: &contentpanes.PaneState{Kind: KindNote, Tabs: []contentpanes.TabState{{
							Ref: contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "note", Value: "nt-abc123"}, Scroll: 1,
						}}},
					},
					B: &contentpanes.NodeState{
						Axis: stateAxisColumns, Ratio: 55,
						A: &contentpanes.NodeState{
							Kind: KindDiff,
							Pane: &contentpanes.PaneState{Kind: KindDiff, Tabs: []contentpanes.TabState{{
								Ref: contentlink.Ref{Kind: contentlink.KindDiff, Value: "wt"}, Scroll: 4, Scope: "all", Mode: "unified", Path: "main.go",
							}}},
						},
						B: &contentpanes.NodeState{
							Axis: stateAxisRows, Ratio: 50,
							A: &contentpanes.NodeState{
								Kind: KindResource,
								Pane: &contentpanes.PaneState{Kind: KindResource, Tabs: []contentpanes.TabState{{
									Ref: contentlink.Ref{Kind: contentlink.KindResource, Provider: "jira-work", Matcher: "issue-key", Value: "CASH-1245"}, Scroll: 5,
								}}},
							},
							B: &contentpanes.NodeState{Kind: stateKindShell},
						},
					},
				},
			},
		},
	}}
}

func fixtureJSON() *state.PaneLayoutJSON {
	return &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
		Axis: axisCols, Ratio: 50,
		A: &state.PaneLayoutJSON{Kind: KindTerminal},
		B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
			Axis: axisRows, Ratio: 40,
			A: &state.PaneLayoutJSON{Kind: KindDoc, Tabs: []state.PaneDocTabJSON{{Path: "README.md", Mode: docModeRendered, Wrap: true, Scroll: 3}}},
			B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
				Axis: axisCols, Ratio: 60,
				A: &state.PaneLayoutJSON{Kind: KindIssue, IssueTabs: []state.PaneIssueTabJSON{{Issue: "td-abc1", Scroll: 2, OwnerName: "Proj-B", OwnerRoot: "/tmp/proj-b"}}},
				B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
					Axis: axisRows, Ratio: 50,
					A: &state.PaneLayoutJSON{Kind: KindNote, NoteTabs: []state.PaneNoteTabJSON{{Note: "nt-abc123", Scroll: 1}}},
					B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
						Axis: axisCols, Ratio: 55,
						A: &state.PaneLayoutJSON{Kind: KindDiff, DiffTabs: []state.PaneDiffTabJSON{{Spec: "wt", Path: "main.go", Scope: "all", Mode: "unified", Scroll: 4}}},
						B: &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{
							Axis: axisRows, Ratio: 50,
							A: &state.PaneLayoutJSON{Kind: KindResource, ResourceTabs: []state.PaneResourceTabJSON{{Provider: "jira-work", Matcher: "issue-key", Locator: "CASH-1245", Scroll: 5}}},
							B: &state.PaneLayoutJSON{Kind: KindShell, Session: "sidecar-tp-abc"},
						}},
					}},
				}},
			}},
		}},
	}}
}

func jsonDiff(t *testing.T, got, want *state.PaneLayoutJSON) string {
	t.Helper()
	gotRaw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantRaw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotRaw) == string(wantRaw) {
		return ""
	}
	var gotV, wantV any
	if err := json.Unmarshal(gotRaw, &gotV); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantRaw, &wantV); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(gotV, wantV) {
		return ""
	}
	return "got  " + string(gotRaw) + "\nwant " + string(wantRaw)
}
