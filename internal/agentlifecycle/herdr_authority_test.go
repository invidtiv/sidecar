package agentlifecycle

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity/manifests"
)

// Herdr publishes, per agent, whether its integration is the authority for
// lifecycle state or only for session identity. That table is vendored beside
// the manifests as authority.upstream.json and it is a *target*: a Sidecar tier
// is earned from real traces and is never copied from it, which is why nothing
// in this file writes to capabilities.json.
//
// What the target buys is that falling behind is visible. Phase 6 of
// docs/plans/active/herdr-detection-parity.md closes the hooks lane by porting
// Herdr's integration assets, and until it lands this test is the standing list
// of what is still open.
//
// It does not fail the build for having a gap. A gap is a fact about work not
// yet done, and a test that failed on it would either be permanently red or
// would push somebody to close it by editing a tier, which is the one thing the
// evidence rules forbid. It fails on staleness instead: if the vendored
// authority table and the manifest lock disagree about which Herdr commit they
// were extracted from, the list below is describing an upstream that is no
// longer the one Sidecar vendors, and a stale list is worse than none.

// belowTarget is the gap rule, and it is the same rule
// internal/tools/herdrsync/report.go applies in renderAuthority (belowTarget
// there). The tool cannot be imported -- it reads capabilities.json out of a
// working tree rather than the embedded registry, because it has to run against
// a checkout -- so the agreement is kept by TestTheGapRuleAgreesWithTheSyncReport
// below, which enumerates every pair the rule can be asked about.
//
// It has two halves and both are shared. The membership half is hooks authority
// only: where Herdr's own authority is session_identity it reads state off the
// screen exactly as Sidecar does, so there is nothing upstream has done that
// Sidecar has not, and calling it a gap would list nine agents as work to do.
// The rank half is what closes a row once a Sidecar source reaches full.
//
// Only one of the two halves used to be shared, and the lists diverged: this
// test printed five agents and report.md printed fourteen, which is exactly the
// "two lists" the comment on TestTheGapRuleAgreesWithTheSyncReport says must not
// happen. The report moved, because the plan's Journey 5 and Phase 4 bullet both
// state the rule as lifecycle-through-hooks.
func belowTarget(authority string, tier Tier) bool {
	if authority != manifests.AuthorityHooks {
		return false
	}
	return herdrAuthorityRank(authority) > sidecarTierRank(tier)
}

func herdrAuthorityRank(authority string) int {
	switch authority {
	case manifests.AuthorityHooks:
		return 2
	case manifests.AuthoritySessionIdentity:
		return 1
	default:
		return 0
	}
}

func sidecarTierRank(tier Tier) int {
	switch tier {
	case TierFull:
		return 2
	case TierSessionIdentity:
		return 1
	default:
		return 0
	}
}

// sidecarTierByHerdrAgent is the strongest tier any Sidecar source has earned
// for each provider, keyed by Herdr's agent id so the two tables line up.
func sidecarTierByHerdrAgent() map[string]Tier {
	tiers := map[string]Tier{}
	for _, capability := range Capabilities() {
		id := capability.Provider
		// Sidecar spells Antigravity out; Herdr's agent id is agy. This is the
		// same one-entry mapping herdrsync's sidecarTiers carries.
		if id == "antigravity" {
			id = "agy"
		}
		if existing, ok := tiers[id]; !ok || sidecarTierRank(capability.Tier) > sidecarTierRank(existing) {
			tiers[id] = capability.Tier
		}
	}
	return tiers
}

