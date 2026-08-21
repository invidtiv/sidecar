package resourceprovider

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/resource"
)

// internal/config restates the protocol's bounds so it can stay a leaf package.
// This is the guard against those two copies drifting.
func TestTerminalResourceBoundsMatchTheProtocol(t *testing.T) {
	for i, ok := range configBoundsMatchProtocol {
		if !ok {
			t.Fatalf("internal/config bound %d no longer matches internal/resource", i)
		}
	}
}

func TestFromConfig(t *testing.T) {
	cfg := config.TerminalResourcesConfig{Providers: []config.TerminalResourceProviderConfig{
		{ID: "first", Command: []string{fixtureBin}, Enabled: true, Timeout: 3 * time.Second, ClaimHosts: []string{"github.com"}},
		{ID: "off", Command: []string{fixtureBin}, Enabled: false},
		{ID: "second", Command: []string{fixtureBin, "-mode=error-response"}, Enabled: true},
	}}
	providers, disabled, err := FromConfig(cfg, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	if len(providers) != 2 || providers[0].Instance() != "first" || providers[1].Instance() != "second" {
		t.Fatalf("providers = %+v", providers)
	}
	if !slices.Equal(disabled, []string{"off"}) {
		t.Fatalf("disabled = %v", disabled)
	}
	if got := providers[0].(*CommandProvider).ResolveTimeout(); got != 3*time.Second {
		t.Fatalf("timeout = %s", got)
	}
	// An unset timeout takes the clamped default.
	if got := providers[1].(*CommandProvider).ResolveTimeout(); got != resource.DefaultResolveTimeout {
		t.Fatalf("default timeout = %s", got)
	}
	// The instance configuration's claimed hosts travel with the provider.
	if !slices.Equal(providers[0].(*CommandProvider).ClaimHosts(), []string{"github.com"}) {
		t.Fatalf("claimHosts = %v", providers[0].(*CommandProvider).ClaimHosts())
	}
}

// The whole M0 path, end to end, against real child processes.
func TestManagerOverConfiguredExecutables(t *testing.T) {
	cfg := config.TerminalResourcesConfig{Providers: []config.TerminalResourceProviderConfig{
		{ID: "good", Command: []string{fixtureBin}, Enabled: true},
		{ID: "broken", Command: []string{fixtureBin, "-mode=invalid-re2"}, Enabled: true},
		{ID: "off", Command: []string{fixtureBin}, Enabled: false},
	}}
	providers, disabled, err := FromConfig(cfg, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	m := NewManager(ManagerOptions{})
	m.SetProviders(providers, disabled)
	statuses := m.DescribeAll(context.Background())

	byID := make(map[string]Status, len(statuses))
	for _, st := range statuses {
		byID[st.Instance] = st
	}
	if byID["good"].State != StateReady || byID["good"].MatcherCount != 2 {
		t.Fatalf("good = %+v", byID["good"])
	}
	if byID["broken"].State != StateIncompatible {
		t.Fatalf("broken = %+v", byID["broken"])
	}
	if byID["off"].State != StateDisabled {
		t.Fatalf("off = %+v", byID["off"])
	}

	// Only the ready provider contributes matchers.
	snap := m.Snapshot()
	if snap.Len() != 2 {
		t.Fatalf("snapshot = %v", ids(snap.Matchers()))
	}
	matcher, ok := snap.Lookup("good", "issue-key")
	if !ok {
		t.Fatal("the ready provider's matcher is not live")
	}
	if got := matcher.Regexp().FindString("agent printed CASH-1245 just now"); got != "CASH-1245" {
		t.Fatalf("whole match = %q", got)
	}

	doc, err := m.Resolve(context.Background(), resource.Reference{Instance: "good", Matcher: "issue-key", Locator: "CASH-1245"}, false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if doc.Identity != "CASH-1245" || doc.Title == "" {
		t.Fatalf("document = %+v", doc)
	}
}
