package manifests

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
)

func TestLoadCompilesEveryVendoredAgentWithoutDiagnostics(t *testing.T) {
	agents, err := Agents()
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range agents {
		compiled, source, err := Load(agent)
		if err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
		if source.Diagnostic != "" {
			t.Fatalf("%s: %s", agent, source.Diagnostic)
		}
		if compiled.Source != source.Label() {
			t.Fatalf("%s: compiled source %q, label %q", agent, compiled.Source, source.Label())
		}
		if source.OverlayApplied != HasOverlay(agent) {
			t.Fatalf("%s: overlay applied = %v, overlay present = %v", agent, source.OverlayApplied, HasOverlay(agent))
		}
	}
}

func TestSourceLabelReadsLikeHerdrs(t *testing.T) {
	plain := Source{Agent: "claude", Version: "2026.08.29.1"}
	if got := plain.Label(); got != "bundled claude 2026.08.29.1" {
		t.Fatalf("label = %q", got)
	}
	overlaid := Source{Agent: "cursor", Version: "2026.08.03.1", OverlayApplied: true}
	if got := overlaid.Label(); got != "bundled cursor 2026.08.03.1 + sidecar overlay" {
		t.Fatalf("label = %q", got)
	}
}

// TestEveryOverlayReplacesAnUpstreamRuleOrIsPrefixed is the merge contract
// checked against the real overlays, not synthetic ones: an overlay may only
// take over an id upstream already has, or add one under the sidecar. prefix.
func TestEveryOverlayReplacesAnUpstreamRuleOrIsPrefixed(t *testing.T) {
	dir, err := SidecarOverlays()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".toml") {
			continue
		}
		found++
		agent := strings.TrimSuffix(name, ".toml")
		overlayBytes, err := fs.ReadFile(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		overlay, err := manifest.ParseOverlay(overlayBytes)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		upstreamBytes, err := UpstreamBytes(agent + ".toml")
		if err != nil {
			t.Fatalf("%s: no vendored manifest to overlay: %v", name, err)
		}
		upstream, err := manifest.ParseAndValidateWith(upstreamBytes,
			manifest.ValidateOptions{AllowIncompatibleRegex: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manifest.Merge(upstream, overlay); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if found == 0 {
		t.Fatal("no overlays found; the four RE2 rewrites should be vendored")
	}
}

// TestOverlaysMakeEveryRuleCompilable is the reason the four overlays exist.
// Without them, the four upstream `\p{Alphabetic}` rules are permanently dead
// in Sidecar; with them, every rule in every merged manifest compiles.
func TestOverlaysMakeEveryRuleCompilable(t *testing.T) {
	agents, err := Agents()
	if err != nil {
		t.Fatal(err)
	}
	var dead []string
	for _, agent := range agents {
		compiled, _, err := Load(agent)
		if err != nil {
			t.Fatal(err)
		}
		// A rule the engine could not compile reports its note in explain.
		_, explain := compiled.Explain(manifest.Input{})
		for _, rule := range explain.EvaluatedRules {
			if rule.Evidence.Incompatible != "" {
				dead = append(dead, agent+"/"+rule.ID+": "+rule.Evidence.Incompatible)
			}
		}
	}
	if len(dead) > 0 {
		t.Fatalf("rules RE2 cannot compile and no overlay rewrites:\n  %s",
			strings.Join(dead, "\n  "))
	}
}

// TestOverlayFailureFallsBackToUpstream is Sidecar's analogue of Herdr's
// invalid_local_override_falls_back_to_cached_remote_manifest: a broken Sidecar
// addition must be reported and ignored, never take the vendored file down with
// it. It exercises Merge directly because the loader's overlays are embedded and
// a test cannot make one of them invalid.
func TestOverlayFailureFallsBackToUpstream(t *testing.T) {
	upstreamBytes, err := UpstreamBytes("cursor.toml")
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := manifest.ParseAndValidateWith(upstreamBytes,
		manifest.ValidateOptions{AllowIncompatibleRegex: true})
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := manifest.ParseOverlay([]byte(`
id = "cursor"

[[rules]]
id = "unprefixed_addition"
state = "idle"
contains = ["anything"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Merge(upstream, overlay); err == nil {
		t.Fatal("merge accepted an overlay it should have refused")
	}
	// The vendored manifest still compiles and classifies on its own.
	compiled, err := manifest.Compile(upstream)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Evaluate(manifest.Input{Screen: "Run this command?\nRun (once) (y)\n"}).MatchedRule == nil {
		t.Fatal("upstream cursor manifest stopped working without its overlay")
	}
}