func TestHerdrAuthorityGaps(t *testing.T) {
	authority, err := manifests.LoadAuthority()
	if err != nil {
		t.Fatalf("load authority.upstream.json: %v", err)
	}
	lock, err := manifests.LoadLock()
	if err != nil {
		t.Fatalf("load upstream.lock.json: %v", err)
	}

	// The one failure condition. Both files are written by the same sync run, so
	// a mismatch means one of them was updated without the other and the gap
	// list below is describing an upstream Sidecar no longer vendors.
	if authority.HerdrRef != lock.Herdr.Ref {
		t.Fatalf("authority.upstream.json was extracted at Herdr %q and upstream.lock.json pins %q.\n"+
			"The authority target is stale against the vendored manifests; re-run scripts/sync-herdr.sh so\n"+
			"the gap list describes the upstream actually vendored here.",
			authority.HerdrRef, lock.Herdr.Ref)
	}

	tiers := sidecarTierByHerdrAgent()
	ids := make([]string, 0, len(authority.Agents))
	for id := range authority.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var gaps []string
	for _, id := range ids {
		agent := authority.Agents[id]
		tier, known := tiers[id]
		if !known {
			tier = "(none)"
		}
		if !belowTarget(agent.LifecycleAuthority, tier) {
			continue
		}
		gaps = append(gaps, fmt.Sprintf("  %-12s herdr=%s (integration v%d, assets %s)  sidecar=%s",
			id, agent.LifecycleAuthority, agent.IntegrationVersion, assetDir(agent.IntegrationAssetDir), tier))
	}

	header := fmt.Sprintf("Herdr lifecycle authority vs Sidecar proved tier, at Herdr %s.\n"+
		"Herdr's table is a target; a Sidecar tier is earned by traces and never copied.\n"+
		"Closing these is Phase 6 (Sidecar-native ports of Herdr's integration assets).\n", authority.HerdrRef)
	if len(gaps) == 0 {
		t.Log(header + "  no gaps: every agent Herdr gives lifecycle authority to has a Sidecar source at full.")
		return
	}
	t.Log(header + strings.Join(gaps, "\n"))
}

func assetDir(dir string) string {
	if dir == "" {
		return "none"
	}
	return dir
}

// The sync report and this test must mean the same thing by "gap", or a
// maintainer reading report.md and a maintainer running the tests see two
// different lists. Neither side can import the other, so the agreement is
// asserted over every pair the rule can be asked about: three Herdr authority
// values against the four Sidecar tiers, plus the absent tier.
//
// Both halves of the rule are covered. The membership half is the one that was
// missing, and its absence is what let the two lists reach five entries and
// fourteen; every session_identity row below is false for that reason, and a
// report that starts marking one again fails here.
func TestTheGapRuleAgreesWithTheSyncReport(t *testing.T) {
	// The expectation is written out rather than computed, so that changing
	// belowTarget or either rank function fails here instead of moving both
	// sides at once. Keep in step with belowTarget/authorityRank/tierRank in
	// internal/tools/herdrsync/report.go.
	want := map[string]bool{
		"hooks/full":                        false,
		"hooks/advisory":                    true,
		"hooks/session-identity":            true,
		"hooks/screen-fallback":             true,
		"hooks/(none)":                      true,
		"session_identity/full":             false,
		"session_identity/advisory":         false,
		"session_identity/session-identity": false,
		"session_identity/screen-fallback":  false,
		"session_identity/(none)":           false,
		"none/full":                         false,
		"none/advisory":                     false,
		"none/session-identity":             false,
		"none/screen-fallback":              false,
		"none/(none)":                       false,
	}
	authorities := []string{manifests.AuthorityHooks, manifests.AuthoritySessionIdentity, manifests.AuthorityNone}
	tiers := append(Tiers(), Tier("(none)"))
	for _, authority := range authorities {
		for _, tier := range tiers {
			key := authority + "/" + string(tier)
			expected, ok := want[key]
			if !ok {
				t.Fatalf("no expectation recorded for %s", key)
			}
			if got := belowTarget(authority, tier); got != expected {
				t.Errorf("gap(%s) = %v, want %v", key, got, expected)
			}
		}
	}
	if len(want) != len(authorities)*len(tiers) {
		t.Errorf("expectation table has %d rows for %d pairs", len(want), len(authorities)*len(tiers))
	}
}

// Every provider named in the vendored authority table that Sidecar has a
// capability entry for must be spelled the way the table spells it, or the
// comparison silently reports a gap that does not exist. Antigravity is the one
// translation and it is the only one allowed.
func TestEverySidecarCapabilityProviderIsFindableInTheAuthorityTable(t *testing.T) {
	authority, err := manifests.LoadAuthority()
	if err != nil {
		t.Fatalf("load authority.upstream.json: %v", err)
	}
	for id := range sidecarTierByHerdrAgent() {
		if _, ok := authority.Agents[id]; !ok {
			t.Errorf("capabilities.json has provider %q, which Herdr's authority table does not name; "+
				"either the spelling needs a mapping in sidecarTierByHerdrAgent or the provider is not one Herdr tracks", id)
		}
	}
}
