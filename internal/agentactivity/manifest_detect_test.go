package agentactivity

import "testing"

// TestExplainRecordsTheAgentAskedAboutNotTheManifestId pins the meaning of the
// explain record's `agent` field on both of the paths that produce one.
//
// Herdr fills it with agent_label(agent) (manifest.rs:501), the agent the caller
// asked about, and never with the loaded manifest's id. Antigravity is the one
// vendored file where those two differ without an override in play: the file is
// antigravity.toml and the manifest inside it declares id = "agy". A local
// override widens the gap further, because it can declare one agent's id while
// carrying another's alias and still be accepted.
func TestExplainRecordsTheAgentAskedAboutNotTheManifestId(t *testing.T) {
	ob := Observation{
		Agent:          "antigravity",
		CurrentCommand: "antigravity",
		Screen:         "nothing in particular\n",
		PaneHeight:     24,
	}

	explain := ExplainManifest(ob)
	if explain == nil {
		t.Fatal("no explain record; the process gate refused before any rule ran")
	}
	if explain.Agent != "antigravity" {
		t.Fatalf("live explain agent = %q, want the requested agent", explain.Agent)
	}

	vendored, ok := ExplainVendoredManifest("antigravity", ob)
	if !ok {
		t.Fatal("no vendored manifest for antigravity")
	}
	if vendored.Agent != "antigravity" {
		t.Fatalf("vendored explain agent = %q, want the requested agent", vendored.Agent)
	}
}
